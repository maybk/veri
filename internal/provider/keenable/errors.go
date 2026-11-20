package keenable

import (
	"encoding/json/v2"
	"net/http"
	"strings"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

// classifyError 按 Keenable 固定 7 类扁平错误 type、具体 code、retryable 与 HTTP 状态码映射到统一 core.Error。
// 重点拆分 HTTP 403：credentials_forbidden 等归鉴权失败并隔离密钥；target_error、private_url_not_allowed 与特性未开通绝不拉黑密钥。
func classifyError(status int, _ http.Header, body []byte) *core.Error {
	var env errorWireResp
	_ = json.Unmarshal(body, &env)
	errType := strings.ToLower(strings.TrimSpace(env.Type))
	errCode := strings.ToLower(strings.TrimSpace(env.Code))
	msg := textnorm.CleanInline(env.Message)
	if msg == "" {
		msg = errCode
	}

	var se *core.Error
	switch errType {
	case "invalid_request_error":
		if status == http.StatusNotFound && (errCode == "not_found" || errCode == "card_not_found" || errCode == "card_expired") {
			se = core.NewError(core.CodeNotFound, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		} else {
			se = core.NewError(core.CodeInvalidArgument, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		}
	case "authentication_error":
		se = core.NewError(core.CodeAuthFailed, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case "permission_error":
		switch {
		case errCode == "private_url_not_allowed":
			se = core.NewError(core.CodeBlockedByPolicy, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		case errCode == "time_machine_not_enabled" ||
			errCode == "monitors_not_enabled" ||
			errCode == "member_roles_not_enabled" ||
			errCode == "monitor_limit_reached" ||
			strings.HasPrefix(errCode, "promo_"):
			se = core.NewError(core.CodeNotEnabled, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		default:
			se = core.NewError(core.CodeAuthFailed, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		}
	case "billing_error":
		se = core.NewError(core.CodeInsufficientBalance, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case "rate_limit_error":
		se = core.NewError(core.CodeRateLimited, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case "target_error":
		if status == http.StatusNotFound || status == http.StatusGone {
			se = core.NewError(core.CodeNotFound, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		} else {
			se = core.NewError(core.CodeContentUnavailable, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
			if status == http.StatusForbidden {
				se = se.WithHintKey(core.HintKeyChallengeDetected)
			}
		}
	case "api_error":
		if status == http.StatusUnprocessableEntity {
			se = core.NewError(core.CodeContentUnavailable, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		} else {
			se = core.NewError(core.CodeUpstreamUnavailable, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		}
	default:
		switch status {
		case http.StatusNotFound, http.StatusGone:
			se = core.NewError(core.CodeNotFound, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		case http.StatusUnprocessableEntity:
			se = core.NewError(core.CodeContentUnavailable, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		case http.StatusForbidden:
			switch errCode {
			case "credentials_forbidden":
				se = core.NewError(core.CodeAuthFailed, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
			case "private_url_not_allowed":
				se = core.NewError(core.CodeBlockedByPolicy, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
			default:
				se = core.NewError(core.CodeContentUnavailable, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
			}
		default:
			se = httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, msg, body)
		}
	}

	if env.Retryable != nil && !*env.Retryable {
		se = se.WithRetryable(false)
	}
	return se
}
