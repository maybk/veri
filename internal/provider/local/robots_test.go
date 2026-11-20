package local

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"veri/internal/core"
	"veri/internal/provider/providertest"
	"veri/internal/textnorm"
)

func TestLocalPlainTextRobotsLinkShellAndStatuses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/robots.txt":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("User-agent: *\nDisallow: /secret\n"))
		case "/secret/doc":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("should be blocked"))
		case "/plain":
			w.Header().Set("Content-Type", "text/markdown")
			_, _ = w.Write([]byte("# Plain Markdown\n\nDirect body."))
		case "/403":
			w.WriteHeader(http.StatusForbidden)
		case "/429":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/500":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	p, _, err := New(Options{
		MinContentRunes: 50,
		RespectRobots:   true,
		UserAgent:       "veri",
		Version:         "1.0.0",
	}, providertest.NewTestFetchClient(t))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/secret/doc"}); core.AsError(err).Code != core.CodeBlockedByPolicy {
		t.Fatalf("expected robots.txt disallow -> blocked_by_policy, got %v", err)
	}
	mdOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/plain"})
	if err != nil || !strings.Contains(mdOut.Page.Content, "# Plain Markdown") {
		t.Fatalf("unexpected plain markdown fetch: %+v err=%v", mdOut, err)
	}
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/403"}); core.AsError(err).Code != core.CodeBlockedByPolicy {
		t.Fatal("expected 403 -> blocked_by_policy")
	}
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/429"}); core.AsError(err).Code != core.CodeRateLimited {
		t.Fatal("expected 429 -> rate_limited")
	}
	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/500"}); core.AsError(err).Code != core.CodeUpstreamUnavailable {
		t.Fatal("expected 500 -> upstream_unavailable")
	}

	// 验证高噪比导航菜单空壳检测（linkRatio > 75% 且无长自然段落）
	linksMD := strings.Repeat("[Menu Link Item](https://example.com/nav) ", 30)
	qr := textnorm.EvaluatePageQuality("Portal", linksMD, 50)
	if !qr.LowQuality || qr.Reason != textnorm.QualityReasonLinkHeavyShell {
		t.Fatalf("expected link_heavy_shell, got %+v", qr)
	}

	robotsTxt := "User-agent: googlebot\nUser-agent: veri\nDisallow: /docs\nAllow: /docs/public\n\nUser-agent: *\nDisallow: /\n"
	if isPathDisallowedByRobots(robotsTxt, "veri/1.0.0", "/docs/public/intro") {
		t.Fatal("expected longer Allow rule in veri group to permit /docs/public/intro")
	}
	if !isPathDisallowedByRobots(robotsTxt, "veri/1.0.0", "/docs/internal") {
		t.Fatal("expected /docs/internal to be disallowed for veri")
	}
	if isPathDisallowedByRobots(robotsTxt, "veri/1.0.0", "/blog") {
		t.Fatal("expected specific veri group to override wildcard Disallow: /")
	}
}
