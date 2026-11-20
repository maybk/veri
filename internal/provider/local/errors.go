package local

import (
	"fmt"
	"net/http"
	"strings"

	"veri/internal/core"
	"veri/internal/textnorm"
)

func classifyFetchHTTPStatus(status int) *core.Error {
	switch {
	case status == http.StatusNotFound || status == http.StatusGone:
		return core.NewError(core.CodeNotFound, fmt.Sprintf("target returned HTTP %d", status)).WithProvider(ProviderName).WithStatus(status)
	case status == http.StatusForbidden:
		return core.NewError(core.CodeBlockedByPolicy, "target returned HTTP 403 Forbidden").WithProvider(ProviderName).WithStatus(status)
	case status == http.StatusTooManyRequests:
		return core.NewError(core.CodeRateLimited, "target returned HTTP 429 Too Many Requests").WithProvider(ProviderName).WithStatus(status)
	case status >= 500:
		return core.NewError(core.CodeUpstreamUnavailable, fmt.Sprintf("target returned HTTP %d", status)).WithProvider(ProviderName).WithStatus(status)
	default:
		return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("target returned HTTP %d", status)).WithProvider(ProviderName).WithStatus(status)
	}
}

func classifyNonOKResponse(status int, h http.Header, preview []byte) error {
	se := classifyFetchHTTPStatus(status)
	if isAntiBotChallengeResponse(status, h, preview) {
		if se.Code == core.CodeNotFound {
			se.Code = core.CodeContentUnavailable
		}
		se = se.WithHint("Target blocked direct HTTP access with an anti-bot challenge; configure an API extractor (e.g. `veri key set tinyfish` or `tavily`).").
			WithHintKey(core.HintKeyChallengeDetected)
	}
	return se
}

func isAntiBotChallengeResponse(status int, h http.Header, preview []byte) bool {
	if h != nil {
		if strings.Contains(strings.ToLower(h.Get("Cf-Mitigated")), "challenge") {
			return true
		}
		if h.Get("X-Datadome") != "" || h.Get("X-Px-Blocked") != "" {
			return true
		}
	}
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusServiceUnavailable:
		if len(preview) > 0 {
			wall := textnorm.DetectChallengeWall("", string(preview))
			if wall.LowQuality && wall.Reason == textnorm.QualityReasonBotOrJSWall {
				return true
			}
		}
	}
	return false
}
