package router

import (
	"net/url"
	"slices"

	"veri/internal/core"
	"veri/internal/spec"
	"veri/internal/urlnorm"
)

func (r *Router) buildFetchSequence(u *url.URL, strategy core.FetchStrategy, explicit []string) ([]core.Fetcher, []string) {
	if len(explicit) > 0 {
		var list []core.Fetcher
		for _, rawName := range explicit {
			if f, ok := r.fetchers[canonicalProviderKey(rawName)]; ok {
				list = append(list, f)
			}
		}
		return list, nil
	}

	var matchedSpecialists []core.Fetcher
	for _, sp := range r.specialists {
		if sp.Match(u) {
			matchedSpecialists = append(matchedSpecialists, sp)
		}
	}

	apiNames := r.opts.FetchProviders
	customGlobal := len(r.opts.FetchProvidersGlobal) > 0
	if customGlobal && !urlnorm.IsLikelyChineseHost(u) {
		apiNames = r.opts.FetchProvidersGlobal
	}
	var apiChain []core.Fetcher
	for _, rawName := range apiNames {
		name := canonicalProviderKey(rawName)
		if f, ok := r.fetchers[name]; ok && name != spec.ProviderLocal && !isSpecialistFetcher(f) {
			apiChain = append(apiChain, f)
		}
	}

	isPDFTarget := urlnorm.LooksLikePDFURL(u)
	if isPDFTarget {
		apiChain = prioritizeFetchersWhere(apiChain, isPDFFetcher)
	} else if !customGlobal && slices.Equal(r.opts.FetchProviders, spec.DefaultFetchProviders()) && !urlnorm.IsLikelyChineseHost(u) {
		apiChain = prioritizeFetchersByOrder(apiChain, spec.DefaultFetchProvidersGlobal())
	}
	apiChain = orderByHealth(r.health, apiChain)

	var seq []core.Fetcher
	var initNotes []string
	switch strategy {
	case core.FetchAPIFirst:
		seq = append(seq, matchedSpecialists...)
		seq = append(seq, apiChain...)
		if r.localFetch != nil {
			seq = append(seq, r.localFetch)
		}
	case core.FetchSmartLocal:
		seq = append(seq, matchedSpecialists...)
		if isPDFTarget && slices.ContainsFunc(apiChain, isPDFFetcher) {
			if len(matchedSpecialists) == 0 {
				initNotes = append(initNotes, "target URL points to PDF; routed directly to PDF-capable API extractor")
			}
		} else if r.localFetch != nil {
			seq = append(seq, r.localFetch)
		}
		seq = append(seq, apiChain...)
	case core.FetchLocalOnly:
		seq = append(seq, matchedSpecialists...)
		if r.localFetch != nil {
			seq = append(seq, r.localFetch)
		}
	case core.FetchAPIOnly:
		seq = append(seq, matchedSpecialists...)
		seq = append(seq, apiChain...)
	}
	return seq, initNotes
}

func isPDFFetcher(f core.Fetcher) bool           { return core.ProfileOfFetcher(f).PDF }
func isDynamicRenderFetcher(f core.Fetcher) bool { return core.ProfileOfFetcher(f).DynamicRender }

func isSpecialistFetcher(f core.Fetcher) bool {
	_, ok := f.(core.SpecialistFetcher)
	return ok
}

func prioritizeFetchersWhere(chain []core.Fetcher, match func(core.Fetcher) bool) []core.Fetcher {
	if len(chain) <= 1 {
		return chain
	}
	out := slices.Clone(chain)
	reorderFetchersWhereInPlace(out, match)
	return out
}

func reorderFetchersWhereInPlace(slice []core.Fetcher, match func(core.Fetcher) bool) {
	if len(slice) <= 1 {
		return
	}
	slices.SortStableFunc(slice, func(a, b core.Fetcher) int {
		ma := match(a)
		mb := match(b)
		switch {
		case ma && !mb:
			return -1
		case !ma && mb:
			return 1
		default:
			return 0
		}
	})
}

func promoteFetcherByName(slice []core.Fetcher, name string) bool {
	for i, f := range slice {
		if f.Name() == name {
			if i > 0 {
				copy(slice[1:i+1], slice[0:i])
				slice[0] = f
			}
			return true
		}
	}
	return false
}

func prioritizeFetchersByOrder(chain []core.Fetcher, preferred []string) []core.Fetcher {
	if len(chain) <= 1 || len(preferred) == 0 {
		return chain
	}
	rank := make(map[string]int, len(preferred))
	for i, name := range preferred {
		rank[name] = i
	}
	out := slices.Clone(chain)
	slices.SortStableFunc(out, func(a, b core.Fetcher) int {
		ra, oka := rank[a.Name()]
		rb, okb := rank[b.Name()]
		switch {
		case oka && okb:
			return ra - rb
		case oka:
			return -1
		case okb:
			return 1
		default:
			return 0
		}
	})
	return out
}
