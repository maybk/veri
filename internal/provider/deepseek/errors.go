package deepseek

import (
	"encoding/json/v2"
	"net/http"

	"veri/internal/core"
	"veri/internal/httpx"
)

// classifyError 将 DeepSeek 错误响应与 HTTP 状态码映射为统一 core.Error。
func classifyError(status int, _ http.Header, body []byte) *core.Error {
	var env errorWireResp
	_ = json.Unmarshal(body, &env)
	return httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, env.Error.Message, body)
}

// classifyFetchError 将 DeepSeek 网页提取错误响应映射为统一 core.Error，目标站 404/410 映射为 CodeNotFound。
func classifyFetchError(status int, h http.Header, body []byte) *core.Error {
	se := classifyError(status, h, body)
	if status == http.StatusNotFound || status == http.StatusGone {
		se.Code = core.CodeNotFound
		se.Retryable = false
	}
	return se
}
