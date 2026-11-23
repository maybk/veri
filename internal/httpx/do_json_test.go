package httpx

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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

type errReadCloser struct{}

func (errReadCloser) Read([]byte) (int, error) { return 0, context.DeadlineExceeded }
func (errReadCloser) Close() error             { return nil }

func TestDoJSONKeyFailoverAndInspectBody(t *testing.T) {
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
	resp, err := client.DoJSON(t.Context(), newTestKeyPool("bad-key,good-key"), NewGate(4, 0), RequestSpec{
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

	resp2, err := client.DoJSON(t.Context(), newTestKeyPool("dead-token,live-token"), NewGate(4, 0), RequestSpec{
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
}

func TestDoJSONRetrySafetyAndNoTimeoutReplay(t *testing.T) {
	client, err := NewAPIClient(Options{})
	if err != nil {
		t.Fatal(err)
	}

	// 1. 单 Key 触发 429 且无 Retry-After 时应立即返回，不空等重试冷却中的同一 Key
	var rlHits atomic.Int32
	rlSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rlHits.Add(1)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer rlSrv.Close()
	if _, err := client.DoJSON(t.Context(), newTestKeyPool("only-key"), NewGate(4, 0), RequestSpec{
		ProviderName: "rl",
		Method:       http.MethodGet,
		URL:          rlSrv.URL,
		AuthHeader:   "Authorization",
		AuthPrefix:   "Bearer ",
	}); core.AsError(err).Code != core.CodeRateLimited || rlHits.Load() != 1 {
		t.Fatalf("expected single-key 429 without Retry-After to fail fast in 1 hit, hits=%d err=%v", rlHits.Load(), err)
	}

	// 2. 单 Key 触发 429 但 ClassifyErr 判定为不可重试业务错误（如余额不足）时绝不退避重试
	var nonRetry429Hits atomic.Int32
	nonRetry429Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		nonRetry429Hits.Add(1)
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"INSUFFICIENT_FUNDS_CREDITS","message":"out of credits"}}`))
	}))
	defer nonRetry429Srv.Close()
	if _, err := client.DoJSON(t.Context(), newTestKeyPool("only-key"), NewGate(4, 0), RequestSpec{
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

	// 3. 网络读超时严禁自动重放请求
	var slowHits atomic.Int32
	slowSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		slowHits.Add(1)
		time.Sleep(150 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer slowSrv.Close()

	_, err = client.DoJSON(t.Context(), newTestKeyPool("k1,k2"), NewGate(4, 0), RequestSpec{
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

	// 4. 无密钥 Provider 遇 HTTP 429 且 Retry-After > maxRetryWait 时必须立即失败，绝不阻塞等待
	var keylessHits atomic.Int32
	keylessSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		keylessHits.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer keylessSrv.Close()

	keylessStart := time.Now()
	_, err = client.DoJSON(t.Context(), nil, NewGate(4, 0), RequestSpec{
		ProviderName: "firecrawl-selfhost",
		Method:       http.MethodPost,
		URL:          keylessSrv.URL,
	})
	if core.AsError(err).Code != core.CodeRateLimited || keylessHits.Load() != 1 || time.Since(keylessStart) > 500*time.Millisecond {
		t.Fatalf("keyless 429 with long Retry-After must fail fast without sleeping; hits=%d elapsed=%v err=%v", keylessHits.Load(), time.Since(keylessStart), err)
	}

	// 5. 收到明确未扣费 503 时自动退避重试 1 次成功（覆盖 debug logger 路径）
	var flakyHits atomic.Int32
	flakySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if flakyHits.Add(1) == 1 {
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
	dbgCli, err := NewAPIClient(Options{Logger: dbgLogger})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dbgCli.DoJSON(t.Context(), newTestKeyPool("k1"), NewGate(2, 0), RequestSpec{
		ProviderName: "flaky",
		Method:       http.MethodGet,
		URL:          flakySrv.URL,
		AuthHeader:   "X-API-Key",
	}); err != nil || flakyHits.Load() != 2 {
		t.Fatalf("expected 503 single retry to succeed on 2nd hit, hits=%d err=%v", flakyHits.Load(), err)
	}
}

func TestDoJSONExhaustedKeyPoolAndErrorClassification(t *testing.T) {
	// ClassifyHTTPError 全状态码矩阵
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

	apiCli, err := NewAPIClient(Options{})
	if err != nil {
		t.Fatal(err)
	}
	specReq := RequestSpec{
		ProviderName: "p",
		Method:       http.MethodGet,
		URL:          "http://127.0.0.1:1",
		AuthHeader:   "X-API-Key",
	}

	// 空池、鉴权失效、余额耗尽、限流冷却 4 种池耗尽映射
	if _, err := apiCli.DoJSON(t.Context(), newTestKeyPool(""), nil, specReq); core.AsError(err).Code != core.CodeAuthFailed {
		t.Fatal("expected missing key -> auth_failed")
	}
	deadPool := newTestKeyPool("k1")
	deadPool.ReportAuthFailed(0)
	if _, err := apiCli.DoJSON(t.Context(), deadPool, nil, specReq); core.AsError(err).Code != core.CodeAuthFailed {
		t.Fatal("expected dead pool -> auth_failed")
	}
	balPool := newTestKeyPool("k1")
	balPool.ReportInsufficientBalance(0)
	if _, err := apiCli.DoJSON(t.Context(), balPool, nil, specReq); core.AsError(err).Code != core.CodeInsufficientBalance {
		t.Fatal("expected balance pool -> insufficient_balance")
	}
	rlPool := newTestKeyPool("k1")
	rlPool.ReportRateLimited(0, time.Minute)
	if _, err := apiCli.DoJSON(t.Context(), rlPool, nil, specReq); core.AsError(err).Code != core.CodeRateLimited {
		t.Fatal("expected rate-limited pool -> rate_limited")
	}
}

func TestReadBodyLimitedDecompressionBombAndErrors(t *testing.T) {
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
}
