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

	KeenableModePro      = "pro"
	KeenableModeRealtime = "realtime"

	ZhihuScopeGlobal      = "global"
	ZhihuScopeZhihu       = "zhihu"
	ZhihuSearchDBAll      = "all"
	ZhihuSearchDBRealtime = "realtime"
	ZhihuSearchDBStatic   = "static"

	LinkupDepthFlash        = "flash"
	LinkupDepthFast         = "fast"
	LinkupDepthStandard     = "standard"
	LinkupDepthDeep         = "deep"
	LinkupFetchModeStandard = "standard"
	LinkupFetchModePro      = "pro"

	FirecrawlProxyAuto     = "auto"
	FirecrawlProxyBasic    = "basic"
	FirecrawlProxyEnhanced = "enhanced"
)

// 跨包共享的按次扩展参数键名与兼容别名常量。
const (
	OptExaSearchType          = "exa.search_type"
	OptExaTypeAlias           = "exa_type"
	OptExaCategory            = "exa.category"
	OptExaCategoryAlias       = "exa_category"
	OptTavilySearchDepth      = "tavily.search_depth"
	OptTavilyDepthAlias       = "tavily_depth"
	OptTavilyTopic            = "tavily.topic"
	OptTavilyTopicAlias       = "tavily_topic"
	OptBraveMode              = "brave.mode"
	OptBraveModeAlias         = "brave_mode"
	OptBraveThreshold         = "brave.threshold"
	OptBraveThresholdAlias    = "brave_threshold"
	OptBraveGoggles           = "brave.goggles"
	OptBraveGogglesAlias      = "brave_goggles"
	OptTinyFishDomainType     = "tinyfish.domain_type"
	OptTinyFishDomainAlias    = "tinyfish_domain_type"
	OptWSAIndustry            = "tencent_wsa.industry"
	OptWSAIndustryAlias       = "wsa_industry"
	OptWSAMode                = "tencent_wsa.mode"
	OptWSAModeAlias           = "wsa_mode"
	OptKeenableMode           = "keenable.mode"
	OptKeenableModeAlias      = "keenable_mode"
	OptZhihuScope             = "zhihu.scope"
	OptZhihuScopeAlias        = "zhihu_scope"
	OptZhihuSearchDB          = "zhihu.search_db"
	OptZhihuSearchDBAlias     = "zhihu_search_db"
	OptZhihuSort              = "zhihu.sort"
	OptZhihuSortAlias         = "zhihu_sort"
	OptLinkupDepth            = "linkup.depth"
	OptLinkupDepthAlias       = "linkup_depth"
	OptFirecrawlCategory      = "firecrawl.category"
	OptFirecrawlCategoryAlias = "firecrawl_category"

	OptFetchFresh             = "fresh"
	OptTavilyExtractDepth     = "tavily.extract_depth"
	OptTavilyExtractAlias     = "tavily_extract_depth"
	OptTinyFishTTL            = "tinyfish.ttl"
	OptTinyFishTTLAlias       = "tinyfish_ttl"
	OptExaMaxAgeHours         = "exa.max_age_hours"
	OptExaMaxAgeAlias         = "exa_max_age_hours"
	OptLinkupFetchMode        = "linkup.fetch_mode"
	OptLinkupFetchModeAlias   = "linkup_fetch_mode"
	OptLinkupRenderJS         = "linkup.render_js"
	OptLinkupRenderJSAlias    = "linkup_render_js"
	OptFirecrawlProxy         = "firecrawl.proxy"
	OptFirecrawlProxyAlias    = "firecrawl_proxy"
	OptFirecrawlMaxPages      = "firecrawl.max_pages"
	OptFirecrawlMaxPagesAlias = "firecrawl_max_pages"
)

// ParseBoolOption 解析布尔扩展参数字符串（支持 1/true/yes/on 与 0/false/no/off，大小写不敏感）。
func ParseBoolOption(raw string) (val, ok bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

// IsFreshFetchOption 判断按次扩展参数是否显式要求绕过缓存实时抓取（fresh=true/1/yes/on）。
func IsFreshFetchOption(opts map[string]string) bool {
	if len(opts) == 0 {
		return false
	}
	v, ok := ParseBoolOption(opts[OptFetchFresh])
	return ok && v
}
