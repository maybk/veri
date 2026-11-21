package app

import (
	"context"
	"time"

	"veri/internal/core"
	"veri/internal/router"
	"veri/internal/spec"
)

func (a *App) ensureMCP() {
	a.mcpOnce.Do(func() {
		cfg := a.loaded.Config
		mcpOpts := a.baseRouterOpts
		mcpOpts.SearchMaxChars = cfg.MCP.SearchMaxChars
		mcpOpts.ContentMaxChars = cfg.MCP.ContentMaxChars
		// MCP 抓取路由强制不设路由级截断，完整正文交由 fetchCache 缓存后在 FetchMCP 按请求动态切片分页。
		mcpOpts.FetchMaxChars = 0

		searchCap := cfg.MCP.CacheSize
		searchTTL := cfg.MCP.CacheTTL.Duration
		fetchCap := 0
		if searchCap > 0 {
			fetchCap = max(searchCap/4, 16)
		}
		fetchTTL := searchTTL * 3

		a.mcpRouter = router.New(mcpOpts, a.enabledProviders)
		a.searchCache = newTTLLRU[*core.Envelope](searchCap, searchTTL)
		a.fetchCache = newBoundedTTLLRU[*core.Envelope](fetchCap, defaultMaxCachedFetchRunes, fetchTTL, envelopeContentRunes)
	})
}

// SearchMCP 执行带 TTL-LRU 缓存、调用方超时隔离与引用计数合并的 MCP 检索。
func (a *App) SearchMCP(ctx context.Context, req *core.SearchRequest) (*core.Envelope, error) {
	if req == nil {
		return a.Search(ctx, nil)
	}
	a.ensureMCP()

	cacheKey := buildSearchCacheKey(req, a.loaded.Config.Search, a.loaded.Config.MCP.SearchMaxChars)
	if cached, ok := a.searchCache.Get(cacheKey); ok {
		return cloneCachedSearchEnvelope(cached), nil
	}

	waitCtx, cancelWait := withCallerTimeout(ctx, req.Timeout)
	defer cancelWait()

	resEnv, completed, doErr := a.sfSearch.Do(waitCtx, cacheKey, func(sharedCtx context.Context) (*core.Envelope, error) {
		if cached, ok := a.searchCache.Get(cacheKey); ok {
			return cloneCachedSearchEnvelope(cached), nil
		}
		sharedReq := *req
		// 共享航班不透传低于默认值的单调用方短超时，防止短超时请求拖死并发合并的其他调用方。
		if sharedReq.Timeout > 0 && sharedReq.Timeout < a.baseRouterOpts.SearchTimeout {
			sharedReq.Timeout = 0
		}
		env, err := a.mcpRouter.Search(sharedCtx, &sharedReq)
		a.scrubEnvelopeSecrets(env)
		err = a.scrubError(err)
		a.logEnvelope("mcp.search", env)
		if err == nil && env != nil && env.OK && len(env.Results) > 0 {
			a.searchCache.Set(cacheKey, env.Clone())
		}
		return env, err
	})
	if !completed {
		se := a.ScrubCoreError(core.WrapError(core.CodeUpstreamTimeout, "MCP search request canceled or timed out", doErr).WithRetryable(false))
		return a.buildMCPErrorEnvelope(spec.KindSearch, req.Query, se), se
	}
	doErr = a.scrubError(doErr)
	if resEnv == nil {
		se := a.ScrubCoreError(core.AsError(doErr))
		return a.buildMCPErrorEnvelope(spec.KindSearch, req.Query, se), se
	}
	return resEnv.Clone(), doErr
}

// FetchMCP 执行带完整正文缓存、调用方超时隔离与零重复请求分页续读的 MCP 抓取。
func (a *App) FetchMCP(ctx context.Context, req *core.FetchRequest) (*core.Envelope, error) {
	if req == nil {
		return a.Fetch(ctx, nil)
	}
	a.ensureMCP()

	maxChars := req.MaxChars
	if maxChars <= 0 {
		maxChars = a.loaded.Config.MCP.FetchMaxChars
	}
	strategy := req.Strategy
	if strategy == "" {
		strategy = core.FetchStrategy(a.loaded.Config.Fetch.Strategy)
	}
	cacheKey := buildFetchCacheKey(req.URL, strategy, req.Providers, req.Options)
	bypassCacheRead := req.Offset == 0 && isFreshFetchBypass(req.Options)

	viewReq := *req
	viewReq.MaxChars = maxChars

	if !bypassCacheRead {
		if cachedFull, ok := a.fetchCache.Get(cacheKey); ok {
			return a.buildViewedFetchEnvelope(cachedFull, &viewReq, true), nil
		}
	}

	waitCtx, cancelWait := withCallerTimeout(ctx, req.Timeout)
	defer cancelWait()

	sfKey := cacheKey
	if bypassCacheRead {
		sfKey = cacheKey + ":fresh"
	}
	fullEnv, completed, doErr := a.sfFetch.Do(waitCtx, sfKey, func(sharedCtx context.Context) (*core.Envelope, error) {
		if !bypassCacheRead {
			if cachedFull, ok := a.fetchCache.Get(cacheKey); ok {
				cp := *cachedFull
				cp.Meta.Cached = true
				return &cp, nil
			}
		}
		fullReq := *req
		fullReq.Offset = 0
		fullReq.MaxChars = 0
		fullReq.Section = ""
		fullReq.Match = ""
		if fullReq.Timeout > 0 && fullReq.Timeout < a.baseRouterOpts.FetchTimeout {
			fullReq.Timeout = 0
		}
		env, err := a.mcpRouter.Fetch(sharedCtx, &fullReq)
		a.scrubEnvelopeSecrets(env)
		err = a.scrubError(err)
		a.logEnvelope("mcp.fetch", env)
		if err == nil && env != nil && env.OK && env.Page != nil && env.Page.Content != "" {
			a.fetchCache.Set(cacheKey, env.Clone())
		}
		return env, err
	})
	if !completed {
		se := a.ScrubCoreError(core.WrapError(core.CodeUpstreamTimeout, "MCP fetch request canceled or timed out", doErr).WithRetryable(false))
		return a.buildMCPErrorEnvelope(spec.KindFetch, req.URL, se), se
	}
	doErr = a.scrubError(doErr)
	if fullEnv == nil {
		se := a.ScrubCoreError(core.AsError(doErr))
		return a.buildMCPErrorEnvelope(spec.KindFetch, req.URL, se), se
	}
	if doErr != nil || !fullEnv.OK || fullEnv.Page == nil {
		return fullEnv.Clone(), doErr
	}
	return a.buildViewedFetchEnvelope(fullEnv, &viewReq, fullEnv.Meta.Cached), nil
}

func (a *App) buildMCPErrorEnvelope(kind, query string, se *core.Error) *core.Envelope {
	return &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     false,
		Kind:   kind,
		Query:  a.ScrubText(query),
		Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
		Error:  se.ToErrorInfo(),
	}
}

func withCallerTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func (a *App) buildViewedFetchEnvelope(full *core.Envelope, viewReq *core.FetchRequest, cached bool) *core.Envelope {
	viewedPage, viewNotes := router.ApplyPageView(full.Page, viewReq)
	cp := &core.Envelope{
		Schema: full.Schema,
		OK:     full.OK,
		Kind:   full.Kind,
		Query:  full.Query,
		Page:   viewedPage,
		Meta:   full.Meta.Clone(),
	}
	if full.Error != nil {
		cp.Error = new(*full.Error)
	}
	for _, note := range viewNotes {
		cp.Meta.Notes = append(cp.Meta.Notes, a.ScrubText(note))
	}
	cp.Meta.Truncated = cp.Page != nil && cp.Page.NextOffset != nil
	if cached {
		cp.Meta.Cached = true
		cp.Meta.Cost = []core.Cost{}
		cp.Meta.DurationMS = 0
		for i := range cp.Meta.Providers {
			cp.Meta.Providers[i].Cost = nil
		}
	}
	return cp
}
