package linkup

import (
	"encoding/json/v2"
	"net/http"
	"strings"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

func classifySearchError(status int, _ http.Header, body []byte) *core.Error {
	return classifyCommonError(status, body, false)
}

func classifyFetchError(status int, _ http.Header, body []byte) *core.Error {
	return classifyCommonError(status, body, true)
}

// classifyCommonError 依据 Linkup 官方 API 线协议错误码（SCREAMING_SNAKE_CASE）精细拆分：
// HTTP 400（SEARCH_QUERY_NO_RESULT / FETCH_* / VALIDATION_ERROR）、
// HTTP 403（IP_NOT_WHITELISTED / TASK_TYPE_NOT_SUPPORTED / 鉴权失败）、
// HTTP 429（INSUFFICIENT_FUNDS_CREDITS & EXCEED_BUDGET_LIMIT 不可重试 / TOO_MANY_REQUESTS 限流）。
func classifyCommonError(status int, body []byte, isFetch bool) *core.Error {
	errCode, msg := parseWireError(body)
	upperCode := strings.ToUpper(errCode)
	lowerMsg := strings.ToLower(msg)

	switch status {
	case http.StatusBadRequest:
		if !isFetch && (upperCode == "SEARCH_QUERY_NO_RESULT" || upperCode == "LINKUPNORESULTERROR" || strings.Contains(lowerMsg, "no result")) {
			return core.NewError(core.CodeContentUnavailable, httpx.ErrorMessageOrStatus(msg, status)).
				WithProvider(ProviderName).
				WithStatus(status).
				WithParam(noResultParamKey, "1")
		}
		if isFetch {
			switch upperCode {
			case "FETCH_TARGET_NOT_FOUND":
				return core.NewError(core.CodeNotFound, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
			case "FETCH_ERROR", "FETCH_RESPONSE_TOO_LARGE", "FETCH_TARGET_UNREACHABLE", "FETCH_UNSUPPORTED_CONTENT_TYPE", "FETCH_URL_IS_FILE":
				if httpx.LooksLikeNotFoundMessage(upperCode, msg) {
					return core.NewError(core.CodeNotFound, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
				}
				se := core.NewError(core.CodeContentUnavailable, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
				if httpx.LooksLikeChallengeMessage(msg) {
					se = se.WithHintKey(core.HintKeyChallengeDetected)
				}
				return se
			default:
				if upperCode != "VALIDATION_ERROR" && !strings.Contains(upperCode, "VALIDATION") {
					if httpx.LooksLikeNotFoundMessage(upperCode, msg) {
						return core.NewError(core.CodeNotFound, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
					}
					if strings.HasPrefix(upperCode, "FETCH_") || strings.Contains(upperCode, "FETCH") {
						se := core.NewError(core.CodeContentUnavailable, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
						if httpx.LooksLikeChallengeMessage(msg) {
							se = se.WithHintKey(core.HintKeyChallengeDetected)
						}
						return se
					}
				}
			}
		}
		return core.NewError(core.CodeInvalidArgument, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)

	case http.StatusUnauthorized:
		return core.NewError(core.CodeAuthFailed, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)

	case http.StatusForbidden:
		switch upperCode {
		case "IP_NOT_WHITELISTED":
			return core.NewError(core.CodeBlockedByPolicy, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		case "TASK_TYPE_NOT_SUPPORTED":
			return core.NewError(core.CodeNotEnabled, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		default:
			return core.NewError(core.CodeAuthFailed, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		}

	case http.StatusPaymentRequired:
		return core.NewError(core.CodeInsufficientBalance, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)

	case http.StatusTooManyRequests:
		if upperCode == "INSUFFICIENT_FUNDS_CREDITS" ||
			upperCode == "EXCEED_BUDGET_LIMIT" ||
			upperCode == "LINKUPINSUFFICIENTCREDITERROR" ||
			strings.Contains(lowerMsg, "insufficient credit") ||
			strings.Contains(lowerMsg, "insufficient balance") ||
			strings.Contains(lowerMsg, "insufficient funds") ||
			strings.Contains(lowerMsg, "out of credit") ||
			strings.Contains(lowerMsg, "budget limit") {
			return core.NewError(core.CodeInsufficientBalance, httpx.ErrorMessageOrStatus(msg, status)).
				WithProvider(ProviderName).
				WithStatus(status).
				WithRetryable(false)
		}
		return core.NewError(core.CodeRateLimited, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)

	default:
		return httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, msg, body)
	}
}

func isNoResultError(err error) bool {
	if err == nil {
		return false
	}
	se := core.AsError(err)
	return se != nil && se.Params != nil && se.Params[noResultParamKey] == "1"
}

func parseWireError(body []byte) (code, msg string) {
	var env errorWireResp
	if err := json.Unmarshal(body, &env); err != nil || env.Error == nil {
		return "", ""
	}
	return strings.TrimSpace(env.Error.Code), textnorm.CleanInline(env.Error.Message)
}
