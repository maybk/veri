package tavily

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/provider/providertest"
	"veri/internal/spec"
)

func TestTavilyAdvancedLanguageAndUsageCredits(t *testing.T) {
	var lastSearchReq searchWireReq
	var lastExtractReq extractWireReq
	extractOK := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/extract" {
			_ = json.UnmarshalRead(r.Body, &lastExtractReq)
			if extractOK {
				_, _ = w.Write([]byte(`{"results":[{"url":"https://ok.test","raw_content":"# T\n\nBody"}],"failed_results":[],"usage":{"credits":0}}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[],"failed_results":[{"url":"https://bad.test","error":"forbidden"}]}`))
			return
		}
		_ = json.UnmarshalRead(r.Body, &lastSearchReq)
		_, _ = w.Write([]byte(`{"request_id":"tv-adv","results":[{"title":"T","url":"https://a.com","content":"C","raw_content":"RAW"}],"usage":{"credits":2}}`))
	}))
	defer srv.Close()

	p, _, err := New(Options{
		Keys:         []string{"tvly-k"},
		BaseURL:      srv.URL,
		SearchDepth:  "advanced",
		ExtractDepth: "advanced",
		PriceCredit:  "USD:0.008",
	}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Capabilities().Lang {
		t.Fatal("expected Tavily Capabilities().Lang to be true")
	}

	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	rel, _ := core.ParseFreshness("d", now)
	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:         "news",
		Detail:        core.DetailContent,
		StrictFilters: true,
		Options:       map[string]string{"tavily.topic": "NEWS"},
		Filters:       core.Filters{Sites: []string{"a.com"}, ExcludeSites: []string{"b.com"}, Lang: []string{"zh-Hans-CN", "en"}, Freshness: rel},
	})
	if err != nil || out.Cost.Micros != 16000 {
		t.Fatalf("expected 2-credit cost (16000 micros), got %+v err=%v", out, err)
	}
	if lastSearchReq.Topic != "news" || lastSearchReq.Language != "zh-cn" || !lastSearchReq.FilterByLanguage || !lastSearchReq.IncludeUsage {
		t.Fatalf("expected topic=news, language=zh-cn, filter_by_language=true, include_usage=true, got %+v", lastSearchReq)
	}
	for in, want := range map[string]string{"pt_BR.UTF-8": "pt-br", "zh-Hant-TW": "zh-tw", "en-US": "en"} {
		if got := normalizeTavilyLang(in); got != want {
			t.Fatalf("normalizeTavilyLang(%q) = %q, want %q", in, got, want)
		}
	}
	if len(out.Warnings) == 0 {
		t.Fatal("expected warning when multiple languages are passed")
	}

	abs, _ := core.ParseFreshness("2026-08-01..2026-08-31", now)
	if _, err := p.Search(context.Background(), &core.SearchRequest{Query: "q", Filters: core.Filters{Freshness: abs}}); err != nil {
		t.Fatal(err)
	}

	// 验证 Extract 返回 usage.credits=0（未满 5 页阶梯扣点）时精确折算为 0 micros
	extractOK = true
	fOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://ok.test"})
	if err != nil || fOut.Cost == nil || fOut.Cost.Micros != 0 || !lastExtractReq.IncludeUsage {
		t.Fatalf("expected 0-credit usage cost and include_usage=true, got %+v req=%+v err=%v", fOut, lastExtractReq, err)
	}
	extractOK = false

	// failed_results 必须返回 content_unavailable 且不计费
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://bad.test"}); core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("expected content_unavailable on failed_results, got %v", err)
	}

	// 验证默认 basic 配置下收到 DynamicRenderHint 时自动升档 advanced，且显式选项可覆盖
	basicProv, _, err := New(Options{Keys: []string{"tvly-k"}, BaseURL: srv.URL, ExtractDepth: "basic"}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	extractOK = true
	shortCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := basicProv.Fetch(shortCtx, &core.FetchRequest{URL: "https://ok.test", DynamicRenderHint: true}); err != nil {
		t.Fatal(err)
	}
	if lastExtractReq.ExtractDepth != spec.TavilyDepthAdvanced || lastExtractReq.Timeout <= 0 || lastExtractReq.Timeout > 5.0 {
		t.Fatalf("expected DynamicRenderHint -> advanced and bounded timeout, got %+v", lastExtractReq)
	}
	if _, err := basicProv.Fetch(context.Background(), &core.FetchRequest{
		URL:               "https://ok.test",
		DynamicRenderHint: true,
		Options:           map[string]string{spec.OptTavilyExtractDepth: "basic"},
	}); err != nil || lastExtractReq.ExtractDepth != spec.TavilyDepthBasic {
		t.Fatalf("expected explicit option to override DynamicRenderHint, got %+v err=%v", lastExtractReq, err)
	}
	if _, err := basicProv.Fetch(context.Background(), &core.FetchRequest{
		URL:     "https://ok.test",
		Options: map[string]string{spec.OptTavilyExtractDepth: "invalid"},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid extract_depth -> invalid_argument, got %v", err)
	}
}
