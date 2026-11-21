package tinyfish

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestTinyFishPaperModeFetchSuccessAndErrorEnums(t *testing.T) {
	var lastQuery url.Values
	searchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastQuery = r.URL.Query()
		_, _ = w.Write([]byte(`{
			"results": [{
				"position": 1,
				"title": "Attention Is All You Need",
				"url": "https://arxiv.org/abs/1706.03762",
				"snippet": "Transformer architecture",
				"date": null,
				"authors": ["Vaswani"],
				"venue": "NeurIPS",
				"year": 2017,
				"cited_by_count": 100000,
				"pdf_url": "https://arxiv.org/pdf/1706.03762"
			}]
		}`))
	}))
	defer searchSrv.Close()

	fetchOKPayload := providertest.LoadFixture(t, "fetch_ok.json")
	fetchSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fetchOKPayload)
	}))
	defer fetchSrv.Close()

	p, _, _ := New(Options{
		Keys:      []string{"tf-k"},
		SearchURL: searchSrv.URL,
		FetchURL:  fetchSrv.URL,
		QPS:       100,
	}, providertest.NewTestAPIClient(t))

	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	fresh, _ := core.ParseFreshness("2017-01-01..2026-01-01", now)
	paperReq := &core.SearchRequest{
		Query:        "transformer",
		IncludeExtra: true,
		Options:      map[string]string{"tinyfish.domain_type": "RESEARCH_PAPER"},
		Filters: core.Filters{
			Lang:         []string{"en", "zh"},
			ExcludeSites: []string{"spam.org"},
			Freshness:    fresh,
		},
	}
	if p.CapabilitiesFor(paperReq).Freshness != core.TimeNone {
		t.Fatal("expected non-full-year paper freshness to report Freshness=none")
	}
	fullYear, _ := core.ParseFreshness("2017-01-01..2026-12-31", now)
	if p.CapabilitiesFor(&core.SearchRequest{Options: paperReq.Options, Filters: core.Filters{Freshness: fullYear}}).Freshness != core.TimeBoth {
		t.Fatal("expected full-year paper freshness to retain Freshness=both")
	}
	sOut, err := p.Search(context.Background(), paperReq)
	if err != nil || len(sOut.Results) != 1 || sOut.Results[0].Extra["venue"] != "NeurIPS" || sOut.Results[0].Published == nil || sOut.Results[0].Published.Year() != 2017 {
		t.Fatalf("unexpected paper search: %+v err=%v", sOut, err)
	}
	strictPaper := *paperReq
	strictPaper.StrictFilters = true
	if _, err := p.Search(context.Background(), &strictPaper); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected strict non-full-year paper freshness -> invalid_argument, got %v", err)
	}

	// 验证中文裸查询自动推导 language=zh，显式 zh_CN.UTF-8 / Chinese 规整为 zh
	if _, err := p.Search(context.Background(), &core.SearchRequest{Query: "人工智能发展现状"}); err != nil || lastQuery.Get("language") != "zh" {
		t.Fatalf("expected auto-inferred language=zh for Chinese query, got %v err=%v", lastQuery, err)
	}
	if _, err := p.Search(context.Background(), &core.SearchRequest{Query: "golang", Filters: core.Filters{Lang: []string{"zh_CN.UTF-8"}}}); err != nil || lastQuery.Get("language") != "zh" {
		t.Fatalf("expected zh_CN.UTF-8 normalized to zh, got %v err=%v", lastQuery, err)
	}
	if got := normalizeTinyFishLang("Chinese"); got != "zh" {
		t.Fatalf("expected Chinese -> zh, got %q", got)
	}

	fOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://spa.test/app"})
	if err != nil || fOut.Page.FinalURL != "https://spa.test/app/home" || fOut.Page.Title != "Rendered App" {
		t.Fatalf("unexpected Fetch success: %+v err=%v", fOut, err)
	}

	// 错误枚举与目标 HTTP 状态码细分全覆盖
	for code, want := range map[string]core.Code{
		"login_required": core.CodeBlockedByPolicy,
		"timeout":        core.CodeUpstreamTimeout,
		"invalid_url":    core.CodeInvalidArgument,
		"page_not_found": core.CodeNotFound,
	} {
		if got := core.AsError(classifyFetchItemError(fetchWireError{Error: code})).Code; got != want {
			t.Fatalf("fetch error %s -> %s, want %s", code, got, want)
		}
	}
	st429, st403, st502 := 429, 403, 502
	if got := core.AsError(classifyFetchItemError(fetchWireError{Error: "target_http_error", Status: &st429})).Code; got != core.CodeRateLimited {
		t.Fatalf("target_http_error 429 -> %s, want rate_limited", got)
	}
	if got := core.AsError(classifyFetchItemError(fetchWireError{Error: "target_http_error", Status: &st403})).Code; got != core.CodeBlockedByPolicy {
		t.Fatalf("target_http_error 403 -> %s, want blocked_by_policy", got)
	}
	if got := core.AsError(classifyFetchItemError(fetchWireError{Error: "target_http_error", Status: &st502})).Code; got != core.CodeUpstreamUnavailable {
		t.Fatalf("target_http_error 502 -> %s, want upstream_unavailable", got)
	}
	for apiCode, want := range map[string]core.Code{
		"INVALID_INPUT":            core.CodeInvalidArgument,
		"INVALID_API_KEY":          core.CodeAuthFailed,
		"FREE_ALLOWANCE_EXHAUSTED": core.CodeInsufficientBalance,
		"FORBIDDEN":                core.CodeNotEnabled,
		"TIMEOUT":                  core.CodeUpstreamTimeout,
		"RATE_LIMIT_EXCEEDED":      core.CodeRateLimited,
		"INTERNAL_ERROR":           core.CodeUpstreamUnavailable,
	} {
		errObj := classifyError(400, nil, []byte(`{"error":{"code":"`+apiCode+`","message":"m"}}`))
		if errObj.Code != want {
			t.Fatalf("API error %s -> %s, want %s", apiCode, errObj.Code, want)
		}
	}

	// 非法 scheme 的 final_url 必须被丢弃，防止透传至输出
	unsafeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"url":"https://spa.test/app","final_url":"javascript:alert(1)","title":"Safe","text":"Clean body text"}]}`))
	}))
	defer unsafeSrv.Close()
	pUnsafe, _, err := New(Options{Keys: []string{"tf-key"}, SearchURL: unsafeSrv.URL, FetchURL: unsafeSrv.URL}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	uOut, err := pUnsafe.Fetch(context.Background(), &core.FetchRequest{URL: "https://spa.test/app"})
	if err != nil || uOut.Page.FinalURL != "" {
		t.Fatalf("unsafe final_url must be dropped, got out=%+v err=%v", uOut, err)
	}
}
