package zhihu

import "veri/internal/spec"

const (
	// ProviderName 是知乎站内搜索适配器的唯一标识。
	ProviderName = spec.ProviderZhihu
	// WebProviderName 是知乎全网搜索适配器的唯一标识。
	WebProviderName = spec.ProviderZhihuWeb

	// DefaultBaseURL 是知乎开放平台官方 API 根地址。
	DefaultBaseURL = spec.DefaultZhihuBaseURL
	// DefaultSearchDB 默认全网索引库（all）。
	DefaultSearchDB = spec.ZhihuSearchDBAll
	// DefaultSearchTimeout 默认检索超时。
	DefaultSearchTimeout = spec.DefaultProviderSearchTimeout
	// DefaultConcurrency 默认并发上限。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	zhihuRootDomain        = "zhihu.com"
	headerRequestTimestamp = "X-Request-Timestamp"
	pathGlobalSearch       = "/api/v1/content/global_search"
	pathZhihuSearch        = "/api/v1/content/zhihu_search"

	maxGlobalSearchCount = 20
	maxZhihuSearchCount  = 10
	maxAuthorityLevel    = 4.0
)
