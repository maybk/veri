package app

import (
	"fmt"
	"net/url"
	"time"

	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/provider/brave"
	"veri/internal/provider/deepseek"
	"veri/internal/provider/exa"
	"veri/internal/provider/github"
	"veri/internal/provider/local"
	"veri/internal/provider/tavily"
	"veri/internal/provider/tencentwsa"
	"veri/internal/provider/tinyfish"
	"veri/internal/spec"
)

// ProviderStatus 描述单个内置 Provider 的启用状态、凭据来源、目标端点、超时与运行参数。
type ProviderStatus struct {
	Name     string `json:"name"`
	Role     string `json:"role"`
	Status   string `json:"status"`            // enabled | disabled
	Source   string `json:"source"`            // env:XXX | secrets.json | config.toml | missing | optional | -
	Endpoint string `json:"endpoint"`          // 目标 API 主机或 "-"
	Timeout  string `json:"timeout,omitempty"` // 生效超时（如 10s 或 10s/25s），未启用时为空
	Details  string `json:"details"`           // 生效运行参数或配置指引
	Enabled  bool   `json:"enabled"`
}

// Deps 聚合构造 Provider 所需的共享基础设施依赖。
type Deps struct {
	APIClient   *httpx.Client
	FetchClient *httpx.Client
	Version     string
}

// Factory 是显式 Provider 注册工厂函数签名（禁止使用 init() 隐式注册）。
type Factory func(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error)

var factories = []Factory{
	buildDeepSeek,
	buildExa,
	buildTinyFish,
	buildTencentWSA,
	buildTavily,
	buildBrave,
	buildGitHub,
	buildLocal,
}

func buildDeepSeek(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.DeepSeek
	cred := loaded.Credentials[spec.ProviderDeepSeek]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderDeepSeek, cred.Source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := deepseek.New(deepseek.Options{
		Keys:         cred.Keys,
		BaseURL:      cfg.BaseURL,
		Timeout:      cfg.Timeout.Duration,
		FetchTimeout: cfg.FetchTimeout.Duration,
		Concurrency:  cfg.Concurrency,
		SearchQPS:    cfg.SearchQPS,
		FetchQPS:     cfg.FetchQPS,
		PriceSearch:  cfg.PriceSearch,
		PriceFetch:   cfg.PriceFetch,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, cfg.FetchTimeout.Duration)
	details := fmt.Sprintf("concurrency=%d", cfg.Concurrency)
	return p, statusEntry(spec.ProviderDeepSeek, enabled, cred.Source, hostFromURL(cfg.BaseURL), timeout, details), nil
}

func buildExa(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.Exa
	cred := loaded.Credentials[spec.ProviderExa]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderExa, cred.Source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := exa.New(exa.Options{
		Keys:         cred.Keys,
		BaseURL:      cfg.BaseURL,
		SearchType:   cfg.SearchType,
		AllowOver10:  cfg.AllowOver10,
		Timeout:      cfg.Timeout.Duration,
		FetchTimeout: cfg.FetchTimeout.Duration,
		Concurrency:  cfg.Concurrency,
		QPS:          cfg.QPS,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, cfg.FetchTimeout.Duration)
	details := fmt.Sprintf("type=%s", cfg.SearchType)
	return p, statusEntry(spec.ProviderExa, enabled, cred.Source, hostFromURL(cfg.BaseURL), timeout, details), nil
}

func buildTinyFish(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.TinyFish
	cred := loaded.Credentials[spec.ProviderTinyFish]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderTinyFish, cred.Source, hostFromURL(cfg.SearchURL)), nil
	}
	p, enabled, err := tinyfish.New(tinyfish.Options{
		Keys:         cred.Keys,
		SearchURL:    cfg.SearchURL,
		FetchURL:     cfg.FetchURL,
		Timeout:      cfg.Timeout.Duration,
		FetchTimeout: cfg.FetchTimeout.Duration,
		Concurrency:  cfg.Concurrency,
		SearchQPS:    cfg.SearchQPS,
		FetchQPS:     cfg.FetchQPS,
		PriceSearch:  cfg.PriceSearch,
		PriceFetch:   cfg.PriceFetch,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, cfg.FetchTimeout.Duration)
	details := fmt.Sprintf("concurrency=%d", cfg.Concurrency)
	return p, statusEntry(spec.ProviderTinyFish, enabled, cred.Source, hostFromURL(cfg.SearchURL), timeout, details), nil
}

func buildTencentWSA(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.TencentWSA
	cred := loaded.Credentials[spec.ProviderTencentWSA]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderTencentWSA, cred.Source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := tencentwsa.New(tencentwsa.Options{
		Keys:        cred.Keys,
		Edition:     cfg.Edition,
		BaseURL:     cfg.BaseURL,
		Timeout:     cfg.Timeout.Duration,
		Concurrency: cfg.Concurrency,
		QPS:         cfg.QPS,
		PriceSearch: cfg.PriceSearch,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, 0)
	details := fmt.Sprintf("edition=%s", cfg.Edition)
	return p, statusEntry(spec.ProviderTencentWSA, enabled, cred.Source, hostFromURL(cfg.BaseURL), timeout, details), nil
}

func buildTavily(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.Tavily
	cred := loaded.Credentials[spec.ProviderTavily]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderTavily, cred.Source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := tavily.New(tavily.Options{
		Keys:         cred.Keys,
		BaseURL:      cfg.BaseURL,
		SearchDepth:  cfg.SearchDepth,
		ExtractDepth: cfg.ExtractDepth,
		Timeout:      cfg.Timeout.Duration,
		FetchTimeout: cfg.FetchTimeout.Duration,
		Concurrency:  cfg.Concurrency,
		QPS:          cfg.QPS,
		PriceCredit:  cfg.PriceCredit,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, cfg.FetchTimeout.Duration)
	details := fmt.Sprintf("depth=%s/%s", cfg.SearchDepth, cfg.ExtractDepth)
	return p, statusEntry(spec.ProviderTavily, enabled, cred.Source, hostFromURL(cfg.BaseURL), timeout, details), nil
}

func buildBrave(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.Brave
	cred := loaded.Credentials[spec.ProviderBrave]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderBrave, cred.Source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := brave.New(brave.Options{
		Keys:        cred.Keys,
		BaseURL:     cfg.BaseURL,
		Mode:        cfg.Mode,
		Timeout:     cfg.Timeout.Duration,
		Concurrency: cfg.Concurrency,
		QPS:         cfg.QPS,
		PriceSearch: cfg.PriceSearch,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, 0)
	details := fmt.Sprintf("mode=%s", cfg.Mode)
	return p, statusEntry(spec.ProviderBrave, enabled, cred.Source, hostFromURL(cfg.BaseURL), timeout, details), nil
}

func buildGitHub(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.GitHub
	cred := loaded.Credentials[spec.ProviderGitHub]
	source := spec.CredSourceOptional
	if len(cred.Keys) > 0 {
		source = cred.Source
	}
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderGitHub, source, hostFromURL(cfg.APIBaseURL)), nil
	}
	p, enabled, err := github.New(github.Options{
		Tokens:      cred.Keys,
		APIBaseURL:  cfg.APIBaseURL,
		RawBaseURL:  cfg.RawBaseURL,
		Timeout:     cfg.Timeout.Duration,
		Concurrency: cfg.Concurrency,
		MaxBytes:    int64(loaded.Config.Fetch.MaxBytes),
		Version:     deps.Version,
	}, deps.APIClient, deps.FetchClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	if p.HasToken() {
		source = cred.Source
	} else {
		source = spec.CredSourceOptional
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, 0)
	details := fmt.Sprintf("concurrency=%d", cfg.Concurrency)
	return p, statusEntry(spec.ProviderGitHub, enabled, source, hostFromURL(cfg.APIBaseURL), timeout, details), nil
}

func buildLocal(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.Local
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderLocal, "-", "-"), nil
	}
	fCfg := loaded.Config.Fetch
	p, enabled, err := local.New(local.Options{
		Timeout:         cfg.Timeout.Duration,
		Concurrency:     cfg.Concurrency,
		MaxBytes:        int64(fCfg.MaxBytes),
		MinContentRunes: fCfg.MinContentRunes,
		RespectRobots:   fCfg.RespectRobots,
		UserAgent:       fCfg.UserAgent,
		Version:         deps.Version,
	}, deps.FetchClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	maxBytesText, _ := fCfg.MaxBytes.MarshalText()
	timeout := formatProviderTimeout(cfg.Timeout.Duration, 0)
	details := fmt.Sprintf("min_runes=%d, max_bytes=%s", fCfg.MinContentRunes, string(maxBytesText))
	return p, statusEntry(spec.ProviderLocal, enabled, "-", "-", timeout, details), nil
}

func formatProviderTimeout(searchTimeout, fetchTimeout time.Duration) string {
	switch {
	case searchTimeout > 0 && fetchTimeout > 0 && fetchTimeout != searchTimeout:
		return fmt.Sprintf("%s/%s", searchTimeout, fetchTimeout)
	case searchTimeout > 0:
		return searchTimeout.String()
	case fetchTimeout > 0:
		return fetchTimeout.String()
	default:
		return "-"
	}
}

func disabledByConfigEntry(name, source, endpoint string) ProviderStatus {
	info, _ := spec.LookupProvider(name)
	return ProviderStatus{
		Name:     name,
		Role:     info.Roles.String(),
		Status:   "disabled",
		Source:   source,
		Endpoint: endpoint,
		Details:  spec.DetailDisabledInConfig,
		Enabled:  false,
	}
}

func hostFromURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "-"
	}
	return u.Host
}

// statusEntry 按 Provider 登记表生成状态行：角色取自登记声明，未启用且需要凭据时给出可直接执行的配置指引。
func statusEntry(name string, enabled bool, source, endpoint, timeout, details string) ProviderStatus {
	info, _ := spec.LookupProvider(name)
	st := "disabled"
	if enabled {
		st = "enabled"
	} else {
		timeout = ""
		if info.NeedsCredential() {
			details = fmt.Sprintf("run `veri key set %s` or set %s", info.Name, info.EnvVars[0])
		}
	}
	return ProviderStatus{
		Name:     name,
		Role:     info.Roles.String(),
		Status:   st,
		Source:   source,
		Endpoint: endpoint,
		Timeout:  timeout,
		Details:  details,
		Enabled:  enabled,
	}
}
