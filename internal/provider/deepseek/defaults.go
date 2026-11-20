package deepseek

import "veri/internal/spec"

const (
	// ProviderName 是 DeepSeek 适配器的唯一标识。
	ProviderName = spec.ProviderDeepSeek

	// DefaultBaseURL 是 DeepSeek 官方 API 根地址。
	DefaultBaseURL = spec.DefaultDeepSeekBaseURL
	// DefaultSearchTimeout 是单次检索默认超时时间。
	DefaultSearchTimeout = spec.DefaultProviderSearchTimeout
	// DefaultFetchTimeout 是单次网页抓取默认超时时间（缓存命中约 120ms，冷抓超时后尽快回退）。
	DefaultFetchTimeout = spec.DefaultDeepSeekFetchTimeout
	// DefaultConcurrency 是默认最大并发数。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	maxQueryRunes     = 128
	maxExcludeSites   = 50
	maxRerankScore    = 3.0
	maxAuthorityScore = 4.0
	maxFetchURLLength = spec.DefaultMaxURLLength
)
