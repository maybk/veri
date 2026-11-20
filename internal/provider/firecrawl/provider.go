package firecrawl

import (
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
)

// Options 配置 Firecrawl v2 适配器参数。
type Options struct {
	Keys         []string
	BaseURL      string
	Proxy        string // auto | basic | enhanced
	MaxPDFPages  int
	Timeout      time.Duration
	FetchTimeout time.Duration
	Concurrency  int
	QPS          float64
	PriceCredit  string
}

// Provider 同时实现 core.Searcher 与 core.Fetcher。
type Provider struct {
	baseURL      string
	selfHosted   bool
	proxy        string
	maxPDFPages  int
	timeout      time.Duration
	fetchTimeout time.Duration
	client       *httpx.Client
	pool         *keypool.Pool
	gate         *httpx.Gate
	creditCost   *core.Cost
}

// New 构造 Firecrawl Provider。
// 官方云端端点（firecrawl.dev 域）强制要求配置密钥，缺密钥时返回 (nil, false, nil) 绝不回退免 Key 公共池；
// 显式指向非官方自托管 base_url 时允许无密钥启用。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}
	selfHosted := !isOfficialCloudHost(base)
	pool := keypool.New(opts.Keys...)
	if !selfHosted && pool.Size() == 0 {
		return nil, false, nil
	}

	proxyMode, err := normalizeProxy(opts.Proxy)
	if err != nil {
		return nil, false, err
	}
	maxPages := opts.MaxPDFPages
	if maxPages <= 0 {
		maxPages = DefaultMaxPDFPages
	}
	if maxPages > spec.MaxFirecrawlPDFPages {
		return nil, false, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("firecrawl max_pdf_pages %d exceeds %d", maxPages, spec.MaxFirecrawlPDFPages)).WithProvider(ProviderName)
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
		selfHosted:   selfHosted,
		proxy:        proxyMode,
		maxPDFPages:  maxPages,
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

// HasAPIKey 返回是否配置了 API Key。
func (p *Provider) HasAPIKey() bool { return p.pool != nil && p.pool.Size() > 0 }

// IsSelfHosted 返回是否指向非官方自托管实例。
func (p *Provider) IsSelfHosted() bool { return p.selfHosted }

// Capabilities 声明 Firecrawl v2 Search 的原生能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	return core.Capabilities{
		MaxCount:                     maxSearchCount,
		QueryMaxRunes:                maxQueryRunes,
		Sites:                        core.SiteMulti,
		MaxSites:                     maxIncludeDomains,
		ExcludeSites:                 maxExcludeDomains,
		SiteExcludeMutuallyExclusive: true,
		SiteHostOnly:                 true,
		Freshness:                    core.TimeBoth,
		Lang:                         false,
		Content:                      core.ContentNone,
		Question:                     false,
	}
}

// FetchProfile 声明 Firecrawl Scrape 支持 PDF 与服务端强抗反爬动态渲染。
func (p *Provider) FetchProfile() core.FetchProfile {
	return core.FetchProfile{PDF: true, DynamicRender: true}
}

func (p *Provider) authSpec() (header, prefix string) {
	if p.HasAPIKey() {
		return spec.HeaderAuthorization, spec.AuthBearerPrefix
	}
	return "", ""
}

func (p *Provider) estimateCreditsCost(credits int64) *core.Cost {
	if p.creditCost == nil || credits <= 0 || p.creditCost.Micros <= 0 {
		return nil
	}
	micros := int64(math.MaxInt64)
	if credits <= math.MaxInt64/p.creditCost.Micros {
		micros = p.creditCost.Micros * credits
	}
	return &core.Cost{
		Currency:  p.creditCost.Currency,
		Micros:    micros,
		Estimated: true,
	}
}

// IsSelfHostedBaseURL 判断给定 base_url 是否指向非官方自托管实例（为空时回退官方云端，返回 false）。
func IsSelfHostedBaseURL(rawBase string) bool {
	if strings.TrimSpace(rawBase) == "" {
		return false
	}
	return !isOfficialCloudHost(rawBase)
}

// isOfficialCloudHost 严格按解析后的主机名判定官方云端域，防止端口或大小写变形绕过无 Key 限制。
func isOfficialCloudHost(rawBase string) bool {
	u, err := url.Parse(strings.TrimSpace(rawBase))
	if err != nil {
		return true
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return host == "" || host == "firecrawl.dev" || strings.HasSuffix(host, ".firecrawl.dev")
}

func normalizeProxy(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return DefaultProxy, nil
	}
	switch v {
	case spec.FirecrawlProxyAuto, spec.FirecrawlProxyBasic, spec.FirecrawlProxyEnhanced:
		return v, nil
	default:
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid firecrawl.proxy %q (valid: auto, basic, enhanced)", raw)).WithProvider(ProviderName)
	}
}
