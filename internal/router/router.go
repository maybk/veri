package router

import (
	"log/slog"
	"slices"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

// Options 配置路由层的检索链、融合参数、超时与字符预算阈值。
type Options struct {
	SearchProviders               []string
	SearchProvidersGlobal         []string
	SearchProvidersExplicit       bool
	FusionProviders               []string
	FusionProvidersGlobal         []string
	FusionProvidersGlobalExplicit bool
	DefaultStrategy               core.Strategy
	DefaultCount                  int
	DefaultDetail                 core.Detail
	SearchTimeout                 time.Duration
	FallbackOnEmpty               bool
	StrictFilters                 bool
	FusionK                       int
	Weights                       map[string]float64
	SummaryMaxChars               int
	ContentMaxChars               int
	SearchMaxChars                int

	FetchStrategy          core.FetchStrategy
	FetchProviders         []string
	FetchProvidersGlobal   []string
	FetchProvidersExplicit bool
	FetchTimeout           time.Duration
	FetchMaxChars          int
	Logger                 *slog.Logger
}

// Router 维护已启用的 Searcher 与 Fetcher 注册表并执行统一路由决策。
type Router struct {
	opts           Options
	searchers      map[string]core.Searcher
	fetchers       map[string]core.Fetcher
	specialists    []core.SpecialistFetcher
	localFetch     core.Fetcher
	health         *healthTracker
	stragglerGrace time.Duration
	logger         *slog.Logger
}

// New 根据已启用的 Provider 实例列表构造 Router。
func New(opts Options, providers []core.Provider) *Router {
	if opts.DefaultCount <= 0 {
		opts.DefaultCount = spec.DefaultSearchCount
	}
	if opts.DefaultDetail == "" {
		opts.DefaultDetail = core.DetailSummary
	}
	if opts.DefaultStrategy == "" {
		opts.DefaultStrategy = core.StrategyAuto
	}
	if opts.SearchTimeout <= 0 {
		opts.SearchTimeout = spec.DefaultSearchTimeout
	}
	if opts.FusionK <= 0 {
		opts.FusionK = spec.DefaultFusionK
	}
	if opts.SummaryMaxChars <= 0 {
		opts.SummaryMaxChars = spec.DefaultSummaryMaxChars
	}
	if opts.FetchStrategy == "" {
		opts.FetchStrategy = core.DefaultFetchStrategy
	}
	if opts.FetchTimeout <= 0 {
		opts.FetchTimeout = spec.DefaultFetchTimeout
	}
	if len(opts.SearchProviders) == 0 {
		opts.SearchProviders = spec.DefaultSearchProviders()
		if len(opts.SearchProvidersGlobal) == 0 && !opts.SearchProvidersExplicit {
			opts.SearchProvidersGlobal = spec.DefaultSearchProvidersGlobal()
		}
	}
	if len(opts.FetchProviders) == 0 {
		opts.FetchProviders = spec.DefaultFetchProviders()
		if len(opts.FetchProvidersGlobal) == 0 && !opts.FetchProvidersExplicit {
			opts.FetchProvidersGlobal = spec.DefaultFetchProvidersGlobal()
		}
	}

	r := &Router{
		opts:           opts,
		searchers:      make(map[string]core.Searcher),
		fetchers:       make(map[string]core.Fetcher),
		health:         newHealthTracker(),
		stragglerGrace: defaultFusionStragglerGrace,
		logger:         opts.Logger,
	}

	for _, p := range providers {
		if p == nil {
			continue
		}
		key := canonicalProviderKey(p.Name())
		if s, ok := p.(core.Searcher); ok {
			r.searchers[key] = s
		}
		if f, ok := p.(core.Fetcher); ok {
			r.fetchers[key] = f
			if sp, isSpec := f.(core.SpecialistFetcher); isSpec {
				r.specialists = append(r.specialists, sp)
			} else if key == spec.ProviderLocal {
				r.localFetch = f
			}
		}
	}
	return r
}

// EnabledSearchers 按配置优先级顺序返回当前已启用的 Searcher 名称列表。
func (r *Router) EnabledSearchers() []string {
	seen := make(map[string]struct{}, len(r.searchers))
	var out []string
	for _, raw := range r.opts.SearchProviders {
		name := canonicalProviderKey(raw)
		if _, ok := r.searchers[name]; ok {
			if _, dup := seen[name]; !dup {
				seen[name] = struct{}{}
				out = append(out, name)
			}
		}
	}
	var extras []string
	for name := range r.searchers {
		if _, dup := seen[name]; !dup {
			extras = append(extras, name)
		}
	}
	slices.Sort(extras)
	return append(out, extras...)
}

// EnabledFetchers 按当前生效 FetchStrategy 的优先级顺序返回可用 Fetcher 名称列表。
func (r *Router) EnabledFetchers() []string {
	return r.resolveFetchChainOrder(r.opts.FetchProviders, r.opts.FetchProvidersGlobal, true)
}

// DefaultSearchStrategy 返回当前生效的默认检索路由策略。
func (r *Router) DefaultSearchStrategy() core.Strategy { return r.opts.DefaultStrategy }

// FetchStrategy 返回当前生效的默认网页提取路由策略。
func (r *Router) FetchStrategy() core.FetchStrategy { return r.opts.FetchStrategy }

// ActiveFusionChain 返回中文或统一 --fusion 模式下默认并发调用的已启用检索源列表。
func (r *Router) ActiveFusionChain() []string {
	return searcherNames(r.resolveFusionChain(false, nil))
}

// ActiveFusionChainGlobal 返回非中文查询在 --fusion 模式下默认并发调用的检索源列表；
// 当未启用独立非中文融合池或有效融合池与主融合池相同时返回 nil。
func (r *Router) ActiveFusionChainGlobal() []string {
	primary := r.ActiveFusionChain()
	global := searcherNames(r.resolveFusionChain(true, nil))
	if len(global) == 0 || slices.Equal(global, primary) {
		return nil
	}
	return global
}

// ActiveFetchChainGlobal 返回非中文站点实际执行的提取链顺序；当未启用独立全球分链或顺序与主链相同时返回 nil。
func (r *Router) ActiveFetchChainGlobal() []string {
	if r.opts.FetchStrategy == core.FetchLocalOnly || len(r.opts.FetchProvidersGlobal) == 0 {
		return nil
	}
	primary := r.resolveFetchChainOrder(r.opts.FetchProviders, nil, false)
	global := r.resolveFetchChainOrder(r.opts.FetchProvidersGlobal, nil, false)
	if len(global) == 0 || slices.Equal(global, primary) {
		return nil
	}
	return global
}

func (r *Router) resolveFetchChainOrder(primary, secondary []string, includeExtras bool) []string {
	seen := make(map[string]struct{}, len(r.fetchers))
	var out []string
	add := func(raw string) {
		name := canonicalProviderKey(raw)
		if _, ok := r.fetchers[name]; ok {
			if _, dup := seen[name]; !dup {
				seen[name] = struct{}{}
				out = append(out, name)
			}
		}
	}
	addSpecialists := func() {
		for _, sp := range r.specialists {
			add(sp.Name())
		}
	}
	addAPIChain := func() {
		for _, name := range primary {
			if canonicalProviderKey(name) != spec.ProviderLocal {
				add(name)
			}
		}
		for _, name := range secondary {
			if canonicalProviderKey(name) != spec.ProviderLocal {
				add(name)
			}
		}
		if !includeExtras {
			return
		}
		var extras []string
		for name, f := range r.fetchers {
			if name == spec.ProviderLocal {
				continue
			}
			if _, isSpec := f.(core.SpecialistFetcher); !isSpec {
				if _, dup := seen[name]; !dup {
					extras = append(extras, name)
				}
			}
		}
		slices.Sort(extras)
		for _, name := range extras {
			add(name)
		}
	}

	switch r.opts.FetchStrategy {
	case core.FetchSmartLocal:
		addSpecialists()
		add(spec.ProviderLocal)
		addAPIChain()
	case core.FetchLocalOnly:
		addSpecialists()
		add(spec.ProviderLocal)
	case core.FetchAPIOnly:
		addSpecialists()
		addAPIChain()
	default:
		addSpecialists()
		addAPIChain()
		add(spec.ProviderLocal)
	}
	return out
}

// ActiveSearchChainGlobal 返回非中文查询实际执行的检索链顺序；当未启用独立非中文分链或顺序与主链相同时返回 nil。
func (r *Router) ActiveSearchChainGlobal() []string {
	if len(r.opts.SearchProvidersGlobal) == 0 {
		return nil
	}
	primary := r.filterEnabledSearchers(r.opts.SearchProviders)
	global := r.filterEnabledSearchers(r.opts.SearchProvidersGlobal)
	if len(global) == 0 || slices.Equal(global, primary) {
		return nil
	}
	return global
}

// resolveFusionPoolCandidates 返回融合模式候选检索源及是否属于自动推导池（自动池支持健康重排与能力递补，显式池保持精确配置）。
func (r *Router) resolveFusionPoolCandidates(nonChinese bool) (chain []core.Searcher, autoDerived bool) {
	if nonChinese {
		if len(r.opts.FusionProvidersGlobal) > 0 {
			if global, _ := r.resolveSearchers(r.opts.FusionProvidersGlobal, false); len(global) > 0 {
				return global, false
			}
		} else if !r.opts.FusionProvidersGlobalExplicit && len(r.opts.FusionProviders) == 0 && len(r.opts.SearchProvidersGlobal) > 0 {
			if global, _ := r.resolveSearchers(r.opts.SearchProvidersGlobal, false); len(global) > 0 {
				return global, true
			}
		}
	}
	if len(r.opts.FusionProviders) > 0 {
		primary, _ := r.resolveSearchers(r.opts.FusionProviders, false)
		return primary, false
	}
	primary, _ := r.resolveSearchers(r.opts.SearchProviders, false)
	return primary, true
}

// resolveFusionChain 解析生效的融合检索链；req 为 nil 时返回静态诊断视图，非 nil 时对自动推导池执行健康排序与能力递补后再截断默认并发上限。
func (r *Router) resolveFusionChain(nonChinese bool, req *core.SearchRequest) []core.Searcher {
	candidates, autoDerived := r.resolveFusionPoolCandidates(nonChinese)
	if !autoDerived || len(candidates) == 0 {
		return candidates
	}
	if req != nil {
		candidates = orderByHealth(r.health, candidates)
		compatible := make([]core.Searcher, 0, len(candidates))
		for _, s := range candidates {
			if matchCapabilities(resolveCapabilities(s, req), req, req.StrictFilters).unsupportedReason == "" {
				compatible = append(compatible, s)
			}
		}
		if len(compatible) > 0 {
			candidates = compatible
		}
	}
	if len(candidates) > spec.DefaultFusionAutoLimit {
		candidates = candidates[:spec.DefaultFusionAutoLimit]
	}
	return candidates
}

func searcherNames(chain []core.Searcher) []string {
	if len(chain) == 0 {
		return nil
	}
	out := make([]string, len(chain))
	for i, s := range chain {
		out[i] = canonicalProviderKey(s.Name())
	}
	return out
}

func (r *Router) filterEnabledSearchers(candidates []string) []string {
	chain, _ := r.resolveSearchers(candidates, false)
	return searcherNames(chain)
}

func (r *Router) weightFor(name string) float64 {
	if r.opts.Weights != nil {
		if w, ok := r.opts.Weights[name]; ok && w > 0 {
			return w
		}
	}
	return 1.0
}

func canonicalProviderKey(raw string) string {
	if norm, ok := spec.NormalizeProviderName(raw); ok {
		return norm
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func cleanProviderKeys(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, raw := range items {
		k := canonicalProviderKey(raw)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; !dup {
			seen[k] = struct{}{}
			out = append(out, k)
		}
	}
	return out
}

func providerNames[T core.Provider](list []T) []string {
	out := make([]string, len(list))
	for i, p := range list {
		out[i] = p.Name()
	}
	return out
}
