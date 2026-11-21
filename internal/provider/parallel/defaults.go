package parallel

import "veri/internal/spec"

const (
	// ProviderName 是 Parallel 适配器在注册表与配置中的唯一标识。
	ProviderName = spec.ProviderParallel
	// DefaultBaseURL 是 Parallel 官方 REST API 基础端点（企业合规客户可覆写为 https://eu.parallel.ai）。
	DefaultBaseURL = spec.DefaultParallelBaseURL
	// DefaultMode 显式锁定 fast 模式（约 700ms、低价高质），避免上游省略 mode 时默认回落至 5 倍价 advanced。
	DefaultMode = spec.ParallelModeFast
	// DefaultSearchTimeout 是单次检索请求默认超时时间。
	DefaultSearchTimeout = spec.DefaultProviderSearchTimeout
	// DefaultFetchTimeout 是单次提取请求默认超时时间。
	DefaultFetchTimeout = spec.DefaultProviderFetchTimeout
	// DefaultConcurrency 是默认单进程并发上限。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	searchEndpointPath  = "/v1/search"
	extractEndpointPath = "/v1/extract"

	// OpenAPI 公开模式单次最多返回 20 条结果；include_domains 与 exclude_domains 合并上限 200（各取 100 保证不变式）。
	maxSearchCount        = 20
	maxIncludeDomains     = 100
	maxExcludeDomains     = 100
	maxTotalSourceDomains = maxIncludeDomains + maxExcludeDomains
	maxFetchURLLength     = spec.DefaultMaxURLLength

	// Extract fetch_policy 官方约束：max_age_seconds 最小 600 秒，live fetch 超时钳制在 [1, 60] 秒。
	minFetchPolicyMaxAgeSeconds  = 600
	minFetchPolicyTimeoutSeconds = 1
	maxFetchPolicyTimeoutSeconds = 60
)
