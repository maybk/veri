package linkup

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/provider/providertest"
	"veri/internal/spec"
)

func TestLinkupSearchFetchAndDynamicRenderEscalation(t *testing.T) {
	searchOK := providertest.LoadFixture(t, "search_ok.json")
	fetchOK := providertest.LoadFixture(t, "fetch_ok.json")

	var gotSearch searchWireReq
	var gotFetch fetchWireReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/search":
			if err := json.UnmarshalRead(r.Body, &gotSearch); err != nil {
				t.Errorf("decode search: %v", err)
			}
			_, _ = w.Write(searchOK)
		case "/v1/fetch":
			if err := json.UnmarshalRead(r.Body, &gotFetch); err != nil {
				t.Errorf("decode fetch: %v", err)
			}
			_, _ = w.Write(fetchOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if p, enabled, err := New(Options{BaseURL: srv.URL}, providertest.NewTestAPIClient(t)); err != nil || enabled || p != nil {
		t.Fatalf("expected keyless Linkup to be disabled, got p=%v enabled=%v err=%v", p, enabled, err)
	}

	p, enabled, err := New(Options{
		Keys:        []string{"lk-test-key-123456"},
		BaseURL:     srv.URL,
		PriceSearch: "USD:0.005",
		PriceFetch:  "USD:0.001",
	}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled {
		t.Fatalf("New: enabled=%v err=%v", enabled, err)
	}

	// 1. Search：固定 outputType=searchResults、默认 depth=fast、过滤 image 结果、映射 name->Title
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	sOut, err := p.Search(context.Background(), &core.SearchRequest{
		Query:        "go 1.26 release",
		Count:        8,
		Detail:       core.DetailSummary,
		IncludeExtra: true,
		Filters: core.Filters{
			Sites:        []string{"go.dev"},
			ExcludeSites: []string{"spam.example"},
			Freshness:    &core.Freshness{After: after, Before: before},
		},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, sOut)
	if gotSearch.OutputType != outputTypeSearchResults || gotSearch.Depth != DefaultDepth || gotSearch.IncludeImages {
		t.Fatalf("unexpected search defaults: %+v", gotSearch)
	}
	if !slices.Equal(gotSearch.IncludeDomains, []string{"go.dev"}) || !slices.Equal(gotSearch.ExcludeDomains, []string{"spam.example"}) {
		t.Fatalf("unexpected domain filters: %+v", gotSearch)
	}
	if gotSearch.FromDate != "2026-01-01" || gotSearch.ToDate != "2026-11-01" {
		t.Fatalf("unexpected dates: from=%q to=%q", gotSearch.FromDate, gotSearch.ToDate)
	}
	if len(sOut.Results) != 1 || sOut.Results[0].Title != "Go 1.26 Release Notes" || sOut.Results[0].Icon != "https://go.dev/favicon.ico" {
		t.Fatalf("expected image filtered and name mapped to Title, got %+v", sOut.Results)
	}
	if sOut.Cost == nil || sOut.Cost.Micros != 5000 {
		t.Fatalf("expected search cost 5000 micros, got %+v", sOut.Cost)
	}

	// 2. Fetch：默认 renderJs=false；遇 DynamicRenderHint=true 自动升档 renderJs=true；首 H1 回填标题
	fOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://go.dev/doc/go1.26"})
	if err != nil {
		t.Fatalf("Fetch default: %v", err)
	}
	providertest.AssertFetchOutcomeInvariants(t, ProviderName, fOut)
	if gotFetch.Mode != spec.LinkupFetchModeStandard || gotFetch.RenderJS {
		t.Fatalf("expected standard static fetch by default, got %+v", gotFetch)
	}
	if fOut.Page.Title != "Go 1.26 Release Notes" {
		t.Fatalf("expected H1 title fallback, got %q", fOut.Page.Title)
	}

	if _, err := p.Fetch(context.Background(), &core.FetchRequest{
		URL:               "https://spa.example.com",
		DynamicRenderHint: true,
	}); err != nil {
		t.Fatalf("Fetch with DynamicRenderHint: %v", err)
	}
	if !gotFetch.RenderJS {
		t.Fatal("expected DynamicRenderHint=true to escalate renderJs=true")
	}

	if _, err := p.Fetch(context.Background(), &core.FetchRequest{
		URL:     "https://hard.example.com",
		Options: map[string]string{spec.OptLinkupFetchMode: "pro", spec.OptLinkupRenderJS: "false"},
	}); err != nil {
		t.Fatalf("Fetch explicit options: %v", err)
	}
	if gotFetch.Mode != spec.LinkupFetchModePro || gotFetch.RenderJS {
		t.Fatalf("expected explicit mode=pro renderJs=false, got %+v", gotFetch)
	}
}

func TestLinkupMultiMeaning400And429NoRetryOnCreditExhaustion(t *testing.T) {
	noResultBody := providertest.LoadFixture(t, "err_400_no_result.json")
	credit429Body := providertest.LoadFixture(t, "err_429_credit.json")
	rate429Body := providertest.LoadFixture(t, "err_429_rate.json")

	// 1. 400 SEARCH_QUERY_NO_RESULT -> Search 返回空结果且不计费、不报错
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/v1/search":
			var body searchWireReq
			_ = json.UnmarshalRead(r.Body, &body)
			switch body.Q {
			case "empty":
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write(noResultBody)
			case "credit":
				w.Header().Set("Retry-After", "1")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write(credit429Body)
			case "rate":
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write(rate429Body)
			}
		case "/v1/fetch":
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"statusCode":400,"error":{"code":"FETCH_TARGET_NOT_FOUND","message":"Target URL was not found"}}`))
		}
	}))
	defer srv.Close()

	p, _, err := New(Options{Keys: []string{"lk-key-1"}, BaseURL: srv.URL, PriceSearch: "USD:0.005"}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}

	emptyOut, err := p.Search(context.Background(), &core.SearchRequest{Query: "empty", Count: 5, Detail: core.DetailSnippet})
	if err != nil || len(emptyOut.Results) != 0 || emptyOut.Cost != nil {
		t.Fatalf("SEARCH_QUERY_NO_RESULT must normalize to empty free result, got out=%+v err=%v", emptyOut, err)
	}

	// 2. 429 INSUFFICIENT_FUNDS_CREDITS（即使带 Retry-After）也必须映射到 CodeInsufficientBalance 且绝不重试（仅 1 次请求）
	beforeHits := hits.Load()
	_, err = p.Search(context.Background(), &core.SearchRequest{Query: "credit", Count: 5, Detail: core.DetailSnippet})
	if se := core.AsError(err); se.Code != core.CodeInsufficientBalance || se.Retryable {
		t.Fatalf("expected non-retryable insufficient_balance, got %+v", se)
	}
	if delta := hits.Load() - beforeHits; delta != 1 {
		t.Fatalf("429 INSUFFICIENT_FUNDS_CREDITS must NOT retry; hits=%d", delta)
	}

	// 3. 429 EXCEED_BUDGET_LIMIT -> 同样不可重试 insufficient_balance；TOO_MANY_REQUESTS -> rate_limited
	if got := classifySearchError(http.StatusTooManyRequests, nil, []byte(`{"statusCode":429,"error":{"code":"EXCEED_BUDGET_LIMIT","message":"API key budget reached"}}`)); got.Code != core.CodeInsufficientBalance || got.Retryable {
		t.Fatalf("429 EXCEED_BUDGET_LIMIT = %+v, want non-retryable insufficient_balance", got)
	}
	if got := classifySearchError(http.StatusTooManyRequests, nil, rate429Body); got.Code != core.CodeRateLimited {
		t.Fatalf("429 rate limit = %s, want rate_limited", got.Code)
	}

	// 4. 403 IP_NOT_WHITELISTED / TASK_TYPE_NOT_SUPPORTED 绝不误标 auth_failed
	if got := classifySearchError(http.StatusForbidden, nil, []byte(`{"statusCode":403,"error":{"code":"IP_NOT_WHITELISTED","message":"IP rejected"}}`)); got.Code != core.CodeBlockedByPolicy {
		t.Fatalf("403 IP_NOT_WHITELISTED = %s, want blocked_by_policy", got.Code)
	}
	if got := classifySearchError(http.StatusForbidden, nil, []byte(`{"statusCode":403,"error":{"code":"TASK_TYPE_NOT_SUPPORTED","message":"not enabled"}}`)); got.Code != core.CodeNotEnabled {
		t.Fatalf("403 TASK_TYPE_NOT_SUPPORTED = %s, want not_enabled", got.Code)
	}

	// 5. Fetch 400 官方 FETCH_* 线协议错误码分流
	p2, _, err := New(Options{Keys: []string{"lk-key-2"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p2.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/missing"}); core.AsError(err).Code != core.CodeNotFound {
		t.Fatalf("expected Fetch 400 FETCH_TARGET_NOT_FOUND -> not_found, got %v", err)
	}
	for _, wireCode := range []string{"FETCH_ERROR", "FETCH_RESPONSE_TOO_LARGE", "FETCH_TARGET_UNREACHABLE", "FETCH_UNSUPPORTED_CONTENT_TYPE", "FETCH_URL_IS_FILE"} {
		body := []byte(`{"statusCode":400,"error":{"code":"` + wireCode + `","message":"fetch failed"}}`)
		if got := classifyFetchError(http.StatusBadRequest, nil, body); got.Code != core.CodeContentUnavailable {
			t.Fatalf("Fetch 400 %s = %s, want content_unavailable", wireCode, got.Code)
		}
	}
	if got := classifyFetchError(http.StatusBadRequest, nil, []byte(`{"statusCode":400,"error":{"code":"VALIDATION_ERROR","message":"invalid url"}}`)); got.Code != core.CodeInvalidArgument {
		t.Fatalf("Fetch 400 VALIDATION_ERROR = %s, want invalid_argument", got.Code)
	}

	// 6. 直接调用时路径排除项绝不误扩为整站域名；上游返回非空结果即便 URL 清洗被丢弃也按实扣费记账
	var gotReq searchWireReq
	billSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = searchWireReq{}
		_ = json.UnmarshalRead(r.Body, &gotReq)
		_, _ = w.Write([]byte(`{"results":[{"type":"text","name":"Bad","url":"javascript:alert(1)","content":"x"}]}`))
	}))
	defer billSrv.Close()
	pBill, _, err := New(Options{Keys: []string{"lk-bill"}, BaseURL: billSrv.URL, PriceSearch: "USD:0.005"}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	bOut, err := pBill.Search(context.Background(), &core.SearchRequest{
		Query:   "go",
		Count:   3,
		Detail:  core.DetailSnippet,
		Filters: core.Filters{Sites: []string{"go.dev/doc"}, ExcludeSites: []string{"reddit.com", "github.com/spam-org"}},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if !slices.Equal(gotReq.IncludeDomains, []string{"go.dev"}) || !slices.Equal(gotReq.ExcludeDomains, []string{"reddit.com"}) {
		t.Fatalf("unexpected domain filters sent to Linkup: inc=%v exc=%v", gotReq.IncludeDomains, gotReq.ExcludeDomains)
	}
	if bOut.Cost == nil || bOut.Cost.Micros != 5000 {
		t.Fatalf("expected cost recorded when upstream returned results, got %+v", bOut.Cost)
	}
	if _, err := pBill.Search(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected nil SearchRequest -> invalid_argument, got %v", err)
	}
	if _, err := pBill.Fetch(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected nil FetchRequest -> invalid_argument, got %v", err)
	}
}
