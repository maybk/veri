package zhihu

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

// Options 配置知乎站内搜索（zhihu）适配器参数。
type Options struct {
	Keys        []string
	BaseURL     string
	Timeout     time.Duration
	Concurrency int
	QPS         float64
	PriceSearch string
}

// WebOptions 配置知乎全网搜索（zhihu_web）适配器参数。
type WebOptions struct {
	Keys        []string
	BaseURL     string
	SearchDB    string // all | realtime | static
	Timeout     time.Duration
	Concurrency int
	QPS         float64
	PriceSearch string
}

// Provider 实现知乎站内（zhihu）与知乎全网（zhihu_web）的 core.Searcher 与 core.DynamicSearcher。
type Provider struct {
	name       string
	webMode    bool
	baseURL    string
	searchDB   string
	timeout    time.Duration
	client     *httpx.Client
	pool       *keypool.Pool
	gate       *httpx.Gate
	searchCost *core.Cost
	nowFn      func() time.Time
}

// New 构造知乎站内搜索（zhihu）Provider；未配置密钥时返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	return newProvider(ProviderName, false, opts, "", client)
}

// NewWeb 构造知乎全网搜索（zhihu_web）Provider；未配置密钥时返回 (nil, false, nil)。
func NewWeb(opts WebOptions, client *httpx.Client) (*Provider, bool, error) {
	return newProvider(WebProviderName, true, Options{
		Keys:        opts.Keys,
		BaseURL:     opts.BaseURL,
		Timeout:     opts.Timeout,
		Concurrency: opts.Concurrency,
		QPS:         opts.QPS,
		PriceSearch: opts.PriceSearch,
	}, opts.SearchDB, client)
}

func newProvider(name string, webMode bool, opts Options, rawSearchDB string, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}
	searchDB := ""
	if webMode {
		searchDB, err = normalizeSearchDB(name, rawSearchDB)
		if err != nil {
			return nil, false, err
		}
	}
	cost, err := core.ParseOptionalPriceSpec(opts.PriceSearch)
	if err != nil {
		return nil, false, err
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = DefaultSearchTimeout
	}
	conc := opts.Concurrency
	if conc <= 0 {
		conc = DefaultConcurrency
	}

	return &Provider{
		name:       name,
		webMode:    webMode,
		baseURL:    base,
		searchDB:   searchDB,
		timeout:    timeout,
		client:     client,
		pool:       pool,
		gate:       httpx.NewGate(conc, opts.QPS),
		searchCost: cost,
		nowFn:      time.Now,
	}, true, nil
}

// Name 返回 Provider 唯一标识（"zhihu" 或 "zhihu_web"）。
func (p *Provider) Name() string { return p.name }

// BaseURL 返回生效的 API 根地址。
func (p *Provider) BaseURL() string { return p.baseURL }

// Capabilities 返回该实例的静态检索能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	return p.capabilities(false)
}

// CapabilitiesFor 按本次请求选项（如 zhihu.sort）动态计算原生能力边界。
func (p *Provider) CapabilitiesFor(req *core.SearchRequest) core.Capabilities {
	if p.webMode {
		return p.capabilities(false)
	}
	hasCustomSort := false
	if req != nil && len(req.Options) > 0 {
		if rawSort := textnorm.FirstNonEmpty(req.Options[spec.OptZhihuSort], req.Options[spec.OptZhihuSortAlias]); strings.TrimSpace(rawSort) != "" {
			hasCustomSort = true
		}
	}
	return p.capabilities(hasCustomSort)
}

func (p *Provider) capabilities(hasCustomSort bool) core.Capabilities {
	if p.webMode {
		// 官方 global_search 的 host== 仅做精确主机名匹配且不含子域，
		// 声明 Sites=SiteNone 交由路由层在非严格模式下做精准后过滤（覆盖全网及 zhihu.com）。
		return core.Capabilities{
			MaxCount:  maxGlobalSearchCount,
			Sites:     core.SiteNone,
			Freshness: core.TimeBoth,
			Lang:      false,
			Content:   core.ContentFree,
			Question:  false,
		}
	}
	freshness := core.TimeBoth
	if hasCustomSort {
		freshness = core.TimeNone
	}
	return core.Capabilities{
		MaxCount:     maxZhihuSearchCount,
		Sites:        core.SiteNone,
		ScopeDomains: []string{zhihuRootDomain},
		Freshness:    freshness,
		Lang:         false,
		Content:      core.ContentFree,
		Question:     false,
	}
}

func normalizeSearchDB(providerName, raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return DefaultSearchDB, nil
	}
	switch v {
	case spec.ZhihuSearchDBAll, spec.ZhihuSearchDBRealtime, spec.ZhihuSearchDBStatic:
		return v, nil
	default:
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid zhihu_web.search_db %q (valid: all, realtime, static)", raw)).WithProvider(providerName)
	}
}
