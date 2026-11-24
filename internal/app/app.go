package app

import (
	"context"
	"io"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"sync"

	"veri/clikit/buildinfo"
	"veri/clikit/redact"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/router"
	"veri/internal/spec"
	"veri/internal/urlnorm"
)

// App 是 veri 的核心应用门面，为 CLI 与 MCP Server 提供同构的检索、抓取与诊断服务。
// CLI 按统一 [cache] 策略按需使用轻量磁盘缓存；MCP 专属路由与内存缓存采用按需懒加载。
type App struct {
	version          string
	loaded           *config.Loaded
	statuses         []ProviderStatus
	enabledProviders []core.Provider
	baseRouterOpts   router.Options
	cliRouter        *router.Router
	diskCache        *diskCache

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
		SearchProviders:               cfg.Search.Providers,
		SearchProvidersGlobal:         cfg.Search.ProvidersGlobal,
		SearchProvidersExplicit:       loaded.SearchProvidersExplicit,
		FusionProviders:               cfg.Search.FusionProviders,
		FusionProvidersGlobal:         cfg.Search.FusionProvidersGlobal,
		FusionProvidersGlobalExplicit: loaded.FusionProvidersGlobalExplicit,
		DefaultStrategy:               core.Strategy(cfg.Search.Strategy),
		DefaultCount:                  cfg.Search.Count,
		DefaultDetail:                 core.Detail(cfg.Search.Detail),
		SearchTimeout:                 cfg.Search.Timeout.Duration,
		FallbackOnEmpty:               cfg.Search.FallbackOnEmpty,
		StrictFilters:                 cfg.Search.StrictFilters,
		FusionK:                       cfg.Search.FusionK,
		Weights:                       cfg.Providers.SearchWeights(),
		SummaryMaxChars:               cfg.Search.SummaryMaxChars,
		ContentMaxChars:               cfg.Search.ContentMaxChars,
		SearchMaxChars:                cfg.Search.MaxChars,
		FetchStrategy:                 core.FetchStrategy(cfg.Fetch.Strategy),
		FetchProviders:                cfg.Fetch.Providers,
		FetchProvidersGlobal:          cfg.Fetch.ProvidersGlobal,
		FetchProvidersExplicit:        loaded.FetchProvidersExplicit,
		FetchTimeout:                  cfg.Fetch.Timeout.Duration,
		FetchMaxChars:                 cfg.Fetch.MaxChars,
		Logger:                        logger,
	}

	cliOpts := baseOpts
	// CLI 抓取路由不设路由级截断，完整正文由 App.Fetch 落盘缓存后再按 fetch.max_chars / 请求参数统一切片。
	cliOpts.FetchMaxChars = 0

	return &App{
		version:          version,
		loaded:           loaded,
		statuses:         statuses,
		enabledProviders: enabledProviders,
		baseRouterOpts:   baseOpts,
		cliRouter:        router.New(cliOpts, enabledProviders),
		diskCache:        newDiskCache(loaded.CacheDir, int64(cfg.Cache.MaxBytes), logger),
		scrubber:         scrubber,
		logger:           logger,
	}, nil
}

// Config 返回已加载的配置包装对象。
func (a *App) Config() *config.Loaded { return a.loaded }

// CacheInfo 返回当前统一缓存策略与磁盘缓存占用统计。
func (a *App) CacheInfo() (CacheInfoResult, error) {
	return InspectCache(a.loaded)
}

// ClearCache 安全清空当前生效目录中的本地磁盘缓存文件。
func (a *App) ClearCache() (CacheClearResult, error) {
	return ClearDiskCache(a.loaded.CacheDir)
}

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

// Search 执行 CLI 单次检索，按统一 [cache] / [cache.cli] 策略支持可选本地磁盘缓存与 --no-cache 绕过。
func (a *App) Search(ctx context.Context, req *core.SearchRequest) (*core.Envelope, error) {
	if a.logger != nil && req != nil {
		a.logger.DebugContext(ctx, "search_start", "query", req.Query, "count", req.Count, "strategy", string(req.Strategy))
	}
	policy := a.loaded.Config.Cache.EffectiveCLI()
	useDiskCache := a.diskCache != nil && policy.Enabled && policy.SearchTTL > 0 && req != nil && !req.NoCache
	var cacheKey string
	if useDiskCache {
		normReq, err := a.cliRouter.ValidateSearchRequest(req)
		if err != nil {
			return a.runCLISearch(ctx, req)
		}
		cacheKey = a.searchCacheKey(&normReq, a.loaded.Config.Search.MaxChars)
		if cached, ok := a.diskCache.Get(diskCacheKindSearch, cacheKey); ok {
			if a.logger != nil {
				a.logger.DebugContext(ctx, "search_cache_hit", "query", normReq.Query)
			}
			return cloneCachedSearchEnvelope(cached), nil
		}
	}

	env, err := a.runCLISearch(ctx, req)
	if useDiskCache && err == nil && isCacheableSearchEnvelope(env) {
		a.diskCache.Set(diskCacheKindSearch, cacheKey, env, policy.SearchTTL)
	}
	return env, err
}

// Fetch 执行 CLI 单次网页抓取；启用磁盘缓存时始终缓存未截断整页并在本地按请求视图参数动态切片分页。
func (a *App) Fetch(ctx context.Context, req *core.FetchRequest) (*core.Envelope, error) {
	if a.logger != nil && req != nil {
		a.logger.DebugContext(ctx, "fetch_start", "url", req.URL, "strategy", string(req.Strategy))
	}
	if req == nil {
		return a.runCLIFetch(ctx, nil)
	}

	normReq, parsedURL, valErr := a.cliRouter.ValidateFetchRequest(req)
	if valErr != nil {
		return a.runCLIFetch(ctx, req)
	}

	effectiveMaxChars := normReq.MaxChars
	if effectiveMaxChars == 0 {
		effectiveMaxChars = a.loaded.Config.Fetch.MaxChars
	}

	policy := a.loaded.Config.Cache.EffectiveCLI()
	useDiskCache := a.diskCache != nil && policy.Enabled && policy.FetchTTL > 0 && !urlnorm.IsLocalOrPrivateTarget(parsedURL)
	if !useDiskCache {
		runReq := *req
		if runReq.MaxChars == 0 {
			runReq.MaxChars = effectiveMaxChars
		}
		return a.runCLIFetch(ctx, &runReq)
	}

	viewReq := normReq
	viewReq.MaxChars = effectiveMaxChars
	cacheKey := a.fetchCacheKey(normReq.URL, normReq.Strategy, normReq.Providers, normReq.Options)
	bypassRead := normReq.Offset == 0 && isFreshFetchBypass(normReq.Options)
	if !bypassRead {
		if cachedFull, ok := a.diskCache.Get(diskCacheKindFetch, cacheKey); ok {
			if a.logger != nil {
				a.logger.DebugContext(ctx, "fetch_cache_hit", "url", normReq.URL)
			}
			return a.buildViewedFetchEnvelope(cachedFull, &viewReq, true), nil
		}
	}

	fullReq := *req
	fullReq.Offset = 0
	fullReq.MaxChars = 0
	fullReq.Section = ""
	fullReq.Match = ""
	fullEnv, err := a.runCLIFetch(ctx, &fullReq)
	if err != nil || fullEnv == nil || !fullEnv.OK || fullEnv.Page == nil {
		return fullEnv, err
	}
	if isCacheableFetchEnvelope(fullEnv, true) {
		a.diskCache.Set(diskCacheKindFetch, cacheKey, fullEnv, policy.FetchTTL)
	}
	return a.buildViewedFetchEnvelope(fullEnv, &viewReq, false), nil
}

func (a *App) runCLISearch(ctx context.Context, req *core.SearchRequest) (*core.Envelope, error) {
	env, err := a.cliRouter.Search(ctx, req)
	a.scrubEnvelopeSecrets(env)
	err = a.scrubError(err)
	a.logEnvelope("search", env)
	return env, err
}

func (a *App) runCLIFetch(ctx context.Context, req *core.FetchRequest) (*core.Envelope, error) {
	env, err := a.cliRouter.Fetch(ctx, req)
	a.scrubEnvelopeSecrets(env)
	err = a.scrubError(err)
	a.logEnvelope("fetch", env)
	return env, err
}

func (a *App) fetchCacheKey(rawURL string, strategy core.FetchStrategy, providers []string, options map[string]string) string {
	ghCred := a.loaded.CredentialFor(spec.ProviderGitHub)
	scope := hashScopeParts(
		a.EnabledFetchers(),
		a.loaded.Config.Fetch.Providers,
		a.loaded.Config.Fetch.ProvidersGlobal,
		ghCred.Keys,
	)
	return buildFetchCacheKeyWithScope(rawURL, strategy, providers, options, scope)
}

func (a *App) searchCacheKey(req *core.SearchRequest, defaultMaxChars int) string {
	sCfg := a.loaded.Config.Search
	scope := hashScopeParts(
		a.EnabledSearchers(),
		sCfg.Providers,
		sCfg.ProvidersGlobal,
		sCfg.FusionProviders,
		sCfg.FusionProvidersGlobal,
		sCfg.FallbackOnEmpty,
		sCfg.FusionK,
		sCfg.SummaryMaxChars,
		sCfg.ContentMaxChars,
		a.loaded.SearchProvidersExplicit,
		a.loaded.FusionProvidersGlobalExplicit,
	)
	return buildSearchCacheKeyWithScope(req, sCfg, defaultMaxChars, scope)
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
