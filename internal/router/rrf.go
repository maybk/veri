package router

import (
	"cmp"
	"math"
	"slices"
	"strings"

	"veri/internal/core"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

type fusedCandidate struct {
	key           string
	score         float64
	bestRank      int
	bestChainIdx  int
	firstChainIdx int
	merged        core.Result
	providerOrder []int
}

func fuseRRF(branches []branchResult, k int, weightFn func(string) float64, limit int) []core.Result {
	byKey := make(map[string]*fusedCandidate)
	var keys []string

	for _, br := range branches {
		w := weightFn(br.name)
		for idx, item := range br.results {
			rank := idx + 1
			rrfTerm := w / float64(k+rank)
			uKey := urlnorm.Key(item.URL)

			cand, exists := byKey[uKey]
			if !exists {
				cp := item.Clone()
				cp.Providers = []string{br.name}
				cand = &fusedCandidate{
					key:           uKey,
					score:         rrfTerm,
					bestRank:      rank,
					bestChainIdx:  br.chainIdx,
					firstChainIdx: br.chainIdx,
					merged:        cp,
					providerOrder: []int{br.chainIdx},
				}
				byKey[uKey] = cand
				keys = append(keys, uKey)
				continue
			}

			cand.score += rrfTerm
			cand.merged.Providers = append(cand.merged.Providers, br.name)
			cand.providerOrder = append(cand.providerOrder, br.chainIdx)

			betterRank := rank < cand.bestRank || (rank == cand.bestRank && br.chainIdx < cand.bestChainIdx)
			if (betterRank && item.Title != "") || cand.merged.Title == "" {
				if item.Title != "" {
					cand.merged.Title = item.Title
				}
			}
			if (betterRank && item.Snippet != "") || cand.merged.Snippet == "" {
				if item.Snippet != "" {
					cand.merged.Snippet = item.Snippet
				}
			}
			if betterRank {
				cand.bestRank = rank
				cand.bestChainIdx = br.chainIdx
			}
			if textnorm.RuneCount(item.Summary) > textnorm.RuneCount(cand.merged.Summary) {
				cand.merged.Summary = item.Summary
			}
			if textnorm.RuneCount(item.Content) > textnorm.RuneCount(cand.merged.Content) {
				cand.merged.Content = item.Content
			}
			if item.Site != "" && cand.merged.Site == "" {
				cand.merged.Site = item.Site
			}
			if len(item.Authors) > 0 {
				cand.merged.Authors = mergeAuthors(cand.merged.Authors, item.Authors)
			}
			if item.Icon != "" && cand.merged.Icon == "" {
				cand.merged.Icon = item.Icon
			}
			if item.Published != nil {
				if cand.merged.Published == nil || item.Published.Before(*cand.merged.Published) {
					cand.merged.Published = new(item.Published.UTC())
				}
			}
			if item.Relevance != nil && (cand.merged.Relevance == nil || *item.Relevance > *cand.merged.Relevance) {
				cand.merged.Relevance = new(*item.Relevance)
			}
			if item.Authority != nil && (cand.merged.Authority == nil || *item.Authority > *cand.merged.Authority) {
				cand.merged.Authority = new(*item.Authority)
			}
			if len(item.Extra) > 0 {
				if cand.merged.Extra == nil {
					cand.merged.Extra = make(map[string]any, len(item.Extra))
				}
				for ek, ev := range item.Extra {
					if _, exists := cand.merged.Extra[ek]; !exists {
						cand.merged.Extra[ek] = ev
					}
				}
			}
		}
	}

	cands := make([]*fusedCandidate, 0, len(keys))
	for _, uk := range keys {
		cands = append(cands, byKey[uk])
	}

	slices.SortStableFunc(cands, func(a, b *fusedCandidate) int {
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

	if limit > 0 && len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]core.Result, len(cands))
	for i, c := range cands {
		res := c.merged
		res.Rank = i + 1
		out[i] = res
	}
	return out
}

func mergeAuthors(existing, incoming []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(incoming))
	out := make([]string, 0, len(existing)+len(incoming))
	for _, a := range existing {
		k := strings.ToLower(strings.TrimSpace(a))
		if k == "" {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			out = append(out, a)
		}
	}
	for _, a := range incoming {
		k := strings.ToLower(strings.TrimSpace(a))
		if k == "" {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			out = append(out, a)
		}
	}
	return out
}
