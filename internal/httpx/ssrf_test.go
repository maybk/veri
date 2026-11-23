package httpx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"

	"veri/internal/core"
)

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
		conn, dialErr := dial(t.Context(), "tcp", host)
		if dialErr == nil {
			_ = conn.Close()
			t.Fatalf("expected %s to be blocked by SSRF guard", host)
		}
		if se, ok := errors.AsType[*core.Error](dialErr); !ok || se.Code != core.CodeContentUnavailable {
			t.Fatalf("expected CodeContentUnavailable for %s, got %v", host, dialErr)
		}
	}

	conn, err := dial(t.Context(), "tcp", "tun-site.example:443")
	if err != nil {
		t.Fatalf("expected Fake-IP resolved domain to succeed, got %v", err)
	}
	_ = conn.Close()
	if dialedAddr != "198.19.0.17:443" {
		t.Fatalf("expected direct IP dial to 198.19.0.17:443, got %q", dialedAddr)
	}

	for _, okHost := range []string{"public.example:443", "nat64-pub.example:443"} {
		conn, err = dial(t.Context(), "tcp", okHost)
		if err != nil {
			t.Fatalf("expected %s to succeed, got %v", okHost, err)
		}
		_ = conn.Close()
	}
}

func TestSSRFGuardCIDRURLAndHostValidation(t *testing.T) {
	if _, err := ParseCIDRs([]string{"bad-cidr"}); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
	fakeCIDRs, err := ParseCIDRs([]string{"198.18.0.0/15", ""})
	if err != nil {
		t.Fatalf("ParseCIDRs valid: %v", err)
	}
	if !IsFakeIP(netip.MustParseAddr("198.18.1.2"), fakeCIDRs) || IsFakeIP(netip.MustParseAddr("8.8.8.8"), fakeCIDRs) {
		t.Fatal("unexpected IsFakeIP result")
	}

	resolver := staticResolver{
		"ok.test":    {netip.MustParseAddr("93.184.216.34")},
		"priv.test":  {netip.MustParseAddr("192.168.1.1")},
		"empty.test": {},
	}
	guard := &SSRFGuard{FakeIPCIDRs: fakeCIDRs, Resolver: resolver}

	if err := guard.ValidateFetchURL(nil); err == nil {
		t.Fatal("expected nil URL error")
	}
	for _, raw := range []string{
		"https:///path",
		"https://u:p@example.com",
		"http://198.18.0.5/a",
	} {
		u, _ := url.Parse(raw)
		if err := guard.ValidateFetchURL(u); err == nil {
			t.Fatalf("expected ValidateFetchURL(%q) to fail", raw)
		}
	}

	// CheckHost（外部代理预解析路径）
	if err := guard.CheckHost(t.Context(), "ok.test"); err != nil {
		t.Fatalf("CheckHost ok.test: %v", err)
	}
	for _, host := range []string{"priv.test", "empty.test", "missing.test", "127.0.0.1"} {
		if err := guard.CheckHost(t.Context(), host); err == nil {
			t.Fatalf("expected CheckHost(%q) to fail", host)
		}
	}

	// AllowPrivate=true 直连分支
	privGuard := &SSRFGuard{AllowPrivate: true}
	allowDial := privGuard.SafeDialContext(func(_ context.Context, _, _ string) (net.Conn, error) {
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	})
	conn, err := allowDial(t.Context(), "tcp", "127.0.0.1:80")
	if err != nil {
		t.Fatalf("AllowPrivate dial failed: %v", err)
	}
	_ = conn.Close()
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

	reqOK, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://public.example/hello", nil)
	resp, err := client.Do(reqOK)
	if err != nil {
		t.Fatalf("expected loopback HTTP proxy to succeed for public host, got %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := ReadBodyLimited(resp, 1024)
	if err != nil || string(body) != "proxied:public.example" {
		t.Fatalf("unexpected proxied response: body=%q err=%v", string(body), err)
	}

	reqPriv, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://internal.example/secret", nil)
	respPriv, err := client.Do(reqPriv)
	if respPriv != nil && respPriv.Body != nil {
		_ = respPriv.Body.Close()
	}
	if core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("expected private host via proxy to be blocked by CheckHost, got %v", err)
	}
}

func TestSafeFetchRedirectHopBlockingAndClientGuards(t *testing.T) {
	internalSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret metadata"))
	}))
	defer internalSrv.Close()

	client, err := NewSafeFetchClient(Options{AllowPrivate: false})
	if err != nil {
		t.Fatalf("NewSafeFetchClient: %v", err)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, internalSrv.URL, nil)
	respLoop, err := client.Do(req)
	if respLoop != nil && respLoop.Body != nil {
		_ = respLoop.Body.Close()
	}
	if err == nil {
		t.Fatal("expected direct loopback fetch to be blocked")
	}

	// 重定向跳数超限
	redirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer redirSrv.Close()

	fc, err := NewSafeFetchClient(Options{AllowPrivate: true, MaxRedirects: 2})
	if err != nil {
		t.Fatalf("NewSafeFetchClient allow private: %v", err)
	}
	redirReq, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, redirSrv.URL, nil)
	respRedir, err := fc.Do(redirReq)
	if respRedir != nil && respRedir.Body != nil {
		_ = respRedir.Body.Close()
	}
	if core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("expected redirect limit content_unavailable, got %v", err)
	}

	// 代理配置校验与未初始化 Client 守卫
	if _, err := NewAPIClient(Options{ProxyURL: "://bad"}); err == nil {
		t.Fatal("expected invalid proxy error")
	}
	if _, err := NewAPIClient(Options{ProxyURL: "http://127.0.0.1:7890"}); err != nil {
		t.Fatalf("expected valid loopback proxy accepted: %v", err)
	}
	var nilCli *Client
	respNil, err := nilCli.Do(redirReq)
	if respNil != nil && respNil.Body != nil {
		_ = respNil.Body.Close()
	}
	if core.AsError(err).Code != core.CodeInternal {
		t.Fatal("expected uninitialized client -> internal")
	}
}

func TestAPIBaseURLValidationRedactionAndRedirectSecurity(t *testing.T) {
	if got, err := ResolveAPIBaseURL("https://api.deepseek.com/", "https://fallback.example"); err != nil || got != "https://api.deepseek.com" {
		t.Fatalf("ResolveAPIBaseURL: got=%q err=%v", got, err)
	}
	if got, err := ResolveAPIBaseURL("", "https://api.exa.ai/"); err != nil || got != "https://api.exa.ai" {
		t.Fatalf("ResolveAPIBaseURL fallback: got=%q err=%v", got, err)
	}
	for _, allowed := range []string{"http://127.0.0.1:8080", "http://localhost:9000"} {
		if _, err := ValidateAPIBaseURL(allowed); err != nil {
			t.Fatalf("expected loopback http %q allowed: %v", allowed, err)
		}
	}
	for _, rejected := range []string{"http://api.deepseek.com", "https://u:p@api.test", "://bad"} {
		if _, err := ValidateAPIBaseURL(rejected); err == nil {
			t.Fatalf("expected ValidateAPIBaseURL(%q) rejected", rejected)
		}
	}
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
