package exa

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

func TestExaExtraBranches(t *testing.T) {
	for tag, want := range map[string]core.Code{
		"CRAWL_TIMEOUT":        core.CodeUpstreamTimeout,
		"SOURCE_NOT_AVAILABLE": core.CodeBlockedByPolicy,
		"UNSUPPORTED_URL":      core.CodeInvalidArgument,
		"CRAWL_NOT_FOUND":      core.CodeNotFound,
	} {
		if got := core.AsError(classifyCrawlTagError(tag, 0)).Code; got != want {
			t.Fatalf("tag %s -> %s, want %s", tag, got, want)
		}
	}
	if got := core.AsError(classifyCrawlTagError("CRAWL_FAILED", 429)).Code; got != core.CodeRateLimited {
		t.Fatalf("expected HTTP 429 crawl status -> rate_limited, got %s", got)
	}

	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.UnmarshalRead(r.Body, &captured)
		_, _ = w.Write([]byte(`{"requestId":"r","results":[],"warnings":[{"code":"W","message":"warn1"}]}`))
	}))
	defer srv.Close()

	p, _, _ := New(Options{Keys: []string{"k"}, BaseURL: srv.URL, AllowOver10: true}, providertest.NewTestAPIClient(t))
	if p.Capabilities().MaxCount != 100 {
		t.Fatal("expected MaxCount=100 when AllowOver10=true")
	}
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	fresh, _ := core.ParseFreshness("2026-08-01..2026-08-31", now)
	out, err := p.Search(context.Background(), &core.SearchRequest{
		Query:   "paper",
		Count:   25,
		Detail:  core.DetailContent,
		Options: map[string]string{"exa.category": "publication"},
		Filters: core.Filters{Sites: []string{"arxiv.org"}, ExcludeSites: []string{"bad.com"}, Freshness: fresh},
	})
	if err != nil || len(out.Warnings) != 1 {
		t.Fatalf("unexpected search out=%+v err=%v", out, err)
	}
	if captured["category"] != "publication" || captured["startPublishedDate"] == nil || captured["excludeDomains"] == nil {
		t.Fatalf("expected publication category to forward freshness and excludeDomains, got %+v", captured)
	}

	// 验证 people / company 类别大小写与多余空白规整、动态能力收紧与冲突参数拦截防 400
	peopleReq := &core.SearchRequest{
		Query:   "researcher",
		Options: map[string]string{"exa.category": "  PEOPLE "},
		Filters: core.Filters{ExcludeSites: []string{"bad.com"}, Freshness: fresh},
	}
	peopleCaps := p.CapabilitiesFor(peopleReq)
	if peopleCaps.Freshness != core.TimeNone || peopleCaps.ExcludeSites != 0 {
		t.Fatalf("expected people category to disable Freshness and ExcludeSites, got %+v", peopleCaps)
	}
	if got := normalizeExaCategory("  Personal   Site "); got != "personal site" {
		t.Fatalf("expected multi-word category normalization, got %q", got)
	}
	captured = nil
	pOut, err := p.Search(context.Background(), peopleReq)
	if err != nil {
		t.Fatalf("unexpected lenient people search error: %v", err)
	}
	if _, hasEx := captured["excludeDomains"]; hasEx {
		t.Fatalf("people category must not send excludeDomains, got %+v", captured)
	}
	if _, hasStart := captured["startPublishedDate"]; hasStart {
		t.Fatalf("people category must not send startPublishedDate, got %+v", captured)
	}
	if len(pOut.Warnings) < 3 {
		t.Fatalf("expected omission warnings + upstream warning, got %v", pOut.Warnings)
	}

	peopleStrict := *peopleReq
	peopleStrict.StrictFilters = true
	if _, err := p.Search(context.Background(), &peopleStrict); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected strict people filter conflict -> invalid_argument, got %v", err)
	}

	// 验证 /contents 的 fresh -> maxAgeHours=0、livecrawlTimeout 透传，以及 cache-only (-1) 不混传 livecrawlTimeout
	captured = nil
	_, _ = p.Fetch(context.Background(), &core.FetchRequest{
		URL:     "https://arxiv.org/pdf/2501.00001",
		Options: map[string]string{spec.OptFetchFresh: "true"},
	})
	if maxAge, ok := captured["maxAgeHours"].(float64); !ok || maxAge != 0 || captured["livecrawlTimeout"] == nil {
		t.Fatalf("expected maxAgeHours=0 and livecrawlTimeout on fresh fetch, got %+v", captured)
	}

	captured = nil
	_, _ = p.Fetch(context.Background(), &core.FetchRequest{
		URL:     "https://arxiv.org/pdf/2501.00001",
		Options: map[string]string{spec.OptExaMaxAgeHours: "-1"},
	})
	if maxAge, ok := captured["maxAgeHours"].(float64); !ok || maxAge != -1 {
		t.Fatalf("expected maxAgeHours=-1, got %+v", captured)
	}
	if _, hasLC := captured["livecrawlTimeout"]; hasLC {
		t.Fatalf("expected cache-only maxAgeHours=-1 to omit livecrawlTimeout, got %+v", captured)
	}

	if _, err := p.Fetch(context.Background(), &core.FetchRequest{
		URL:     "https://arxiv.org/pdf/2501.00001",
		Options: map[string]string{spec.OptExaMaxAgeHours: "-2"},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected exa.max_age_hours=-2 -> invalid_argument, got %v", err)
	}
}
