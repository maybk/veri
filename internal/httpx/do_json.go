package httpx

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/keypool"
	"veri/internal/textnorm"
)

// ErrorClassifier 将上游 HTTP 响应体分类为结构化 *core.Error。
type ErrorClassifier func(status int, headers http.Header, body []byte) *core.Error

// RequestSpec 描述一次向厂商固定 API 发起的请求参数。
type RequestSpec struct {
	ProviderName string
	Method       string
	URL          string
	Timeout      time.Duration
	AuthHeader   string
	AuthPrefix   string
	Headers      map[string]string
	Body         any
	ClassifyErr  ErrorClassifier
	// InspectBody 允许对 HTTP 2xx 响应体做应用层错误分类（如腾讯云 HTTP 200 内嵌 Response.Error）。
	InspectBody ErrorClassifier
}

// Response 包含成功读取的上游 HTTP 响应体与追踪 ID。
type Response struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
	RequestID  string
}

// DoJSON 执行一次经并发/QPS 门控、KeyPool 轮询冷却与计费安全单次重试保护的 API 调用。
func (c *Client) DoJSON(ctx context.Context, pool *keypool.Pool, gate *Gate, spec RequestSpec) (*Response, error) {
	release, err := gate.Acquire(ctx)
	if err != nil {
		return nil, core.AsError(err).WithProvider(spec.ProviderName)
	}
	defer release()

	var bodyBytes []byte
	if spec.Body != nil {
		encoded, encErr := json.Marshal(spec.Body)
		if encErr != nil {
			return nil, core.WrapError(core.CodeInternal, "failed to marshal request JSON", encErr).WithProvider(spec.ProviderName)
		}
		bodyBytes = encoded
	}

	requireKey := spec.AuthHeader != ""
	if requireKey && (pool == nil || pool.Size() == 0) {
		return nil, core.NewError(core.CodeAuthFailed, fmt.Sprintf("missing API key for %s", spec.ProviderName)).
			WithProvider(spec.ProviderName)
	}

	excludeKeyID := -1
	var pendingLease *keypool.Lease
	for attempt := range 2 {
		var lease keypool.Lease
		if requireKey {
			if pendingLease != nil {
				lease = *pendingLease
				pendingLease = nil
			} else {
				var ok bool
				lease, ok = pool.Acquire(excludeKeyID)
				if !ok {
					if attempt > 0 {
						break
					}
					return nil, exhaustedKeyPoolError(spec.ProviderName, pool.DiagnoseExhaustion())
				}
			}
		}

		reqCtx := ctx
		var cancel context.CancelFunc
		if spec.Timeout > 0 {
			reqCtx, cancel = context.WithTimeout(ctx, spec.Timeout)
		}

		var bodyReader io.Reader
		if bodyBytes != nil {
			bodyReader = bytes.NewReader(bodyBytes)
		}
		httpReq, reqErr := http.NewRequestWithContext(reqCtx, spec.Method, spec.URL, bodyReader)
		if reqErr != nil {
			if cancel != nil {
				cancel()
			}
			return nil, core.WrapError(core.CodeInvalidArgument, "failed to create HTTP request", reqErr).WithProvider(spec.ProviderName)
		}

		if bodyBytes != nil {
			httpReq.Header.Set("Content-Type", "application/json")
		}
		httpReq.Header.Set("Accept", "application/json")
		for k, v := range spec.Headers {
			httpReq.Header.Set(k, v)
		}
		if requireKey {
			httpReq.Header.Set(spec.AuthHeader, spec.AuthPrefix+lease.Value)
		}

		resp, doErr := c.Do(httpReq)
		if doErr != nil {
			if cancel != nil {
				cancel()
			}
			// 计费安全红线：请求已发出但未收到响应（读超时、连接中断）时严禁自动重放。
			return nil, core.AsError(doErr).WithProvider(spec.ProviderName)
		}

		respBody, readErr := ReadBodyLimited(resp, DefaultAPIResponseMaxBytes)
		_ = resp.Body.Close()
		if cancel != nil {
			cancel()
		}
		if readErr != nil {
			return nil, core.AsError(readErr).WithProvider(spec.ProviderName)
		}

		reqID := extractRequestID(resp.Header)
		var classified *core.Error
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if spec.InspectBody != nil {
				classified = spec.InspectBody(resp.StatusCode, resp.Header, respBody)
			}
			if classified == nil {
				if requireKey {
					pool.ReportSuccess(lease.ID)
				}
				return &Response{
					StatusCode: resp.StatusCode,
					Headers:    resp.Header,
					Body:       respBody,
					RequestID:  reqID,
				}, nil
			}
		} else {
			if spec.ClassifyErr != nil {
				classified = spec.ClassifyErr(resp.StatusCode, resp.Header, respBody)
			}
			if classified == nil {
				classified = ClassifyHTTPError(spec.ProviderName, resp.StatusCode, respBody)
			}
		}
		classified = classified.WithProvider(spec.ProviderName).WithStatus(resp.StatusCode)

		now := time.Now()
		retryAfter := ParseRetryAfter(resp.Header.Get("Retry-After"), now)
		if requireKey {
			switch classified.Code {
			case core.CodeRateLimited:
				pool.ReportRateLimited(lease.ID, retryAfter)
			case core.CodeInsufficientBalance:
				pool.ReportInsufficientBalance(lease.ID)
			case core.CodeAuthFailed:
				pool.ReportAuthFailed(lease.ID)
			}
		}

		if attempt == 0 {
			// 存在同池其他可用 Key 时立即切换备用 Key 重试 1 次，不重复拨动游标。
			if requireKey && pool.Size() > 1 &&
				(classified.Code == core.CodeRateLimited || classified.Code == core.CodeInsufficientBalance || classified.Code == core.CodeAuthFailed) {
				excludeKeyID = lease.ID
				if nextLease, hasOther := pool.Acquire(excludeKeyID); hasOther {
					pendingLease = &nextLease
					if c.logger != nil {
						c.logger.DebugContext(ctx, "provider_key_failover", "provider", spec.ProviderName, "code", string(classified.Code))
					}
					continue
				}
			}
			// 单 Key 触发限流且无短 Retry-After 提示时不再空等重试同一冷却中 Key，直接交由路由层回退。
			if classified.Code == core.CodeRateLimited && requireKey && (retryAfter <= 0 || retryAfter > maxRetryWait) {
				return nil, classified
			}
			// 仅对上游明确未扣费的瞬时状态码重试最多 1 次。
			if ShouldRetryStatus(resp.StatusCode) {
				delay := RetryDelay(resp.Header.Get("Retry-After"), now)
				if classified.Code == core.CodeRateLimited && retryAfter > 0 {
					delay = retryAfter + 5*time.Millisecond
				}
				if c.logger != nil {
					c.logger.DebugContext(ctx, "provider_retry", "provider", spec.ProviderName, "status", resp.StatusCode, "delay_ms", delay.Milliseconds())
				}
				if sleepErr := SleepContext(ctx, delay); sleepErr != nil {
					return nil, classified
				}
				excludeKeyID = -1
				continue
			}
		}
		return nil, classified
	}

	return nil, exhaustedKeyPoolError(spec.ProviderName, pool.DiagnoseExhaustion())
}

// ClassifyHTTPError 提供标准的 HTTP 状态码到 core.Error 映射基线。
func ClassifyHTTPError(providerName string, status int, body []byte) *core.Error {
	return ClassifyHTTPErrorWithMessage(providerName, status, "", body)
}

// ClassifyHTTPErrorWithMessage 使用已解析错误消息（为空时自动截取响应片段）映射 HTTP 状态码。
func ClassifyHTTPErrorWithMessage(providerName string, status int, message string, body []byte) *core.Error {
	snippet := textnorm.CleanInline(textnorm.TruncateRunes(message, 200))
	if snippet == "" {
		snippet = textnorm.CleanInline(textnorm.TruncateRunes(string(body), 160))
	}
	if snippet == "" {
		snippet = http.StatusText(status)
	}
	switch {
	case status == http.StatusBadRequest || status == http.StatusRequestEntityTooLarge || status == http.StatusUnprocessableEntity:
		return core.NewError(core.CodeInvalidArgument, snippet).WithProvider(providerName).WithStatus(status)
	case status == http.StatusUnauthorized:
		return core.NewError(core.CodeAuthFailed, snippet).WithProvider(providerName).WithStatus(status)
	case status == http.StatusPaymentRequired:
		return core.NewError(core.CodeInsufficientBalance, snippet).WithProvider(providerName).WithStatus(status)
	case status == http.StatusForbidden:
		return core.NewError(core.CodeBlockedByPolicy, snippet).WithProvider(providerName).WithStatus(status)
	case status == http.StatusNotFound:
		return core.NewError(core.CodeContentUnavailable, snippet).WithProvider(providerName).WithStatus(status)
	case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
		return core.NewError(core.CodeUpstreamTimeout, snippet).WithProvider(providerName).WithStatus(status)
	case status == http.StatusTooManyRequests:
		return core.NewError(core.CodeRateLimited, snippet).WithProvider(providerName).WithStatus(status)
	case status >= 500:
		return core.NewError(core.CodeUpstreamUnavailable, snippet).WithProvider(providerName).WithStatus(status)
	default:
		return core.NewError(core.CodeUpstreamBadResponse, snippet).WithProvider(providerName).WithStatus(status)
	}
}

func extractRequestID(h http.Header) string {
	for _, k := range []string{"X-Request-Id", "X-Request-ID", "Request-Id", "X-Trace-Id"} {
		if v := strings.TrimSpace(h.Get(k)); v != "" {
			return v
		}
	}
	return ""
}

func exhaustedKeyPoolError(providerName string, reason keypool.ExhaustReason) *core.Error {
	switch reason {
	case keypool.ExhaustAuthFailed, keypool.ExhaustEmpty:
		return core.NewError(core.CodeAuthFailed, fmt.Sprintf("all API keys for %s failed authentication", providerName)).
			WithProvider(providerName)
	case keypool.ExhaustBalance:
		return core.NewError(core.CodeInsufficientBalance, fmt.Sprintf("API key balance or quota exhausted for %s (in cooldown)", providerName)).
			WithProvider(providerName)
	default:
		return core.NewError(core.CodeRateLimited, fmt.Sprintf("all API keys for %s are rate-limited and cooling down", providerName)).
			WithProvider(providerName)
	}
}
