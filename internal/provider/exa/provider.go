package exa

import (
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Options 配置 Exa 适配器参数。
type Options struct {
	Keys         []string
	BaseURL      string
	SearchType   string // lite | auto | deep-lite | deep
	AllowOver10  bool
	Timeout      time.Duration
	FetchTimeout time.Duration
	Concurrency  int
	QPS          float64
}

// Provider 同时实现 core.Searcher 与 core.Fetcher。
type Provider struct {
	baseURL      string
	searchType   string
	allowOver10  bool
	timeout      time.Duration
	fetchTimeout time.Duration
	client       *httpx.Client
	pool         *keypool.Pool
	gate         *httpx.Gate
}

// New 构造 Exa Provider；未配置密钥时返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}
	st := strings.ToLower(strings.TrimSpace(opts.SearchType))
	if st == "" {
		st = DefaultSearchType
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
		searchType:   st,
		allowOver10:  opts.AllowOver10,
		timeout:      timeout,
		fetchTimeout: fetchTimeout,
		client:       client,
		pool:         pool,
		gate:         httpx.NewGate(conc, opts.QPS),
	}, true, nil
}

// Name 返回 Provider 唯一标识。
func (p *Provider) Name() string { return ProviderName }

// BaseURL 返回生效的 API 根地址。
func (p *Provider) BaseURL() string { return p.baseURL }

// Capabilities 声明 Exa 的原生能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	maxCount := defaultMaxCount
	if p.allowOver10 {
		maxCount = extendedMaxCount
	}
	return core.Capabilities{
		MaxCount:     maxCount,
		Sites:        core.SiteMulti,
		MaxSites:     maxDomainFilters,
		ExcludeSites: maxDomainFilters,
		Freshness:    core.TimeAbsolute,
		Lang:         false,
		Content:      core.ContentFree,
		Question:     true,
	}
}

// FetchProfile 声明 Exa Contents 原生支持动态页面抓取与 PDF 提取。
func (p *Provider) FetchProfile() core.FetchProfile {
	return core.FetchProfile{PDF: true, DynamicRender: true}
}

// CapabilitiesFor 按单次请求的 exa.category 专属约束动态收紧能力边界（防止 company/people 触发上游 HTTP 400）。
func (p *Provider) CapabilitiesFor(req *core.SearchRequest) core.Capabilities {
	caps := p.Capabilities()
	if req == nil {
		return caps
	}
	cat := normalizeExaCategory(textnorm.FirstNonEmpty(req.Options[spec.OptExaCategory], req.Options[spec.OptExaCategoryAlias]))
	if cat == "" {
		_, cat = extractInlineExaCategory(req.Query)
	}
	switch cat {
	case categoryCompany:
		caps.Freshness = core.TimeNone
	case categoryPeople:
		caps.Freshness = core.TimeNone
		caps.ExcludeSites = 0
	}
	return caps
}
