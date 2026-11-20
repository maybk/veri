package exa

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestExaLiteWhitelistCostAndContentsTags(t *testing.T) {
	okPayload := providertest.LoadFixture(t, "search_ok.json")
	emptyPayload := providertest.LoadFixture(t, "search_empty.json")
	contentsOKPayload := providertest.LoadFixture(t, "contents_ok.json")
	errPayload := providertest.LoadFixture(t, "error.json")

	var rawSearchPayload map[string]any
	mode := "ok"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "exa-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if mode == "err403" {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(errPayload)
			return
		}
		switch r.URL.Path {
		case "/search":
			rawSearchPayload = nil
			_ = json.UnmarshalRead(r.Body, &rawSearchPayload)
			if mode == "empty" {
				_, _ = w.Write(emptyPayload)
				return
			}
			_, _ = w.Write(okPayload)
		case "/contents":
			var req contentsWireReq
			_ = json.UnmarshalRead(r.Body, &req)
			if len(req.URLs) > 0 && req.URLs[0] == "https://blocked.example.com" {
				_, _ = w.Write([]byte(`{
					"requestId": "exa-c-err",
					"results": [],
					"statuses": [{"id": "https://blocked.example.com", "status": "error", "error": {"tag": "CRAWL_HTTP_403", "httpStatusCode": 403}}]
				}`))
				return
			}
			_, _ = w.Write(contentsOKPayload)
		}
	}))
	defer srv.Close()

	p, enabled, err := New(Options{Keys: []string{"exa-key"}, BaseURL: srv.URL, SearchType: "lite"}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled {
		t.Fatalf("New: enabled=%v err=%v", enabled, err)
	}

	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:        "exa api",
		Count:        5,
		Detail:       core.DetailSummary,
		IncludeExtra: true,
	})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	providertest.AssertSearchOutcomeInvariants(t, ProviderName, out)

	contentsField, _ := rawSearchPayload["contents"].(map[string]any)
	if _, bad := contentsField["maxAgeHours"]; bad {
		t.Fatal("lite mode must not send maxAgeHours")
	}
	if _, bad := contentsField["summary"]; bad {
		t.Fatal("summary must not be requested by default")
	}
	if out.Cost == nil || out.Cost.Micros != 4000 || out.Cost.Estimated {
		t.Fatalf("expected exact costDollars $0.004, got %+v", out.Cost)
	}

	mode = "empty"
	eOut, err := p.Search(context.Background(), &core.SearchRequest{Query: "none"})
	if err != nil || len(eOut.Results) != 0 {
		t.Fatalf("unexpected empty search: %+v err=%v", eOut, err)
	}

	mode = "err403"
	_, err = p.Search(context.Background(), &core.SearchRequest{Query: "none"})
	if core.AsError(err).Code != core.CodeNotEnabled {
		t.Fatalf("expected FEATURE_DISABLED -> not_enabled, got %v", err)
	}

	mode = "ok"
	_, err = p.Fetch(context.Background(), &core.FetchRequest{URL: "https://blocked.example.com"})
	if core.AsError(err).Code != core.CodeBlockedByPolicy {
		t.Fatalf("expected blocked_by_policy for CRAWL_HTTP_403, got %v", err)
	}
	fOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://exa.ai/doc"})
	if err != nil || fOut.Cost.Micros != 1000 || fOut.Cost.Estimated {
		t.Fatalf("unexpected Fetch outcome: %+v err=%v", fOut, err)
	}

	// 验证内联 category:research_paper -> publication 归一化与 Question -> highlights.query 透传
	_, err = p.Search(context.Background(), &core.SearchRequest{
		Query:    `category:"research paper" attention mechanism`,
		Question: "How does multi-head attention scale?",
		Detail:   core.DetailContent,
	})
	if err != nil {
		t.Fatalf("Search with inline category and question failed: %v", err)
	}
	if rawSearchPayload["query"] != "attention mechanism" || rawSearchPayload["category"] != "publication" {
		t.Fatalf("expected inline category stripped and mapped to publication, got %+v", rawSearchPayload)
	}
	cMap, _ := rawSearchPayload["contents"].(map[string]any)
	hlMap, _ := cMap["highlights"].(map[string]any)
	if hlMap["query"] != "How does multi-head attention scale?" {
		t.Fatalf("expected highlights.query populated, got %+v", cMap)
	}
}
