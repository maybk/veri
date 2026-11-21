package keenable

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/provider/providertest"
	"veri/internal/spec"
)

func TestKeenableSearchFetchContractsAndUsageCost(t *testing.T) {
	searchOK := providertest.LoadFixture(t, "search_ok.json")
	fetchOK := providertest.LoadFixture(t, "fetch_ok.json")

	var gotSearchReq searchWireReq
	var gotFetchQuery string
	var gotKeyHeader string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKeyHeader = r.Header.Get(spec.HeaderAPIKey)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/search":
			gotSearchReq = searchWireReq{}
			if err := json.UnmarshalRead(r.Body, &gotSearchReq); err != nil {
				t.Errorf("decode search request: %v", err)
			}
			w.Header().Set("X-Request-Id", "keen-s-1")
			_, _ = w.Write(searchOK)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/fetch":
			gotFetchQuery = r.URL.RawQuery
			w.Header().Set("X-Request-Id", "keen-f-1")
			_, _ = w.Write(fetchOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	// 未配置密钥时静默禁用，绝不启用免 Key 通道
	if p, enabled, err := New(Options{BaseURL: srv.URL}, providertest.NewTestAPIClient(t)); err != nil || enabled || p != nil {
		t.Fatalf("expected keyless Keenable to be disabled, got p=%v enabled=%v err=%v", p, enabled, err)
	}

	p, enabled, err := New(Options{
		Keys:        []string{"keen_test_key_123456"},
		BaseURL:     srv.URL,
		PriceCredit: "USD:0.004",
	}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled {
		t.Fatalf("New: enabled=%v err=%v", enabled, err)
	}

	// 1. 多站点 + 含路径排除项 + 时间窗 + realtime 覆盖 + 免费额度内 paid=false 记零成本
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	sOut, err := p.Search(context.Background(), &core.SearchRequest{
		Query:  "golang mcp sdk",
		Count:  5,
		Detail: core.DetailSummary,
		Filters: core.Filters{
			Sites:        []string{"github.com", "go.dev"},
			ExcludeSites: []string{"medium.com", "un.org/press"},
			Freshness:    &core.Freshness{After: after, Before: before},
		},
		Options: map[string]string{spec.OptKeenableMode: "realtime"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, sOut)
	if gotKeyHeader != "keen_test_key_123456" {
		t.Fatalf("expected X-API-Key header, got %q", gotKeyHeader)
	}
	if !gotSearchReq.DisableCards {
		t.Fatal("expected disable_cards=true by default")
	}
	if gotSearchReq.Mode != "realtime" || gotSearchReq.Site != "" {
		t.Fatalf("unexpected mode/site: %+v", gotSearchReq)
	}
	if !strings.Contains(gotSearchReq.Query, "(site:github.com OR site:go.dev)") || !strings.Contains(gotSearchReq.Query, "-site:un.org/press") {
		t.Fatalf("unexpected composed query: %q", gotSearchReq.Query)
	}
	if !slices.Equal(gotSearchReq.ExcludeDomains, []string{"medium.com"}) {
		t.Fatalf("expected exclude_domains=[medium.com], got %v", gotSearchReq.ExcludeDomains)
	}
	if gotSearchReq.PublishedAfter != "2026-01-01" || gotSearchReq.PublishedBefore != "2026-11-01" {
		t.Fatalf("unexpected date filters: after=%q before=%q", gotSearchReq.PublishedAfter, gotSearchReq.PublishedBefore)
	}
	if gotSearchReq.SnippetMaxLength != snippetLenSummary {
		t.Fatalf("snippet_max_length = %d, want %d", gotSearchReq.SnippetMaxLength, snippetLenSummary)
	}
	if sOut.Cost != nil {
		t.Fatalf("expected nil Cost when usage.paid=false, got %+v", sOut.Cost)
	}

	// 2. 纯域名单站点走原生 site 字段；含路径单站点走 query site: 防触发上游 400
	if _, err := p.Search(context.Background(), &core.SearchRequest{
		Query:   "effective go",
		Count:   3,
		Detail:  core.DetailSnippet,
		Filters: core.Filters{Sites: []string{"go.dev"}},
	}); err != nil {
		t.Fatalf("single host site Search: %v", err)
	}
	if gotSearchReq.Site != "go.dev" || gotSearchReq.SnippetMaxLength != snippetLenSnippet {
		t.Fatalf("expected native site=go.dev and snippet cap, got %+v", gotSearchReq)
	}
	if _, err := p.Search(context.Background(), &core.SearchRequest{
		Query:   "effective go",
		Count:   3,
		Detail:  core.DetailSnippet,
		Filters: core.Filters{Sites: []string{"go.dev/doc"}},
	}); err != nil {
		t.Fatalf("single path site Search: %v", err)
	}
	if gotSearchReq.Site != "" || !strings.Contains(gotSearchReq.Query, "site:go.dev/doc") {
		t.Fatalf("expected path-scoped single site routed to query site:, got %+v", gotSearchReq)
	}

	// 2b. usage.paid 缺省（nil）但 credits>0 时仍按估算计费；显式 paid=false 才免计费
	oneCredit := 1.0
	if c := p.costFromUsage(&wireUsage{Credits: &oneCredit}); c == nil || c.Micros != 4000 {
		t.Fatalf("expected omitted usage.paid to still estimate 4000 micros, got %+v", c)
	}
	if c := p.costFromUsage(&wireUsage{Credits: &oneCredit, Paid: new(false)}); c != nil {
		t.Fatalf("expected explicit usage.paid=false -> nil cost, got %+v", c)
	}
	if _, err := p.Search(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected nil SearchRequest -> invalid_argument, got %v", err)
	}
	if _, err := p.Fetch(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected nil FetchRequest -> invalid_argument, got %v", err)
	}

	// 3. Fetch GET + --fresh -> live=true + 旧版 Unix 秒 published_at + paid=true 计费
	fOut, err := p.Fetch(context.Background(), &core.FetchRequest{
		URL:     "https://golang.org/doc/effective_go",
		Options: map[string]string{spec.OptFetchFresh: "true"},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	providertest.AssertFetchOutcomeInvariants(t, ProviderName, fOut)
	if !strings.Contains(gotFetchQuery, "live=true") || !strings.Contains(gotFetchQuery, "max_chars=500000") {
		t.Fatalf("unexpected fetch query: %q", gotFetchQuery)
	}
	if fOut.Page.FinalURL != "https://go.dev/doc/effective_go" || fOut.Page.Author != "The Go Authors" || fOut.Page.Published == nil {
		t.Fatalf("unexpected fetch page metadata: %+v", fOut.Page)
	}
	if fOut.Cost == nil || fOut.Cost.Micros != 4000 {
		t.Fatalf("expected paid fetch cost 4000 micros, got %+v", fOut.Cost)
	}
}

func TestKeenable403SplitAndErrorClassification(t *testing.T) {
	permBody := providertest.LoadFixture(t, "err_403_perm.json")
	targetBody := providertest.LoadFixture(t, "err_403_target.json")

	// permission_error 403 精细拆分：credentials_forbidden 隔离密钥；private_url_not_allowed 归策略拒绝；功能未开通归 not_enabled
	if got := classifyError(http.StatusForbidden, nil, permBody); got.Code != core.CodeAuthFailed {
		t.Fatalf("403 permission_error(credentials_forbidden) = %s, want auth_failed", got.Code)
	}
	if got := classifyError(http.StatusForbidden, nil, []byte(`{"type":"permission_error","code":"private_url_not_allowed","message":"private address blocked"}`)); got.Code != core.CodeBlockedByPolicy {
		t.Fatalf("403 private_url_not_allowed = %s, want blocked_by_policy (must not poison key)", got.Code)
	}
	if got := classifyError(http.StatusForbidden, nil, []byte(`{"type":"permission_error","code":"time_machine_not_enabled","message":"Time Machine not enabled"}`)); got.Code != core.CodeNotEnabled {
		t.Fatalf("403 time_machine_not_enabled = %s, want not_enabled (must not poison key)", got.Code)
	}
	if got := classifyError(http.StatusForbidden, nil, targetBody); got.Code != core.CodeContentUnavailable || got.HintKey != core.HintKeyChallengeDetected {
		t.Fatalf("403 target_error = %+v, want content_unavailable with challenge hint", got)
	}
	if got := classifyError(http.StatusPaymentRequired, nil, []byte(`{"type":"billing_error","code":"insufficient_credits","message":"exhausted"}`)); got.Code != core.CodeInsufficientBalance {
		t.Fatalf("402 billing_error = %s, want insufficient_balance", got.Code)
	}
	if got := classifyError(http.StatusNotFound, nil, []byte(`{"type":"target_error","code":"page_not_found","message":"page missing"}`)); got.Code != core.CodeNotFound {
		t.Fatalf("404 target_error = %s, want not_found", got.Code)
	}
	if got := classifyError(http.StatusNotFound, nil, []byte(`{"type":"invalid_request_error","code":"card_expired","message":"card expired"}`)); got.Code != core.CodeNotFound {
		t.Fatalf("404 invalid_request_error(card_expired) = %s, want not_found", got.Code)
	}
	if got := classifyError(http.StatusTooManyRequests, nil, []byte(`{"type":"rate_limit_error","code":"rate_limit_per_second","message":"slow down","retryable":true}`)); got.Code != core.CodeRateLimited || !got.Retryable {
		t.Fatalf("429 rate_limit_error = %+v, want retryable rate_limited", got)
	}
	if got := classifyError(http.StatusInternalServerError, nil, []byte(`{"type":"api_error","code":"billing_account_not_found","message":"missing record","retryable":false}`)); got.Code != core.CodeUpstreamUnavailable || got.Retryable {
		t.Fatalf("500 retryable=false = %+v, want non-retryable upstream_unavailable", got)
	}

	// 验证目标站 403 target_error 不会把 KeyPool 里的唯一密钥标记为鉴权失效
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		if hits == 1 {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(targetBody)
			return
		}
		_, _ = w.Write(providertest.LoadFixture(t, "fetch_ok.json"))
	}))
	defer srv.Close()

	p, _, err := New(Options{Keys: []string{"keen_single_key_12345"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://blocked.example.com"}); core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("first fetch expected content_unavailable, got %v", err)
	}
	if out, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://go.dev/doc/effective_go"}); err != nil || out == nil {
		t.Fatalf("second fetch must succeed without key poisoning, got err=%v", err)
	}

	// 验证上游 truncated=true 时发出清晰截断告警
	truncSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"url":"https://example.com/long","title":"Long Doc","content":"# Long\n\nBody text","truncated":true,"total_chars":105000}`))
	}))
	defer truncSrv.Close()
	pTrunc, _, err := New(Options{Keys: []string{"keen_trunc_key_12345"}, BaseURL: truncSrv.URL}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	tOut, err := pTrunc.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/long"})
	if err != nil || len(tOut.Warnings) == 0 || !strings.Contains(tOut.Warnings[0], "105000") {
		t.Fatalf("expected upstream truncation warning with full page size, got out=%+v err=%v", tOut, err)
	}
}
