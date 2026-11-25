package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestGitHubReleasePRLineRangeBinaryAndStaleTokenFallback(t *testing.T) {
	var sawToken string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth == "Bearer ghp_expiredtoken" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		sawToken = auth
		switch r.URL.Path {
		case "/repos/maybk/veri/releases/tags/v1.0.0", "/repos/maybk/veri/releases/latest", "/repos/maybk/veri/releases/tags/gopls/v0.16.0":
			_, _ = w.Write([]byte(`{"name":"v1.0.0 GA","tag_name":"v1.0.0","body":"Initial release notes with <T>","published_at":"2026-11-16T08:00:00Z","author":{"login":"maybk"}}`))
		case "/repos/maybk/veri/pulls/7":
			_, _ = w.Write([]byte(`{"title":"Add TinyFish","state":"closed","merged":true,"body":"Implements adapter","created_at":"2026-11-16T06:00:00Z","user":{"login":"maybk"},"head":{"ref":"feat/tinyfish"},"base":{"ref":"main"}}`))
		case "/repos/maybk/veri/issues/7/comments":
			_, _ = w.Write([]byte(`[]`))
		case "/repos/maybk/veri/pulls/7/comments":
			_, _ = w.Write([]byte(`[{"body":"Nit: check nil slice","created_at":"2026-11-16T07:00:00Z","path":"fetch.go","line":42,"user":{"login":"alice"}}]`))
		case "/repos/maybk/veri/discussions/12":
			_, _ = w.Write([]byte(`{"title":"Architecture Q&A","state":"open","body":"Why pure Go?","created_at":"2026-11-16T09:00:00Z","answer_html_url":"https://github.com/x","category":{"name":"Q&A"},"user":{"login":"bob"}}`))
		case "/repos/maybk/veri/discussions/12/comments":
			_, _ = w.Write([]byte(`[{"body":"Zero external runtime.","created_at":"2026-11-16T09:05:00Z","user":{"login":"maybk"}}]`))
		case "/repos/maybk/veri/commits/a1b2c3d4":
			_, _ = w.Write([]byte(`{"sha":"a1b2c3d4e5f60000","commit":{"message":"feat: init\n\ndetailed body","author":{"name":"May","date":"2026-11-16T10:00:00Z"}},"author":{"login":"maybk"},"stats":{"additions":10,"deletions":2,"total":12},"files":[{"filename":"main.go","status":"modified","additions":10,"deletions":2,"patch":"@@ -1 +1 @@\n+func main() {}"}]}`))
		case "/maybk/veri/main/cmd.go":
			_, _ = w.Write([]byte("line1\nline2\nline3\nline4\n"))
		case "/maybk/veri/main/logo.png":
			_, _ = w.Write([]byte{0x89, 0x50, 0x4e, 0x47, 0x00, 0x0d})
		}
	}))
	defer apiSrv.Close()

	p, _, err := New(Options{
		Tokens:     []string{"ghp_testtoken1234"},
		APIBaseURL: apiSrv.URL,
		RawBaseURL: apiSrv.URL,
	}, providertest.NewTestAPIClient(t))
	if err != nil || !p.HasToken() {
		t.Fatalf("New with token failed: %v", err)
	}

	relOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/maybk/veri/releases/tag/v1.0.0"})
	if err != nil || relOut.Page.SourceType != "github_release" || !strings.Contains(relOut.Page.Content, "Initial release notes with <T>") {
		t.Fatalf("unexpected release fetch: %+v err=%v", relOut, err)
	}
	if sawToken != "Bearer ghp_testtoken1234" {
		t.Fatalf("expected Bearer token header, got %q", sawToken)
	}

	prOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/maybk/veri/pull/7/files"})
	if err != nil || prOut.Page.SourceType != "github_pr" || !strings.Contains(prOut.Page.Content, "**State**: merged") || !strings.Contains(prOut.Page.Content, "feat/tinyfish -> main") || !strings.Contains(prOut.Page.Content, "fetch.go:42") {
		t.Fatalf("unexpected PR fetch: %+v err=%v", prOut, err)
	}

	latestOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/maybk/veri/releases/latest"})
	if err != nil || latestOut.Page.SourceType != core.SourceGitHubRelease {
		t.Fatalf("unexpected releases/latest fetch: %+v err=%v", latestOut, err)
	}
	monoTagOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/maybk/veri/releases/tag/gopls%2Fv0.16.0"})
	if err != nil || monoTagOut.Page.SourceType != core.SourceGitHubRelease {
		t.Fatalf("unexpected encoded-slash release tag fetch: %+v err=%v", monoTagOut, err)
	}
	discOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/maybk/veri/discussions/12"})
	if err != nil || discOut.Page.SourceType != core.SourceGitHubDiscussion || !strings.Contains(discOut.Page.Content, "Zero external runtime.") || !strings.Contains(discOut.Page.Content, "**Answered**: yes") {
		t.Fatalf("unexpected discussion fetch: %+v err=%v", discOut, err)
	}
	commitOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/maybk/veri/commit/a1b2c3d4"})
	if err != nil || commitOut.Page.SourceType != core.SourceGitHubCommit || !strings.Contains(commitOut.Page.Content, "```diff") {
		t.Fatalf("unexpected commit fetch: %+v err=%v", commitOut, err)
	}

	lineOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/maybk/veri/blob/main/cmd.go#L2-L3"})
	if err != nil || !strings.Contains(lineOut.Page.Title, "#L2-L3") || !strings.Contains(lineOut.Page.Content, "line2\nline3") || strings.Contains(lineOut.Page.Content, "line1") {
		t.Fatalf("unexpected line-range blob fetch: %+v err=%v", lineOut, err)
	}

	if _, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/maybk/veri/blob/main/logo.png"}); core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("expected binary blob rejection, got %v", err)
	}

	pStale, _, _ := New(Options{
		Tokens:     []string{"ghp_expiredtoken"},
		APIBaseURL: apiSrv.URL,
		RawBaseURL: apiSrv.URL,
	}, providertest.NewTestAPIClient(t))
	if anonOut, err := pStale.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/maybk/veri/releases/tag/v1.0.0"}); err != nil || anonOut == nil {
		t.Fatalf("expected stale token 401 to fall back to anonymous public fetch, got %v", err)
	}
}
