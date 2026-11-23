package zhihu

import (
	"encoding/json/v2"
	"fmt"
	"net/http"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

func (p *Provider) inspectBody(status int, _ http.Header, body []byte) *core.Error {
	var env statusWireResp
	if err := json.Unmarshal(body, &env); err == nil && env.Code != 0 {
		return classifyZhihuCode(p.name, env.Code, env.Message).WithStatus(status)
	}
	return nil
}

func (p *Provider) classifyHTTPError(status int, h http.Header, body []byte) *core.Error {
	if err := p.inspectBody(status, h, body); err != nil {
		return err
	}
	return httpx.ClassifyHTTPError(p.name, status, body)
}

func classifyZhihuCode(providerName string, code int, message string) *core.Error {
	msg := textnorm.CleanInline(message)
	if msg == "" {
		msg = fmt.Sprintf("zhihu error code %d", code)
	}
	switch code {
	case 10001:
		return core.NewError(core.CodeInvalidArgument, msg).
			WithProvider(providerName).
			WithHint("Check query parameters and ensure the local system clock is within 10 minutes of standard time.")
	case 20001:
		return core.NewError(core.CodeAuthFailed, msg).WithProvider(providerName)
	case 30001:
		return core.NewError(core.CodeRateLimited, msg).WithProvider(providerName)
	case 30002:
		return core.NewError(core.CodeInsufficientBalance, msg).WithProvider(providerName)
	case 30003:
		return core.NewError(core.CodeBlockedByPolicy, msg).WithProvider(providerName).WithRetryable(false)
	case 90001:
		return core.NewError(core.CodeUpstreamUnavailable, msg).WithProvider(providerName)
	default:
		return core.NewError(core.CodeUpstreamBadResponse, msg).WithProvider(providerName)
	}
}
