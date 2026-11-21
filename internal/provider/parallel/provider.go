package parallel

import (
	"fmt"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Options 配置 Parallel REST v1 适配器参数。
type Options struct {
	Keys         []string
	BaseURL      string
	Mode         string // turbo | fast | basic | advanced
	Timeout      time.Duration
	FetchTimeout time.Duration
	Concurrency  int
	QPS          float64
	PriceSearch  string
	PriceFetch   string
}

// Provider 同时实现 core.DynamicSearcher、core.Fetcher 与 core.FetchProfiler。
type Provider struct {
	baseURL      string
	mode         string
	timeout      time.Duration
	fetchTimeout time.Duration
	client       *httpx.Client
	pool         *keypool.Pool
	gate         *httpx.Gate
	searchCost   *core.Cost
	fetchCost    *core.Cost
}

// New 构造 Parallel Provider；未配置密钥时返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	if client == nil {
		return nil, false, core.NewError(core.CodeInternal, "HTTP client must not be nil").WithProvider(ProviderName)
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}
	mode, err := normalizeMode(opts.Mode)
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
		mode:         mode,
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

// Capabilities 声明当前默认模式下的原生能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	return capabilitiesForMode(p.mode)
}

// CapabilitiesFor 按单次请求生效的 parallel.mode 动态收紧能力边界（turbo 模式不支持路径前缀过滤）。
func (p *Provider) CapabilitiesFor(req *core.SearchRequest) core.Capabilities {
	mode := p.mode
	if req != nil && len(req.Options) > 0 {
		if raw := textnorm.FirstNonEmpty(req.Options[spec.OptParallelMode], req.Options[spec.OptParallelModeAlias]); raw != "" {
			if parsed, err := normalizeMode(raw); err == nil {
				mode = parsed
			}
		}
	}
	return capabilitiesForMode(mode)
}

func capabilitiesForMode(mode string) core.Capabilities {
	return core.Capabilities{
		MaxCount:     maxSearchCount,
		Sites:        core.SiteMulti,
		MaxSites:     maxIncludeDomains,
		ExcludeSites: maxExcludeDomains,
		SiteHostOnly: mode == spec.ParallelModeTurbo,
		Freshness:    core.TimeAfterOnly,
		Lang:         false,
		Content:      core.ContentFree,
		Question:     true,
	}
}

// FetchProfile 声明 Parallel Extract 原生支持 PDF 文档与服务端动态渲染。
func (p *Provider) FetchProfile() core.FetchProfile {
	return core.FetchProfile{PDF: true, DynamicRender: true}
}

func normalizeMode(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return DefaultMode, nil
	}
	switch v {
	case spec.ParallelModeTurbo, spec.ParallelModeFast, spec.ParallelModeBasic, spec.ParallelModeAdvanced:
		return v, nil
	default:
		return "", core.NewError(
			core.CodeInvalidArgument,
			fmt.Sprintf("invalid parallel.mode %q (valid: turbo, fast, basic, advanced)", raw),
		).WithProvider(ProviderName)
	}
}

func normalizeLocation(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", nil
	}
	if len(v) != 2 || v[0] < 'a' || v[0] > 'z' || v[1] < 'a' || v[1] > 'z' {
		return "", core.NewError(
			core.CodeInvalidArgument,
			fmt.Sprintf("invalid parallel.location %q (expected 2-letter ISO country code, e.g. us, gb, jp)", raw),
		).WithProvider(ProviderName)
	}
	return v, nil
}
