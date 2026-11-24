package router

import (
	"cmp"
	"math"
	"slices"

	"veri/internal/core"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

type fusedCluster struct {
	score         float64
	firstChainIdx int
	providers     []string
	seenBranches  map[int]struct{}
	groupScores   map[string]float64
	members       []clusterMember
	anchorCK      resultContentKey
	merged        core.Result
}

func fuseRRF(branches []branchResult, k int, weightFn func(string) float64, limit int) ([]core.Result, int) {
	byURLKey := make(map[string]*fusedCluster)
	var clusters []*fusedCluster
	contentDeduped := 0

	// 阶段 1：精确 URL + 内容指纹双阶段聚簇与同凭据组最大池化 RRF 计分
	for _, br := range branches {
		w := weightFn(br.name)
		group := providerGroup(br.name)
		for idx, item := range br.results {
			rank := idx + 1
			rrfTerm := w / float64(k+rank)
			uKey := urlnorm.Key(item.URL)
			ck := buildResultContentKey(item)

			var (
				cl       *fusedCluster
				knownURL bool
			)
			if uKey != "" {
				cl, knownURL = byURLKey[uKey]
			}
			if !knownURL && ck.bodyFP.Matchable() {
				for _, existing := range clusters {
					if textnorm.FingerprintsNearDuplicate(existing.anchorCK.titleTokens, existing.anchorCK.bodyFP, ck.titleTokens, ck.bodyFP) {
						cl = existing
						if uKey != "" {
							byURLKey[uKey] = cl
						}
						contentDeduped++
						break
					}
				}
			}

			member := clusterMember{
				result:   item.Clone(),
				urlKey:   uKey,
				branchID: br.chainIdx,
				group:    group,
				rank:     rank,
			}

			if cl == nil {
				cl = &fusedCluster{
					score:         rrfTerm,
					firstChainIdx: br.chainIdx,
					providers:     []string{br.name},
					seenBranches:  map[int]struct{}{br.chainIdx: {}},
					groupScores:   map[string]float64{group: rrfTerm},
					members:       []clusterMember{member},
					anchorCK:      ck,
				}
				if uKey != "" {
					byURLKey[uKey] = cl
				}
				clusters = append(clusters, cl)
				continue
			}

			// 同一检索分支最多记录一次来源；同一凭据组（如 zhihu 与 zhihu_web）取组内最大 RRF 贡献，防止高相关同源双票放大。
			if _, scored := cl.seenBranches[br.chainIdx]; !scored {
				cl.seenBranches[br.chainIdx] = struct{}{}
				cl.providers = append(cl.providers, br.name)
				if prev, ok := cl.groupScores[group]; !ok {
					cl.groupScores[group] = rrfTerm
					cl.score += rrfTerm
				} else if rrfTerm > prev {
					cl.score += rrfTerm - prev
					cl.groupScores[group] = rrfTerm
				}
			}
			cl.members = append(cl.members, member)
			upgradeAnchorKey(&cl.anchorCK, ck)
		}
	}

	// 阶段 2：基于整簇成员信息统一选出规范代表页并合并富字段（消除分支顺序敏感性）
	for _, cl := range clusters {
		cl.merged = resolveClusterResult(cl.members, cl.providers, false)
	}

	slices.SortStableFunc(clusters, func(a, b *fusedCluster) int {
		if math.Abs(a.score-b.score) > rrfScoreEpsilon {
			return cmp.Compare(b.score, a.score)
		}
		relA, relB := -1.0, -1.0
		if a.merged.Relevance != nil {
			relA = *a.merged.Relevance
		}
		if b.merged.Relevance != nil {
			relB = *b.merged.Relevance
		}
		return cmp.Or(
			cmp.Compare(len(b.groupScores), len(a.groupScores)),
			cmp.Compare(relB, relA),
			cmp.Compare(a.firstChainIdx, b.firstChainIdx),
		)
	})

	if limit > 0 && len(clusters) > limit {
		clusters = clusters[:limit]
	}
	out := make([]core.Result, len(clusters))
	for i, cl := range clusters {
		res := cl.merged
		res.Rank = i + 1
		out[i] = res
	}
	return out, contentDeduped
}
