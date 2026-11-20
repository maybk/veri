package tencentwsa

import (
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
)

// Options 配置腾讯云联网搜索适配器参数。
type Options struct {
	Keys        []string
	Edition     string // lite | standard | flagship
	BaseURL     string
	Timeout     time.Duration
	Concurrency int
	QPS         float64
	PriceSearch string
}

// Provider 实现 core.Searcher。
type Provider struct {
	edition    string
	baseURL    string
	timeout    time.Duration
	client     *httpx.Client
	pool       *keypool.Pool
	gate       *httpx.Gate
	searchCost *core.Cost
}

// New 构造腾讯云联网搜索 Provider；未配置密钥时返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}

	ed, err := normalizeEdition(opts.Edition)
	if err != nil {
		return nil, false, err
	}
	cost, err := core.ParseOptionalPriceSpec(opts.PriceSearch)
	if err != nil {
		return nil, false, err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	conc := opts.Concurrency
	if conc <= 0 {
		conc = DefaultConcurrency
	}

	return &Provider{
		edition:    ed,
		baseURL:    base,
		timeout:    timeout,
		client:     client,
		pool:       pool,
		gate:       httpx.NewGate(conc, opts.QPS),
		searchCost: cost,
	}, true, nil
}

// Name 返回 Provider 唯一标识。
func (p *Provider) Name() string { return ProviderName }

// BaseURL 返回生效的 API 根地址。
func (p *Provider) BaseURL() string { return p.baseURL }

// Edition 返回当前实例配置的版本档位。
func (p *Provider) Edition() string { return p.edition }

// Capabilities 按开通的版本（极速/标准/旗舰）如实声明能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	if p.edition == spec.WSAEditionLite {
		return core.Capabilities{
			MaxCount:                     liteMaxCount,
			Sites:                        core.SiteSingle,
			ExcludeSites:                 maxExcludeSites,
			SiteExcludeMutuallyExclusive: true,
			Freshness:                    core.TimeBoth,
			Content:                      core.ContentNone,
		}
	}
	steps := make([]int, len(wsaCountSteps))
	copy(steps, wsaCountSteps)
	return core.Capabilities{
		MaxCount:                     spec.MaxSearchCount,
		CountSteps:                   steps,
		Sites:                        core.SiteSingle,
		ExcludeSites:                 maxExcludeSites,
		SiteExcludeMutuallyExclusive: true,
		Freshness:                    core.TimeBoth,
		Content:                      core.ContentNone,
	}
}
