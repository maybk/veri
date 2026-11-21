package app

import (
	"fmt"

	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/provider/brave"
	"veri/internal/provider/deepseek"
	"veri/internal/provider/exa"
	"veri/internal/provider/firecrawl"
	"veri/internal/provider/github"
	"veri/internal/provider/keenable"
	"veri/internal/provider/linkup"
	"veri/internal/provider/local"
	"veri/internal/provider/parallel"
	"veri/internal/provider/tavily"
	"veri/internal/provider/tencentwsa"
	"veri/internal/provider/tinyfish"
	"veri/internal/provider/zhihu"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

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
		QPS:          cfg.QPS,
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

func buildKeenable(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.Keenable
	cred := loaded.Credentials[spec.ProviderKeenable]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderKeenable, cred.Source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := keenable.New(keenable.Options{
		Keys:         cred.Keys,
		BaseURL:      cfg.BaseURL,
		Mode:         cfg.Mode,
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
	details := fmt.Sprintf("mode=%s", textnorm.FirstNonEmpty(cfg.Mode, spec.KeenableModePro))
	return p, statusEntry(spec.ProviderKeenable, enabled, cred.Source, hostFromURL(cfg.BaseURL), timeout, details), nil
}

func buildZhihu(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.Zhihu
	cred := loaded.Credentials[spec.ProviderZhihu]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderZhihu, cred.Source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := zhihu.New(zhihu.Options{
		Keys:             cred.Keys,
		BaseURL:          cfg.BaseURL,
		Scope:            cfg.Scope,
		SearchDB:         cfg.SearchDB,
		Timeout:          cfg.Timeout.Duration,
		Concurrency:      cfg.Concurrency,
		QPS:              cfg.QPS,
		PriceSearch:      cfg.PriceSearch,
		PriceZhihuSearch: cfg.PriceZhihuSearch,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, 0)
	scope := cfg.Scope
	if scope == "" {
		scope = spec.ZhihuScopeGlobal
	}
	details := fmt.Sprintf("scope=%s (%s|%s)", scope, spec.ZhihuScopeGlobal, spec.ZhihuScopeZhihu)
	return p, statusEntry(spec.ProviderZhihu, enabled, cred.Source, hostFromURL(cfg.BaseURL), timeout, details), nil
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
		QPS:          cfg.QPS,
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

func buildLinkup(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.Linkup
	cred := loaded.Credentials[spec.ProviderLinkup]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderLinkup, cred.Source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := linkup.New(linkup.Options{
		Keys:         cred.Keys,
		BaseURL:      cfg.BaseURL,
		Depth:        cfg.Depth,
		FetchMode:    cfg.FetchMode,
		RenderJS:     cfg.RenderJS,
		Timeout:      cfg.Timeout.Duration,
		FetchTimeout: cfg.FetchTimeout.Duration,
		Concurrency:  cfg.Concurrency,
		QPS:          cfg.QPS,
		PriceSearch:  cfg.PriceSearch,
		PriceFetch:   cfg.PriceFetch,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, cfg.FetchTimeout.Duration)
	details := fmt.Sprintf("depth=%s, fetch=%s", textnorm.FirstNonEmpty(cfg.Depth, spec.LinkupDepthFast), textnorm.FirstNonEmpty(cfg.FetchMode, spec.LinkupFetchModeStandard))
	return p, statusEntry(spec.ProviderLinkup, enabled, cred.Source, hostFromURL(cfg.BaseURL), timeout, details), nil
}

func buildParallel(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.Parallel
	cred := loaded.Credentials[spec.ProviderParallel]
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderParallel, cred.Source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := parallel.New(parallel.Options{
		Keys:         cred.Keys,
		BaseURL:      cfg.BaseURL,
		Mode:         cfg.Mode,
		Timeout:      cfg.Timeout.Duration,
		FetchTimeout: cfg.FetchTimeout.Duration,
		Concurrency:  cfg.Concurrency,
		QPS:          cfg.QPS,
		PriceSearch:  cfg.PriceSearch,
		PriceFetch:   cfg.PriceFetch,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, cfg.FetchTimeout.Duration)
	details := fmt.Sprintf("mode=%s", textnorm.FirstNonEmpty(cfg.Mode, spec.ParallelModeFast))
	return p, statusEntry(spec.ProviderParallel, enabled, cred.Source, hostFromURL(cfg.BaseURL), timeout, details), nil
}

func buildFirecrawl(loaded *config.Loaded, deps Deps) (core.Provider, ProviderStatus, error) {
	cfg := loaded.Config.Providers.Firecrawl
	cred := loaded.Credentials[spec.ProviderFirecrawl]
	source := cred.Source
	if len(cred.Keys) == 0 && firecrawl.IsSelfHostedBaseURL(cfg.BaseURL) {
		source = spec.CredSourceOptional
	}
	if !cfg.Enabled {
		return nil, disabledByConfigEntry(spec.ProviderFirecrawl, source, hostFromURL(cfg.BaseURL)), nil
	}
	p, enabled, err := firecrawl.New(firecrawl.Options{
		Keys:         cred.Keys,
		BaseURL:      cfg.BaseURL,
		Proxy:        cfg.Proxy,
		MaxPDFPages:  cfg.MaxPDFPages,
		Timeout:      cfg.Timeout.Duration,
		FetchTimeout: cfg.FetchTimeout.Duration,
		Concurrency:  cfg.Concurrency,
		QPS:          cfg.QPS,
		PriceCredit:  cfg.PriceCredit,
	}, deps.APIClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	maxPDF := cfg.MaxPDFPages
	if maxPDF <= 0 {
		maxPDF = spec.DefaultFirecrawlMaxPDFPages
	}
	details := fmt.Sprintf("proxy=%s, max_pdf=%d", textnorm.FirstNonEmpty(cfg.Proxy, spec.FirecrawlProxyAuto), maxPDF)
	if p != nil && p.IsSelfHosted() {
		details += ", self_host"
		if !p.HasAPIKey() {
			source = spec.CredSourceOptional
		}
	}
	timeout := formatProviderTimeout(cfg.Timeout.Duration, cfg.FetchTimeout.Duration)
	return p, statusEntry(spec.ProviderFirecrawl, enabled, source, hostFromURL(cfg.BaseURL), timeout, details), nil
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
		MinContentChars: fCfg.MinContentChars,
		RespectRobots:   fCfg.RespectRobots,
		UserAgent:       fCfg.UserAgent,
		Version:         deps.Version,
	}, deps.FetchClient)
	if err != nil {
		return nil, ProviderStatus{}, err
	}
	maxBytesText, _ := fCfg.MaxBytes.MarshalText()
	timeout := formatProviderTimeout(cfg.Timeout.Duration, 0)
	details := fmt.Sprintf("min_chars=%d, max_bytes=%s", fCfg.MinContentChars, string(maxBytesText))
	return p, statusEntry(spec.ProviderLocal, enabled, "-", "-", timeout, details), nil
}
