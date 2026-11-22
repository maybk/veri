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
	members       []clusterMember
	anchorCK      resultContentKey
	merged        core.Result
}

func fuseRRF(branches []branchResult, k int, weightFn func(string) float64, limit int) ([]core.Result, int) {
	byURLKey := make(map[string]*fusedCluster)
	var clusters []*fusedCluster
	contentDeduped := 0

	// 阶段 1：精确 URL + 内容指纹双阶段聚簇与同分支单票 RRF 计分
	for _, br := range branches {
		w := weightFn(br.name)
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
				rank:     rank,
			}

			if cl == nil {
				cl = &fusedCluster{
					score:         rrfTerm,
					firstChainIdx: br.chainIdx,
					providers:     []string{br.name},
					seenBranches:  map[int]struct{}{br.chainIdx: {}},
					members:       []clusterMember{member},
					anchorCK:      ck,
				}
				if uKey != "" {
					byURLKey[uKey] = cl
				}
				clusters = append(clusters, cl)
				continue
			}

			// 同一检索分支在同一内容聚类中最多贡献一次 RRF 计分，防止同分支多条转载放大权重并污染 providers[]。
			if _, scored := cl.seenBranches[br.chainIdx]; !scored {
				cl.seenBranches[br.chainIdx] = struct{}{}
				cl.score += rrfTerm
				cl.providers = append(cl.providers, br.name)
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
			cmp.Compare(len(b.merged.Providers), len(a.merged.Providers)),
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
