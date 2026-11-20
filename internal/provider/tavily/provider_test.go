package tavily

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/provider/providertest"
)

func TestTavilyCustomStatusCodesAndExtractHeadingFallback(t *testing.T) {
	okPayload := providertest.LoadFixture(t, "search_ok.json")
	emptyPayload := providertest.LoadFixture(t, "search_empty.json")
	extPayload := providertest.LoadFixture(t, "extract_ok.json")
	errPayload := providertest.LoadFixture(t, "error.json")

	statusToReturn := http.StatusOK
	emptyMode := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if statusToReturn != http.StatusOK {
			w.WriteHeader(statusToReturn)
			_, _ = w.Write(errPayload)
			return
		}
		if r.URL.Path == "/search" {
			if emptyMode {
				_, _ = w.Write(emptyPayload)
				return
			}
			_, _ = w.Write(okPayload)
			return
		}
		_, _ = w.Write(extPayload)
	}))
	defer srv.Close()

	p, enabled, err := New(Options{Keys: []string{"tvly-test"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled {
		t.Fatalf("New: enabled=%v err=%v", enabled, err)
	}

	sOut, err := p.Search(context.Background(), &core.SearchRequest{Query: "tavily", Count: 5, Detail: core.DetailSummary})
	if err != nil || len(sOut.Results) != 1 || sOut.Cost != nil {
		t.Fatalf("unexpected search out=%+v err=%v", sOut, err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, sOut)

	priced, _, err := New(Options{Keys: []string{"tvly-test"}, BaseURL: srv.URL, PriceCredit: "USD:0.008"}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatalf("New priced: %v", err)
	}
	psOut, err := priced.Search(context.Background(), &core.SearchRequest{Query: "tavily", Count: 5, Detail: core.DetailSummary})
	if err != nil || psOut.Cost == nil || psOut.Cost.Micros != 8000 {
		t.Fatalf("unexpected priced search out=%+v err=%v", psOut, err)
	}

	emptyMode = true
	eOut, err := p.Search(context.Background(), &core.SearchRequest{Query: "none"})
	if err != nil || len(eOut.Results) != 0 {
		t.Fatalf("unexpected empty search: %+v err=%v", eOut, err)
	}
	emptyMode = false

	fOut, err := priced.Fetch(context.Background(), &core.FetchRequest{URL: "https://docs.tavily.com/a"})
	if err != nil || fOut.Page.Title != "Extracted Heading" || fOut.Cost == nil || fOut.Cost.Micros != 1600 {
		t.Fatalf("unexpected extract out=%+v err=%v", fOut, err)
	}

	for _, code := range []int{432, 433} {
		statusToReturn = code
		pt, _, _ := New(Options{Keys: []string{"tvly-test"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
		_, err = pt.Search(context.Background(), &core.SearchRequest{Query: "q"})
		if core.AsError(err).Code != core.CodeInsufficientBalance {
			t.Fatalf("HTTP %d should map to insufficient_balance, got %v", code, err)
		}
	}
	statusToReturn = 434
	pt434, _, _ := New(Options{Keys: []string{"tvly-test"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	_, err = pt434.Search(context.Background(), &core.SearchRequest{Query: "q"})
	if core.AsError(err).Code != core.CodeUpstreamUnavailable {
		t.Fatalf("HTTP 434 should map to upstream_unavailable, got %v", err)
	}
}

func TestTavilyExtractNotFoundReasonMapping(t *testing.T) {
	if !httpx.LooksLikeNotFoundMessage("", "404 page not found") || !httpx.LooksLikeNotFoundMessage("", "HTTP 410 Gone") || !httpx.LooksLikeNotFoundMessage("", "resource does not exist") {
		t.Fatal("expected clear 404/410 reasons to map to not_found")
	}
	if httpx.LooksLikeNotFoundMessage("", "403 Forbidden: Cloudflare challenge") || httpx.LooksLikeNotFoundMessage("", "captcha required on 404") || httpx.LooksLikeNotFoundMessage("", "") {
		t.Fatal("anti-bot challenge or empty reason must not map to not_found")
	}
	if !httpx.LooksLikeChallengeMessage("403 Forbidden: Cloudflare challenge") || httpx.LooksLikeChallengeMessage("404 Not Found") {
		t.Fatal("unexpected LooksLikeChallengeMessage result")
	}

	// 验证非法 tavily.topic 前置拦截与 failed_results 挑战盾提示透传
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[],"failed_results":[{"url":"https://protected.example","error":"403 Forbidden: Cloudflare challenge"}]}`))
	}))
	defer srv.Close()
	p, _, err := New(Options{Keys: []string{"tvly-test"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Search(context.Background(), &core.SearchRequest{Query: "q", Options: map[string]string{"tavily.topic": "invalid"}}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid tavily.topic -> invalid_argument, got %v", err)
	}
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://protected.example"}); err == nil {
		t.Fatal("expected failed_results error")
	} else if se := core.AsError(err); se.Code != core.CodeContentUnavailable || se.HintKey != core.HintKeyChallengeDetected {
		t.Fatalf("expected challenge hint on failed_results 403, got %+v", se)
	}
}
