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

// Options 配置知乎开放平台适配器参数。
type Options struct {
	Keys             []string
	BaseURL          string
	Scope            string // global | zhihu
	SearchDB         string // all | realtime | static
	Timeout          time.Duration
	Concurrency      int
	QPS              float64
	PriceSearch      string
	PriceZhihuSearch string
}

// Provider 实现 core.Searcher 与 core.DynamicSearcher。
type Provider struct {
	baseURL          string
	scope            string
	searchDB         string
	timeout          time.Duration
	client           *httpx.Client
	pool             *keypool.Pool
	gate             *httpx.Gate
	globalSearchCost *core.Cost
	zhihuSearchCost  *core.Cost
	nowFn            func() time.Time
}

// New 构造知乎开放平台 Provider；未配置密钥时返回 (nil, false, nil)。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}
	scope, err := normalizeScope(opts.Scope)
	if err != nil {
		return nil, false, err
	}
	searchDB, err := normalizeSearchDB(opts.SearchDB)
	if err != nil {
		return nil, false, err
	}
	gCost, err := core.ParseOptionalPriceSpec(opts.PriceSearch)
	if err != nil {
		return nil, false, err
	}
	zCost, err := core.ParseOptionalPriceSpec(opts.PriceZhihuSearch)
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
		baseURL:          base,
		scope:            scope,
		searchDB:         searchDB,
		timeout:          timeout,
		client:           client,
		pool:             pool,
		gate:             httpx.NewGate(conc, opts.QPS),
		globalSearchCost: gCost,
		zhihuSearchCost:  zCost,
		nowFn:            time.Now,
	}, true, nil
}

// Name 返回 Provider 唯一标识。
func (p *Provider) Name() string { return ProviderName }

// BaseURL 返回生效的 API 根地址。
func (p *Provider) BaseURL() string { return p.baseURL }

// Capabilities 返回当前默认作用域下的检索能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	return capabilitiesForScope(p.scope, false)
}

// CapabilitiesFor 按本次请求选项（zhihu.scope / zhihu.sort）动态计算原生能力边界。
func (p *Provider) CapabilitiesFor(req *core.SearchRequest) core.Capabilities {
	scope := p.scope
	hasCustomSort := false
	if req != nil && len(req.Options) > 0 {
		if rawScope := textnorm.FirstNonEmpty(req.Options[spec.OptZhihuScope], req.Options[spec.OptZhihuScopeAlias]); rawScope != "" {
			if s, err := normalizeScope(rawScope); err == nil {
				scope = s
			}
		}
		if rawSort := textnorm.FirstNonEmpty(req.Options[spec.OptZhihuSort], req.Options[spec.OptZhihuSortAlias]); strings.TrimSpace(rawSort) != "" {
			hasCustomSort = true
		}
	}
	return capabilitiesForScope(scope, hasCustomSort)
}

func capabilitiesForScope(scope string, hasCustomSort bool) core.Capabilities {
	// 官方 global_search 的 host== 仅做精确主机名匹配不含子域且禁止 zhihu.com，
	// 因此统一声明 Sites=SiteNone 交由路由层精确子域后过滤，保持跨源 -s 语义一致。
	if scope == spec.ZhihuScopeZhihu {
		freshness := core.TimeBoth
		if hasCustomSort {
			freshness = core.TimeNone
		}
		return core.Capabilities{
			MaxCount:  maxZhihuSearchCount,
			Sites:     core.SiteNone,
			Freshness: freshness,
			Lang:      false,
			Content:   core.ContentFree,
			Question:  false,
		}
	}
	return core.Capabilities{
		MaxCount:  maxGlobalSearchCount,
		Sites:     core.SiteNone,
		Freshness: core.TimeBoth,
		Lang:      false,
		Content:   core.ContentFree,
		Question:  false,
	}
}

func normalizeScope(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return DefaultScope, nil
	}
	switch v {
	case spec.ZhihuScopeGlobal, spec.ZhihuScopeZhihu:
		return v, nil
	default:
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid zhihu.scope %q (valid: global, zhihu)", raw)).WithProvider(ProviderName)
	}
}

func normalizeSearchDB(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return DefaultSearchDB, nil
	}
	switch v {
	case spec.ZhihuSearchDBAll, spec.ZhihuSearchDBRealtime, spec.ZhihuSearchDBStatic:
		return v, nil
	default:
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid zhihu.search_db %q (valid: all, realtime, static)", raw)).WithProvider(ProviderName)
	}
}
