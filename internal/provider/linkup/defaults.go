package linkup

import "veri/internal/spec"

const (
	// ProviderName 是 Linkup 适配器的唯一标识。
	ProviderName = spec.ProviderLinkup

	// DefaultBaseURL 是 Linkup 官方 API 根地址。
	DefaultBaseURL = spec.DefaultLinkupBaseURL
	// DefaultDepth 默认检索深度（fast：~1s 纯索引，防止意外触发 10 倍价 deep）。
	DefaultDepth = spec.LinkupDepthFast
	// DefaultFetchMode 默认提取模式（standard）。
	DefaultFetchMode = spec.LinkupFetchModeStandard
	// DefaultSearchTimeout 默认检索超时。
	DefaultSearchTimeout = spec.DefaultProviderSearchTimeout
	// DefaultFetchTimeout 默认提取超时。
	DefaultFetchTimeout = spec.DefaultProviderFetchTimeout
	// DefaultConcurrency 默认并发上限。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	outputTypeSearchResults = "searchResults"
	maxSearchCount          = spec.MaxSearchCount
	maxIncludeDomains       = 100
	maxExcludeDomains       = 100
	maxFetchURLLength       = spec.DefaultMaxURLLength
	noResultParamKey        = "linkup_no_result"
)
