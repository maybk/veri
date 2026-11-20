package tinyfish

import "veri/internal/spec"

const (
	// ProviderName 是 TinyFish 适配器的唯一标识。
	ProviderName = spec.ProviderTinyFish

	// DefaultSearchURL 是 TinySearch 默认端点。
	DefaultSearchURL = spec.DefaultTinyFishSearchURL
	// DefaultFetchURL 是 TinyFetch 默认端点。
	DefaultFetchURL = spec.DefaultTinyFishFetchURL
	// DefaultSearchTimeout 默认检索超时。
	DefaultSearchTimeout = spec.DefaultProviderSearchTimeout
	// DefaultFetchTimeout 默认服务端渲染抓取超时。
	DefaultFetchTimeout = spec.DefaultProviderFetchTimeout
	// DefaultConcurrency 默认最大并发数。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	maxPageCount      = 10
	maxExcludeSites   = 100
	maxQueryRunes     = 2000
	maxRecencyMinutes = 5256000 // 10 年
	maxFetchURLLength = spec.DefaultMaxURLLength

	domainWeb           = "web"
	domainNews          = "news"
	domainResearchPaper = "research_paper"
)
