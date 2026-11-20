package tavily

import (
	"encoding/json/v2"
	"net/http"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

// classifyError 将 Tavily 错误状态码（含 432/433 配额耗尽与 434 容量超限）映射为统一 core.Error。
func classifyError(status int, _ http.Header, body []byte) *core.Error {
	msg := extractDetailMessage(body)
	switch status {
	case 432, 433:
		if msg == "" {
			msg = "Tavily plan or pay-as-you-go credit limit exhausted"
		}
		return core.NewError(core.CodeInsufficientBalance, msg).WithProvider(ProviderName).WithStatus(status)
	case 434:
		if msg == "" {
			msg = "Tavily flex tier capacity exceeded"
		}
		return core.NewError(core.CodeUpstreamUnavailable, msg).WithProvider(ProviderName).WithStatus(status)
	default:
		return httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, msg, body)
	}
}

func extractDetailMessage(body []byte) string {
	var env errorWireResp
	if err := json.Unmarshal(body, &env); err != nil || env.Detail == nil {
		return ""
	}
	switch v := env.Detail.(type) {
	case string:
		return textnorm.CleanInline(v)
	case map[string]any:
		if e, ok := v["error"].(string); ok {
			return textnorm.CleanInline(e)
		}
	}
	return ""
}
