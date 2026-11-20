package keenable

import (
	"fmt"
	"math"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/keypool"
	"veri/internal/spec"
)

// Options 配置 Keenable 适配器参数。
type Options struct {
	Keys         []string
	BaseURL      string
	Mode         string // pro | realtime
	Timeout      time.Duration
	FetchTimeout time.Duration
	Concurrency  int
	QPS          float64
	PriceCredit  string
}

// Provider 同时实现 core.Searcher 与 core.Fetcher。
type Provider struct {
	baseURL      string
	mode         string
	timeout      time.Duration
	fetchTimeout time.Duration
	client       *httpx.Client
	pool         *keypool.Pool
	gate         *httpx.Gate
	creditCost   *core.Cost
}

// New 构造 Keenable Provider；未配置密钥时返回 (nil, false, nil)，绝不静默请求免 Key 公共池。
func New(opts Options, client *httpx.Client) (*Provider, bool, error) {
	pool := keypool.New(opts.Keys...)
	if pool.Size() == 0 {
		return nil, false, nil
	}
	base, err := httpx.ResolveAPIBaseURL(opts.BaseURL, DefaultBaseURL)
	if err != nil {
		return nil, false, err
	}
	mode, err := normalizeMode(opts.Mode)
	if err != nil {
		return nil, false, err
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
		mode:         mode,
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

// Capabilities 声明 Keenable 的原生检索能力边界。
func (p *Provider) Capabilities() core.Capabilities {
	return core.Capabilities{
		MaxCount:     maxSearchCount,
		Sites:        core.SiteMulti,
		ExcludeSites: maxExcludeDomains,
		Freshness:    core.TimeBoth,
		Lang:         false,
		Content:      core.ContentFree,
		Question:     false,
	}
}

// FetchProfile 声明 Keenable Fetch 的文档与渲染特征（保守声明不误抢 PDF/动态渲染优先权）。
func (p *Provider) FetchProfile() core.FetchProfile {
	return core.FetchProfile{}
}

func (p *Provider) costFromUsage(usage *wireUsage) *core.Cost {
	if p.creditCost == nil {
		return nil
	}
	if usage != nil && usage.Paid != nil && !*usage.Paid {
		return nil
	}
	micros := p.creditCost.Micros
	if usage != nil && usage.Credits != nil && !math.IsNaN(*usage.Credits) && !math.IsInf(*usage.Credits, 0) && *usage.Credits >= 0 {
		scaled := math.Round(*usage.Credits * float64(p.creditCost.Micros))
		if scaled >= float64(math.MaxInt64) {
			micros = math.MaxInt64
		} else {
			micros = int64(scaled)
		}
	}
	if micros <= 0 {
		return nil
	}
	return &core.Cost{
		Currency:  p.creditCost.Currency,
		Micros:    micros,
		Estimated: true,
	}
}

func normalizeMode(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return DefaultMode, nil
	}
	switch v {
	case spec.KeenableModePro, spec.KeenableModeRealtime:
		return v, nil
	default:
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid keenable.mode %q (valid: pro, realtime)", raw)).WithProvider(ProviderName)
	}
}
