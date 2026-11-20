package firecrawl

import (
	"encoding/json/v2"
	"net/http"
	"strings"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

func inspectBody(status int, h http.Header, body []byte) *core.Error {
	var env statusWireResp
	if err := json.Unmarshal(body, &env); err == nil && env.Success != nil && !*env.Success {
		return classifyError(status, h, body)
	}
	return nil
}

func classifyError(status int, _ http.Header, body []byte) *core.Error {
	var env errorWireResp
	_ = json.Unmarshal(body, &env)
	code := strings.ToUpper(strings.TrimSpace(env.Code))
	msg := textnorm.CleanInline(env.Error)
	if msg == "" {
		msg = code
	}

	switch code {
	case "INSUFFICIENT_CREDITS":
		return core.NewError(core.CodeInsufficientBalance, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case "SCRAPE_PROMPT_INJECTION_DETECTED", "UNSUPPORTED_SITE":
		return core.NewError(core.CodeBlockedByPolicy, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case "THIRD_PARTY_DATA_NOT_ENABLED", "THIRD_PARTY_DATA_ENRICHMENT_NOT_ENABLED", "THIRD_PARTY_DATA_TERMS_REQUIRED":
		return core.NewError(core.CodeNotEnabled, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case "THIRD_PARTY_DATA_UNSUPPORTED_URL", "SCRAPE_NO_CACHED_DATA", "SCRAPE_LOCKDOWN_CACHE_MISS":
		return core.NewError(core.CodeContentUnavailable, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case "THIRD_PARTY_DATA_NOT_FOUND":
		return core.NewError(core.CodeNotFound, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	}

	switch status {
	case http.StatusUnauthorized:
		return core.NewError(core.CodeAuthFailed, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case http.StatusPaymentRequired:
		return core.NewError(core.CodeInsufficientBalance, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case http.StatusForbidden:
		// 官方错误表中 403 且无特定站点/策略码（如 "Forbidden"）表示密钥缺少对应端点权限；
		// 目标站不支持或策略拦截（如 "do not support this site"）归 blocked_by_policy，绝不误拉黑健康密钥。
		lower := strings.ToLower(msg)
		if strings.Contains(lower, "do not support this site") ||
			strings.Contains(lower, "not supported") ||
			strings.Contains(lower, "policy") ||
			strings.Contains(lower, "prompt injection") {
			return core.NewError(core.CodeBlockedByPolicy, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		}
		return core.NewError(core.CodeAuthFailed, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case http.StatusNotFound, http.StatusGone:
		return core.NewError(core.CodeNotFound, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	default:
		return httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, msg, body)
	}
}
