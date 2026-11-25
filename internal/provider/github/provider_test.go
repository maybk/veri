package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestGitHubMatchRoutesAndRateLimitHint(t *testing.T) {
	issuePayload := providertest.LoadFixture(t, "issue_ok.json")
	errPayload := providertest.LoadFixture(t, "error.json")

	rateLimited := false
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-GitHub-Api-Version") != "2022-11-28" {
			t.Errorf("missing X-GitHub-Api-Version header")
		}
		if rateLimited {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write(errPayload)
			return
		}
		switch r.URL.Path {
		case "/repos/modelcontextprotocol/go-sdk/readme":
			_, _ = w.Write([]byte("# MCP Go SDK\n\nOfficial SDK."))
		case "/repos/modelcontextprotocol/go-sdk/issues/42":
			_, _ = w.Write(issuePayload)
		case "/repos/modelcontextprotocol/go-sdk/issues/42/comments":
			_, _ = w.Write([]byte(`[{"body":"LGTM!","created_at":"2026-10-02T00:00:00Z","user":{"login":"reviewer"}}]`))
		}
	}))
	defer apiSrv.Close()

	rawSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("package main\n\nfunc main() {}\n"))
	}))
	defer rawSrv.Close()

	p, enabled, err := New(Options{
		APIBaseURL: apiSrv.URL,
		RawBaseURL: rawSrv.URL,
	}, providertest.NewTestAPIClient(t))
	if err != nil || !enabled {
		t.Fatalf("New: enabled=%v err=%v", enabled, err)
	}

	mustURL := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	if !p.Match(mustURL("https://github.com/modelcontextprotocol/go-sdk")) ||
		!p.Match(mustURL("https://github.com/modelcontextprotocol/go-sdk/blob/main/main.go")) ||
		!p.Match(mustURL("https://github.com/modelcontextprotocol/go-sdk/issues/42")) {
		t.Fatal("expected GitHub routes to match")
	}
	if p.Match(mustURL("https://github.com/modelcontextprotocol/go-sdk/wiki/Home")) ||
		p.Match(mustURL("https://github.com/trending")) {
		t.Fatal("wiki and trending must not match specialist extractor")
	}

	readmeOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/modelcontextprotocol/go-sdk"})
	if err != nil || readmeOut.Page.SourceType != "github_readme" || !strings.Contains(readmeOut.Page.Content, "# MCP Go SDK") {
		t.Fatalf("unexpected readme fetch: %+v err=%v", readmeOut, err)
	}

	blobOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/modelcontextprotocol/go-sdk/blob/main/main.go"})
	if err != nil || blobOut.Page.SourceType != "github_file" || !strings.Contains(blobOut.Page.Content, "```go") {
		t.Fatalf("unexpected blob fetch: %+v err=%v", blobOut, err)
	}

	issueOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/modelcontextprotocol/go-sdk/issues/42"})
	if err != nil || issueOut.Page.SourceType != "github_issue" || !strings.Contains(issueOut.Page.Content, "LGTM!") {
		t.Fatalf("unexpected issue fetch: %+v err=%v", issueOut, err)
	}

	rateLimited = true
	_, err = p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/modelcontextprotocol/go-sdk"})
	se := core.AsError(err)
	if se.Code != core.CodeRateLimited || !strings.Contains(se.Hint, "GITHUB_TOKEN") {
		t.Fatalf("expected rate_limited with GITHUB_TOKEN hint, got %+v", se)
	}
}

func TestGitHubPathTraversalAndTokenFailover(t *testing.T) {
	mustURL := func(s string) *url.URL { u, _ := url.Parse(s); return u }
	for _, bad := range []string{
		"https://github.com/octocat/Hello-World/blob/main/../../secret/repo/main/a.md",
		"https://github.com/octocat/Hello-World/blob/main/%2e%2e/secret.md",
		"https://github.com/octocat/Hello-World/blob/main/dir%2ffile.go",
		"https://github.com/octocat/Hello-World/blob/main/./file.go",
		"https://github.com/octocat/...git",
		"https://github.com/octocat/..git",
	} {
		if parseGitHubURL(mustURL(bad)).kind != routeNone {
			t.Fatalf("expected path-traversal URL %s to be rejected", bad)
		}
	}

	var seenAuth []string
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		seenAuth = append(seenAuth, auth)
		switch auth {
		case "Bearer ghp_bad1":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
		case "Bearer ghp_good2":
			_, _ = w.Write([]byte("# Private Readme\n"))
		case "Bearer ghp_limited", "Bearer ghp_limited2", "Bearer ghp_limited3", "Bearer ghp_limited4":
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer apiSrv.Close()

	// 1. 多 Token 故障切换不应跳过第 2 把健康 Token
	pPool, _, err := New(Options{
		Tokens:     []string{"ghp_bad1", "ghp_good2", "ghp_spare3"},
		APIBaseURL: apiSrv.URL,
		RawBaseURL: apiSrv.URL,
	}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	out, err := pPool.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/org/private-repo"})
	if err != nil || !strings.Contains(out.Page.Content, "Private Readme") {
		t.Fatalf("expected failover to ghp_good2, got out=%+v err=%v", out, err)
	}
	if len(seenAuth) != 2 || seenAuth[1] != "Bearer ghp_good2" {
		t.Fatalf("expected exact sequential failover [ghp_bad1 -> ghp_good2], got %v", seenAuth)
	}

	// 2. 已配置 Token 触发限流时必须直接返回 CodeRateLimited，不得静默降级匿名请求导致私有仓库误报 404
	seenAuth = nil
	pRL, _, err := New(Options{
		Tokens:     []string{"ghp_limited"},
		APIBaseURL: apiSrv.URL,
		RawBaseURL: apiSrv.URL,
	}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pRL.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/org/private-repo"}); core.AsError(err).Code != core.CodeRateLimited || len(seenAuth) != 1 {
		t.Fatalf("expected single rate-limited token to return CodeRateLimited without anonymous retry, err=%v seen=%v", err, seenAuth)
	}

	// 3. 多把限流 Token 最多切换重试 1 次，且始终保留真实 CodeRateLimited（不误报 CodeAuthFailed）
	seenAuth = nil
	pMultiRL, _, err := New(Options{
		Tokens:     []string{"ghp_limited", "ghp_limited2", "ghp_limited3", "ghp_limited4"},
		APIBaseURL: apiSrv.URL,
		RawBaseURL: apiSrv.URL,
	}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pMultiRL.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/org/private-repo"}); core.AsError(err).Code != core.CodeRateLimited || len(seenAuth) != 2 {
		t.Fatalf("expected 4-token rate-limited pool to stop after 1 spare retry with CodeRateLimited, err=%v seen=%v", err, seenAuth)
	}
}

func TestGitHubNoStorePropagation(t *testing.T) {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/org/secret/readme":
			w.Header().Set("Cache-Control", "private, no-store")
			_, _ = w.Write([]byte("# Secret README"))
		default:
			w.Header().Set("Cache-Control", "private, max-age=60")
			_, _ = w.Write([]byte("# Public README"))
		}
	}))
	defer apiSrv.Close()

	p, _, err := New(Options{
		APIBaseURL: apiSrv.URL,
		RawBaseURL: apiSrv.URL,
	}, providertest.NewTestAPIClient(t))
	if err != nil {
		t.Fatal(err)
	}
	outNoStore, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/org/secret"})
	if err != nil || outNoStore == nil || !outNoStore.NoStore {
		t.Fatalf("expected NoStore=true for GitHub no-store response, got out=%+v err=%v", outNoStore, err)
	}
	outNormal, err := p.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/org/public"})
	if err != nil || outNormal == nil || outNormal.NoStore {
		t.Fatalf("expected NoStore=false for private max-age=60 response, got out=%+v err=%v", outNormal, err)
	}
}
