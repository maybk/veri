package spec

import "strings"

// 跨包共享的厂商运行模式枚举常量。
const (
	ExaTypeLite          = "lite"
	ExaTypeAuto          = "auto"
	ExaTypeFast          = "fast"
	ExaTypeInstant       = "instant"
	ExaTypeDeepLite      = "deep-lite"
	ExaTypeDeep          = "deep"
	ExaTypeDeepReasoning = "deep-reasoning"

	WSAEditionLite     = "lite"
	WSAEditionStandard = "standard"
	WSAEditionFlagship = "flagship"

	TavilyDepthUltraFast = "ultra-fast"
	TavilyDepthFast      = "fast"
	TavilyDepthBasic     = "basic"
	TavilyDepthAdvanced  = "advanced"

	BraveModeAuto    = "auto"
	BraveModeContext = "context"
	BraveModeWeb     = "web"
)

// 跨包共享的按次扩展参数键名与兼容别名常量。
const (
	OptExaSearchType       = "exa.search_type"
	OptExaTypeAlias        = "exa_type"
	OptExaCategory         = "exa.category"
	OptExaCategoryAlias    = "exa_category"
	OptTavilySearchDepth   = "tavily.search_depth"
	OptTavilyDepthAlias    = "tavily_depth"
	OptTavilyTopic         = "tavily.topic"
	OptTavilyTopicAlias    = "tavily_topic"
	OptBraveMode           = "brave.mode"
	OptBraveModeAlias      = "brave_mode"
	OptBraveThreshold      = "brave.threshold"
	OptBraveThresholdAlias = "brave_threshold"
	OptBraveGoggles        = "brave.goggles"
	OptBraveGogglesAlias   = "brave_goggles"
	OptTinyFishDomainType  = "tinyfish.domain_type"
	OptTinyFishDomainAlias = "tinyfish_domain_type"
	OptWSAIndustry         = "tencent_wsa.industry"
	OptWSAIndustryAlias    = "wsa_industry"
	OptWSAMode             = "tencent_wsa.mode"
	OptWSAModeAlias        = "wsa_mode"

	OptFetchFresh         = "fresh"
	OptTavilyExtractDepth = "tavily.extract_depth"
	OptTavilyExtractAlias = "tavily_extract_depth"
	OptTinyFishTTL        = "tinyfish.ttl"
	OptTinyFishTTLAlias   = "tinyfish_ttl"
	OptExaMaxAgeHours     = "exa.max_age_hours"
	OptExaMaxAgeAlias     = "exa_max_age_hours"
)

// IsFreshFetchOption 判断按次扩展参数是否显式要求绕过缓存实时抓取（fresh=true/1/yes/on）。
func IsFreshFetchOption(opts map[string]string) bool {
	if len(opts) == 0 {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(opts[OptFetchFresh])) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}
