package github

import (
	"encoding/json/v2"
	"net/http"
	"strings"

	"veri/internal/core"
	"veri/internal/httpx"
)

// classifyGitHubError 识别 GitHub API 限流响应头并将 HTTP 状态码映射为统一 core.Error。
func classifyGitHubError(status int, headers http.Header, body []byte) *core.Error {
	if (status == http.StatusForbidden || status == http.StatusTooManyRequests) &&
		strings.TrimSpace(headers.Get("X-RateLimit-Remaining")) == "0" {
		return core.NewError(core.CodeRateLimited, "GitHub API rate limit exceeded").
			WithProvider(ProviderName).
			WithStatus(status).
			WithHint("Configure GITHUB_TOKEN to raise the GitHub API rate limit from 60 to 5,000 requests/hour.")
	}
	var env errorWire
	_ = json.Unmarshal(body, &env)
	return httpx.ClassifyHTTPErrorWithMessage(ProviderName, status, env.Message, body)
}
