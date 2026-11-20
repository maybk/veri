package github

import (
	"veri/internal/httpx"
	"veri/internal/spec"
)

const (
	// ProviderName 是 GitHub 专用提取器的唯一标识。
	ProviderName = spec.ProviderGitHub

	// DefaultAPIBaseURL 是 GitHub REST API 根地址。
	DefaultAPIBaseURL = spec.DefaultGitHubAPIBaseURL
	// DefaultRawBaseURL 是 GitHub Raw 文件读取根地址。
	DefaultRawBaseURL = spec.DefaultGitHubRawBaseURL
	// DefaultTimeout 默认单次请求超时时间。
	DefaultTimeout = spec.DefaultProviderSearchTimeout
	// DefaultConcurrency 默认并发上限。
	DefaultConcurrency = spec.DefaultProviderConcurrency
	// DefaultMaxBytes 默认响应最大读取字节上限。
	DefaultMaxBytes = httpx.DefaultFetchResponseMaxBytes

	apiVersion        = "2022-11-28"
	maxFetchURLLength = spec.DefaultMaxURLLength
)

var reservedTopPaths = map[string]struct{}{
	"orgs": {}, "organizations": {}, "settings": {}, "explore": {}, "trending": {},
	"topics": {}, "search": {}, "marketplace": {}, "notifications": {}, "login": {},
	"signup": {}, "pricing": {}, "features": {}, "about": {}, "sponsors": {},
	"collections": {}, "events": {}, "codespaces": {}, "copilot": {}, "security": {},
}
