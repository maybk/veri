package router

import (
	"fmt"
	"slices"
	"time"

	"veri/internal/core"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

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

	// 2. Sites 白名单匹配与模拟（含仅支持主机名的厂商路径前缀降级）
	if len(req.Filters.Sites) > 0 {
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

	// 3. ExcludeSites 排除站点拆分模拟（含单字段互斥厂商与仅主机名厂商路径排除防误杀）
	if len(req.Filters.ExcludeSites) > 0 {
		switch {
		case caps.SiteExcludeMutuallyExclusive && len(plan.adapted.Filters.Sites) > 0:
			if strict {
				plan.unsupportedReason = "sites+exclude_sites"
				return plan
			}
			plan.adapted.Filters.ExcludeSites = nil
			plan.emulateExcludes = slices.Clone(req.Filters.ExcludeSites)
			plan.degraded = append(plan.degraded, "exclude_sites:emulated")
		case caps.ExcludeSites <= 0:
			if strict {
				plan.unsupportedReason = "exclude_sites"
				return plan
			}
			plan.adapted.Filters.ExcludeSites = nil
			plan.emulateExcludes = slices.Clone(req.Filters.ExcludeSites)
			plan.degraded = append(plan.degraded, "exclude_sites:emulated")
		case caps.SiteHostOnly && urlnorm.HasPathDomainFilter(req.Filters.ExcludeSites):
			if strict {
				plan.unsupportedReason = "exclude_sites"
				return plan
			}
			hostOnly, pathOnly := urlnorm.SplitHostAndPathFilters(req.Filters.ExcludeSites)
			if len(hostOnly) > caps.ExcludeSites {
				plan.adapted.Filters.ExcludeSites = slices.Clone(hostOnly[:caps.ExcludeSites])
				plan.emulateExcludes = slices.Concat(hostOnly[caps.ExcludeSites:], pathOnly)
			} else {
				plan.adapted.Filters.ExcludeSites = hostOnly
				plan.emulateExcludes = pathOnly
			}
			plan.degraded = append(plan.degraded, "exclude_sites:emulated")
		case len(req.Filters.ExcludeSites) > caps.ExcludeSites:
			if strict {
				plan.unsupportedReason = "exclude_sites"
				return plan
			}
			plan.adapted.Filters.ExcludeSites = slices.Clone(req.Filters.ExcludeSites[:caps.ExcludeSites])
			plan.emulateExcludes = slices.Clone(req.Filters.ExcludeSites[caps.ExcludeSites:])
			plan.degraded = append(plan.degraded, "exclude_sites:emulated")
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
