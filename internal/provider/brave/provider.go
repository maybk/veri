package brave

import (
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Options 配置 Brave Search 适配器参数。
type Options struct {
	Keys        []string
	BaseURL     string
	Mode        string // auto | context | web
	Timeout     time.Duration
	Concurrency int
	QPS         float64
	PriceSearch string
}

// Provider 实现 core.Searcher。
type Provider struct {
	baseURL    string
	mode       string
	timeout    time.Duration
	client     *httpx.Client
	pool       *keypool.Pool
	gate       *httpx.Gate
	searchCost *core.Cost
}

// New 构造 Brave Search Provider；未配置密钥时返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}
	mode := strings.ToLower(strings.TrimSpace(opts.Mode))
	if mode == "" {
		mode = DefaultMode
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
		baseURL:    base,
		mode:       mode,
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

// Capabilities 声明 Brave Search 的原生能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	maxCount := spec.MaxSearchCount
	contentCost := core.ContentFree
	if p.mode == spec.BraveModeWeb {
		maxCount = maxWebSearchCount
		contentCost = core.ContentNone
	}
	return core.Capabilities{
		MaxCount:      maxCount,
		QueryMaxRunes: maxQueryRunes,
		Sites:         core.SiteMulti,
		ExcludeSites:  maxExcludeSites,
		Freshness:     core.TimeBoth,
		Lang:          true,
		Content:       contentCost,
		Question:      false,
	}
}

// CapabilitiesFor 按本次请求实际命中的端点（web 最多 20 条且不含全文，context 最多 50 条）返回能力边界。
func (p *Provider) CapabilitiesFor(req *core.SearchRequest) core.Capabilities {
	caps := p.Capabilities()
	if req == nil {
		return caps
	}
	mode := strings.ToLower(textnorm.FirstNonEmpty(req.Options[spec.OptBraveMode], req.Options[spec.OptBraveModeAlias], p.mode))
	if mode == spec.BraveModeWeb || (mode == spec.BraveModeAuto && req.Detail == core.DetailSnippet) {
		caps.MaxCount = maxWebSearchCount
		caps.Content = core.ContentNone
	} else {
		caps.MaxCount = spec.MaxSearchCount
		caps.Content = core.ContentFree
	}
	return caps
}
