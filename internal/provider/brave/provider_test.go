package brave

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestBraveLangNormalizationAgeGuardAndGogglesFallback(t *testing.T) {
	okPayload := providertest.LoadFixture(t, "search_ok.json")
	webOKPayload := providertest.LoadFixture(t, "web_search_ok.json")
	emptyPayload := providertest.LoadFixture(t, "search_empty.json")
	errPayload := providertest.LoadFixture(t, "error.json")

	var captured contextWireReq
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "brave-k" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if mode == "err422" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = w.Write(errPayload)
			return
		}
		if r.URL.Path == "/res/v1/llm/context" {
			_ = json.UnmarshalRead(r.Body, &captured)
			if mode == "empty" {
				_, _ = w.Write(emptyPayload)
				return
			}
			_, _ = w.Write(okPayload)
			return
		}
		_, _ = w.Write(webOKPayload)
	}))
	defer srv.Close()

	p, enabled, err := New(Options{Keys: []string{"brave-k"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled {
		t.Fatalf("New: enabled=%v err=%v", enabled, err)
	}

	manySites := make([]string, 40)
	for i := range manySites {
		manySites[i] = fmt.Sprintf("subdomain-%02d.documentation.example.org", i)
	}

	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:  "distributed consensus",
		Count:  5,
		Detail: core.DetailSummary,
		Filters: core.Filters{
			Lang:  []string{"zh"},
			Sites: manySites,
		},
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, out)
	if captured.SearchLang != "zh-hans" {
		t.Fatalf("expected zh -> zh-hans normalization, got %q", captured.SearchLang)
	}
	if captured.Q != "distributed consensus" || len(captured.Goggles) == 0 {
		t.Fatalf("expected oversized site list to switch to inline goggles, q=%q goggles=%v", captured.Q, captured.Goggles)
	}
	if out.Results[0].Published == nil || out.Results[1].Published != nil {
		t.Fatalf("expected age[3] parsed on #1 and empty age[] safely nil on #2, got %+v", out.Results)
	}

	mode = "empty"
	eOut, err := p.Search(context.Background(), &core.SearchRequest{Query: "none", Detail: core.DetailSummary})
	if err != nil || len(eOut.Results) != 0 {
		t.Fatalf("unexpected empty result: %+v err=%v", eOut, err)
	}

	mode = "err422"
	_, err = p.Search(context.Background(), &core.SearchRequest{Query: "none", Detail: core.DetailSummary})
	if core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected 422 -> invalid_argument, got %v", err)
	}

	mode = "ok"
	snippetReq := &core.SearchRequest{
		Query:  "quick query",
		Detail: core.DetailSnippet,
	}
	snippetCaps := p.CapabilitiesFor(snippetReq)
	if snippetCaps.MaxCount != 20 || snippetCaps.Content != core.ContentNone {
		t.Fatalf("expected CapabilitiesFor(snippet) MaxCount=20 & Content=none, got %+v", snippetCaps)
	}
	for in, want := range map[string]string{"zh-Hant-TW": "zh-hant", "en-US.UTF-8": "en", "en-GB": "en-gb", "pt_BR": "pt-br"} {
		if got := normalizeBraveLang(in); got != want {
			t.Fatalf("normalizeBraveLang(%q) = %q, want %q", in, got, want)
		}
	}
	wOut, err := p.Search(context.Background(), snippetReq)
	if err != nil || len(wOut.Results) != 2 || wOut.Results[0].Site != "Example Docs" || wOut.Results[1].Published == nil {
		t.Fatalf("unexpected web/search result (expected web_search_ok.json & age fallback parsed): %+v err=%v", wOut, err)
	}
}
