package tavily

import "veri/internal/spec"

const (
	// ProviderName 是 Tavily 适配器的唯一标识。
	ProviderName = spec.ProviderTavily

	// DefaultBaseURL 是 Tavily 官方 API 根地址。
	DefaultBaseURL = spec.DefaultTavilyBaseURL
	// DefaultSearchDepth 默认检索深度（basic）。
	DefaultSearchDepth = spec.TavilyDepthBasic
	// DefaultExtractDepth 默认提取深度（basic）。
	DefaultExtractDepth = spec.TavilyDepthBasic
	// DefaultSearchTimeout 默认检索超时。
	DefaultSearchTimeout = spec.DefaultProviderSearchTimeout
	// DefaultFetchTimeout 默认提取超时。
	DefaultFetchTimeout = spec.DefaultProviderFetchTimeout
	// DefaultConcurrency 默认并发上限。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	maxSearchCount    = 20
	maxIncludeDomains = 300
	maxExcludeDomains = 150
	maxFetchURLLength = spec.DefaultMaxURLLength
)
