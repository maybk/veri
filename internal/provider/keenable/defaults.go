package keenable

import "veri/internal/spec"

const (
	// ProviderName 是 Keenable 适配器的唯一标识。
	ProviderName = spec.ProviderKeenable

	// DefaultBaseURL 是 Keenable 官方 API 根地址。
	DefaultBaseURL = spec.DefaultKeenableBaseURL
	// DefaultMode 默认检索模式（pro）。
	DefaultMode = spec.KeenableModePro
	// DefaultSearchTimeout 默认检索超时。
	DefaultSearchTimeout = spec.DefaultProviderSearchTimeout
	// DefaultFetchTimeout 默认提取超时。
	DefaultFetchTimeout = spec.DefaultProviderFetchTimeout
	// DefaultConcurrency 默认并发上限。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	maxSearchCount    = 50
	maxExcludeDomains = 1000
	// maxFetchChars 放宽上游默认 50,000 字符截断，避免长文档与 PDF 被提前腰斩，交由路由层统一分页。
	maxFetchChars     = 500000
	maxFetchURLLength = spec.DefaultMaxURLLength

	snippetLenSnippet = 300
	snippetLenSummary = 1200
	snippetLenContent = 6000
)
