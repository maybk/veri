package httpx

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"veri/internal/core"
)

func TestHTTPXExtraCoverageBranches(t *testing.T) {
	if _, err := ParseCIDRs([]string{"bad-cidr"}); err == nil {
		t.Fatal("expected invalid CIDR error")
	}
	fakeCIDRs, _ := ParseCIDRs([]string{"198.18.0.0/15", ""})
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
	uNoHost, _ := url.Parse("https:///path")
	if err := guard.ValidateFetchURL(uNoHost); err == nil {
		t.Fatal("expected missing hostname error")
	}
	uCred, _ := url.Parse("https://u:p@example.com")
	if err := guard.ValidateFetchURL(uCred); err == nil {
		t.Fatal("expected credentials error")
	}
	uFakeLit, _ := url.Parse("http://198.18.0.5/a")
	if err := guard.ValidateFetchURL(uFakeLit); err == nil {
		t.Fatal("expected Fake-IP literal in URL to be blocked")
	}

	// CheckHost（外部代理预解析路径）
	if err := guard.CheckHost(context.Background(), "ok.test"); err != nil {
		t.Fatalf("CheckHost ok.test: %v", err)
	}
	if err := guard.CheckHost(context.Background(), "priv.test"); err == nil {
		t.Fatal("expected CheckHost priv.test to block")
	}
	if err := guard.CheckHost(context.Background(), "empty.test"); err == nil {
		t.Fatal("expected CheckHost empty.test to fail")
	}
	if err := guard.CheckHost(context.Background(), "missing.test"); err == nil {
		t.Fatal("expected CheckHost missing.test to fail")
	}
	if err := guard.CheckHost(context.Background(), "127.0.0.1"); err == nil {
		t.Fatal("expected CheckHost loopback literal to fail")
	}

	// ParseRetryAfter HTTP-date 分支与 SleepContext 取消分支
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	httpDate := now.Add(4 * time.Second).Format(http.TimeFormat)
	if d := ParseRetryAfter(httpDate, now); d != 4*time.Second {
		t.Fatalf("ParseRetryAfter HTTP-date = %v, want 4s", d)
	}
	if ParseRetryAfter("-5", now) != 0 || ParseRetryAfter("", now) != 0 {
		t.Fatal("expected 0 for negative/empty Retry-After")
	}
	if d := RetryDelay("", now); d < minRetryBackoff || d >= minRetryBackoff+retryBackoffJitter {
		t.Fatalf("RetryDelay out of jitter range: %v", d)
	}
	if RetryDelay("999", now) != maxRetryWait || SleepContext(context.Background(), 0) != nil {
		t.Fatal("expected RetryDelay cap at maxRetryWait and zero SleepContext nil")
	}
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := SleepContext(canceledCtx, time.Second); err == nil {
		t.Fatal("expected SleepContext on canceled ctx to fail")
	}

	// Gate QPS 限速与排队超时覆盖
	gate := NewGate(1, 50) // 20ms 间隔
	rel1, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rel1()
	rel2, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 不释放 rel2，验证第二个并发在极短超时下触发排队超时
	shortCtx, shortCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer shortCancel()
	if _, err := gate.Acquire(shortCtx); err == nil {
		t.Fatal("expected Gate concurrency timeout")
	}
	rel2()

	// ClassifyHTTPError 全状态码矩阵覆盖
	for _, tc := range []struct {
		status int
		want   core.Code
	}{
		{400, core.CodeInvalidArgument},
		{401, core.CodeAuthFailed},
		{402, core.CodeInsufficientBalance},
		{403, core.CodeBlockedByPolicy},
		{404, core.CodeContentUnavailable},
		{408, core.CodeUpstreamTimeout},
		{429, core.CodeRateLimited},
		{502, core.CodeUpstreamUnavailable},
		{302, core.CodeUpstreamBadResponse},
	} {
		if got := ClassifyHTTPError("p", tc.status, nil).Code; got != tc.want {
			t.Fatalf("status %d -> %s, want %s", tc.status, got, tc.want)
		}
	}

	// 测试重定向超限与代理配置错误
	if _, err := NewAPIClient(Options{ProxyURL: "://bad"}); err == nil {
		t.Fatal("expected invalid proxy error")
	}
	hops := 0
	redirSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops++
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer redirSrv.Close()
	fc, _ := NewSafeFetchClient(Options{AllowPrivate: true, MaxRedirects: 2})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, redirSrv.URL, nil)
	respRedir, err := fc.Do(req)
	if respRedir != nil && respRedir.Body != nil {
		_ = respRedir.Body.Close()
	}
	if core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("expected redirect limit content_unavailable, got %v", err)
	}

	// 覆盖 exhaustedKeyPoolError 三种耗尽分支、503 自动退避重试 1 次、以及异常 Body 读取错误
	flakyHits := 0
	flakySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flakyHits++
		if flakyHits == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("busy"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer flakySrv.Close()

	dbgLogger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	apiCli, _ := NewAPIClient(Options{Logger: dbgLogger})
	if _, err := apiCli.DoJSON(context.Background(), newTestKeyPool("k1"), NewGate(2, 0), RequestSpec{
		ProviderName: "flaky",
		Method:       http.MethodGet,
		URL:          flakySrv.URL,
		AuthHeader:   "X-API-Key",
	}); err != nil || flakyHits != 2 {
		t.Fatalf("expected 503 single retry to succeed on 2nd hit, hits=%d err=%v", flakyHits, err)
	}

	// 空池缺失 Key + 已剔除/已余额冷却池耗尽错误映射
	if _, err := apiCli.DoJSON(context.Background(), newTestKeyPool(""), nil, RequestSpec{
		ProviderName: "p", Method: http.MethodGet, URL: flakySrv.URL, AuthHeader: "X-API-Key",
	}); core.AsError(err).Code != core.CodeAuthFailed {
		t.Fatal("expected missing key -> auth_failed")
	}
	deadPool := newTestKeyPool("k1")
	deadPool.ReportAuthFailed(0)
	if _, err := apiCli.DoJSON(context.Background(), deadPool, nil, RequestSpec{
		ProviderName: "p", Method: http.MethodGet, URL: flakySrv.URL, AuthHeader: "X-API-Key",
	}); core.AsError(err).Code != core.CodeAuthFailed {
		t.Fatal("expected dead pool -> auth_failed")
	}
	balPool := newTestKeyPool("k1")
	balPool.ReportInsufficientBalance(0)
	if _, err := apiCli.DoJSON(context.Background(), balPool, nil, RequestSpec{
		ProviderName: "p", Method: http.MethodGet, URL: flakySrv.URL, AuthHeader: "X-API-Key",
	}); core.AsError(err).Code != core.CodeInsufficientBalance {
		t.Fatal("expected balance pool -> insufficient_balance")
	}
	rlPool := newTestKeyPool("k1")
	rlPool.ReportRateLimited(0, time.Minute)
	if _, err := apiCli.DoJSON(context.Background(), rlPool, nil, RequestSpec{
		ProviderName: "p", Method: http.MethodGet, URL: flakySrv.URL, AuthHeader: "X-API-Key",
	}); core.AsError(err).Code != core.CodeRateLimited {
		t.Fatal("expected rate-limited pool -> rate_limited")
	}

	// ReadBodyLimited 损坏 gzip 与读取流错误覆盖
	badGzResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Encoding": []string{"gzip"}},
		Body:       errReadCloser{},
	}
	if _, err := ReadBodyLimited(badGzResp, 1024); err == nil {
		t.Fatal("expected broken gzip reader to fail")
	}
	errBodyResp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{},
		Body:       errReadCloser{},
	}
	if _, err := ReadBodyLimited(errBodyResp, 0); err == nil {
		t.Fatal("expected broken body reader to fail")
	}

	// ValidateAPIBaseURL 异常分支与未初始化 Client
	if _, err := ValidateAPIBaseURL("https://u:p@api.test"); err == nil {
		t.Fatal("expected credentials in base_url to fail")
	}
	if _, err := ValidateAPIBaseURL("://bad"); err == nil {
		t.Fatal("expected malformed base_url to fail")
	}
	if _, err := ValidateAPIBaseURL("http://localhost:9000"); err != nil {
		t.Fatalf("expected localhost http allowed: %v", err)
	}
	var nilCli *Client
	respNil, err := nilCli.Do(req)
	if respNil != nil && respNil.Body != nil {
		_ = respNil.Body.Close()
	}
	if core.AsError(err).Code != core.CodeInternal {
		t.Fatal("expected uninitialized client -> internal")
	}
	// 覆盖 AllowPrivate=true 直连分支与显式 ProxyURL 分支
	privGuard := &SSRFGuard{AllowPrivate: true}
	allowDial := privGuard.SafeDialContext(func(_ context.Context, _, _ string) (net.Conn, error) {
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	})
	if c, dErr := allowDial(context.Background(), "tcp", "127.0.0.1:80"); dErr != nil {
		t.Fatal(dErr)
	} else {
		_ = c.Close()
	}
	if _, pErr := NewAPIClient(Options{ProxyURL: "http://127.0.0.1:7890"}); pErr != nil {
		t.Fatal(pErr)
	}
}

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }
func (errReadCloser) Close() error             { return nil }
