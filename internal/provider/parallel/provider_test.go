package parallel

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
)

func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func newTestClient(t *testing.T) *httpx.Client {
	t.Helper()
	cli, err := httpx.NewAPIClient(httpx.Options{AllowPrivate: true})
	if err != nil {
		t.Fatalf("new test client: %v", err)
	}
	return cli
}

func TestNewValidationAndCapabilities(t *testing.T) {
	cli := newTestClient(t)

	if p, ok, err := New(Options{}, cli); err != nil || ok || p != nil {
		t.Fatalf("expected disabled without keys, got p=%v ok=%v err=%v", p, ok, err)
	}
	if _, _, err := New(Options{Keys: []string{"k1"}}, nil); core.AsError(err).Code != core.CodeInternal {
		t.Fatalf("expected CodeInternal for nil HTTP client, got %v", err)
	}
	if _, _, err := New(Options{Keys: []string{"k1"}, Mode: "ultra"}, cli); err == nil {
		t.Fatal("expected error for invalid mode")
	}

	p, ok, err := New(Options{Keys: []string{"k1"}}, cli)
	if err != nil || !ok {
		t.Fatalf("New failed: ok=%v err=%v", ok, err)
	}
	if p.Name() != spec.ProviderParallel || p.BaseURL() != DefaultBaseURL {
		t.Fatalf("unexpected identity: name=%q base=%q", p.Name(), p.BaseURL())
	}

	caps := p.Capabilities()
	if caps.MaxCount != 20 || caps.Sites != core.SiteMulti || caps.MaxSites != 100 || caps.ExcludeSites != 100 {
		t.Fatalf("unexpected caps: %+v", caps)
	}
	if caps.SiteHostOnly || caps.Freshness != core.TimeAfterOnly || caps.Lang || caps.Content != core.ContentFree || !caps.Question {
		t.Fatalf("unexpected default mode caps: %+v", caps)
	}

	turboCaps := p.CapabilitiesFor(&core.SearchRequest{Options: map[string]string{spec.OptParallelMode: spec.ParallelModeTurbo}})
	if !turboCaps.SiteHostOnly {
		t.Fatalf("expected turbo mode to declare SiteHostOnly=true, got %+v", turboCaps)
	}

	prof := p.FetchProfile()
	if !prof.PDF || !prof.DynamicRender {
		t.Fatalf("expected PDF+DynamicRender profile, got %+v", prof)
	}
}

func TestSearchRequestContractAndResponseMapping(t *testing.T) {
	fixture := loadFixture(t, "search_ok.json")
	var gotKey string
	var gotReq searchWireReq

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != searchEndpointPath {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotKey = r.Header.Get(spec.HeaderAPIKey)
		gotReq = searchWireReq{}
		if err := json.UnmarshalRead(r.Body, &gotReq); err != nil {
			t.Errorf("unmarshal req: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	p, ok, err := New(Options{
		Keys:        []string{"parallel-test-key"},
		BaseURL:     srv.URL,
		PriceSearch: "USD:0.001",
	}, newTestClient(t))
	if err != nil || !ok {
		t.Fatalf("New failed: ok=%v err=%v", ok, err)
	}

	after := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:    "python asyncio run",
		Question: "How do I run top-level coroutines in Python asyncio?",
		Count:    12,
		Detail:   core.DetailContent,
		Filters: core.Filters{
			Sites:        []string{"https://www.Docs.Python.org/3/Library/", "*.gov"},
			ExcludeSites: []string{"example.com/Archive", "*.spam.org"},
			Freshness:    &core.Freshness{After: after},
		},
		Options: map[string]string{
			spec.OptParallelLocation: "US",
		},
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if gotKey != "parallel-test-key" {
		t.Fatalf("expected x-api-key header, got %q", gotKey)
	}
	if !slices.Equal(gotReq.SearchQueries, []string{"python asyncio run"}) {
		t.Fatalf("unexpected search_queries: %v", gotReq.SearchQueries)
	}
	if gotReq.Mode != spec.ParallelModeFast {
		t.Fatalf("expected default mode=fast, got %q", gotReq.Mode)
	}
	if gotReq.Objective != "How do I run top-level coroutines in Python asyncio?" {
		t.Fatalf("unexpected objective: %q", gotReq.Objective)
	}
	if gotReq.AdvancedSettings == nil || gotReq.AdvancedSettings.MaxResults != 12 || gotReq.AdvancedSettings.Location != "us" {
		t.Fatalf("unexpected advanced_settings: %+v", gotReq.AdvancedSettings)
	}
	sp := gotReq.AdvancedSettings.SourcePolicy
	if sp == nil || sp.AfterDate != "2026-06-01" {
		t.Fatalf("unexpected source_policy: %+v", sp)
	}
	// host 小写并剥离 www.，path 严格保持原大小写；*.gov 保留 .gov，*.spam.org 转为 spam.org
	if !slices.Equal(sp.IncludeDomains, []string{"docs.python.org/3/Library", ".gov"}) {
		t.Fatalf("unexpected include_domains: %v", sp.IncludeDomains)
	}
	if !slices.Equal(sp.ExcludeDomains, []string{"example.com/Archive", "spam.org"}) {
		t.Fatalf("unexpected exclude_domains: %v", sp.ExcludeDomains)
	}
	if len(out.Results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(out.Results))
	}
	if out.RequestID != "search_019a0000000000000000000001" {
		t.Fatalf("unexpected request_id: %q", out.RequestID)
	}
	if out.Results[0].Published == nil || out.Results[0].Published.Format("2006-01-02") != "2026-08-15" {
		t.Fatalf("unexpected published date: %v", out.Results[0].Published)
	}
	if !strings.Contains(out.Results[0].Content, "asyncio.run()") {
		t.Fatalf("expected joined excerpts in content, got %q", out.Results[0].Content)
	}
	if out.Results[1].Published != nil {
		t.Fatalf("expected nil publish date for null fixture field, got %v", out.Results[1].Published)
	}
	if out.Cost == nil || out.Cost.Micros != 1000 {
		t.Fatalf("expected $1/1k = 1000 micros cost, got %+v", out.Cost)
	}
	if !slices.Contains(out.Warnings, "parallel: max_results capped to 20") {
		t.Fatalf("expected upstream warning propagated, got %v", out.Warnings)
	}
}

func TestSearchObjectiveDedupTurboHostOnlyAndValidation(t *testing.T) {
	var gotReq searchWireReq
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = searchWireReq{}
		_ = json.UnmarshalRead(r.Body, &gotReq)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"search_id":"s1","session_id":"ss1","results":[]}`))
	}))
	defer srv.Close()

	p, _, err := New(Options{Keys: []string{"k1"}, BaseURL: srv.URL, PriceSearch: "USD:0.001"}, newTestClient(t))
	if err != nil {
		t.Fatal(err)
	}

	// nil 与空查询防御
	if _, err := p.Search(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for nil req, got %v", err)
	}
	if _, err := p.Search(context.Background(), &core.SearchRequest{Query: "   "}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for empty query, got %v", err)
	}
	// 非法 mode 与 location 校验
	if _, err := p.Search(context.Background(), &core.SearchRequest{
		Query:   "go",
		Options: map[string]string{spec.OptParallelMode: "invalid"},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for invalid mode, got %v", err)
	}
	if _, err := p.Search(context.Background(), &core.SearchRequest{
		Query:   "go",
		Options: map[string]string{spec.OptParallelLocation: "usa"},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for invalid location, got %v", err)
	}
	// include/exclude 经 www. 归一化后同项冲突前置拦截
	if _, err := p.Search(context.Background(), &core.SearchRequest{
		Query: "go",
		Filters: core.Filters{
			Sites:        []string{"www.example.com"},
			ExcludeSites: []string{"example.com"},
		},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for source_policy conflict, got %v", err)
	}

	// Question 与 Query 相同时不发 objective；turbo 模式先归一化协议/端口/www./尾点再剥离路径前缀；空结果不计费
	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:    "Go 1.26",
		Question: "go 1.26",
		Filters: core.Filters{
			Sites:        []string{"https://www.Go.Dev:443/doc/?v=1", "go.dev."},
			ExcludeSites: []string{"https://example.com/archive", "spam.com."},
		},
		Options: map[string]string{spec.OptParallelMode: spec.ParallelModeTurbo},
	})
	if err != nil {
		t.Fatalf("turbo search failed: %v", err)
	}
	if gotReq.Objective != "" {
		t.Fatalf("expected empty objective when identical to query, got %q", gotReq.Objective)
	}
	if gotReq.Mode != spec.ParallelModeTurbo {
		t.Fatalf("expected turbo mode, got %q", gotReq.Mode)
	}
	if gotReq.AdvancedSettings == nil || gotReq.AdvancedSettings.SourcePolicy == nil {
		t.Fatalf("expected source_policy in turbo request")
	}
	if !slices.Equal(gotReq.AdvancedSettings.SourcePolicy.IncludeDomains, []string{"go.dev"}) {
		t.Fatalf("expected normalized host-only include in turbo mode, got %v", gotReq.AdvancedSettings.SourcePolicy.IncludeDomains)
	}
	if !slices.Equal(gotReq.AdvancedSettings.SourcePolicy.ExcludeDomains, []string{"spam.com"}) {
		t.Fatalf("expected path exclude omitted in turbo mode, got %v", gotReq.AdvancedSettings.SourcePolicy.ExcludeDomains)
	}
	if out.Cost != nil {
		t.Fatalf("expected nil cost on empty search results, got %+v", out.Cost)
	}

	// include + exclude 合并超 200 上限前置拦截
	many := make([]string, 105)
	for i := range many {
		many[i] = "site" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".example.com"
	}
	manyExc := make([]string, 105)
	for i := range manyExc {
		manyExc[i] = "exc" + string(rune('a'+i%26)) + string(rune('0'+i/26)) + ".example.com"
	}
	if _, err := buildSourcePolicy(core.Filters{Sites: many, ExcludeSites: manyExc}, false); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument when combined source_policy > 200, got %v", err)
	}
}

func TestFetchFullContentFreshAndFallback(t *testing.T) {
	fixture := loadFixture(t, "extract_ok.json")
	var gotReq extractWireReq

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != extractEndpointPath {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		gotReq = extractWireReq{}
		if err := json.UnmarshalRead(r.Body, &gotReq); err != nil {
			t.Errorf("unmarshal extract req: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	defer srv.Close()

	p, _, err := New(Options{
		Keys:         []string{"k1"},
		BaseURL:      srv.URL,
		PriceFetch:   "USD:0.001",
		FetchTimeout: 20 * time.Second,
	}, newTestClient(t))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Fetch(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for nil fetch req, got %v", err)
	}

	// 常规抓取不传 fetch_policy（保留服务端智能缓存策略）
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://go.dev/doc/effective_go"}); err != nil {
		t.Fatalf("default Fetch failed: %v", err)
	}
	if gotReq.AdvancedSettings == nil || !gotReq.AdvancedSettings.FullContent || gotReq.AdvancedSettings.FetchPolicy != nil {
		t.Fatalf("expected default fetch full_content=true and nil fetch_policy, got %+v", gotReq.AdvancedSettings)
	}

	// 显式 --fresh 时传 fetch_policy（max_age_seconds=600 + disable_cache_fallback=true + 预算钳制 timeout_seconds）
	out, err := p.Fetch(context.Background(), &core.FetchRequest{
		URL:     "https://go.dev/doc/effective_go",
		Options: map[string]string{spec.OptFetchFresh: "true"},
	})
	if err != nil {
		t.Fatalf("Fetch failed: %v", err)
	}
	if !slices.Equal(gotReq.URLs, []string{"https://go.dev/doc/effective_go"}) {
		t.Fatalf("unexpected urls: %v", gotReq.URLs)
	}
	if gotReq.AdvancedSettings == nil || !gotReq.AdvancedSettings.FullContent {
		t.Fatalf("expected full_content=true, got %+v", gotReq.AdvancedSettings)
	}
	fp := gotReq.AdvancedSettings.FetchPolicy
	if fp == nil || fp.MaxAgeSeconds != minFetchPolicyMaxAgeSeconds || !fp.DisableCacheFallback || fp.TimeoutSeconds <= 0 || fp.TimeoutSeconds > 20 {
		t.Fatalf("unexpected fetch_policy: %+v", fp)
	}
	if out.Page == nil || out.Page.Title != "Effective Go" || !strings.Contains(out.Page.Content, "unusual properties") {
		t.Fatalf("unexpected page: %+v", out.Page)
	}
	if out.Page.Published == nil || out.Page.Published.Format("2006-01-02") != "2026-05-01" {
		t.Fatalf("unexpected page published: %v", out.Page.Published)
	}
	if out.Cost == nil || out.Cost.Micros != 1000 {
		t.Fatalf("expected 1000 micros fetch cost, got %+v", out.Cost)
	}
	if out.RequestID != "extract_019a0000000000000000000002" {
		t.Fatalf("unexpected extract request_id: %q", out.RequestID)
	}
}

func TestFetchInnerErrorsAndExcerptsFallback(t *testing.T) {
	inner404 := loadFixture(t, "extract_inner_error.json")
	var mode atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch mode.Load() {
		case 0:
			_, _ = w.Write(inner404)
		case 1:
			_, _ = w.Write([]byte(`{"extract_id":"e2","session_id":"s2","results":[],"errors":[{"url":"https://a.example","error_type":"http_client_error","http_status_code":403,"content":"Cloudflare challenge"}]}`))
		case 2:
			_, _ = w.Write([]byte(`{"extract_id":"e3","session_id":"s3","results":[{"url":"javascript:alert(1)","title":null,"excerpts":["# Fallback Heading\n\nBody text from excerpts"],"full_content":null}],"errors":[]}`))
		}
	}))
	defer srv.Close()

	p, _, err := New(Options{Keys: []string{"k1"}, BaseURL: srv.URL}, newTestClient(t))
	if err != nil {
		t.Fatal(err)
	}

	// 1. 内层 404 -> CodeNotFound；空 content 时自动生成清晰 HTTP 状态说明
	_, err = p.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/missing"})
	if se := core.AsError(err); se.Code != core.CodeNotFound || se.Status != 404 {
		t.Fatalf("expected CodeNotFound(404), got %+v", se)
	}
	code404 := 404
	if se := classifyExtractItemError(extractWireError{ErrorType: "http_error", HTTPStatusCode: &code404}); se.Code != core.CodeNotFound || !strings.Contains(se.Message, "HTTP 404") {
		t.Fatalf("expected human-readable HTTP 404 message, got %+v", se)
	}
	// 全文提取场景过滤掉针对 excerpts 的无关噪音警告，保留其他真实警告
	if got := filterExtractWarnings([]wireWarning{
		{Message: "Neither objective nor search_queries were provided, provide at least one to increase the relevance of excerpts."},
		{Message: "content truncated at 100000 chars"},
	}, true); !slices.Equal(got, []string{"parallel: content truncated at 100000 chars"}) {
		t.Fatalf("unexpected filtered extract warnings: %v", got)
	}

	// 2. 内层 403 挑战 -> CodeContentUnavailable + HintKeyChallengeDetected
	mode.Store(1)
	_, err = p.Fetch(context.Background(), &core.FetchRequest{URL: "https://a.example"})
	if se := core.AsError(err); se.Code != core.CodeContentUnavailable || se.HintKey != core.HintKeyChallengeDetected {
		t.Fatalf("expected challenge error, got %+v", se)
	}

	// 3. full_content 为 null 时兜底 excerpts、从 H1 提取标题，且拦截非法 javascript: FinalURL（返回空串不污染）
	mode.Store(2)
	out, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://safe.example/doc"})
	if err != nil {
		t.Fatalf("excerpts fallback fetch failed: %v", err)
	}
	if out.Page.Title != "Fallback Heading" || out.Page.FinalURL != "" {
		t.Fatalf("unexpected fallback page: %+v", out.Page)
	}
}

func TestHTTPErrorClassificationAndKeyFailover(t *testing.T) {
	err402 := loadFixture(t, "err_402.json")
	err422 := loadFixture(t, "err_422.json")
	okSearch := loadFixture(t, "search_ok.json")

	// 验证分类矩阵
	cases := []struct {
		name     string
		status   int
		body     []byte
		wantCode core.Code
	}{
		{name: "401", status: 401, body: []byte(`{"type":"error","error":{"ref_id":"r1","message":"Invalid API key"}}`), wantCode: core.CodeAuthFailed},
		{name: "402", status: 402, body: err402, wantCode: core.CodeInsufficientBalance},
		{name: "403_processor", status: 403, body: []byte(`{"type":"error","error":{"ref_id":"r2","message":"Invalid processor in request"}}`), wantCode: core.CodeInvalidArgument},
		{name: "403_eu_mode", status: 403, body: []byte(`{"type":"error","error":{"ref_id":"r2b","message":"EU endpoint only supports basic mode"}}`), wantCode: core.CodeInvalidArgument},
		{name: "403_modern_auth", status: 403, body: []byte(`{"type":"error","error":{"ref_id":"r3a","message":"Modern authentication required"}}`), wantCode: core.CodeAuthFailed},
		{name: "403_auth", status: 403, body: []byte(`{"type":"error","error":{"ref_id":"r3","message":"Account suspended"}}`), wantCode: core.CodeAuthFailed},
		{name: "408", status: 408, body: []byte(`{"type":"error","error":{"ref_id":"r4","message":"Request timeout"}}`), wantCode: core.CodeUpstreamTimeout},
		{name: "422", status: 422, body: err422, wantCode: core.CodeInvalidArgument},
		{name: "429", status: 429, body: []byte(`{"type":"error","error":{"ref_id":"r5","message":"Rate limit exceeded"}}`), wantCode: core.CodeRateLimited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			se := classifyError(tc.status, nil, tc.body)
			if se.Code != tc.wantCode {
				t.Fatalf("status=%d got code=%s want=%s (msg=%q)", tc.status, se.Code, tc.wantCode, se.Message)
			}
		})
	}

	// 验证多 Key 收到 402 时自动切换到池内第二个健康 Key 成功返回
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get(spec.HeaderAPIKey) == "bad-key" {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write(err402)
			return
		}
		_, _ = w.Write(okSearch)
	}))
	defer srv.Close()

	p, _, err := New(Options{Keys: []string{"bad-key", "good-key"}, BaseURL: srv.URL}, newTestClient(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := p.Search(context.Background(), &core.SearchRequest{Query: "golang", Count: 2})
	if err != nil || len(out.Results) != 2 || calls.Load() != 2 {
		t.Fatalf("expected key failover to succeed in 2 calls, got len=%d calls=%d err=%v", len(out.Results), calls.Load(), err)
	}
}
