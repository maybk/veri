package firecrawl

import "veri/internal/spec"

const (
	// ProviderName 是 Firecrawl 适配器的唯一标识。
	ProviderName = spec.ProviderFirecrawl

	// DefaultBaseURL 是 Firecrawl 官方云端 API 根地址。
	DefaultBaseURL = spec.DefaultFirecrawlBaseURL
	// DefaultProxy 默认代理模式（auto：失败自动升档 enhanced 且不加收 credits）。
	DefaultProxy = spec.FirecrawlProxyAuto
	// DefaultMaxPDFPages 默认 PDF 最大解析页数上限，防止大文档按页扣光免费额度。
	DefaultMaxPDFPages = spec.DefaultFirecrawlMaxPDFPages
	// DefaultSearchTimeout 默认检索超时。
	DefaultSearchTimeout = spec.DefaultProviderSearchTimeout
	// DefaultFetchTimeout 默认提取超时（45s，覆盖 enhanced 代理排队与渲染耗时）。
	DefaultFetchTimeout = spec.DefaultFirecrawlFetchTimeout
	// DefaultConcurrency 默认并发上限（2，匹配 Free 档 2 并发浏览器上限）。
	DefaultConcurrency = spec.DefaultFirecrawlConcurrency

	maxSearchCount    = 10
	maxQueryRunes     = 500
	maxIncludeDomains = 100
	maxExcludeDomains = 100
	maxFetchURLLength = spec.DefaultMaxURLLength
	searchCredits     = 2
)
