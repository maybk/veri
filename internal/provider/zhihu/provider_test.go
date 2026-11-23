package zhihu

import (
	"context"
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

func TestZhihuWebAndCommunitySearchContracts(t *testing.T) {
	globalOK := providertest.LoadFixture(t, "global_search_ok.json")
	zhihuOK := providertest.LoadFixture(t, "zhihu_search_ok.json")

	var gotPath string
	var gotQuery map[string]string
	var gotAuth, gotTS, gotCT string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get(spec.HeaderAuthorization)
		gotTS = r.Header.Get(headerRequestTimestamp)
		gotCT = r.Header.Get("Content-Type")
		gotQuery = make(map[string]string)
		for k, vals := range r.URL.Query() {
			if len(vals) > 0 {
				gotQuery[k] = vals[0]
			}
		}
		switch r.URL.Path {
		case pathGlobalSearch:
			_, _ = w.Write(globalOK)
		case pathZhihuSearch:
			_, _ = w.Write(zhihuOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	if p, enabled, err := New(Options{BaseURL: srv.URL}, providertest.NewTestAPIClient(t)); err != nil || enabled || p != nil {
		t.Fatalf("expected keyless zhihu to be disabled, got p=%v enabled=%v err=%v", p, enabled, err)
	}
	if p, enabled, err := NewWeb(WebOptions{BaseURL: srv.URL}, providertest.NewTestAPIClient(t)); err != nil || enabled || p != nil {
		t.Fatalf("expected keyless zhihu_web to be disabled, got p=%v enabled=%v err=%v", p, enabled, err)
	}

	pWeb, enabled, err := NewWeb(WebOptions{
		Keys:        []string{"zh-secret-test-123456"},
		BaseURL:     srv.URL,
		PriceSearch: "CNY:0.005",
	}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled || pWeb.Name() != WebProviderName {
		t.Fatalf("NewWeb: name=%q enabled=%v err=%v", pWeb.Name(), enabled, err)
	}
	pWeb.nowFn = func() time.Time { return time.Unix(1760000000, 0).UTC() }

	pCommunity, enabled, err := New(Options{
		Keys:        []string{"zh-secret-test-123456"},
		BaseURL:     srv.URL,
		PriceSearch: "CNY:0.02",
	}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled || pCommunity.Name() != ProviderName {
		t.Fatalf("New: name=%q enabled=%v err=%v", pCommunity.Name(), enabled, err)
	}
	pCommunity.nowFn = func() time.Time { return time.Unix(1760000000, 0).UTC() }

	// 1. zhihu_web 全网搜索：校验能力声明、三头、时间 Filter、<em> 精确剥离（保留 <T>）、UTM 清理、权威度与定价
	if caps := pWeb.Capabilities(); caps.MaxCount != maxGlobalSearchCount || len(caps.ScopeDomains) != 0 {
		t.Fatalf("unexpected zhihu_web capabilities: %+v", caps)
	}
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	out, err := pWeb.Search(context.Background(), &core.SearchRequest{
		Query:        "Go 泛型",
		Count:        15,
		Detail:       core.DetailSummary,
		IncludeExtra: true,
		Filters:      core.Filters{Freshness: &core.Freshness{After: after}},
		Options:      map[string]string{spec.OptZhihuWebSearchDB: "realtime"},
	})
	if err != nil {
		t.Fatalf("zhihu_web Search: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, WebProviderName, out)
	if gotPath != pathGlobalSearch || gotAuth != "Bearer zh-secret-test-123456" || gotTS != "1760000000" || gotCT != "application/json" {
		t.Fatalf("unexpected headers/path: path=%q auth=%q ts=%q ct=%q", gotPath, gotAuth, gotTS, gotCT)
	}
	if gotQuery["SearchDB"] != "realtime" || !strings.Contains(gotQuery["Filter"], "publish_time>=") {
		t.Fatalf("unexpected global query params: %+v", gotQuery)
	}
	r0 := out.Results[0]
	if r0.Title != "深入理解 Go 泛型 <T> 实践" || !strings.Contains(r0.Summary, "<T any>") || strings.Contains(r0.Summary, "<em>") {
		t.Fatalf("highlight stripping damaged generic syntax or kept <em>: title=%q summary=%q", r0.Title, r0.Summary)
	}
	if strings.Contains(r0.URL, "utm_") {
		t.Fatalf("expected UTM params stripped, got %q", r0.URL)
	}
	if r0.Authority == nil || *r0.Authority != 0.75 {
		t.Fatalf("expected authority 0.75, got %v", r0.Authority)
	}
	if out.Cost == nil || out.Cost.Micros != 5000 || out.Cost.Currency != core.CurrencyCNY {
		t.Fatalf("expected zhihu_web cost CNY 5000 micros, got %+v", out.Cost)
	}

	// 2. zhihu 站内搜索（无自定义 sort 时，Freshness 原生映射 EditTime:none:(min,max)）
	zReq := &core.SearchRequest{
		Query:        "RAG 评测",
		Count:        8,
		Detail:       core.DetailSummary,
		IncludeExtra: true,
		Filters:      core.Filters{Freshness: &core.Freshness{After: after}},
	}
	if caps := pCommunity.CapabilitiesFor(zReq); caps.MaxCount != maxZhihuSearchCount || caps.Freshness != core.TimeBoth || !slices.Equal(caps.ScopeDomains, []string{"zhihu.com"}) {
		t.Fatalf("unexpected zhihu capabilities: %+v", caps)
	}
	zOut, err := pCommunity.Search(context.Background(), zReq)
	if err != nil {
		t.Fatalf("zhihu Search: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, zOut)
	if gotPath != pathZhihuSearch || !strings.HasPrefix(gotQuery["SortBy"], "EditTime:none:(") {
		t.Fatalf("unexpected zhihu query params: path=%q query=%+v", gotPath, gotQuery)
	}
	if zOut.RequestID != "zh-hash-987654" || zOut.Cost == nil || zOut.Cost.Micros != 20000 {
		t.Fatalf("unexpected zhihu SearchHashId/Cost: reqID=%q cost=%+v", zOut.RequestID, zOut.Cost)
	}
	if zOut.Results[0].Extra["vote_up_count"] != 256 {
		t.Fatalf("expected vote_up_count in Extra, got %+v", zOut.Results[0].Extra)
	}

	// 3. 显式传 zhihu.sort 时动态能力 Freshness 退让为 TimeNone，防参数冲突
	customSortReq := &core.SearchRequest{
		Query:   "RAG 评测",
		Count:   5,
		Detail:  core.DetailSnippet,
		Options: map[string]string{spec.OptZhihuSort: "VoteUpCount:desc"},
	}
	if caps := pCommunity.CapabilitiesFor(customSortReq); caps.Freshness != core.TimeNone {
		t.Fatalf("expected TimeNone when custom zhihu.sort is set, got %+v", caps)
	}
	if _, err := pCommunity.Search(context.Background(), customSortReq); err != nil {
		t.Fatalf("zhihu custom sort Search: %v", err)
	}
	if gotQuery["SortBy"] != "VoteUpCount:desc" {
		t.Fatalf("SortBy = %q, want VoteUpCount:desc", gotQuery["SortBy"])
	}
}

func TestZhihuEmbeddedBusinessErrorCodes(t *testing.T) {
	quotaBody := providertest.LoadFixture(t, "err_code_30002.json")
	riskBody := providertest.LoadFixture(t, "err_code_30003.json")

	// HTTP 200 内嵌 30002 配额耗尽 -> CodeInsufficientBalance；30003 风控拒绝 -> 不可重试 CodeBlockedByPolicy，且精准归因实例名
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		if r.URL.Query().Get("Query") == "risk" {
			_, _ = w.Write(riskBody)
			return
		}
		_, _ = w.Write(quotaBody)
	}))
	defer srv.Close()

	pWeb, _, err := NewWeb(WebOptions{Keys: []string{"zh-secret-1"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pWeb.Search(context.Background(), &core.SearchRequest{Query: "risk", Count: 5, Detail: core.DetailSnippet}); err == nil {
		t.Fatal("expected error for code 30003")
	} else if se := core.AsError(err); se.Code != core.CodeBlockedByPolicy || se.Retryable || se.Provider != WebProviderName {
		t.Fatalf("code 30003 expected non-retryable blocked_by_policy on zhihu_web, got %+v", se)
	}

	pCommunity, _, err := New(Options{Keys: []string{"zh-secret-1"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pCommunity.Search(context.Background(), &core.SearchRequest{Query: "quota", Count: 5, Detail: core.DetailSnippet}); err == nil {
		t.Fatal("expected error for code 30002")
	} else if se := core.AsError(err); se.Code != core.CodeInsufficientBalance || se.Provider != ProviderName {
		t.Fatalf("code 30002 expected insufficient_balance on zhihu, got %+v", se)
	}

	// 校验非法 zhihu.sort 与 zhihu_web.search_db 拦截
	for _, badSort := range []string{
		"InvalidField:desc",
		"EditTime:",
		"EditTime::(100,200)",
		"VoteUpCount:desc:garbage",
		"EditTime:none:(,)",
		"VoteUpCount:desc:(100,50)",
		"VoteUpCount:desc:(-1,10)",
	} {
		if _, err := pCommunity.Search(context.Background(), &core.SearchRequest{
			Query:   "go",
			Count:   3,
			Detail:  core.DetailSnippet,
			Options: map[string]string{spec.OptZhihuSort: badSort},
		}); core.AsError(err).Code != core.CodeInvalidArgument {
			t.Fatalf("expected invalid_argument for zhihu.sort %q, got %v", badSort, err)
		}
	}
	if _, err := pWeb.Search(context.Background(), &core.SearchRequest{
		Query:   "go",
		Count:   3,
		Detail:  core.DetailSnippet,
		Options: map[string]string{spec.OptZhihuWebSearchDB: "invalid"},
	}); core.AsError(err).Code != core.CodeInvalidArgument || core.AsError(err).Provider != WebProviderName {
		t.Fatalf("expected invalid_argument on zhihu_web for bad search_db, got %v", err)
	}
	for _, okSort := range []string{"VoteUpCount", "VoteUpCount:desc", "VoteUpCount:desc:(10,)", "EditTime:none:(1000,2000)", "CommentCount:asc:(,500)"} {
		if err := validateSortBy(ProviderName, okSort); err != nil {
			t.Fatalf("expected valid zhihu.sort %q, got %v", okSort, err)
		}
	}
	if _, err := pCommunity.Search(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected nil SearchRequest -> invalid_argument, got %v", err)
	}
}
