package tencentwsa

import (
	"encoding/json/v2"
	"net/http"
	"strings"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

func inspectWSABody(status int, _ http.Header, body []byte) *core.Error {
	var wire errorEnvelopeWireResp
	if err := json.Unmarshal(body, &wire); err == nil && wire.Response.Error != nil && strings.TrimSpace(wire.Response.Error.Code) != "" {
		return classifyWSACode(wire.Response.Error.Code, wire.Response.Error.Message).WithStatus(status)
	}
	return nil
}

func classifyHTTPError(status int, h http.Header, body []byte) *core.Error {
	if err := inspectWSABody(status, h, body); err != nil {
		return err
	}
	return httpx.ClassifyHTTPError(ProviderName, status, body)
}

// classifyWSACode 将腾讯云 API 3.0 Response.Error.Code（含主码与点分子码）映射为统一 core.Error。
func classifyWSACode(code, message string) *core.Error {
	msg := textnorm.CleanInline(message)
	trimmedCode := strings.TrimSpace(code)
	if msg == "" {
		msg = trimmedCode
	}
	var mapped core.Code
	switch {
	case trimmedCode == "InvalidParameter" || strings.HasPrefix(trimmedCode, "InvalidParameter.") || strings.HasPrefix(trimmedCode, "InvalidParameterValue") || strings.HasPrefix(trimmedCode, "MissingParameter"):
		mapped = core.CodeInvalidArgument
	case trimmedCode == "UnauthorizedOperation" || strings.HasPrefix(trimmedCode, "UnauthorizedOperation.") || trimmedCode == "AuthFailure" || strings.HasPrefix(trimmedCode, "AuthFailure."):
		mapped = core.CodeAuthFailed
	case trimmedCode == "ResourceNotFound" || strings.HasPrefix(trimmedCode, "ResourceNotFound.") || trimmedCode == "FailedOperation.ServiceNotOpened":
		mapped = core.CodeNotEnabled
	case trimmedCode == "ResourceUnavailable" || strings.HasPrefix(trimmedCode, "ResourceUnavailable.") ||
		trimmedCode == "ResourceInsufficient" || strings.HasPrefix(trimmedCode, "ResourceInsufficient.") ||
		trimmedCode == "FailedOperation.BalanceInsufficient" || trimmedCode == "FailedOperation.Arrears" || trimmedCode == "FailedOperation.QuotaExhausted":
		mapped = core.CodeInsufficientBalance
	case trimmedCode == "RequestLimitExceeded" || strings.HasPrefix(trimmedCode, "RequestLimitExceeded.") ||
		trimmedCode == "LimitExceeded" || strings.HasPrefix(trimmedCode, "LimitExceeded."):
		mapped = core.CodeRateLimited
	case trimmedCode == "InternalError" || strings.HasPrefix(trimmedCode, "InternalError.") || trimmedCode == "ServiceUnavailable":
		mapped = core.CodeUpstreamUnavailable
	default:
		mapped = core.CodeUpstreamBadResponse
	}
	return core.NewError(mapped, msg).WithProvider(ProviderName)
}
