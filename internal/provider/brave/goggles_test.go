package brave

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestBraveExtraBranches(t *testing.T) {
	if normalizeBraveLang("zh_TW") != "zh-hant" || normalizeBraveLang("en") != "en" {
		t.Fatal("unexpected normalizeBraveLang")
	}
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	for u, want := range map[core.FreshnessUnit]string{
		core.UnitDay: "pd", core.UnitWeek: "pw", core.UnitMonth: "pm", core.UnitYear: "py",
	} {
		if got := formatBraveFreshness(&core.Freshness{Unit: u, N: 1}, now); got != want {
			t.Fatalf("unit %s -> %s, want %s", u, got, want)
		}
	}
	abs, _ := core.ParseFreshness("2026-08-01..2026-08-31", now)
	if got := formatBraveFreshness(abs, now); got != "2026-08-01to2026-08-31" {
		t.Fatalf("unexpected abs freshness: %s", got)
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"detail":"Invalid token"}}`))
	}))
	defer srv.Close()

	p, _, _ := New(Options{Keys: []string{"k"}, BaseURL: srv.URL, Mode: "web", PriceSearch: "USD:0.005"}, providertest.NewTestAPIClient(t))
	if p.Capabilities().MaxCount != 20 {
		t.Fatal("expected MaxCount=20 in web mode")
	}
	// 超出 50 词或 400 字符直接拒绝
	if _, err := p.Search(context.Background(), &core.SearchRequest{Query: strings.Repeat("word ", 51)}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatal("expected >50 words invalid_argument")
	}
	if _, err := p.Search(context.Background(), &core.SearchRequest{Query: strings.Repeat("a", 401)}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatal("expected >400 chars invalid_argument")
	}
	// 403 -> auth_failed
	if _, err := p.Search(context.Background(), &core.SearchRequest{Query: "go"}); core.AsError(err).Code != core.CodeAuthFailed {
		t.Fatal("expected 403 -> auth_failed")
	}
}
