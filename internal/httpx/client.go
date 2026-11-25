package httpx

import (
	"compress/gzip"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"veri/clikit/redact"
	"veri/internal/core"
	"veri/internal/spec"
)

const (
	defaultDialTimeout              = 5 * time.Second
	defaultTLSHandshakeTimeout      = 5 * time.Second
	defaultAPIResponseHeaderTimeout = 90 * time.Second
	defaultFetchHeaderTimeout       = 30 * time.Second
	defaultMaxRedirects             = 5
	// DefaultAPIResponseMaxBytes 厂商 JSON 响应最大读取字节上限（16 MB）。
	DefaultAPIResponseMaxBytes int64 = spec.DefaultAPIResponseBytes
	// DefaultFetchResponseMaxBytes 本地网页抓取解压后最大字节上限（5 MB）。
	DefaultFetchResponseMaxBytes int64 = spec.DefaultFetchMaxBytes
)

// Options 配置安全 HTTP 客户端的网络参数。
type Options struct {
	ProxyURL         string
	AllowPrivate     bool
	FakeIPCIDRs      []netip.Prefix
	AutoDetectFakeIP bool
	Resolver         Resolver
	MaxRedirects     int
	Logger           *slog.Logger
}

// Client 封装经过超时与安全加固的 HTTP 客户端。
type Client struct {
	hc        *http.Client
	guard     *SSRFGuard
	usesProxy bool
	logger    *slog.Logger
}

// NewAPIClient 构造用于访问固定厂商 API 端点的安全客户端（启用连接池、TLS >= 1.2、重定向安全策略与代理支持）。
func NewAPIClient(opts Options) (*Client, error) {
	proxyFn, fixedProxy, err := buildProxyFunc(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}
	dialer := &net.Dialer{
		Timeout:   defaultDialTimeout,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		Proxy:                 proxyFn,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout: defaultAPIResponseHeaderTimeout,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	hc := &http.Client{
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return core.NewError(core.CodeUpstreamBadResponse, fmt.Sprintf("stopped after %d API redirects", maxRedirects))
			}
			if err := validateAPIRedirectTarget(req.URL, via); err != nil {
				return err
			}
			stripSensitiveRedirectHeaders(req, via)
			return nil
		},
	}
	return &Client{
		hc:        hc,
		usesProxy: fixedProxy,
		logger:    opts.Logger,
	}, nil
}

// NewSafeFetchClient 构造用于本地网页抓取（local）的安全客户端，支持本地代理分流、SSRF 守卫与逐跳重定向校验。
func NewSafeFetchClient(opts Options) (*Client, error) {
	proxyFn, fixedProxy, err := buildProxyFunc(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	cidrs := opts.FakeIPCIDRs
	if cidrs == nil {
		cidrs = []netip.Prefix{fakeIPv4Benchmark, fakeIPv6Benchmark}
	}
	guard := &SSRFGuard{
		AllowPrivate:     opts.AllowPrivate,
		FakeIPCIDRs:      cidrs,
		AutoDetectFakeIP: opts.AutoDetectFakeIP,
		Resolver:         opts.Resolver,
	}
	maxRedirects := opts.MaxRedirects
	if maxRedirects <= 0 {
		maxRedirects = defaultMaxRedirects
	}

	dialer := &net.Dialer{
		Timeout:   defaultDialTimeout,
		KeepAlive: 30 * time.Second,
	}
	newBaseTransport := func(reqProxy func(*http.Request) (*url.URL, error), dialCtx func(context.Context, string, string) (net.Conn, error)) *http.Transport {
		return &http.Transport{
			Proxy:                 reqProxy,
			DialContext:           dialCtx,
			DisableCompression:    true,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			MaxIdleConnsPerHost:   8,
			IdleConnTimeout:       60 * time.Second,
			TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
			ResponseHeaderTimeout: defaultFetchHeaderTimeout,
			ExpectContinueTimeout: 1 * time.Second,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
		}
	}

	rt := &safeFetchRoundTripper{
		proxyFn:         proxyFn,
		directTransport: newBaseTransport(nil, guard.SafeDialContext(dialer.DialContext)),
		proxyTransport:  newBaseTransport(proxyFn, dialer.DialContext),
		guard:           guard,
	}

	hc := &http.Client{
		Transport: rt,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("stopped after %d redirects", maxRedirects))
			}
			if err := guard.ValidateFetchURL(req.URL); err != nil {
				return err
			}
			stripSensitiveRedirectHeaders(req, via)
			return nil
		},
	}

	return &Client{
		hc:        hc,
		guard:     guard,
		usesProxy: fixedProxy,
		logger:    opts.Logger,
	}, nil
}

type safeFetchRoundTripper struct {
	proxyFn         func(*http.Request) (*url.URL, error)
	directTransport *http.Transport
	proxyTransport  *http.Transport
	guard           *SSRFGuard
}

func (rt *safeFetchRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := rt.guard.ValidateFetchURL(req.URL); err != nil {
		return nil, err
	}
	outReq := req
	if req.Header.Get("Accept-Encoding") == "" && req.Header.Get("Range") == "" && req.Method != http.MethodHead {
		outReq = req.Clone(req.Context())
		outReq.Header.Set("Accept-Encoding", "gzip")
	}
	var proxyURL *url.URL
	if rt.proxyFn != nil {
		var err error
		proxyURL, err = rt.proxyFn(outReq)
		if err != nil {
			return nil, core.WrapError(core.CodeInvalidArgument, "failed to evaluate proxy URL", err)
		}
	}
	if proxyURL != nil {
		// 经由显式 HTTP/SOCKS 代理连接（含本地 127.0.0.1:7890）时，先预解析校验目标主机，再交由代理传输层连接代理地址。
		if err := rt.guard.CheckHost(outReq.Context(), outReq.URL.Hostname()); err != nil {
			return nil, err
		}
		return rt.proxyTransport.RoundTrip(outReq)
	}
	return rt.directTransport.RoundTrip(outReq)
}

func (rt *safeFetchRoundTripper) CloseIdleConnections() {
	rt.directTransport.CloseIdleConnections()
	rt.proxyTransport.CloseIdleConnections()
}

// Do 执行一次 HTTP 请求。对于绑定了 SSRFGuard 的客户端，自动在发送前校验 URL。
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	if c == nil || c.hc == nil {
		return nil, core.NewError(core.CodeInternal, "uninitialized httpx.Client")
	}
	if req == nil || req.URL == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "empty HTTP request")
	}
	if c.guard != nil {
		if err := c.guard.ValidateFetchURL(req.URL); err != nil {
			return nil, err
		}
	}
	start := time.Now()
	resp, err := c.hc.Do(req)
	if err != nil {
		if c.logger != nil {
			c.logger.DebugContext(req.Context(), "http_request_error",
				"method", req.Method,
				"host", req.URL.Host,
				"elapsed_ms", time.Since(start).Milliseconds(),
				"error", err.Error(),
			)
		}
		return nil, classifyTransportError(req.Context(), err)
	}
	if c.logger != nil {
		c.logger.DebugContext(req.Context(), "http_response",
			"method", req.Method,
			"host", req.URL.Host,
			"status", resp.StatusCode,
			"elapsed_ms", time.Since(start).Milliseconds(),
		)
	}
	return resp, nil
}

// ReadBodyLimited 读取并关闭响应体，同时限制原始流与解压后字节数（防止超大响应与压缩炸弹耗尽内存）。
func ReadBodyLimited(resp *http.Response, maxBytes int64) ([]byte, error) {
	if resp == nil || resp.Body == nil {
		return nil, nil
	}
	defer func() { _ = resp.Body.Close() }()

	if maxBytes <= 0 {
		maxBytes = DefaultFetchResponseMaxBytes
	}

	isGzip := strings.EqualFold(strings.TrimSpace(resp.Header.Get("Content-Encoding")), "gzip")
	if !isGzip && resp.ContentLength > maxBytes {
		return nil, core.NewError(core.CodeContentUnavailable, fmt.Sprintf("response Content-Length (%d bytes) exceeds maximum allowed size (%d bytes)", resp.ContentLength, maxBytes)).
			WithHint("Increase fetch.max_bytes in config if you intentionally need larger pages.")
	}

	var reader io.Reader = resp.Body
	if isGzip {
		rawCap := cappedWireBytes(maxBytes)
		gz, err := gzip.NewReader(io.LimitReader(resp.Body, rawCap))
		if err != nil {
			return nil, core.WrapError(core.CodeUpstreamBadResponse, "invalid gzip response stream", err)
		}
		defer func() { _ = gz.Close() }()
		reader = gz
	}

	limited := io.LimitReader(reader, safeAddOne(maxBytes))
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, classifyReadError(err)
	}
	if int64(len(data)) > maxBytes {
		return nil, core.NewError(core.CodeContentUnavailable, fmt.Sprintf("response body exceeds maximum allowed size (%d bytes)", maxBytes)).
			WithHint("Increase fetch.max_bytes in config if you intentionally need larger pages.")
	}
	return data, nil
}

func cappedWireBytes(maxBytes int64) int64 {
	overhead := maxBytes/16 + 64*1024
	if maxBytes > math.MaxInt64-overhead {
		return math.MaxInt64
	}
	return maxBytes + overhead
}

func safeAddOne(n int64) int64 {
	if n == math.MaxInt64 {
		return math.MaxInt64
	}
	return n + 1
}

func stripSensitiveRedirectHeaders(req *http.Request, via []*http.Request) {
	if len(via) == 0 {
		return
	}
	origin := via[0].URL
	crossOrigin := !strings.EqualFold(origin.Host, req.URL.Host)
	downgraded := strings.EqualFold(origin.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https")
	if !crossOrigin && !downgraded {
		return
	}
	for k := range req.Header {
		if redact.IsSensitiveHeader(k) || strings.EqualFold(k, "Cookie") {
			req.Header.Del(k)
		}
	}
}

func validateAPIRedirectTarget(target *url.URL, via []*http.Request) error {
	if target == nil || len(via) == 0 || via[0].URL == nil {
		return core.NewError(core.CodeBlockedByPolicy, "refusing invalid API redirect target")
	}
	if _, err := ValidateAPIBaseURL(target.String()); err != nil {
		return core.NewError(core.CodeBlockedByPolicy, "refusing insecure API redirect target")
	}
	originURL := via[0].URL
	if strings.EqualFold(originURL.Scheme, "https") && !strings.EqualFold(target.Scheme, "https") {
		return core.NewError(core.CodeBlockedByPolicy, "refusing HTTPS-to-HTTP API redirect downgrade")
	}
	if !isLoopbackHost(originURL.Hostname()) {
		targetHost := target.Hostname()
		if isLoopbackHost(targetHost) || strings.Contains(targetHost, "%") {
			return core.NewError(core.CodeBlockedByPolicy, "refusing API redirect from remote origin to loopback host")
		}
		if ip, err := netip.ParseAddr(targetHost); err == nil && (ip.Zone() != "" || isBlockedIP(ip.Unmap(), nil)) {
			return core.NewError(core.CodeBlockedByPolicy, "refusing API redirect to non-public IP literal")
		}
	}
	return nil
}

func buildProxyFunc(rawProxy string) (func(*http.Request) (*url.URL, error), bool, error) {
	trimmed := strings.TrimSpace(rawProxy)
	if trimmed == "" {
		return http.ProxyFromEnvironment, false, nil
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return nil, false, fmt.Errorf("invalid network.proxy %q", redact.SanitizeURL(rawProxy))
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, false, fmt.Errorf("unsupported network.proxy scheme %q", u.Scheme)
	}
	return http.ProxyURL(u), true, nil
}

func classifyTransportError(ctx context.Context, err error) error {
	if se, ok := errors.AsType[*core.Error](err); ok {
		return se
	}
	if errors.Is(err, context.DeadlineExceeded) || (ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
		return core.WrapError(core.CodeUpstreamTimeout, "request timed out", err)
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return core.WrapError(core.CodeUpstreamTimeout, "network timeout", err)
	}
	if errors.Is(err, context.Canceled) {
		return core.WrapError(core.CodeUpstreamTimeout, "request canceled", err).WithRetryable(false)
	}
	return core.WrapError(core.CodeUpstreamUnavailable, "upstream network request failed", err)
}

func classifyReadError(err error) error {
	if se, ok := errors.AsType[*core.Error](err); ok {
		return se
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return core.WrapError(core.CodeUpstreamTimeout, "reading response body timed out", err)
	}
	if netErr, ok := errors.AsType[net.Error](err); ok && netErr.Timeout() {
		return core.WrapError(core.CodeUpstreamTimeout, "network timeout while reading body", err)
	}
	return core.WrapError(core.CodeUpstreamBadResponse, "failed to read response body", err)
}
