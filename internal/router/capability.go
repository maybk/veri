package router

import (
	"fmt"
	"slices"
	"time"

	"veri/internal/core"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

const reasonSiteScope = "site_scope"

// executionPlan 描述路由层为单个 Searcher 计算出的能力匹配与后过滤模拟计划。
type executionPlan struct {
	unsupportedReason string
	adapted           core.SearchRequest
	degraded          []string
	warnings          []string
	emulateSites      []string
	emulateExcludes   []string
	emulateFreshness  *core.Freshness
	targetCount       int
}

func resolveCapabilities(s core.Searcher, req *core.SearchRequest) core.Capabilities {
	if ds, ok := s.(core.DynamicSearcher); ok && req != nil {
		return ds.CapabilitiesFor(req)
	}
	return s.Capabilities()
}

func matchCapabilities(caps core.Capabilities, req *core.SearchRequest, strict bool) executionPlan {
	plan := executionPlan{
		adapted:     *req,
		targetCount: req.Count,
	}
	plan.adapted.Filters = core.Filters{
		Sites:        slices.Clone(req.Filters.Sites),
		ExcludeSites: slices.Clone(req.Filters.ExcludeSites),
		Lang:         slices.Clone(req.Filters.Lang),
		Freshness:    req.Filters.Freshness,
	}

	// 0. 查询长度上限前置匹配
	if caps.QueryMaxRunes > 0 && textnorm.RuneCount(req.Query) > caps.QueryMaxRunes {
		plan.unsupportedReason = "query_length"
		return plan
	}

	// 1. Count 上限裁剪与离散档位向上取整
	reqCount := req.Count
	if caps.MaxCount > 0 && reqCount > caps.MaxCount {
		plan.warnings = append(plan.warnings, fmt.Sprintf("count %d capped to provider max %d", reqCount, caps.MaxCount))
		reqCount = caps.MaxCount
		plan.targetCount = reqCount
	}
	if len(caps.CountSteps) > 0 {
		stepped := caps.CountSteps[len(caps.CountSteps)-1]
		for _, step := range caps.CountSteps {
			if reqCount <= step {
				stepped = step
				break
			}
		}
		plan.adapted.Count = stepped
	} else {
		plan.adapted.Count = reqCount
	}

	// 2. Sites 白名单匹配与模拟（含领域作用域前置判定与仅主机名厂商路径前缀降级）
	if len(req.Filters.Sites) > 0 {
		sitesHandledByScope := false
		if len(caps.ScopeDomains) > 0 {
			if !anyFilterOverlapsScope(caps.ScopeDomains, req.Filters.Sites) {
				plan.unsupportedReason = reasonSiteScope
				return plan
			}
			if allFiltersCoverAllDomains(req.Filters.Sites, caps.ScopeDomains) {
				plan.adapted.Filters.Sites = nil
				sitesHandledByScope = true
			}
		}

		if !sitesHandledByScope {
			hasPath := caps.SiteHostOnly && urlnorm.HasPathDomainFilter(req.Filters.Sites)
			if hasPath && strict {
				plan.unsupportedReason = "sites"
				return plan
			}
			nativeSites := req.Filters.Sites
			if hasPath {
				nativeSites = urlnorm.ExtractFilterHosts(req.Filters.Sites)
			}
			switch caps.Sites {
			case core.SiteMulti:
				if caps.MaxSites > 0 && len(nativeSites) > caps.MaxSites {
					if strict {
						plan.unsupportedReason = "sites"
						return plan
					}
					plan.adapted.Filters.Sites = nil
					plan.emulateSites = slices.Clone(req.Filters.Sites)
					plan.degraded = append(plan.degraded, "sites:emulated")
				} else if hasPath {
					plan.adapted.Filters.Sites = nativeSites
					plan.emulateSites = slices.Clone(req.Filters.Sites)
					plan.degraded = append(plan.degraded, "sites:emulated")
				}
			case core.SiteSingle:
				if len(nativeSites) > 1 {
					if strict {
						plan.unsupportedReason = "sites"
						return plan
					}
					plan.adapted.Filters.Sites = nil
					plan.emulateSites = slices.Clone(req.Filters.Sites)
					plan.degraded = append(plan.degraded, "sites:emulated")
				} else if hasPath {
					plan.adapted.Filters.Sites = nativeSites
					plan.emulateSites = slices.Clone(req.Filters.Sites)
					plan.degraded = append(plan.degraded, "sites:emulated")
				}
			default:
				if strict {
					plan.unsupportedReason = "sites"
					return plan
				}
				plan.adapted.Filters.Sites = nil
				plan.emulateSites = slices.Clone(req.Filters.Sites)
				plan.degraded = append(plan.degraded, "sites:emulated")
			}
		}
	}

	// 3. ExcludeSites 排除站点拆分模拟（含领域作用域前置裁剪、单字段互斥厂商与路径排除防误杀）
	if len(req.Filters.ExcludeSites) > 0 {
		effectiveExcludes := req.Filters.ExcludeSites
		if len(caps.ScopeDomains) > 0 {
			if allDomainsCoveredByAnyFilter(caps.ScopeDomains, effectiveExcludes) {
				plan.unsupportedReason = reasonSiteScope
				return plan
			}
			effectiveExcludes = filterOverlappingDomainFilters(caps.ScopeDomains, effectiveExcludes)
		}
		plan.adapted.Filters.ExcludeSites = slices.Clone(effectiveExcludes)

		if len(effectiveExcludes) > 0 {
			switch {
			case caps.SiteExcludeMutuallyExclusive && len(plan.adapted.Filters.Sites) > 0:
				if strict {
					plan.unsupportedReason = "sites+exclude_sites"
					return plan
				}
				plan.adapted.Filters.ExcludeSites = nil
				plan.emulateExcludes = slices.Clone(effectiveExcludes)
				plan.degraded = append(plan.degraded, "exclude_sites:emulated")
			case caps.ExcludeSites <= 0:
				if strict {
					plan.unsupportedReason = "exclude_sites"
					return plan
				}
				plan.adapted.Filters.ExcludeSites = nil
				plan.emulateExcludes = slices.Clone(effectiveExcludes)
				plan.degraded = append(plan.degraded, "exclude_sites:emulated")
			case caps.SiteHostOnly && urlnorm.HasPathDomainFilter(effectiveExcludes):
				if strict {
					plan.unsupportedReason = "exclude_sites"
					return plan
				}
				hostOnly, pathOnly := urlnorm.SplitHostAndPathFilters(effectiveExcludes)
				if len(hostOnly) > caps.ExcludeSites {
					plan.adapted.Filters.ExcludeSites = slices.Clone(hostOnly[:caps.ExcludeSites])
					plan.emulateExcludes = slices.Concat(hostOnly[caps.ExcludeSites:], pathOnly)
				} else {
					plan.adapted.Filters.ExcludeSites = hostOnly
					plan.emulateExcludes = pathOnly
				}
				plan.degraded = append(plan.degraded, "exclude_sites:emulated")
			case len(effectiveExcludes) > caps.ExcludeSites:
				if strict {
					plan.unsupportedReason = "exclude_sites"
					return plan
				}
				plan.adapted.Filters.ExcludeSites = slices.Clone(effectiveExcludes[:caps.ExcludeSites])
				plan.emulateExcludes = slices.Clone(effectiveExcludes[caps.ExcludeSites:])
				plan.degraded = append(plan.degraded, "exclude_sites:emulated")
			}
		}
	}

	// 4. Freshness 时间窗匹配与后过滤模拟
	if req.Filters.Freshness != nil {
		switch caps.Freshness {
		case core.TimeBoth, core.TimeAbsolute:
		case core.TimeAfterOnly:
			if !req.Filters.Freshness.Before.IsZero() {
				if strict {
					plan.unsupportedReason = "freshness"
					return plan
				}
				if req.Filters.Freshness.After.IsZero() {
					plan.adapted.Filters.Freshness = nil
				} else {
					afterOnly := *req.Filters.Freshness
					afterOnly.Before = time.Time{}
					plan.adapted.Filters.Freshness = &afterOnly
				}
				plan.emulateFreshness = req.Filters.Freshness
				plan.degraded = append(plan.degraded, "freshness:emulated")
			}
		case core.TimeRelative:
			if !req.Filters.Freshness.IsRelative() {
				if strict {
					plan.unsupportedReason = "freshness"
					return plan
				}
				plan.adapted.Filters.Freshness = nil
				plan.emulateFreshness = req.Filters.Freshness
				plan.degraded = append(plan.degraded, "freshness:emulated")
			}
		default:
			if strict {
				plan.unsupportedReason = "freshness"
				return plan
			}
			plan.adapted.Filters.Freshness = nil
			plan.emulateFreshness = req.Filters.Freshness
			plan.degraded = append(plan.degraded, "freshness:emulated")
		}
	}

	// 5. Lang 语言过滤
	if len(req.Filters.Lang) > 0 && !caps.Lang {
		if strict {
			plan.unsupportedReason = "lang"
			return plan
		}
		plan.adapted.Filters.Lang = nil
		plan.degraded = append(plan.degraded, "lang:ignored")
	}

	// 6. Question 原始问题辅助（不支持时静默忽略）
	if req.Question != "" && !caps.Question {
		plan.adapted.Question = ""
	}

	// 7. Detail=content 支持检查
	if req.Detail == core.DetailContent && caps.Content == core.ContentNone {
		if strict {
			plan.unsupportedReason = "content"
			return plan
		}
		plan.degraded = append(plan.degraded, "content:unavailable")
	}

	return plan
}

// applyEmulation 对单个 Provider 返回的结果执行本地模拟后过滤与离散条数截断，绝不触发二次计费请求。
func applyEmulation(plan executionPlan, results []core.Result) ([]core.Result, []string) {
	if len(results) == 0 {
		return []core.Result{}, nil
	}
	hasFilter := len(plan.emulateSites) > 0 || len(plan.emulateExcludes) > 0 || plan.emulateFreshness != nil
	var notes []string
	undatedKept := 0

	filtered := make([]core.Result, 0, len(results))
	for _, r := range results {
		if len(plan.emulateSites) > 0 && !urlnorm.MatchesAnyDomain(r.URL, plan.emulateSites) {
			continue
		}
		if len(plan.emulateExcludes) > 0 && urlnorm.MatchesAnyDomain(r.URL, plan.emulateExcludes) {
			continue
		}
		if plan.emulateFreshness != nil {
			if r.Published == nil {
				undatedKept++
			} else if !withinFreshness(*r.Published, plan.emulateFreshness) {
				continue
			}
		}
		filtered = append(filtered, r)
	}

	if undatedKept > 0 {
		notes = append(notes, fmt.Sprintf("%d result(s) without published date kept during emulated freshness filtering", undatedKept))
	}
	if !hasFilter && len(filtered) <= plan.targetCount {
		return filtered, notes
	}
	if plan.targetCount > 0 && len(filtered) > plan.targetCount {
		filtered = filtered[:plan.targetCount]
	}
	for i := range filtered {
		filtered[i].Rank = i + 1
	}
	return filtered, notes
}

func withinFreshness(pub time.Time, f *core.Freshness) bool {
	if f == nil {
		return true
	}
	u := pub.UTC()
	if !f.After.IsZero() && u.Before(f.After.UTC()) {
		return false
	}
	if !f.Before.IsZero() && u.After(f.EndOfBeforeDay()) {
		return false
	}
	return true
}

func anyFilterOverlapsScope(scopeDomains, filters []string) bool {
	for _, f := range filters {
		for _, sd := range scopeDomains {
			if urlnorm.DomainFiltersOverlap(sd, f) {
				return true
			}
		}
	}
	return false
}

// allFiltersCoverAllDomains 判断用户请求的每个站点过滤项都覆盖全部作用域域名（如 -s zhihu.com 覆盖 ["zhihu.com"]）。
func allFiltersCoverAllDomains(filters, scopeDomains []string) bool {
	for _, f := range filters {
		for _, sd := range scopeDomains {
			if !urlnorm.FilterCoversDomain(f, sd) {
				return false
			}
		}
	}
	return len(filters) > 0 && len(scopeDomains) > 0
}

// allDomainsCoveredByAnyFilter 判断数据源的全部作用域域名均被用户排除列表中的整域项完全覆盖（如 zhihu 遇 -x zhihu.com）。
func allDomainsCoveredByAnyFilter(scopeDomains, excludes []string) bool {
	if len(scopeDomains) == 0 || len(excludes) == 0 {
		return false
	}
	for _, sd := range scopeDomains {
		covered := false
		for _, ex := range excludes {
			if urlnorm.FilterCoversDomain(ex, sd) {
				covered = true
				break
			}
		}
		if !covered {
			return false
		}
	}
	return true
}

func filterOverlappingDomainFilters(scopeDomains, filters []string) []string {
	var out []string
	for _, f := range filters {
		for _, sd := range scopeDomains {
			if urlnorm.DomainFiltersOverlap(sd, f) {
				out = append(out, f)
				break
			}
		}
	}
	return out
}
