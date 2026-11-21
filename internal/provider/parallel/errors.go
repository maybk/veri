package parallel

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

func classifyError(status int, _ http.Header, body []byte) *core.Error {
	var env errorWireResp
	_ = json.Unmarshal(body, &env)
	var msg string
	if env.Error != nil {
		msg = textnorm.CleanInline(env.Error.Message)
	}

	switch status {
	case http.StatusUnauthorized:
		return core.NewError(core.CodeAuthFailed, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case http.StatusPaymentRequired:
		return core.NewError(core.CodeInsufficientBalance, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case http.StatusForbidden:
		lower := strings.ToLower(msg)
		switch {
		case strings.Contains(lower, "invalid processor"),
			strings.Contains(lower, "unsupported mode"),
			strings.Contains(lower, "not supported"),
			strings.Contains(lower, "only supports"):
			// 请求级模式/端点限制（例如向 EU 端点发送非 basic 模式），绝不拉黑健康密钥。
			return core.NewError(core.CodeInvalidArgument, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		case strings.Contains(lower, "policy"), strings.Contains(lower, "blocked"):
			return core.NewError(core.CodeBlockedByPolicy, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		default:
			return core.NewError(core.CodeAuthFailed, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
		}
	case http.StatusNotFound, http.StatusGone:
		return core.NewError(core.CodeNotFound, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case http.StatusRequestTimeout:
		return core.NewError(core.CodeUpstreamTimeout, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case http.StatusUnprocessableEntity:
		return core.NewError(core.CodeInvalidArgument, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	case http.StatusTooManyRequests:
		return core.NewError(core.CodeRateLimited, httpx.ErrorMessageOrStatus(msg, status)).WithProvider(ProviderName).WithStatus(status)
	default:
		return httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, msg, body)
	}
}

func classifyExtractItemError(item extractWireError) *core.Error {
	status := 0
	if item.HTTPStatusCode != nil {
		status = *item.HTTPStatusCode
	}
	errType := strings.ToLower(strings.TrimSpace(item.ErrorType))
	detail := textnorm.CleanInline(item.Content)
	if detail == "" {
		if status > 0 {
			detail = fmt.Sprintf("target page returned HTTP %d", status)
		} else {
			detail = textnorm.CleanInline(item.ErrorType)
		}
	}
	if detail == "" {
		detail = "Parallel extract failed for target URL"
	}

	withOptStatus := func(se *core.Error) *core.Error {
		se = se.WithProvider(ProviderName)
		if status > 0 {
			se = se.WithStatus(status)
		}
		return se
	}

	switch status {
	case http.StatusNotFound, http.StatusGone:
		return withOptStatus(core.NewError(core.CodeNotFound, detail))
	case http.StatusForbidden, http.StatusTooManyRequests:
		return withOptStatus(core.NewError(core.CodeContentUnavailable, detail).WithHintKey(core.HintKeyChallengeDetected))
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return withOptStatus(core.NewError(core.CodeUpstreamTimeout, detail))
	}

	switch {
	case strings.Contains(errType, "timeout"):
		return withOptStatus(core.NewError(core.CodeUpstreamTimeout, detail))
	case httpx.LooksLikeNotFoundMessage(errType, detail):
		return withOptStatus(core.NewError(core.CodeNotFound, detail))
	case httpx.LooksLikeChallengeMessage(detail) || httpx.LooksLikeChallengeMessage(errType):
		return withOptStatus(core.NewError(core.CodeContentUnavailable, detail).WithHintKey(core.HintKeyChallengeDetected))
	default:
		// 内层错误反映目标站抓取结果而非 Parallel 平台故障，归为 content_unavailable 以安全回退且不误熔断平台健康度。
		return withOptStatus(core.NewError(core.CodeContentUnavailable, detail))
	}
}
