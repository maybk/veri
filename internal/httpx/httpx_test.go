package httpx

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/keypool"
)

func newTestKeyPool(spec string) *keypool.Pool {
	return keypool.New(spec)
}

type staticResolver map[string][]netip.Addr

func (r staticResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	if addrs, ok := r[strings.ToLower(host)]; ok {
		return addrs, nil
	}
	return nil, errors.New("host not found")
}

func TestSSRFGuardAddressMatrixAndFakeIP(t *testing.T) {
	fakeCIDRs, err := ParseCIDRs([]string{"198.18.0.0/15"})
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}
	resolver := staticResolver{
		"loopback.evil.test": {netip.MustParseAddr("127.0.0.1")},
		"metadata.evil.test": {netip.MustParseAddr("169.254.169.254")},
		"private.evil.test":  {netip.MustParseAddr("10.0.0.8")},
		"cgnat.evil.test":    {netip.MustParseAddr("100.64.1.2")},
		"classe.evil.test":   {netip.MustParseAddr("240.0.0.1")},
		"ula6.evil.test":     {netip.MustParseAddr("fd00::1")},
		"nat64.evil.test":    {netip.MustParseAddr("64:ff9b::7f00:1")},
		"6to4.evil.test":     {netip.MustParseAddr("2002:7f00:0001::1")},
		"teredo.evil.test":   {netip.MustParseAddr("2001:0000:4136:e378:8000:63bf:3fff:fdd2")},
		"nat64-pub.example":  {netip.MustParseAddr("64:ff9b::5db8:d822")},
		"tun-site.example":   {netip.MustParseAddr("198.19.0.17")},
		"public.example":     {netip.MustParseAddr("93.184.216.34")},
	}
	guard := &SSRFGuard{
		FakeIPCIDRs: fakeCIDRs,
		Resolver:    resolver,
	}

	var dialedAddr string
	fakeBaseDial := func(_ context.Context, _, addr string) (net.Conn, error) {
		dialedAddr = addr
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}
	dial := guard.SafeDialContext(fakeBaseDial)

	for _, host := range []string{
		"loopback.evil.test:80",
		"metadata.evil.test:80",
		"private.evil.test:443",
		"cgnat.evil.test:443",
		"classe.evil.test:443",
		"[::1]:80",
		"[fe80::1%en0]:80",
		"127.0.0.1:8080",
		"169.254.169.254:80",
		"240.1.2.3:80",
		"ula6.evil.test:443",
		"nat64.evil.test:443",
		"6to4.evil.test:443",
		"teredo.evil.test:443",
		"198.19.0.17:443",
	} {
		conn, dialErr := dial(context.Background(), "tcp", host)
		if dialErr == nil {
			_ = conn.Close()
			t.Fatalf("expected %s to be blocked by SSRF guard", host)
		}
		if se, ok := errors.AsType[*core.Error](dialErr); !ok || se.Code != core.CodeContentUnavailable {
			t.Fatalf("expected CodeContentUnavailable for %s, got %v", host, dialErr)
		}
	}

	conn, err := dial(context.Background(), "tcp", "tun-site.example:443")
	if err != nil {
		t.Fatalf("expected Fake-IP resolved domain to succeed, got %v", err)
	}
	_ = conn.Close()
	if dialedAddr != "198.19.0.17:443" {
		t.Fatalf("expected direct IP dial to 198.19.0.17:443, got %q", dialedAddr)
	}

	for _, okHost := range []string{"public.example:443", "nat64-pub.example:443"} {
		conn, err = dial(context.Background(), "tcp", okHost)
		if err != nil {
			t.Fatalf("expected %s to succeed, got %v", okHost, err)
		}
		_ = conn.Close()
	}
}

func TestSafeFetchLoopbackProxyAllowedWhilePrivateTargetBlocked(t *testing.T) {
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("proxied:" + r.URL.Host))
	}))
	defer proxySrv.Close()

	resolver := staticResolver{
		"public.example":   {netip.MustParseAddr("93.184.216.34")},
		"internal.example": {netip.MustParseAddr("10.0.0.9")},
	}
	client, err := NewSafeFetchClient(Options{
		ProxyURL:     proxySrv.URL,
		AllowPrivate: false,
		Resolver:     resolver,
	})
	if err != nil {
		t.Fatalf("NewSafeFetchClient with loopback proxy: %v", err)
	}

	reqOK, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://public.example/hello", nil)
	resp, err := client.Do(reqOK)
	if err != nil {
		t.Fatalf("expected loopback HTTP proxy to succeed for public host, got %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := ReadBodyLimited(resp, 1024)
	if err != nil || string(body) != "proxied:public.example" {
		t.Fatalf("unexpected proxied response: body=%q err=%v", string(body), err)
	}

	reqPriv, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://internal.example/secret", nil)
	respPriv, err := client.Do(reqPriv)
	if respPriv != nil && respPriv.Body != nil {
		_ = respPriv.Body.Close()
	}
	if core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("expected private host via proxy to be blocked by CheckHost, got %v", err)
	}
}

func TestSafeFetchRedirectHopBlocking(t *testing.T) {
	internalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret metadata"))
	}))
	defer internalSrv.Close()

	client, err := NewSafeFetchClient(Options{AllowPrivate: false})
	if err != nil {
		t.Fatalf("NewSafeFetchClient: %v", err)
	}

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, internalSrv.URL, nil)
	respLoop, err := client.Do(req)
	if respLoop != nil && respLoop.Body != nil {
		_ = respLoop.Body.Close()
	}
	if err == nil {
		t.Fatal("expected direct loopback fetch to be blocked")
	}
}

func TestReadBodyLimitedDecompressionBombAndContentLengthProtection(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	zeros := make([]byte, 2<<20)
	if _, err := gz.Write(zeros); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Encoding": []string{"gzip"}},
		Body:       io.NopCloser(bytes.NewReader(buf.Bytes())),
	}
	if _, err := ReadBodyLimited(resp, 64<<10); core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("expected gzip bomb exceeding maxBytes to fail, got %v", err)
	}

	declaredOversize := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: 10 << 20,
		Header:        http.Header{},
		Body:          io.NopCloser(strings.NewReader("tiny")),
	}
	if _, err := ReadBodyLimited(declaredOversize, 1024); core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("expected oversize Content-Length fast-fail, got %v", err)
	}
}

func TestValidateAPIBaseURLAndRetryRules(t *testing.T) {
	if got, err := ResolveAPIBaseURL("https://api.deepseek.com/", "https://fallback.example"); err != nil || got != "https://api.deepseek.com" {
		t.Fatalf("ResolveAPIBaseURL: got=%q err=%v", got, err)
	}
	if got, err := ResolveAPIBaseURL("", "https://api.exa.ai/"); err != nil || got != "https://api.exa.ai" {
		t.Fatalf("ResolveAPIBaseURL fallback: got=%q err=%v", got, err)
	}
	if _, err := ValidateAPIBaseURL("http://127.0.0.1:8080"); err != nil {
		t.Fatalf("expected loopback http to be allowed for local gateway/tests: %v", err)
	}
	if _, err := ValidateAPIBaseURL("http://api.deepseek.com"); err == nil {
		t.Fatal("expected remote http API base_url to be rejected")
	}

	if !ShouldRetryStatus(429) || !ShouldRetryStatus(503) || ShouldRetryStatus(400) || ShouldRetryStatus(402) {
		t.Fatal("unexpected ShouldRetryStatus matrix")
	}

	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if d := RetryDelay("3", now); d != 3*time.Second {
		t.Fatalf("RetryDelay(3) = %v, want 3s", d)
	}
	if d := RetryDelay("999999999999", now); d != 10*time.Second {
		t.Fatalf("RetryDelay(huge) = %v, want capped 10s", d)
	}
	if d := ParseRetryAfter("999999999999", now); d != 86400*time.Second {
		t.Fatalf("ParseRetryAfter(huge) = %v, want capped 86400s", d)
	}
}

func TestDoJSONKeyFailoverInspectBodyAndNoTimeoutReplay(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("Authorization") == "Bearer bad-key" {
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":"rate limit"}`))
			return
		}
		w.Header().Set("X-Request-ID", "ok-req")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	client, err := NewAPIClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.DoJSON(context.Background(), newTestKeyPool("bad-key,good-key"), NewGate(4, 0), RequestSpec{
		ProviderName: "demo",
		Method:       http.MethodPost,
		URL:          srv.URL,
		Timeout:      2 * time.Second,
		AuthHeader:   "Authorization",
		AuthPrefix:   "Bearer ",
		Body:         map[string]string{"q": "go"},
	})
	if err != nil || resp.RequestID != "ok-req" || hits.Load() != 2 {
		t.Fatalf("expected key failover on 2nd hit, resp=%+v hits=%d err=%v", resp, hits.Load(), err)
	}

	// HTTP 200 内嵌业务错误通过 InspectBody 触发备用 Key 切换
	var appErrHits atomic.Int32
	appErrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		appErrHits.Add(1)
		w.WriteHeader(http.StatusOK)
		if r.Header.Get("Authorization") == "Bearer dead-token" {
			_, _ = w.Write([]byte(`{"error":"AuthFailure"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer appErrSrv.Close()

	resp2, err := client.DoJSON(context.Background(), newTestKeyPool("dead-token,live-token"), NewGate(4, 0), RequestSpec{
		ProviderName: "wsa",
		Method:       http.MethodPost,
		URL:          appErrSrv.URL,
		AuthHeader:   "Authorization",
		AuthPrefix:   "Bearer ",
		InspectBody: func(_ int, _ http.Header, body []byte) *core.Error {
			if strings.Contains(string(body), "AuthFailure") {
				return core.NewError(core.CodeAuthFailed, "auth failed in 200 payload")
			}
			return nil
		},
	})
	if err != nil || resp2 == nil || appErrHits.Load() != 2 {
		t.Fatalf("expected InspectBody failover on 2nd hit, hits=%d err=%v", appErrHits.Load(), err)
	}

	// 单 Key 触发 429 且无 Retry-After 时应立即返回，不空等重试冷却中的同一 Key
	var rlHits atomic.Int32
	rlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rlHits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer rlSrv.Close()
	if _, err := client.DoJSON(context.Background(), newTestKeyPool("only-key"), NewGate(4, 0), RequestSpec{
		ProviderName: "rl",
		Method:       http.MethodGet,
		URL:          rlSrv.URL,
		AuthHeader:   "Authorization",
		AuthPrefix:   "Bearer ",
	}); core.AsError(err).Code != core.CodeRateLimited || rlHits.Load() != 1 {
		t.Fatalf("expected single-key 429 without Retry-After to fail fast in 1 hit, hits=%d err=%v", rlHits.Load(), err)
	}

	// 单 Key 触发 429 但 ClassifyErr 判定为不可重试业务错误（如 Linkup 余额不足）时绝不退避重试
	var nonRetry429Hits atomic.Int32
	nonRetry429Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nonRetry429Hits.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"INSUFFICIENT_FUNDS_CREDITS","message":"out of credits"}}`))
	}))
	defer nonRetry429Srv.Close()
	if _, err := client.DoJSON(context.Background(), newTestKeyPool("only-key"), NewGate(4, 0), RequestSpec{
		ProviderName: "linkup",
		Method:       http.MethodPost,
		URL:          nonRetry429Srv.URL,
		AuthHeader:   "Authorization",
		AuthPrefix:   "Bearer ",
		ClassifyErr: func(status int, _ http.Header, _ []byte) *core.Error {
			return core.NewError(core.CodeInsufficientBalance, "out of credits").WithStatus(status)
		},
	}); core.AsError(err).Code != core.CodeInsufficientBalance || nonRetry429Hits.Load() != 1 {
		t.Fatalf("non-retryable 429 must NOT retry even with Retry-After; hits=%d err=%v", nonRetry429Hits.Load(), err)
	}

	// 网络读超时严禁自动重放请求
	var slowHits atomic.Int32
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		slowHits.Add(1)
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowSrv.Close()

	_, err = client.DoJSON(context.Background(), newTestKeyPool("k1,k2"), NewGate(4, 0), RequestSpec{
		ProviderName: "slow",
		Method:       http.MethodPost,
		URL:          slowSrv.URL,
		Timeout:      40 * time.Millisecond,
		AuthHeader:   "Authorization",
		AuthPrefix:   "Bearer ",
	})
	if core.AsError(err).Code != core.CodeUpstreamTimeout || slowHits.Load() != 1 {
		t.Fatalf("read timeout must NOT retry; hits=%d err=%v", slowHits.Load(), err)
	}

	// 无密钥 Provider（如自托管 Firecrawl）遇 HTTP 429 且 Retry-After > maxRetryWait 时必须立即失败，绝不阻塞等待
	var keylessHits atomic.Int32
	keylessSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		keylessHits.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer keylessSrv.Close()

	keylessStart := time.Now()
	_, err = client.DoJSON(context.Background(), nil, NewGate(4, 0), RequestSpec{
		ProviderName: "firecrawl-selfhost",
		Method:       http.MethodPost,
		URL:          keylessSrv.URL,
	})
	if core.AsError(err).Code != core.CodeRateLimited || keylessHits.Load() != 1 || time.Since(keylessStart) > 500*time.Millisecond {
		t.Fatalf("keyless 429 with long Retry-After must fail fast without sleeping; hits=%d elapsed=%v err=%v", keylessHits.Load(), time.Since(keylessStart), err)
	}
}

func TestAPIBaseURLRedactionAndRedirectSecurity(t *testing.T) {
	if _, err := ValidateAPIBaseURL("https://user:TopSecretPass42@api.example.com"); err == nil || strings.Contains(err.Error(), "TopSecretPass42") {
		t.Fatalf("ValidateAPIBaseURL must reject and redact credentials, got %v", err)
	}

	mustReq := func(rawURL string) *http.Request {
		req, err := http.NewRequest(http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	mustURL := func(rawURL string) *url.URL {
		u, err := url.Parse(rawURL)
		if err != nil {
			t.Fatal(err)
		}
		return u
	}

	// 远端 HTTPS 源严禁 302 降级至 http://127.0.0.1 或跳往私网 IP 字面量
	remoteOrigin := []*http.Request{mustReq("https://api.example.com/v1/search")}
	for _, blocked := range []string{
		"http://127.0.0.1:8080/internal",
		"https://localhost:8443/admin",
		"https://localhost.:8443/admin",
		"https://api.localhost:8443/admin",
		"https://10.0.0.8/secret",
		"https://192.168.1.1/router",
	} {
		if err := validateAPIRedirectTarget(mustURL(blocked), remoteOrigin); err == nil {
			t.Fatalf("expected redirect from remote HTTPS to %s to be blocked", blocked)
		}
	}
	if err := validateAPIRedirectTarget(mustURL("https://cdn.example.com/v2/search"), remoteOrigin); err != nil {
		t.Fatalf("expected public HTTPS redirect to be allowed, got %v", err)
	}
}
