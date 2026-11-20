package tinyfish

import (
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Options 配置 TinyFish 适配器参数。
type Options struct {
	Keys         []string
	SearchURL    string
	FetchURL     string
	Timeout      time.Duration
	FetchTimeout time.Duration
	Concurrency  int
	SearchQPS    float64
	FetchQPS     float64
	PriceSearch  string
	PriceFetch   string
}

// Provider 同时实现 core.Searcher 与 core.Fetcher。
type Provider struct {
	searchURL    string
	fetchURL     string
	timeout      time.Duration
	fetchTimeout time.Duration
	client       *httpx.Client
	pool         *keypool.Pool
	searchGate   *httpx.Gate
	fetchGate    *httpx.Gate
	searchCost   *core.Cost
	fetchCost    *core.Cost
}

// New 构造 TinyFish Provider；未配置密钥时返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	sURL, err := httpx.ResolveAPIBaseURL(opts.SearchURL, DefaultSearchURL)
	if err != nil {
		return nil, false, err
	}
	fURL, err := httpx.ResolveAPIBaseURL(opts.FetchURL, DefaultFetchURL)
	if err != nil {
		return nil, false, err
	}
	sCost, err := core.ParseOptionalPriceSpec(opts.PriceSearch)
	if err != nil {
		return nil, false, err
	}
	fCost, err := core.ParseOptionalPriceSpec(opts.PriceFetch)
	if err != nil {
		return nil, false, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultSearchTimeout
	}
	fetchTimeout := opts.FetchTimeout
	if fetchTimeout <= 0 {
		fetchTimeout = DefaultFetchTimeout
	}
	conc := opts.Concurrency
	if conc <= 0 {
		conc = DefaultConcurrency
	}

	return &Provider{
		searchURL:    sURL,
		fetchURL:     fURL,
		timeout:      timeout,
		fetchTimeout: fetchTimeout,
		client:       client,
		pool:         pool,
		searchGate:   httpx.NewGate(conc, opts.SearchQPS),
		fetchGate:    httpx.NewGate(conc, opts.FetchQPS),
		searchCost:   sCost,
		fetchCost:    fCost,
	}, true, nil
}

// Name 返回 Provider 唯一标识。
func (p *Provider) Name() string { return ProviderName }

// SearchURL 返回生效的检索端点地址。
func (p *Provider) SearchURL() string { return p.searchURL }

// Capabilities 声明 TinySearch 的静态能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	return core.Capabilities{
		MaxCount:      maxPageCount,
		QueryMaxRunes: maxQueryRunes,
		Sites:         core.SiteMulti,
		ExcludeSites:  maxExcludeSites,
		Freshness:     core.TimeBoth,
		Lang:          true,
		Content:       core.ContentNone,
		Question:      true,
	}
}

// FetchProfile 声明 TinyFetch 原生支持服务端无头动态渲染与 PDF 提取。
func (p *Provider) FetchProfile() core.FetchProfile {
	return core.FetchProfile{PDF: true, DynamicRender: true}
}

// CapabilitiesFor 按单次请求的 tinyfish.domain_type 动态收紧时间窗能力（research_paper 仅原生支持整年 pub_year_min/max）。
func (p *Provider) CapabilitiesFor(req *core.SearchRequest) core.Capabilities {
	caps := p.Capabilities()
	if req == nil {
		return caps
	}
	domainType := strings.ToLower(strings.TrimSpace(textnorm.FirstNonEmpty(req.Options[spec.OptTinyFishDomainType], req.Options[spec.OptTinyFishDomainAlias])))
	if domainType == domainResearchPaper && req.Filters.Freshness != nil && !isFullYearFreshness(req.Filters.Freshness) {
		caps.Freshness = core.TimeNone
	}
	return caps
}
