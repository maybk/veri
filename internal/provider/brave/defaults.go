package brave

import "veri/internal/spec"

const (
	// ProviderName 是 Brave Search 适配器的唯一标识。
	ProviderName = spec.ProviderBrave

	// DefaultBaseURL 是 Brave Search API 官方根地址。
	DefaultBaseURL = spec.DefaultBraveBaseURL
	// DefaultMode 默认端点选择模式（auto）。
	DefaultMode = spec.BraveModeAuto
	// DefaultTimeout 默认检索超时时间。
	DefaultTimeout = spec.DefaultProviderSearchTimeout
	// DefaultConcurrency 默认并发上限。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	maxWebSearchCount          = 20
	maxExcludeSites            = 50
	maxQueryRunes              = 400
	maxQueryWords              = 50
	defaultContextTokensPerURL = 1024
	contentContextTokensPerURL = 4096
)
