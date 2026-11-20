package tinyfish

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

// classifyFetchItemError 将抓取响应内 errors[] 子项（枚举与可选目标站 HTTP 状态码）映射为统一 core.Error。
func classifyFetchItemError(item fetchWireError) error {
	code := strings.ToLower(strings.TrimSpace(item.Error))
	if code == "" {
		code = "unknown"
	}
	status := 0
	if item.Status != nil && *item.Status > 0 {
		status = *item.Status
	}

	withStatus := func(err *core.Error) *core.Error {
		if status > 0 {
			return err.WithStatus(status)
		}
		return err
	}

	switch code {
	case "page_not_found":
		return withStatus(core.NewError(core.CodeNotFound, fmt.Sprintf("TinyFish target not found (%s)", code)).WithProvider(ProviderName))
	case "bot_blocked":
		return withStatus(core.NewError(core.CodeBlockedByPolicy, fmt.Sprintf("TinyFish target blocked (%s)", code)).
			WithProvider(ProviderName).
			WithHintKey(core.HintKeyChallengeDetected))
	case "login_required":
		return withStatus(core.NewError(core.CodeBlockedByPolicy, fmt.Sprintf("TinyFish target blocked (%s)", code)).WithProvider(ProviderName))
	case "timeout":
		return withStatus(core.NewError(core.CodeUpstreamTimeout, "TinyFish per-URL fetch timed out").WithProvider(ProviderName))
	case "invalid_url", "invalid_redirect_url":
		return withStatus(core.NewError(core.CodeInvalidArgument, fmt.Sprintf("TinyFish rejected URL (%s)", code)).WithProvider(ProviderName))
	case "target_http_error":
		switch {
		case status == http.StatusNotFound || status == http.StatusGone:
			return withStatus(core.NewError(core.CodeNotFound, fmt.Sprintf("TinyFish target returned HTTP %d", status)).WithProvider(ProviderName))
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			se := withStatus(core.NewError(core.CodeBlockedByPolicy, fmt.Sprintf("TinyFish target returned HTTP %d", status)).WithProvider(ProviderName))
			if status == http.StatusForbidden {
				se = se.WithHintKey(core.HintKeyChallengeDetected)
			}
			return se
		case status == http.StatusTooManyRequests:
			return withStatus(core.NewError(core.CodeRateLimited, "TinyFish target returned HTTP 429").
				WithProvider(ProviderName).
				WithHintKey(core.HintKeyChallengeDetected))
		case status == http.StatusRequestTimeout || status == http.StatusGatewayTimeout:
			return withStatus(core.NewError(core.CodeUpstreamTimeout, fmt.Sprintf("TinyFish target timed out (HTTP %d)", status)).WithProvider(ProviderName))
		case status >= 500:
			return withStatus(core.NewError(core.CodeUpstreamUnavailable, fmt.Sprintf("TinyFish target returned HTTP %d", status)).WithProvider(ProviderName))
		}
	}

	if status == http.StatusNotFound || status == http.StatusGone {
		return withStatus(core.NewError(core.CodeNotFound, fmt.Sprintf("TinyFish target not found (%s, HTTP %d)", code, status)).WithProvider(ProviderName))
	}
	if status > 0 {
		return withStatus(core.NewError(core.CodeContentUnavailable, fmt.Sprintf("TinyFish failed to fetch target (%s, HTTP %d)", code, status)).WithProvider(ProviderName))
	}
	return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("TinyFish failed to fetch target (%s)", code)).WithProvider(ProviderName)
}

// classifyError 将非 2xx 顶层 UPPER_SNAKE_CASE 错误码映射为统一 core.Error。
func classifyError(status int, _ http.Header, body []byte) *core.Error {
	var env apiErrorWireResp
	_ = json.Unmarshal(body, &env)
	code := strings.ToUpper(strings.TrimSpace(env.Error.Code))
	msg := textnorm.CleanInline(env.Error.Message)

	var reason string
	if env.Error.Details != nil {
		reason = strings.ToLower(strings.TrimSpace(env.Error.Details.Reason))
	}

	var mapped core.Code
	nonRetryable := false
	switch code {
	case "INVALID_INPUT":
		mapped = core.CodeInvalidArgument
	case "MISSING_API_KEY", "INVALID_API_KEY", "UNAUTHORIZED":
		mapped = core.CodeAuthFailed
	case "INSUFFICIENT_CREDITS", "FREE_ALLOWANCE_EXHAUSTED":
		mapped = core.CodeInsufficientBalance
		nonRetryable = true
	case "FORBIDDEN", "FEATURE_NOT_AVAILABLE":
		mapped = core.CodeNotEnabled
	case "RATE_LIMIT_EXCEEDED":
		// 官方文档约定：未开通钱包扣费且免费日配额用尽时返回 HTTP 429 + details.reason="free_allowance_exhausted"，严禁短退避重试。
		if reason == "free_allowance_exhausted" {
			mapped = core.CodeInsufficientBalance
			nonRetryable = true
		} else {
			mapped = core.CodeRateLimited
		}
	case "SERVICE_BUSY":
		mapped = core.CodeRateLimited
	case "TIMEOUT":
		mapped = core.CodeUpstreamTimeout
	case "INTERNAL_ERROR", "BACKEND_UNAVAILABLE":
		mapped = core.CodeUpstreamUnavailable
	}
	if mapped != "" {
		if msg == "" {
			msg = code
		}
		se := core.NewError(mapped, msg).WithProvider(ProviderName).WithStatus(status)
		if nonRetryable {
			se = se.WithRetryable(false)
		}
		return se
	}
	return httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, msg, body)
}
