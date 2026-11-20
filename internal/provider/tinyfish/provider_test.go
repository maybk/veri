package tinyfish

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/provider/providertest"
	"veri/internal/spec"
)

func TestTinyFishSearchParamsAndFetchDualArrays(t *testing.T) {
	okPayload := providertest.LoadFixture(t, "search_ok.json")
	emptyPayload := providertest.LoadFixture(t, "search_empty.json")
	fetchErrPayload := providertest.LoadFixture(t, "error.json")

	var lastQuery url.Values
	emptyMode := false
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "tf-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		lastQuery = r.URL.Query()
		if emptyMode {
			_, _ = w.Write(emptyPayload)
			return
		}
		_, _ = w.Write(okPayload)
	}))
	defer searchSrv.Close()

	var lastFetchBody map[string]any
	fetchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.UnmarshalRead(r.Body, &lastFetchBody)
		_, _ = w.Write(fetchErrPayload)
	}))
	defer fetchSrv.Close()

	p, enabled, err := New(Options{
		Keys:      []string{"tf-key"},
		SearchURL: searchSrv.URL,
		FetchURL:  fetchSrv.URL,
		SearchQPS: 100,
		FetchQPS:  100,
	}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled {
		t.Fatalf("New: enabled=%v err=%v", enabled, err)
	}

	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	relFresh, _ := core.ParseFreshness("d7", now)
	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:    "go concurrency",
		Question: "best pattern for pipelines",
		Count:    5,
		Detail:   core.DetailSummary,
		Filters: core.Filters{
			Sites:     []string{"go.dev", "github.com"},
			Lang:      []string{"en"},
			Freshness: relFresh,
		},
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, out)
	if lastQuery.Get("recency_minutes") != "10080" || lastQuery.Get("after_date") != "" {
		t.Fatalf("expected recency_minutes=10080 without after_date, got %v", lastQuery)
	}
	if lastQuery.Get("purpose") != "best pattern for pipelines" || lastQuery.Get("include_domains") != "go.dev,github.com" {
		t.Fatalf("unexpected query params: %v", lastQuery)
	}
	if out.Cost != nil {
		t.Fatalf("expected nil default cost, got %+v", out.Cost)
	}

	// 绝对日期区间严禁混传 recency_minutes
	absFresh, _ := core.ParseFreshness("2026-08-01..2026-08-31", now)
	_, err = p.Search(context.Background(), &core.SearchRequest{
		Query:   "go concurrency",
		Filters: core.Filters{Freshness: absFresh},
	})
	if err != nil || lastQuery.Get("recency_minutes") != "" || lastQuery.Get("after_date") != "2026-08-01" {
		t.Fatalf("unexpected absolute date query params: %v, err=%v", lastQuery, err)
	}

	emptyMode = true
	eOut, err := p.Search(context.Background(), &core.SearchRequest{Query: "none"})
	if err != nil || len(eOut.Results) != 0 {
		t.Fatalf("unexpected empty result: %+v err=%v", eOut, err)
	}

	// 验证 Fetch HTTP 200 内 errors[] 小写 snake_case 枚举映射，以及 fresh -> ttl=0 与 per_url_timeout_ms 透传
	shortCtx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, err = p.Fetch(shortCtx, &core.FetchRequest{
		URL:     "https://spa.example.com",
		Options: map[string]string{spec.OptFetchFresh: "true"},
	})
	if se := core.AsError(err); se.Code != core.CodeBlockedByPolicy || se.HintKey != core.HintKeyChallengeDetected {
		t.Fatalf("expected bot_blocked -> blocked_by_policy with challenge hint, got %+v", se)
	}
	if ttlVal, ok := lastFetchBody["ttl"].(float64); !ok || ttlVal != 0 {
		t.Fatalf("expected fresh=true -> ttl=0 in wire body, got %+v", lastFetchBody)
	}
	if timeoutMS, ok := lastFetchBody["per_url_timeout_ms"].(float64); !ok || timeoutMS <= 0 || timeoutMS > 4000 {
		t.Fatalf("expected bounded per_url_timeout_ms in wire body, got %+v", lastFetchBody)
	}
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{
		URL:     "https://spa.example.com",
		Options: map[string]string{spec.OptTinyFishTTL: "-5"},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected negative tinyfish.ttl -> invalid_argument, got %v", err)
	}
	if got := unwrapRedirectURL("/url?q=https%3A%2F%2Fgo.dev%2Fdoc&sa=U"); got != "https://go.dev/doc" {
		t.Fatalf("expected /url?q= unwrapped, got %q", got)
	}

	// 验证 HTTP 429 RATE_LIMIT_EXCEEDED + details.reason="free_allowance_exhausted" 归不可重试 insufficient_balance（绝不退避重试）
	dailyHits := 0
	dailySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		dailyHits++
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"RATE_LIMIT_EXCEEDED","message":"daily free limit reached","details":{"reason":"free_allowance_exhausted","window_seconds":86400}}}`))
	}))
	defer dailySrv.Close()
	pDaily, _, err := New(Options{Keys: []string{"tf-daily"}, SearchURL: dailySrv.URL, FetchURL: fetchSrv.URL}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pDaily.Search(context.Background(), &core.SearchRequest{Query: "daily"}); core.AsError(err).Code != core.CodeInsufficientBalance || core.AsError(err).Retryable || dailyHits != 1 {
		t.Fatalf("expected non-retryable insufficient_balance in 1 hit, got err=%+v hits=%d", err, dailyHits)
	}
	if got := classifyError(http.StatusTooManyRequests, nil, []byte(`{"error":{"code":"RATE_LIMIT_EXCEEDED","message":"too many requests"}}`)); got.Code != core.CodeRateLimited {
		t.Fatalf("normal 429 RATE_LIMIT_EXCEEDED = %s, want rate_limited", got.Code)
	}
}
