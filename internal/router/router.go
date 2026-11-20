package router

import (
	"slices"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

// Options 配置路由层的检索链、融合参数、超时与字符预算阈值。
type Options struct {
	SearchProviders   []string
	SearchProvidersEN []string
	FusionProviders   []string
	DefaultStrategy   core.Strategy
	DefaultCount      int
	DefaultDetail     core.Detail
	SearchTimeout     time.Duration
	FallbackOnEmpty   bool
	StrictFilters     bool
	FusionK           int
	Weights           map[string]float64
	SummaryMaxRunes   int
	ContentMaxRunes   int
	SearchMaxChars    int

	FetchStrategy        core.FetchStrategy
	FetchProviders       []string
	FetchProvidersGlobal []string
	FetchTimeout         time.Duration
	FetchMaxChars        int
}

// Router 维护已启用的 Searcher 与 Fetcher 注册表并执行统一路由决策。
type Router struct {
	opts        Options
	searchers   map[string]core.Searcher
	fetchers    map[string]core.Fetcher
	specialists []core.SpecialistFetcher
	localFetch  core.Fetcher
	health      *healthTracker
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
	if opts.SummaryMaxRunes <= 0 {
		opts.SummaryMaxRunes = spec.DefaultSummaryMaxRunes
	}
	if opts.FetchStrategy == "" {
		opts.FetchStrategy = core.DefaultFetchStrategy
	}
	if opts.FetchTimeout <= 0 {
		opts.FetchTimeout = spec.DefaultFetchTimeout
	}
	if len(opts.SearchProviders) == 0 {
		opts.SearchProviders = spec.DefaultSearchProviders()
	}
	if len(opts.FetchProviders) == 0 {
		opts.FetchProviders = spec.DefaultFetchProviders()
	}

	r := &Router{
		opts:      opts,
		searchers: make(map[string]core.Searcher),
		fetchers:  make(map[string]core.Fetcher),
		health:    newHealthTracker(),
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
		for _, name := range r.opts.FetchProviders {
			if canonicalProviderKey(name) != spec.ProviderLocal {
				add(name)
			}
		}
		for _, name := range r.opts.FetchProvidersGlobal {
			if canonicalProviderKey(name) != spec.ProviderLocal {
				add(name)
			}
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

// ActiveSearchChainEN 返回当前已启用的英文优先检索链名称列表（未配置时返回 nil）。
func (r *Router) ActiveSearchChainEN() []string {
	if len(r.opts.SearchProvidersEN) == 0 {
		return nil
	}
	seen := make(map[string]struct{})
	var out []string
	for _, raw := range r.opts.SearchProvidersEN {
		name := canonicalProviderKey(raw)
		if _, ok := r.searchers[name]; ok {
			if _, dup := seen[name]; !dup {
				seen[name] = struct{}{}
				out = append(out, name)
			}
		}
	}
	return out
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
