package linkup

import (
	"fmt"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
)

// Options 配置 Linkup 适配器参数。
type Options struct {
	Keys         []string
	BaseURL      string
	Depth        string // flash | fast | standard | deep
	FetchMode    string // standard | pro
	RenderJS     bool
	Timeout      time.Duration
	FetchTimeout time.Duration
	Concurrency  int
	QPS          float64
	PriceSearch  string
	PriceFetch   string
}

// Provider 同时实现 core.Searcher 与 core.Fetcher。
type Provider struct {
	baseURL      string
	depth        string
	fetchMode    string
	renderJS     bool
	timeout      time.Duration
	fetchTimeout time.Duration
	client       *httpx.Client
	pool         *keypool.Pool
	gate         *httpx.Gate
	searchCost   *core.Cost
	fetchCost    *core.Cost
}

// New 构造 Linkup Provider；未配置密钥时返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}
	depth, err := normalizeDepth(opts.Depth)
	if err != nil {
		return nil, false, err
	}
	fetchMode, err := normalizeFetchMode(opts.FetchMode)
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
		depth:        depth,
		fetchMode:    fetchMode,
		renderJS:     opts.RenderJS,
		timeout:      timeout,
		fetchTimeout: fetchTimeout,
		client:       client,
		pool:         pool,
		gate:         httpx.NewGate(conc, opts.QPS),
		searchCost:   sCost,
		fetchCost:    fCost,
	}, true, nil
}

// Name 返回 Provider 唯一标识。
func (p *Provider) Name() string { return ProviderName }

// BaseURL 返回生效的 API 根地址。
func (p *Provider) BaseURL() string { return p.baseURL }

// Capabilities 声明 Linkup 的原生检索能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	return core.Capabilities{
		MaxCount:     maxSearchCount,
		Sites:        core.SiteMulti,
		MaxSites:     maxIncludeDomains,
		ExcludeSites: maxExcludeDomains,
		SiteHostOnly: true,
		Freshness:    core.TimeBoth,
		Lang:         false,
		Content:      core.ContentFree,
		Question:     false,
	}
}

// FetchProfile 声明 Linkup Fetch 支持 PDF 与服务端 JS 渲染。
func (p *Provider) FetchProfile() core.FetchProfile {
	return core.FetchProfile{PDF: true, DynamicRender: true}
}

func normalizeDepth(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return DefaultDepth, nil
	}
	switch v {
	case spec.LinkupDepthFlash, spec.LinkupDepthFast, spec.LinkupDepthStandard, spec.LinkupDepthDeep:
		return v, nil
	default:
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid linkup.depth %q (valid: flash, fast, standard, deep)", raw)).WithProvider(ProviderName)
	}
}

func normalizeFetchMode(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return DefaultFetchMode, nil
	}
	switch v {
	case spec.LinkupFetchModeStandard, spec.LinkupFetchModePro:
		return v, nil
	default:
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid linkup.fetch_mode %q (valid: standard, pro)", raw)).WithProvider(ProviderName)
	}
}
