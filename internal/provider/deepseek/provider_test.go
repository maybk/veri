package deepseek

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestDeepSeekDisabledWithoutKey(t *testing.T) {
	p, ok, err := New(Options{}, providertest.NewTestAPIClient(t))
	if err != nil || ok || p != nil {
		t.Fatalf("expected (nil, false, nil) when no key configured, got (%v, %v, %v)", p, ok, err)
	}
}

func TestDeepSeekSearchMappingAndEmptyNoCost(t *testing.T) {
	okPayload := providertest.LoadFixture(t, "search_ok.json")
	emptyPayload := providertest.LoadFixture(t, "search_empty.json")

	var captured searchWireReq
	emptyMode := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tools/search" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer sk-test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
			return
		}
		_ = json.UnmarshalRead(r.Body, &captured)
		w.Header().Set("X-Request-ID", "ds-req-1")
		if emptyMode {
			_, _ = w.Write(emptyPayload)
			return
		}
		_, _ = w.Write(okPayload)
	}))
	defer srv.Close()

	p, ok, err := New(Options{Keys: []string{"sk-test"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	if err != nil || !ok {
		t.Fatalf("New: ok=%v err=%v", ok, err)
	}

	fresh, _ := core.ParseFreshness("2026-08-01..2026-09-30", time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:    "go mcp",
		Question: "how to write go mcp server",
		Count:    5,
		Detail:   core.DetailSummary,
		Filters: core.Filters{
			Sites:        []string{"github.com", "example.com"},
			ExcludeSites: []string{"spam.com"},
			Lang:         []string{"zh", "en"},
			Freshness:    fresh,
		},
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, out)
	if !strings.Contains(captured.Query, "site:github.com OR site:example.com") ||
		!strings.Contains(captured.Query, "-site:spam.com") ||
		!strings.Contains(captured.Query, "lang:zh,en") ||
		!strings.Contains(captured.Query, "after:2026-08-01") {
		t.Fatalf("unexpected composed query: %q", captured.Query)
	}
	if out.Cost != nil {
		t.Fatalf("expected nil default cost without price_search, got %+v", out.Cost)
	}
	if len(out.Results) != 2 || *out.Results[0].Authority != 0.75 || out.Results[1].Authority != nil {
		t.Fatalf("unexpected normalized results: %+v", out.Results)
	}

	// 显式配置 price_search 时在有结果下按配置计费，空结果不计费
	priced, _, err := New(Options{Keys: []string{"sk-test"}, BaseURL: srv.URL, PriceSearch: "CNY:0.005"}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatalf("New priced: %v", err)
	}
	pricedOut, err := priced.Search(context.Background(), &core.SearchRequest{Query: "go mcp", Count: 5, Detail: core.DetailSummary})
	if err != nil || pricedOut.Cost == nil || pricedOut.Cost.Micros != 5000 || pricedOut.Cost.Currency != core.CurrencyCNY {
		t.Fatalf("unexpected configured cost: %+v err=%v", pricedOut.Cost, err)
	}

	// 空结果绝不计费
	emptyMode = true
	emptyOut, err := priced.Search(context.Background(), &core.SearchRequest{Query: "nothing", Count: 5, Detail: core.DetailSummary})
	if err != nil || emptyOut.Cost != nil {
		t.Fatalf("expected nil cost on empty results, cost=%+v err=%v", emptyOut.Cost, err)
	}

	// 超过 128 Unicode 字符必须在本地拒绝并标注 Provider="deepseek"
	_, err = p.Search(context.Background(), &core.SearchRequest{Query: strings.Repeat("深", 129)})
	if se := core.AsError(err); se.Code != core.CodeInvalidArgument || se.Provider != ProviderName {
		t.Fatalf("expected provider-scoped invalid_argument, got %v", err)
	}
}

func TestDeepSeekFetchAndErrors(t *testing.T) {
	fetchPayload := providertest.LoadFixture(t, "fetch_ok.json")
	errPayload := providertest.LoadFixture(t, "error.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body fetchWireReq
		_ = json.UnmarshalRead(r.Body, &body)
		if strings.Contains(body.URL, "402") {
			w.WriteHeader(http.StatusPaymentRequired)
			_, _ = w.Write(errPayload)
			return
		}
		if strings.Contains(body.URL, "404") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"message":"Page not readable"}}`))
			return
		}
		_, _ = w.Write(fetchPayload)
	}))
	defer srv.Close()

	p, _, _ := New(Options{Keys: []string{"sk-test"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	out, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/doc"})
	if err != nil || out.Page.Title != "Doc Page" || out.Cost != nil {
		t.Fatalf("unexpected Fetch result: out=%+v err=%v", out, err)
	}
	pricedFetch, _, _ := New(Options{Keys: []string{"sk-test"}, BaseURL: srv.URL, PriceFetch: "CNY:0.005"}, providertest.NewTestAPIClient(t))
	pfOut, err := pricedFetch.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/doc"})
	if err != nil || pfOut.Cost == nil || pfOut.Cost.Micros != 5000 {
		t.Fatalf("unexpected priced Fetch result: out=%+v err=%v", pfOut, err)
	}

	_, err = p.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/402"})
	if core.AsError(err).Code != core.CodeInsufficientBalance {
		t.Fatalf("expected insufficient_balance, got %v", err)
	}

	p2, _, _ := New(Options{Keys: []string{"sk-test"}, BaseURL: srv.URL}, providertest.NewTestAPIClient(t))
	_, err = p2.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/404"})
	if core.AsError(err).Code != core.CodeNotFound {
		t.Fatalf("expected not_found, got %v", err)
	}
}
