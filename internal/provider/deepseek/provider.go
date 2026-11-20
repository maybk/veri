package deepseek

import (
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
)

// Options 配置 DeepSeek 适配器实例参数。
type Options struct {
	Keys         []string
	BaseURL      string
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
	baseURL      string
	timeout      time.Duration
	fetchTimeout time.Duration
	client       *httpx.Client
	pool         *keypool.Pool
	searchGate   *httpx.Gate
	fetchGate    *httpx.Gate
	searchCost   *core.Cost
	fetchCost    *core.Cost
}

// New 构造 DeepSeek Provider；若未配置密钥则返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
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
		baseURL:      base,
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

// BaseURL 返回生效的 API 根地址。
func (p *Provider) BaseURL() string { return p.baseURL }

// Capabilities 声明 DeepSeek Search 的原生能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	return core.Capabilities{
		MaxCount:      spec.MaxSearchCount,
		QueryMaxRunes: maxQueryRunes,
		Sites:         core.SiteMulti,
		ExcludeSites:  maxExcludeSites,
		Freshness:     core.TimeAbsolute,
		Lang:          true,
		Content:       core.ContentFree,
		Question:      true,
	}
}
