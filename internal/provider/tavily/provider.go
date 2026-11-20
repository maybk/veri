package tavily

import (
	"math"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
)

// Options 配置 Tavily 适配器参数。
type Options struct {
	Keys         []string
	BaseURL      string
	SearchDepth  string // ultra-fast | fast | basic | advanced
	ExtractDepth string // basic | advanced
	Timeout      time.Duration
	FetchTimeout time.Duration
	Concurrency  int
	QPS          float64
	PriceCredit  string
}

// Provider 同时实现 core.Searcher 与 core.Fetcher。
type Provider struct {
	baseURL      string
	searchDepth  string
	extractDepth string
	timeout      time.Duration
	fetchTimeout time.Duration
	client       *httpx.Client
	pool         *keypool.Pool
	gate         *httpx.Gate
	creditCost   *core.Cost
}

// New 构造 Tavily Provider；未配置密钥时返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}

	sDepth := strings.ToLower(strings.TrimSpace(opts.SearchDepth))
	if sDepth == "" {
		sDepth = DefaultSearchDepth
	}
	eDepth := strings.ToLower(strings.TrimSpace(opts.ExtractDepth))
	if eDepth == "" {
		eDepth = DefaultExtractDepth
	}

	cCost, err := core.ParseOptionalPriceSpec(opts.PriceCredit)
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
		searchDepth:  sDepth,
		extractDepth: eDepth,
		timeout:      timeout,
		fetchTimeout: fetchTimeout,
		client:       client,
		pool:         pool,
		gate:         httpx.NewGate(conc, opts.QPS),
		creditCost:   cCost,
	}, true, nil
}

// Name 返回 Provider 唯一标识。
func (p *Provider) Name() string { return ProviderName }

// BaseURL 返回生效的 API 根地址。
func (p *Provider) BaseURL() string { return p.baseURL }

// Capabilities 声明 Tavily 的原生能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	return core.Capabilities{
		MaxCount:     maxSearchCount,
		Sites:        core.SiteMulti,
		MaxSites:     maxIncludeDomains,
		ExcludeSites: maxExcludeDomains,
		Freshness:    core.TimeBoth,
		Lang:         true,
		Content:      core.ContentFree,
		Question:     false,
	}
}

// FetchProfile 声明 Tavily Extract 支持 PDF 与高级动态渲染页面提取。
func (p *Provider) FetchProfile() core.FetchProfile {
	return core.FetchProfile{PDF: true, DynamicRender: true}
}

func (p *Provider) costFromUsage(usage *wireUsage, fallbackMicros int64) *core.Cost {
	if p.creditCost == nil {
		return nil
	}
	micros := fallbackMicros
	if usage != nil && usage.Credits != nil && !math.IsNaN(*usage.Credits) && !math.IsInf(*usage.Credits, 0) && *usage.Credits >= 0 {
		scaled := math.Round(*usage.Credits * float64(p.creditCost.Micros))
		if scaled >= float64(math.MaxInt64) {
			micros = math.MaxInt64
		} else {
			micros = int64(scaled)
		}
	}
	return &core.Cost{
		Currency:  p.creditCost.Currency,
		Micros:    micros,
		Estimated: true,
	}
}
