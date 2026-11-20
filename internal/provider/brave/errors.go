package brave

import (
	"encoding/json/v2"
	"net/http"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
)

// classifyError 将 Brave 错误响应与 HTTP 状态码映射为统一 core.Error。
func classifyError(status int, _ http.Header, body []byte) *core.Error {
	var env errorWireResp
	_ = json.Unmarshal(body, &env)
	msg := textnorm.CleanInline(env.Error.Detail)

	if status == http.StatusForbidden {
		if msg == "" {
			msg = "Brave subscription token forbidden or invalid"
		}
		return core.NewError(core.CodeAuthFailed, msg).WithProvider(ProviderName).WithStatus(status)
	}
	return httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, msg, body)
}
