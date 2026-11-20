package zhihu

import (
	"encoding/json/v2"
	"fmt"
	"net/http"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

func inspectBody(status int, _ http.Header, body []byte) *core.Error {
	var env statusWireResp
	if err := json.Unmarshal(body, &env); err == nil && env.Code != 0 {
		return classifyZhihuCode(env.Code, env.Message).WithStatus(status)
	}
	return nil
}

func classifyHTTPError(status int, h http.Header, body []byte) *core.Error {
	if err := inspectBody(status, h, body); err != nil {
		return err
	}
	return httpx.ClassifyHTTPError(ProviderName, status, body)
}

func classifyZhihuCode(code int, message string) *core.Error {
	msg := textnorm.CleanInline(message)
	if msg == "" {
		msg = fmt.Sprintf("zhihu error code %d", code)
	}
	switch code {
	case 10001:
		return core.NewError(core.CodeInvalidArgument, msg).
			WithProvider(ProviderName).
			WithHint("Check query parameters and ensure the local system clock is within 10 minutes of standard time.")
	case 20001:
		return core.NewError(core.CodeAuthFailed, msg).WithProvider(ProviderName)
	case 30001:
		return core.NewError(core.CodeRateLimited, msg).WithProvider(ProviderName)
	case 30002:
		return core.NewError(core.CodeInsufficientBalance, msg).WithProvider(ProviderName)
	case 30003:
		return core.NewError(core.CodeBlockedByPolicy, msg).WithProvider(ProviderName).WithRetryable(false)
	case 90001:
		return core.NewError(core.CodeUpstreamUnavailable, msg).WithProvider(ProviderName)
	default:
		return core.NewError(core.CodeUpstreamBadResponse, msg).WithProvider(ProviderName)
	}
}
