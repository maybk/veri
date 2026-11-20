package exa

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
	tag := strings.TrimSpace(env.Tag)
	msg := textnorm.CleanInline(env.Error)

	switch tag {
	case "FEATURE_DISABLED":
		if msg == "" {
			msg = "Exa feature disabled on current plan"
		}
		return core.NewError(core.CodeNotEnabled, msg).WithProvider(ProviderName).WithStatus(status)
	case "TEAM_BLOCKED":
		if msg == "" {
			msg = "Exa team or API key blocked"
		}
		return core.NewError(core.CodeAuthFailed, msg).WithProvider(ProviderName).WithStatus(status)
	}

	return httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, msg, body)
}

func classifyCrawlTagError(tag string, httpStatus int) error {
	switch tag {
	case "CRAWL_NOT_FOUND":
		return core.NewError(core.CodeNotFound, fmt.Sprintf("Exa target not found (%s)", tag)).WithProvider(ProviderName).WithStatus(httpStatus)
	case "CRAWL_TIMEOUT", "CRAWL_LIVECRAWL_TIMEOUT":
		return core.NewError(core.CodeUpstreamTimeout, fmt.Sprintf("Exa crawl timed out (%s)", tag)).WithProvider(ProviderName).WithStatus(httpStatus)
	case "CRAWL_HTTP_403":
		return core.NewError(core.CodeBlockedByPolicy, fmt.Sprintf("Exa target blocked (%s)", tag)).
			WithProvider(ProviderName).
			WithStatus(httpStatus).
			WithHintKey(core.HintKeyChallengeDetected)
	case "SOURCE_NOT_AVAILABLE":
		return core.NewError(core.CodeBlockedByPolicy, fmt.Sprintf("Exa target blocked (%s)", tag)).WithProvider(ProviderName).WithStatus(httpStatus)
	case "UNSUPPORTED_URL":
		return core.NewError(core.CodeInvalidArgument, "unsupported URL for Exa contents").WithProvider(ProviderName).WithStatus(httpStatus)
	}
	switch {
	case httpStatus == http.StatusNotFound || httpStatus == http.StatusGone:
		return core.NewError(core.CodeNotFound, fmt.Sprintf("Exa target returned HTTP %d", httpStatus)).WithProvider(ProviderName).WithStatus(httpStatus)
	case httpStatus == http.StatusForbidden || httpStatus == http.StatusUnauthorized:
		return core.NewError(core.CodeBlockedByPolicy, fmt.Sprintf("Exa target returned HTTP %d", httpStatus)).WithProvider(ProviderName).WithStatus(httpStatus)
	case httpStatus == http.StatusTooManyRequests:
		return core.NewError(core.CodeRateLimited, "Exa target returned HTTP 429").WithProvider(ProviderName).WithStatus(httpStatus)
	case httpStatus == http.StatusRequestTimeout || httpStatus == http.StatusGatewayTimeout:
		return core.NewError(core.CodeUpstreamTimeout, fmt.Sprintf("Exa target timed out (%d)", httpStatus)).WithProvider(ProviderName).WithStatus(httpStatus)
	case httpStatus >= 500:
		return core.NewError(core.CodeUpstreamUnavailable, fmt.Sprintf("Exa target returned HTTP %d", httpStatus)).WithProvider(ProviderName).WithStatus(httpStatus)
	default:
		return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("Exa failed to extract content (%s)", tag)).WithProvider(ProviderName).WithStatus(httpStatus)
	}
}
