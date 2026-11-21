package local

import (
	"strings"

	"veri/internal/httpx"
	"veri/internal/spec"
)

const (
	// ProviderName 是本地通用提取器的唯一标识。
	ProviderName = spec.ProviderLocal

	// DefaultTimeout 默认单次抓取超时时间。
	DefaultTimeout = spec.DefaultProviderSearchTimeout
	// DefaultConcurrency 默认最大并发数。
	DefaultConcurrency = spec.DefaultProviderConcurrency
	// DefaultMaxBytes 默认解压后最大响应体字节数（5 MB）。
	DefaultMaxBytes = httpx.DefaultFetchResponseMaxBytes
	// DefaultMinContentChars 默认正文质量闸门最小非空白字符数。
	DefaultMinContentChars = spec.DefaultMinContentChars

	maxFetchURLLength = spec.DefaultMaxURLLength
)

func resolveUserAgent(uaSpec, version string) string {
	trimmed := strings.TrimSpace(uaSpec)
	switch strings.ToLower(trimmed) {
	case "", spec.UserAgentBrowserMode:
		return spec.BrowserUserAgent
	case spec.AppName:
		return spec.UserAgent(version)
	default:
		return trimmed
	}
}
