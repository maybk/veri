package router

import (
	"cmp"
	"math"
	"slices"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

// searchCandidate 汇总路由选源所需的健康状态、作用域、能力适配度与凭据组归属。
type searchCandidate struct {
	searcher          core.Searcher
	name              string
	group             string
	healthDegraded    bool
	unsupported       bool
	degradedCount     int
	matchedSpecialist bool
	isVertical        bool
}

// providerGroup 返回检索源所属独立厂商/凭据组标识（共享凭据源归入同组，未登记源回退规范名）。
func providerGroup(name string) string {
	canon := canonicalProviderKey(name)
	if info, ok := spec.LookupProvider(canon); ok {
		if owner := info.CredentialOwner(); owner != "" {
			return owner
		}
	}
	return canon
}

func evaluateSearchCandidates(chain []core.Searcher, health *healthTracker, req *core.SearchRequest) []searchCandidate {
	var now time.Time
	if health != nil {
		now = health.nowFn()
	}
	hasSites := req != nil && len(req.Filters.Sites) > 0
	out := make([]searchCandidate, len(chain))
	for i, s := range chain {
		name := canonicalProviderKey(s.Name())
		caps := resolveCapabilities(s, req)
		cand := searchCandidate{
			searcher: s,
			name:     name,
			group:    providerGroup(name),
		}
		if health != nil {
			cand.healthDegraded = health.isDegraded(s.Name(), now)
		}
		if req != nil {
			plan := matchCapabilities(caps, req, req.StrictFilters)
			if plan.unsupportedReason != "" {
				cand.unsupported = true
			} else {
				cand.degradedCount = len(plan.degraded)
			}
		}
		if len(caps.ScopeDomains) > 0 {
			if hasSites && !cand.unsupported && allFiltersCoverAllDomains(req.Filters.Sites, caps.ScopeDomains) {
				cand.matchedSpecialist = true
			} else {
				cand.isVertical = true
			}
		}
		out[i] = cand
	}
	return out
}

// orderAutoSearchers 为 auto 自动回退执行稳定排序（健康 -> 作用域保底 -> 原生能力 -> 精确专长 -> 原序）。
// req 为 nil 时仅按作用域分离全网与垂直单站源，供静态诊断复用。
func orderAutoSearchers(chain []core.Searcher, health *healthTracker, req *core.SearchRequest) []core.Searcher {
	if len(chain) <= 1 {
		return chain
	}
	cands := evaluateSearchCandidates(chain, health, req)
	slices.SortStableFunc(cands, func(a, b searchCandidate) int {
		return cmp.Or(
			compareBoolFalseFirst(a.healthDegraded, b.healthDegraded),
			compareBoolFalseFirst(a.isVertical, b.isVertical),
			cmp.Compare(candidateDegradedRank(a), candidateDegradedRank(b)),
			compareBoolFalseFirst(!a.matchedSpecialist, !b.matchedSpecialist),
		)
	})
	return candidatesToSearchers(cands)
}

// selectAutoFusionPool 为自动推导的 --fusion 池选择独立异构检索源：
// 兼容筛选 -> 健康分层 -> 非中文垂直源抑制 -> 原生能力与专长排序 -> 独立凭据组选源（含全网保底与单厂商互补）。
func selectAutoFusionPool(chain []core.Searcher, health *healthTracker, useGlobalPool bool, req *core.SearchRequest, limit int) []core.Searcher {
	if len(chain) == 0 || limit <= 0 {
		return nil
	}
	cands := evaluateSearchCandidates(chain, health, req)
	if req != nil {
		compatible := filterCandidates(cands, func(c searchCandidate) bool { return !c.unsupported })
		if len(compatible) == 0 {
			return chain[:min(len(chain), limit)]
		}
		cands = compatible
	}

	// 存在健康源时仅在健康池内选源，避免在冷却期内空打失效源，也防止不健康全网源压掉健康垂直源。
	active := preferCandidates(cands, func(c searchCandidate) bool { return !c.healthDegraded })
	if useGlobalPool {
		active = preferCandidates(active, func(c searchCandidate) bool { return !c.isVertical })
	}

	slices.SortStableFunc(active, func(a, b searchCandidate) int {
		return cmp.Or(
			cmp.Compare(candidateDegradedRank(a), candidateDegradedRank(b)),
			compareBoolFalseFirst(!a.matchedSpecialist, !b.matchedSpecialist),
		)
	})

	// Pass 1：按独立凭据组每组最多取 1 席；主链其他全网组不足 2 家且同组有同等能力全网兄弟时让位全网兄弟。
	usedGroups := make(map[string]struct{}, limit)
	selected := make([]searchCandidate, 0, min(limit, len(active)))
	for _, c := range active {
		if len(selected) >= limit {
			break
		}
		if _, dup := usedGroups[c.group]; dup {
			continue
		}
		if !useGlobalPool && c.isVertical && shouldDeferVerticalForGeneral(c, active) {
			continue
		}
		selected = append(selected, c)
		usedGroups[c.group] = struct{}{}
	}

	// Pass 2：仅剩单一厂商时（如仅配知乎单 Key），在不增加降级的前提下补入 1 个同组互补源。
	if len(selected) == 1 && limit > 1 {
		capCount := min(limit, 2)
		maxDegraded := selected[0].degradedCount
		firstName := selected[0].name
		for _, c := range active {
			if len(selected) >= capCount {
				break
			}
			if c.name == firstName || c.degradedCount > maxDegraded {
				continue
			}
			selected = append(selected, c)
		}
	}

	return candidatesToSearchers(selected)
}

func shouldDeferVerticalForGeneral(vert searchCandidate, active []searchCandidate) bool {
	hasGeneralSibling := false
	otherGeneralGroups := make(map[string]struct{}, len(active))
	for _, cand := range active {
		if cand.isVertical || cand.degradedCount > vert.degradedCount {
			continue
		}
		if cand.group == vert.group {
			hasGeneralSibling = true
			continue
		}
		otherGeneralGroups[cand.group] = struct{}{}
	}
	return hasGeneralSibling && len(otherGeneralGroups) < defaultFusionMinGeneralGroups
}

func filterCandidates(cands []searchCandidate, keep func(searchCandidate) bool) []searchCandidate {
	out := make([]searchCandidate, 0, len(cands))
	for _, c := range cands {
		if keep(c) {
			out = append(out, c)
		}
	}
	return out
}

func preferCandidates(cands []searchCandidate, keep func(searchCandidate) bool) []searchCandidate {
	if filtered := filterCandidates(cands, keep); len(filtered) > 0 {
		return filtered
	}
	return cands
}

func candidateDegradedRank(c searchCandidate) int {
	if c.unsupported {
		return math.MaxInt
	}
	return c.degradedCount
}

func compareBoolFalseFirst(a, b bool) int {
	switch {
	case !a && b:
		return -1
	case a && !b:
		return 1
	default:
		return 0
	}
}

func candidatesToSearchers(cands []searchCandidate) []core.Searcher {
	out := make([]core.Searcher, len(cands))
	for i, c := range cands {
		out[i] = c.searcher
	}
	return out
}
