package spec

import "time"

// 跨包共享的默认条数、并发、字节上限与超时预算常量。
const (
	DefaultSearchCount                 = 8
	MinSearchCount                     = 1
	MaxSearchCount                     = 50
	DefaultMCPMaxCount                 = 20
	DefaultFusionK                     = 60
	DefaultFusionAutoLimit             = 3
	MaxBatchFetchURLs                  = 10
	MaxMCPBatchFetchURLs               = 5
	DefaultBatchFetchConcurrency       = 4
	DefaultSummaryMaxChars             = 800
	DefaultSnippetMaxRunes             = 200
	DefaultMCPSearchMaxChars           = 24000
	DefaultMCPFetchMaxChars            = 30000
	DefaultMCPContentMaxChars          = 3000
	MinMCPFetchMaxChars                = 1000
	DefaultCacheMCPMaxEntries          = 256
	DefaultCacheMaxBytes         int64 = 32 << 20
	DefaultMinContentChars             = 300
	DefaultProviderConcurrency         = 4
	DefaultFirecrawlConcurrency        = 2
	DefaultFirecrawlMaxPDFPages        = 20
	MaxFirecrawlPDFPages               = 500
	DefaultFetchMaxBytes         int64 = 5 << 20
	DefaultAPIResponseBytes      int64 = 16 << 20
	DefaultMaxURLLength                = 8192
	DefaultFakeIPCIDR                  = "198.18.0.0/15"

	DefaultSearchTimeout          = 20 * time.Second
	DefaultFetchTimeout           = 45 * time.Second
	DefaultProviderSearchTimeout  = 10 * time.Second
	DefaultProviderFetchTimeout   = 25 * time.Second
	DefaultDeepSeekFetchTimeout   = 10 * time.Second
	DefaultFirecrawlFetchTimeout  = 45 * time.Second
	DefaultSmartLocalProbeTimeout = 8 * time.Second
	DefaultCacheFetchTTL          = 15 * time.Minute
	DefaultCacheSearchTTL         = time.Duration(0)
	DefaultCacheMCPSearchTTL      = 5 * time.Minute
)

// DefaultFakeIPCIDRs 返回默认放行的 TUN Fake-IP 网段列表副本。
func DefaultFakeIPCIDRs() []string {
	return []string{DefaultFakeIPCIDR}
}

// ClampSearchCount 将请求条数约束在 [1, maxCount] 合法范围内。
func ClampSearchCount(count, maxCount int) int {
	if count <= 0 {
		count = DefaultSearchCount
	}
	if maxCount > 0 && count > maxCount {
		count = maxCount
	}
	return count
}
