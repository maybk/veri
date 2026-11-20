package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"
	"time"

	"veri/clikit/buildinfo"
	"veri/clikit/redact"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/router"
	"veri/internal/spec"
)

// App 是 veri 的核心应用门面，为 CLI 与 MCP Server 提供同构的检索、抓取与诊断服务。
// MCP 专属路由与缓存采用按需懒加载，确保单次 CLI 执行保持零额外缓存分配。
type App struct {
	version          string
	loaded           *config.Loaded
	statuses         []ProviderStatus
	enabledProviders []core.Provider
	baseRouterOpts   router.Options
	cliRouter        *router.Router

	mcpOnce     sync.Once
	mcpRouter   *router.Router
	searchCache *ttlLRU[*core.Envelope]
	fetchCache  *ttlLRU[*core.Envelope]
	sfSearch    flightGroup[*core.Envelope]
	sfFetch     flightGroup[*core.Envelope]

	scrubber    *strings.Replacer
	logger      *slog.Logger
	lookupNetIP func(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// New 根据加载后的配置实例化全部已启用 Provider 与 CLI 路由（日志默认写入 os.Stderr）。
func New(loaded *config.Loaded, version string) (*App, error) {
	return NewWithStderr(loaded, version, os.Stderr)
}

// NewWithStderr 允许注入自定义日志输出流（传 nil 时回退至 io.Discard）。
func NewWithStderr(loaded *config.Loaded, version string, stderr io.Writer) (*App, error) {
	if loaded == nil {
		var err error
		loaded, err = config.Load("")
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(version) == "" {
		version = buildinfo.DefaultDevVersion
	}
	if stderr == nil {
		stderr = io.Discard
	}
	cfg := loaded.Config
	scrubber := redact.NewScrubber(loaded.AllSecrets)
	logger := redact.NewLogger(stderr, loaded.LogLevel, loaded.LogFormat, loaded.AllSecrets)

	fakeCIDRs, err := httpx.ParseCIDRs(cfg.Network.FakeIPCIDRs)
	if err != nil {
		return nil, core.WrapError(core.CodeInvalidArgument, redact.ScrubWith(err.Error(), scrubber), err)
	}

	apiClient, err := httpx.NewAPIClient(httpx.Options{
		ProxyURL: cfg.Network.Proxy,
		Logger:   logger,
	})
	if err != nil {
		return nil, core.WrapError(core.CodeInvalidArgument, redact.ScrubWith(err.Error(), scrubber), err)
	}
	fetchClient, err := httpx.NewSafeFetchClient(httpx.Options{
		ProxyURL:     cfg.Network.Proxy,
		AllowPrivate: cfg.Network.AllowPrivate,
		FakeIPCIDRs:  fakeCIDRs,
		Logger:       logger,
	})
	if err != nil {
		return nil, core.WrapError(core.CodeInvalidArgument, redact.ScrubWith(err.Error(), scrubber), err)
	}

	deps := Deps{
		APIClient:   apiClient,
		FetchClient: fetchClient,
		Version:     version,
	}

	var enabledProviders []core.Provider
	statuses := make([]ProviderStatus, 0, len(factories))
	for _, fn := range factories {
		p, st, fErr := fn(loaded, deps)
		if fErr != nil {
			return nil, core.WrapError(core.CodeInvalidArgument, redact.ScrubWith(fErr.Error(), scrubber), fErr)
		}
		statuses = append(statuses, st)
		if st.Enabled && p != nil {
			enabledProviders = append(enabledProviders, p)
		}
	}

	baseOpts := router.Options{
		SearchProviders:      cfg.Search.Providers,
		SearchProvidersEN:    cfg.Search.ProvidersEN,
		FusionProviders:      cfg.Search.FusionProviders,
		DefaultStrategy:      core.Strategy(cfg.Search.Strategy),
		DefaultCount:         cfg.Search.Count,
		DefaultDetail:        core.Detail(cfg.Search.Detail),
		SearchTimeout:        cfg.Search.Timeout.Duration,
		FallbackOnEmpty:      cfg.Search.FallbackOnEmpty,
		StrictFilters:        cfg.Search.StrictFilters,
		FusionK:              cfg.Search.FusionK,
		Weights:              cfg.Providers.SearchWeights(),
		SummaryMaxRunes:      cfg.Search.SummaryMaxRunes,
		ContentMaxRunes:      cfg.Search.ContentMaxRunes,
		SearchMaxChars:       cfg.Search.MaxChars,
		FetchStrategy:        core.FetchStrategy(cfg.Fetch.Strategy),
		FetchProviders:       cfg.Fetch.Providers,
		FetchProvidersGlobal: cfg.Fetch.ProvidersGlobal,
		FetchTimeout:         cfg.Fetch.Timeout.Duration,
		FetchMaxChars:        cfg.Fetch.MaxChars,
	}

	return &App{
		version:          version,
		loaded:           loaded,
		statuses:         statuses,
		enabledProviders: enabledProviders,
		baseRouterOpts:   baseOpts,
		cliRouter:        router.New(baseOpts, enabledProviders),
		scrubber:         scrubber,
		logger:           logger,
	}, nil
}

func (a *App) ensureMCP() {
	a.mcpOnce.Do(func() {
		cfg := a.loaded.Config
		mcpOpts := a.baseRouterOpts
		mcpOpts.SearchMaxChars = cfg.MCP.SearchMaxChars
		mcpOpts.ContentMaxRunes = cfg.MCP.ContentMaxRunes
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

// Config 返回已加载的配置包装对象。
func (a *App) Config() *config.Loaded { return a.loaded }

// ProviderStatuses 返回全部内置 Provider 的状态描述表。
func (a *App) ProviderStatuses() []ProviderStatus {
	out := make([]ProviderStatus, len(a.statuses))
	copy(out, a.statuses)
	return out
}

// EnabledSearchers 返回当前已启用的 Searcher 名称列表。
func (a *App) EnabledSearchers() []string { return a.cliRouter.EnabledSearchers() }

// EnabledFetchers 返回当前已启用的 Fetcher 名称列表。
func (a *App) EnabledFetchers() []string { return a.cliRouter.EnabledFetchers() }

// Search 执行 CLI 单次检索（CLI 模式不启用进程内缓存）。
func (a *App) Search(ctx context.Context, req *core.SearchRequest) (*core.Envelope, error) {
	if a.logger != nil && req != nil {
		a.logger.DebugContext(ctx, "search_start", "query", req.Query, "count", req.Count, "strategy", string(req.Strategy))
	}
	env, err := a.cliRouter.Search(ctx, req)
	a.scrubEnvelopeSecrets(env)
	err = a.scrubError(err)
	a.logEnvelope("search", env)
	return env, err
}

// Fetch 执行 CLI 单次网页抓取。
func (a *App) Fetch(ctx context.Context, req *core.FetchRequest) (*core.Envelope, error) {
	if a.logger != nil && req != nil {
		a.logger.DebugContext(ctx, "fetch_start", "url", req.URL, "strategy", string(req.Strategy))
	}
	env, err := a.cliRouter.Fetch(ctx, req)
	a.scrubEnvelopeSecrets(env)
	err = a.scrubError(err)
	a.logEnvelope("fetch", env)
	return env, err
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
		return &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     false,
			Kind:   spec.KindSearch,
			Query:  a.ScrubText(req.Query),
			Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
			Error:  se.ToErrorInfo(),
		}, se
	}
	doErr = a.scrubError(doErr)
	if resEnv == nil {
		se := a.ScrubCoreError(core.AsError(doErr))
		return &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     false,
			Kind:   spec.KindSearch,
			Query:  a.ScrubText(req.Query),
			Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
			Error:  se.ToErrorInfo(),
		}, se
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
				return cachedFull, nil
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
		return &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     false,
			Kind:   spec.KindFetch,
			Query:  a.ScrubText(req.URL),
			Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
			Error:  se.ToErrorInfo(),
		}, se
	}
	doErr = a.scrubError(doErr)
	if fullEnv == nil {
		se := a.ScrubCoreError(core.AsError(doErr))
		return &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     false,
			Kind:   spec.KindFetch,
			Query:  a.ScrubText(req.URL),
			Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
			Error:  se.ToErrorInfo(),
		}, se
	}
	if doErr != nil || !fullEnv.OK || fullEnv.Page == nil {
		return fullEnv.Clone(), doErr
	}
	return a.buildViewedFetchEnvelope(fullEnv, &viewReq, false), nil
}

func withCallerTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

func (a *App) buildViewedFetchEnvelope(full *core.Envelope, viewReq *core.FetchRequest, cached bool) *core.Envelope {
	cp := full.Clone()
	viewedPage, viewNotes := router.ApplyPageView(full.Page, viewReq)
	cp.Page = viewedPage
	for _, note := range viewNotes {
		cp.Meta.Notes = append(cp.Meta.Notes, a.ScrubText(note))
	}
	cp.Meta.Truncated = cp.Page.NextOffset != nil
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

// ScrubText 使用应用内已加载密钥表与通用凭据规则对任意文本脱敏（接收者为 nil 时仍执行通用 URL 与认证头清洗）。
func (a *App) ScrubText(s string) string {
	if s == "" {
		return ""
	}
	var rep *strings.Replacer
	if a != nil {
		rep = a.scrubber
	}
	return redact.ScrubWith(s, rep)
}

func (a *App) scrubError(err error) error {
	if err == nil {
		return nil
	}
	return a.ScrubCoreError(core.AsError(err))
}

// ScrubCoreError 返回字段（Message / Hint / Params / Cause）均已脱敏的 *core.Error 副本。
func (a *App) ScrubCoreError(se *core.Error) *core.Error {
	var rep *strings.Replacer
	if a != nil {
		rep = a.scrubber
	}
	return ScrubCoreErrorWith(se, rep)
}

// ScrubCoreErrorWith 使用指定密钥替换器（可为 nil）对 *core.Error 的全部可读字段执行脱敏并返回独立副本。
func ScrubCoreErrorWith(se *core.Error, rep *strings.Replacer) *core.Error {
	if se == nil {
		return core.NewError(core.CodeInternal, "unknown internal error")
	}
	scrub := func(s string) string { return redact.ScrubWith(s, rep) }
	cp := *se
	cp.Message = scrub(cp.Message)
	cp.Hint = scrub(cp.Hint)
	if len(se.Params) > 0 {
		cp.Params = make(map[string]string, len(se.Params))
		for k, v := range se.Params {
			cp.Params[k] = scrub(v)
		}
	}
	if cp.Cause != nil {
		rawCause := cp.Cause.Error()
		cleanedCause := scrub(rawCause)
		if cp.Message == "" {
			cp.Message = cleanedCause
		}
		if cleanedCause != rawCause {
			cp.Cause = errors.New(cleanedCause)
		}
	}
	return &cp
}

func (a *App) scrubEnvelopeSecrets(env *core.Envelope) {
	if env == nil {
		return
	}
	env.Query = a.ScrubText(env.Query)
	if env.Page != nil {
		env.Page.URL = a.ScrubText(env.Page.URL)
		env.Page.FinalURL = a.ScrubText(env.Page.FinalURL)
	}
	for i := range env.Pages {
		env.Pages[i].URL = a.ScrubText(env.Pages[i].URL)
		env.Pages[i].FinalURL = a.ScrubText(env.Pages[i].FinalURL)
		if env.Pages[i].Error != nil {
			env.Pages[i].Error.Message = a.ScrubText(env.Pages[i].Error.Message)
			env.Pages[i].Error.Hint = a.ScrubText(env.Pages[i].Error.Hint)
		}
	}
	if env.Error != nil {
		env.Error.Message = a.ScrubText(env.Error.Message)
		env.Error.Hint = a.ScrubText(env.Error.Hint)
	}
	for i := range env.Meta.Notes {
		env.Meta.Notes[i] = a.ScrubText(env.Meta.Notes[i])
	}
	for i := range env.Meta.Providers {
		if env.Meta.Providers[i].Error != nil {
			env.Meta.Providers[i].Error.Message = a.ScrubText(env.Meta.Providers[i].Error.Message)
			env.Meta.Providers[i].Error.Hint = a.ScrubText(env.Meta.Providers[i].Error.Hint)
		}
		for j := range env.Meta.Providers[i].Warnings {
			env.Meta.Providers[i].Warnings[j] = a.ScrubText(env.Meta.Providers[i].Warnings[j])
		}
	}
}

func (a *App) logEnvelope(op string, env *core.Envelope) {
	if a.logger == nil || env == nil {
		return
	}
	for _, rep := range env.Meta.Providers {
		attrs := []any{
			"op", op,
			"provider", rep.Name,
			"status", rep.Status,
			"latency_ms", rep.LatencyMS,
		}
		if rep.Cost != nil {
			attrs = append(attrs, "cost", rep.Cost.Format())
		}
		a.logger.Info("provider_call", attrs...)
	}
}
