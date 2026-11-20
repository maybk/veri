package firecrawl

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/provider/providertest"
	"veri/internal/spec"
)

func TestFirecrawlCloudKeyGuardAndSelfHostKeyless(t *testing.T) {
	cli := providertest.NewTestAPIClient(t)

	// 官方云端及其大小写/端口变形均必须配置 Key，缺 Key 静默禁用绝不走官方免 Key 公共池
	for _, cloudURL := range []string{
		"",
		DefaultBaseURL,
		"https://API.FIRECRAWL.DEV:443/",
		"https://firecrawl.dev",
	} {
		if p, enabled, err := New(Options{BaseURL: cloudURL}, cli); err != nil || enabled || p != nil {
			t.Fatalf("official cloud URL %q without key must stay disabled, got p=%v enabled=%v err=%v", cloudURL, p, enabled, err)
		}
	}

	scrapeOK := providertest.LoadFixture(t, "scrape_ok.json")
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get(spec.HeaderAuthorization)
		_, _ = w.Write(scrapeOK)
	}))
	defer srv.Close()

	// 本地回环自托管实例允许无 Key 启用且不发 Authorization 头
	p, enabled, err := New(Options{BaseURL: srv.URL}, cli)
	if err != nil || !enabled || !p.IsSelfHosted() || p.HasAPIKey() {
		t.Fatalf("expected self-hosted keyless enabled, got enabled=%v selfHost=%v hasKey=%v err=%v", enabled, p != nil && p.IsSelfHosted(), p != nil && p.HasAPIKey(), err)
	}
	out, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://docs.firecrawl.dev/scrape"})
	if err != nil {
		t.Fatalf("self-host Fetch: %v", err)
	}
	providertest.AssertFetchOutcomeInvariants(t, ProviderName, out)
	if gotAuth != "" {
		t.Fatalf("expected no Authorization header for keyless self-host, got %q", gotAuth)
	}
	if out.Page.FinalURL != "https://docs.firecrawl.dev/features/scrape" {
		t.Fatalf("unexpected FinalURL: %q", out.Page.FinalURL)
	}
}

func TestFirecrawlScrapeContractsDoubleStatusAndArrayMeta(t *testing.T) {
	scrapeOK := providertest.LoadFixture(t, "scrape_ok.json")
	scrapeArray := providertest.LoadFixture(t, "scrape_array_meta.json")
	scrape404 := providertest.LoadFixture(t, "scrape_404_inner.json")
	err402 := providertest.LoadFixture(t, "err_402.json")

	var gotReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = nil
		_ = json.UnmarshalRead(r.Body, &gotReq)
		target, _ := gotReq["url"].(string)
		switch target {
		case "https://example.com/array-meta":
			_, _ = w.Write(scrapeArray)
		case "https://example.com/missing":
			_, _ = w.Write(scrape404)
		case "https://example.com/blocked":
			_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"Just a moment...","metadata":{"statusCode":403,"error":"Access denied"}}}`))
		case "https://example.com/quota":
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write(err402)
		default:
			_, _ = w.Write(scrapeOK)
		}
	}))
	defer srv.Close()

	p, _, err := New(Options{
		Keys:        []string{"fc-test-key-123456"},
		BaseURL:     srv.URL,
		PriceCredit: "USD:0.005",
	}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}

	// 1. 常规抓取 + --fresh -> maxAge=0 + max_pages 覆盖 + 成本估算
	out, err := p.Fetch(context.Background(), &core.FetchRequest{
		URL: "https://docs.firecrawl.dev/scrape",
		Options: map[string]string{
			spec.OptFetchFresh:        "true",
			spec.OptFirecrawlMaxPages: "8",
			spec.OptFirecrawlProxy:    "enhanced",
		},
	})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotReq["proxy"] != "enhanced" || gotReq["maxAge"] != float64(0) {
		t.Fatalf("unexpected scrape options: %+v", gotReq)
	}
	parsers, _ := gotReq["parsers"].([]any)
	if len(parsers) != 1 {
		t.Fatalf("expected 1 pdf parser cap, got %+v", gotReq["parsers"])
	}
	pdfSpec, _ := parsers[0].(map[string]any)
	if pdfSpec["maxPages"] != float64(8) {
		t.Fatalf("expected maxPages=8, got %+v", pdfSpec)
	}
	if out.Cost == nil || out.Cost.Micros != 5000 {
		t.Fatalf("expected 1-credit scrape cost 5000 micros, got %+v", out.Cost)
	}

	// 2. 多态 metadata.title=[]string 解码
	arrOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/array-meta"})
	if err != nil {
		t.Fatalf("Fetch array meta: %v", err)
	}
	if arrOut.Page.Title != "Array Title Primary" {
		t.Fatalf("expected first element of array title, got %q", arrOut.Page.Title)
	}

	// 3. 双层状态检查：外层 200 内层 404 -> CodeNotFound；内层 403 -> CodeContentUnavailable + HintKeyChallengeDetected
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/missing"}); core.AsError(err).Code != core.CodeNotFound {
		t.Fatalf("expected inner 404 -> not_found, got %v", err)
	}
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/blocked"}); err == nil {
		t.Fatal("expected inner 403 error")
	} else if se := core.AsError(err); se.Code != core.CodeContentUnavailable || se.HintKey != core.HintKeyChallengeDetected {
		t.Fatalf("expected inner 403 -> content_unavailable + challenge hint, got %+v", se)
	}

	// 4. 402 INSUFFICIENT_CREDITS -> CodeInsufficientBalance；内层目标 500 -> content_unavailable（不误熔断 Firecrawl）；
	// 外层 403 精确按官方 errors 表拆分：标准 "Forbidden" 归 auth_failed，UNSUPPORTED_SITE / 策略文案归 blocked_by_policy，THIRD_PARTY_DATA_* 归 not_enabled/content_unavailable
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/quota"}); core.AsError(err).Code != core.CodeInsufficientBalance {
		t.Fatalf("expected 402 -> insufficient_balance, got %v", err)
	}
	if got := checkInnerStatus(http.StatusInternalServerError, "origin 500"); got.Code != core.CodeContentUnavailable {
		t.Fatalf("inner origin 500 must map to content_unavailable, got %+v", got)
	}
	if got := classifyError(http.StatusForbidden, nil, []byte(`{"success":false,"error":"Forbidden"}`)); got.Code != core.CodeAuthFailed {
		t.Fatalf("403 bare Forbidden (key permission) must map to auth_failed, got %+v", got)
	}
	if got := classifyError(http.StatusForbidden, nil, []byte(`{"success":false,"code":"UNSUPPORTED_SITE","error":"We apologize for the inconvenience but we do not support this site"}`)); got.Code != core.CodeBlockedByPolicy {
		t.Fatalf("403 UNSUPPORTED_SITE must map to blocked_by_policy without poisoning key, got %+v", got)
	}
	if got := classifyError(http.StatusForbidden, nil, []byte(`{"success":false,"error":"Website access forbidden by policy"}`)); got.Code != core.CodeBlockedByPolicy {
		t.Fatalf("403 policy rejection must map to blocked_by_policy without poisoning key, got %+v", got)
	}
	if got := classifyError(http.StatusForbidden, nil, []byte(`{"success":false,"code":"THIRD_PARTY_DATA_NOT_ENABLED","error":"provider disabled"}`)); got.Code != core.CodeNotEnabled {
		t.Fatalf("403 THIRD_PARTY_DATA_NOT_ENABLED must map to not_enabled, got %+v", got)
	}
	if got := classifyError(http.StatusBadRequest, nil, []byte(`{"success":false,"code":"THIRD_PARTY_DATA_UNSUPPORTED_URL","error":"sub-page unsupported"}`)); got.Code != core.CodeContentUnavailable {
		t.Fatalf("400 THIRD_PARTY_DATA_UNSUPPORTED_URL must map to content_unavailable, got %+v", got)
	}

	// 5. PDF 按页计费与 maxPages 截断透明告警
	pdfSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"# Spec PDF\n\nContent","metadata":{"statusCode":200,"numPages":10,"totalPages":42}}}`))
	}))
	defer pdfSrv.Close()
	pPDF, _, err := New(Options{Keys: []string{"fc-pdf-key"}, BaseURL: pdfSrv.URL, PriceCredit: "USD:0.005"}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	pdfOut, err := pPDF.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/spec.pdf"})
	if err != nil {
		t.Fatalf("PDF Fetch: %v", err)
	}
	if pdfOut.Cost == nil || pdfOut.Cost.Micros != 50000 {
		t.Fatalf("expected 10-page PDF cost 50000 micros, got %+v", pdfOut.Cost)
	}
	if len(pdfOut.Warnings) == 0 {
		t.Fatal("expected PDF truncation warning when totalPages > numPages")
	}
}

func TestFirecrawlSearchGuardsAndCategoryValidation(t *testing.T) {
	searchOK := providertest.LoadFixture(t, "search_ok.json")
	var gotRaw map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRaw = nil
		_ = json.UnmarshalRead(r.Body, &gotRaw)
		_, _ = w.Write(searchOK)
	}))
	defer srv.Close()

	p, _, err := New(Options{Keys: []string{"fc-key-1"}, BaseURL: srv.URL, PriceCredit: "USD:0.005"}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}

	after := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	before := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:        "effective go",
		Count:        20, // 应被安全上限截断为 10
		Detail:       core.DetailSummary,
		IncludeExtra: true,
		Filters: core.Filters{
			Sites:     []string{"go.dev/doc"},
			Freshness: &core.Freshness{After: after, Before: before},
		},
		Options: map[string]string{spec.OptFirecrawlCategory: "research,pdf"},
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, out)
	if _, hasScrape := gotRaw["scrapeOptions"]; hasScrape {
		t.Fatal("Firecrawl Search must never send scrapeOptions by default")
	}
	if gotRaw["limit"] != float64(maxSearchCount) {
		t.Fatalf("expected limit capped to %d, got %v", maxSearchCount, gotRaw["limit"])
	}
	if gotRaw["tbs"] != "cdr:1,cd_min:03/01/2026,cd_max:11/01/2026" {
		t.Fatalf("unexpected tbs: %v", gotRaw["tbs"])
	}
	if inc, _ := gotRaw["includeDomains"].([]any); !slices.Equal(anyToStrings(inc), []string{"go.dev"}) {
		t.Fatalf("expected hostname-only includeDomains=[go.dev], got %+v", gotRaw["includeDomains"])
	}
	if out.Cost == nil || out.Cost.Micros != 10000 {
		t.Fatalf("expected 2-credit search cost 10000 micros, got %+v", out.Cost)
	}

	// 校验已移除的 github 类别与 developer / gov 独享类别混用拦截
	for _, badCat := range []string{"github", "gov,research", "developer,research", "unknown"} {
		if _, err := p.Search(context.Background(), &core.SearchRequest{
			Query:   "go",
			Count:   5,
			Detail:  core.DetailSnippet,
			Options: map[string]string{spec.OptFirecrawlCategory: badCat},
		}); core.AsError(err).Code != core.CodeInvalidArgument {
			t.Fatalf("expected invalid_argument for category %q, got %v", badCat, err)
		}
	}

	// 校验响应携带真实 creditsUsed 与 warning 时优先采信上游计费并透传告警
	creditsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"success":true,"creditsUsed":4,"warning":"partial results","data":{"web":[{"url":"https://go.dev","title":"Go","description":"The Go Programming Language"}]}}`))
	}))
	defer creditsSrv.Close()
	pCredits, _, err := New(Options{Keys: []string{"fc-key-credits"}, BaseURL: creditsSrv.URL, PriceCredit: "USD:0.005"}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	cOut, err := pCredits.Search(context.Background(), &core.SearchRequest{Query: "golang", Count: 5, Detail: core.DetailSnippet})
	if err != nil {
		t.Fatalf("Search creditsUsed: %v", err)
	}
	if cOut.Cost == nil || cOut.Cost.Micros != 20000 {
		t.Fatalf("expected creditsUsed=4 -> 20000 micros, got %+v", cOut.Cost)
	}
	if len(cOut.Warnings) == 0 {
		t.Fatal("expected upstream warning propagated")
	}

	// 校验直接调用时含路径 ExcludeSites 绝不被截断成裸域名发往 excludeDomains
	var gotExclRaw map[string]any
	exclSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotExclRaw = nil
		_ = json.UnmarshalRead(r.Body, &gotExclRaw)
		_, _ = w.Write(searchOK)
	}))
	defer exclSrv.Close()
	pExcl, _, err := New(Options{Keys: []string{"fc-excl"}, BaseURL: exclSrv.URL}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pExcl.Search(context.Background(), &core.SearchRequest{
		Query:   "golang",
		Count:   5,
		Detail:  core.DetailSnippet,
		Filters: core.Filters{ExcludeSites: []string{"reddit.com", "github.com/spam-org"}},
	}); err != nil {
		t.Fatalf("exclude search: %v", err)
	}
	if exc, _ := gotExclRaw["excludeDomains"].([]any); !slices.Equal(anyToStrings(exc), []string{"reddit.com"}) {
		t.Fatalf("path-scoped exclude must NOT widen to bare host github.com, got %+v", gotExclRaw["excludeDomains"])
	}
}

func TestFirecrawlInspectBodyKeyFailoverAndDynamicRenderHint(t *testing.T) {
	// 1. HTTP 200 内嵌 success=false + INSUFFICIENT_CREDITS 经 InspectBody 触发多 Key 故障切换
	var authSeen []string
	var lastProxy string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		authSeen = append(authSeen, auth)
		var payload map[string]any
		_ = json.UnmarshalRead(r.Body, &payload)
		if p, ok := payload["proxy"].(string); ok {
			lastProxy = p
		}
		if auth == "Bearer fc-empty-key" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"success":false,"code":"INSUFFICIENT_CREDITS","error":"Insufficient credits"}`))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"success":true,"data":{"markdown":"# OK\n\nContent.","metadata":{"title":"OK","statusCode":200}}}`))
	}))
	defer srv.Close()

	p, _, err := New(Options{
		Keys:    []string{"fc-empty-key", "fc-healthy-key"},
		BaseURL: srv.URL,
		Proxy:   spec.FirecrawlProxyBasic,
	}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Fetch(context.Background(), &core.FetchRequest{
		URL:               "https://protected.example.com",
		DynamicRenderHint: true,
	})
	if err != nil || out.Page == nil {
		t.Fatalf("expected failover to healthy key on HTTP 200 success=false, got out=%+v err=%v", out, err)
	}
	if !slices.Equal(authSeen, []string{"Bearer fc-empty-key", "Bearer fc-healthy-key"}) {
		t.Fatalf("expected key failover sequence, got %v", authSeen)
	}
	if lastProxy != spec.FirecrawlProxyAuto {
		t.Fatalf("expected DynamicRenderHint to upgrade basic -> auto proxy, got %q", lastProxy)
	}
	if _, err := p.Search(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected nil SearchRequest -> invalid_argument, got %v", err)
	}
	if _, err := p.Fetch(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected nil FetchRequest -> invalid_argument, got %v", err)
	}
}

func anyToStrings(items []any) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
