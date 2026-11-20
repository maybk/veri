package urlnorm

import (
	"net/url"
	"testing"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestTargetClassificationHelpers(t *testing.T) {
	privateURLs := []string{
		"http://localhost:3000/docs",
		"http://api.localhost:8080/v1",
		"http://127.0.0.1:8080/health",
		"http://[::1]:5173/",
		"http://192.168.1.25/wiki",
		"http://10.0.4.12:9000/metrics",
		"http://172.20.0.3/status",
		"http://100.101.102.103:8080/tailscale-service",
		"http://198.18.0.15/fakeip",
		"http://wiki.internal/roadmap",
		"http://nas.local:5000",
		"http://intranet-service:8080/api",
	}
	for _, raw := range privateURLs {
		if !IsLocalOrPrivateTarget(mustParseURL(t, raw)) {
			t.Errorf("expected IsLocalOrPrivateTarget(%q) == true", raw)
		}
	}

	publicURLs := []string{
		"https://go.dev/doc/effective_go",
		"https://93.184.216.34/index.html",
		"https://docs.tinyfish.ai/fetch-api",
	}
	for _, raw := range publicURLs {
		if IsLocalOrPrivateTarget(mustParseURL(t, raw)) {
			t.Errorf("expected IsLocalOrPrivateTarget(%q) == false", raw)
		}
	}

	if !LooksLikePDFURL(mustParseURL(t, "https://example.com/papers/spec-v2.PDF?download=1")) {
		t.Error("expected .PDF URL to match LooksLikePDFURL")
	}
	if !LooksLikePDFURL(mustParseURL(t, "https://arxiv.org/pdf/2502.12345")) {
		t.Error("expected arxiv /pdf/ URL without extension to match LooksLikePDFURL")
	}
	if LooksLikePDFURL(mustParseURL(t, "https://arxiv.org/abs/2502.12345")) {
		t.Error("expected arxiv /abs/ HTML abstract page not to match LooksLikePDFURL")
	}

	if !IsLikelyChineseHost(mustParseURL(t, "https://juejin.cn/post/123")) ||
		!IsLikelyChineseHost(mustParseURL(t, "https://zhuanlan.zhihu.com/p/456")) ||
		!IsLikelyChineseHost(mustParseURL(t, "https://mp.weixin.qq.com/s/abc")) {
		t.Error("expected Chinese domains to match IsLikelyChineseHost")
	}
	if IsLikelyChineseHost(mustParseURL(t, "https://developer.mozilla.org/en-US/docs/Web")) {
		t.Error("expected overseas domain not to match IsLikelyChineseHost")
	}
}
