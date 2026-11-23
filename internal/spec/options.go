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

	ZhihuSearchDBAll      = "all"
	ZhihuSearchDBRealtime = "realtime"
	ZhihuSearchDBStatic   = "static"

	LinkupDepthFlash        = "flash"
	LinkupDepthFast         = "fast"
	LinkupDepthStandard     = "standard"
	LinkupDepthDeep         = "deep"
	LinkupFetchModeStandard = "standard"
	LinkupFetchModePro      = "pro"

	ParallelModeTurbo    = "turbo"
	ParallelModeFast     = "fast"
	ParallelModeBasic    = "basic"
	ParallelModeAdvanced = "advanced"

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
	OptZhihuWebSearchDB       = "zhihu_web.search_db"
	OptZhihuWebSearchDBAlias  = "zhihu_web_search_db"
	OptZhihuSort              = "zhihu.sort"
	OptZhihuSortAlias         = "zhihu_sort"
	OptLinkupDepth            = "linkup.depth"
	OptLinkupDepthAlias       = "linkup_depth"
	OptParallelMode           = "parallel.mode"
	OptParallelModeAlias      = "parallel_mode"
	OptParallelLocation       = "parallel.location"
	OptParallelLocationAlias  = "parallel_location"
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

// NormalizeOptions 返回键已小写去空白、值已去空白的扩展参数副本（无有效项时返回 nil）。
func NormalizeOptions(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var out map[string]string
	for k, v := range raw {
		nk := strings.ToLower(strings.TrimSpace(k))
		if nk == "" {
			continue
		}
		if out == nil {
			out = make(map[string]string, len(raw))
		}
		out[nk] = strings.TrimSpace(v)
	}
	return out
}

// LookupOption 按大小写与前后空白不敏感方式查找首个非空扩展参数值（常规小写键走 O(1) 零分配快路径）。
func LookupOption(opts map[string]string, keys ...string) string {
	if len(opts) == 0 || len(keys) == 0 {
		return ""
	}
	for _, want := range keys {
		if v := strings.TrimSpace(opts[want]); v != "" {
			return v
		}
	}
	for _, want := range keys {
		for k, v := range opts {
			if strings.EqualFold(strings.TrimSpace(k), want) {
				if tv := strings.TrimSpace(v); tv != "" {
					return tv
				}
			}
		}
	}
	return ""
}

// IsFreshFetchOption 判断按次扩展参数是否显式要求绕过缓存实时抓取（fresh=true/1/yes/on，键大小写与空白不敏感）。
func IsFreshFetchOption(opts map[string]string) bool {
	v, ok := ParseBoolOption(LookupOption(opts, OptFetchFresh))
	return ok && v
}
