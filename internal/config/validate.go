package config

import (
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"veri/clikit/redact"
	"veri/internal/core"
	"veri/internal/spec"
)

// strictEnumTable 维护所有封闭字符串枚举配置项的唯一合法值表，供配置校验与 CLI 补全共同引用。
var strictEnumTable = map[string][]string{
	"mcp.text_format":               {spec.TextFormatMarkdown, spec.TextFormatJSON},
	"providers.tencent_wsa.edition": {spec.WSAEditionLite, spec.WSAEditionStandard, spec.WSAEditionFlagship},
	"providers.exa.search_type":     {spec.ExaTypeLite, spec.ExaTypeAuto, spec.ExaTypeFast, spec.ExaTypeInstant, spec.ExaTypeDeepLite, spec.ExaTypeDeep, spec.ExaTypeDeepReasoning},
	"providers.tavily.search_depth": {spec.TavilyDepthUltraFast, spec.TavilyDepthFast, spec.TavilyDepthBasic, spec.TavilyDepthAdvanced},
	"providers.tavily.extract_depth": {
		spec.TavilyDepthBasic, spec.TavilyDepthAdvanced,
	},
	"providers.brave.mode":        {spec.BraveModeAuto, spec.BraveModeContext, spec.BraveModeWeb},
	"providers.keenable.mode":     {spec.KeenableModePro, spec.KeenableModeRealtime},
	"providers.zhihu.scope":       {spec.ZhihuScopeGlobal, spec.ZhihuScopeZhihu},
	"providers.zhihu.search_db":   {spec.ZhihuSearchDBAll, spec.ZhihuSearchDBRealtime, spec.ZhihuSearchDBStatic},
	"providers.linkup.depth":      {spec.LinkupDepthFlash, spec.LinkupDepthFast, spec.LinkupDepthStandard, spec.LinkupDepthDeep},
	"providers.linkup.fetch_mode": {spec.LinkupFetchModeStandard, spec.LinkupFetchModePro},
	"providers.parallel.mode":     {spec.ParallelModeTurbo, spec.ParallelModeFast, spec.ParallelModeBasic, spec.ParallelModeAdvanced},
	"providers.firecrawl.proxy":   {spec.FirecrawlProxyAuto, spec.FirecrawlProxyBasic, spec.FirecrawlProxyEnhanced},
}

func strictEnumAllowed(key string) []string {
	return strictEnumTable[key]
}

func validateEnumValue(key, raw string) error {
	allowed := strictEnumAllowed(key)
	if slices.Contains(allowed, strings.ToLower(strings.TrimSpace(raw))) {
		return nil
	}
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid %s %q: must be one of %s", key, scrubConfigScalar(raw), strings.Join(allowed, ", ")))
}

func scrubConfigScalar(raw string) string {
	if redact.LooksLikeSecret(raw) {
		return redact.MaskKey(raw)
	}
	return redact.Scrub(raw, nil)
}

func validateConfig(cfg *Config) error {
	if !isValidUILangSetting(cfg.UI.Lang) {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid ui.lang %q: must be auto, zh, or en", scrubConfigScalar(cfg.UI.Lang)))
	}

	if _, err := core.ParseStrategy(cfg.Search.Strategy); err != nil {
		return err
	}
	if _, err := core.ParseDetail(cfg.Search.Detail); err != nil {
		return err
	}
	if cfg.Search.Count < spec.MinSearchCount || cfg.Search.Count > spec.MaxSearchCount {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("search.count %d out of range [%d, %d]", cfg.Search.Count, spec.MinSearchCount, spec.MaxSearchCount))
	}
	if cfg.Search.FusionK < 1 {
		return core.NewError(core.CodeInvalidArgument, "search.fusion_k must be >= 1")
	}
	if cfg.Search.SummaryMaxChars < 0 || cfg.Search.ContentMaxChars < 0 || cfg.Search.MaxChars < 0 {
		return core.NewError(core.CodeInvalidArgument, "search char budgets must be >= 0")
	}
	var err error
	if cfg.Search.Providers, err = normalizeProviderList("search.providers", cfg.Search.Providers, spec.RoleSearch); err != nil {
		return err
	}
	if cfg.Search.ProvidersGlobal, err = normalizeProviderList("search.providers_global", cfg.Search.ProvidersGlobal, spec.RoleSearch); err != nil {
		return err
	}
	if cfg.Search.FusionProviders, err = normalizeProviderList("search.fusion_providers", cfg.Search.FusionProviders, spec.RoleSearch); err != nil {
		return err
	}
	if cfg.Search.FusionProvidersGlobal, err = normalizeProviderList("search.fusion_providers_global", cfg.Search.FusionProvidersGlobal, spec.RoleSearch); err != nil {
		return err
	}

	if _, err := core.ParseFetchStrategy(cfg.Fetch.Strategy); err != nil {
		return err
	}
	if cfg.Fetch.MaxBytes <= 0 {
		return core.NewError(core.CodeInvalidArgument, "fetch.max_bytes must be > 0")
	}
	if cfg.Fetch.MinContentChars < 0 || cfg.Fetch.MaxChars < 0 {
		return core.NewError(core.CodeInvalidArgument, "fetch.min_content_chars and fetch.max_chars must be >= 0")
	}
	if cfg.Fetch.Providers, err = normalizeProviderList("fetch.providers", cfg.Fetch.Providers, spec.RoleFetch); err != nil {
		return err
	}
	if cfg.Fetch.ProvidersGlobal, err = normalizeProviderList("fetch.providers_global", cfg.Fetch.ProvidersGlobal, spec.RoleFetch); err != nil {
		return err
	}

	if err := validateEnumValue("mcp.text_format", cfg.MCP.TextFormat); err != nil {
		return err
	}
	if cfg.MCP.SearchMaxChars < 0 || cfg.MCP.FetchMaxChars < 0 || cfg.MCP.ContentMaxChars < 0 || cfg.MCP.MaxCount < 0 || cfg.MCP.CacheSize < 0 {
		return core.NewError(core.CodeInvalidArgument, "mcp budget, max_count, and cache_size settings must be >= 0")
	}

	if err := validateNetwork(&cfg.Network); err != nil {
		return err
	}
	return validateProviders(&cfg.Providers)
}

func normalizeProviderList(field string, items []string, wantRole spec.Role) ([]string, error) {
	if len(items) == 0 {
		return items, nil
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		info, ok := spec.LookupProvider(raw)
		if !ok {
			return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown provider %q in %s", scrubConfigScalar(raw), field))
		}
		if !info.Roles.Has(wantRole) {
			return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("provider %q in %s does not support %s", info.Name, field, wantRole))
		}
		out = append(out, info.Name)
	}
	return out, nil
}

func isValidUILangSetting(raw string) bool {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	if cleaned == "" || cleaned == "auto" || cleaned == "system" {
		return true
	}
	if strings.ContainsAny(cleaned, `"'()[] ,`) {
		return false
	}
	if idx := strings.IndexAny(cleaned, ".@;"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	cleaned = strings.ReplaceAll(cleaned, "_", "-")
	base, _, _ := strings.Cut(cleaned, "-")
	return base == "zh" || base == "en"
}

func validateNetwork(netCfg *NetworkConfig) error {
	if proxy := strings.TrimSpace(netCfg.Proxy); proxy != "" {
		u, err := url.Parse(proxy)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid network.proxy %q", redact.SanitizeURL(netCfg.Proxy)))
		}
		switch strings.ToLower(u.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported network.proxy scheme %q", u.Scheme))
		}
	}
	for _, cidr := range netCfg.FakeIPCIDRs {
		trimmed := strings.TrimSpace(cidr)
		if trimmed == "" {
			continue
		}
		if _, err := netip.ParsePrefix(trimmed); err != nil {
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid network.fake_ip_cidrs entry %q", cidr))
		}
	}
	return nil
}

func validateProviders(p *ProvidersConfig) error {
	enumChecks := [][2]string{
		{"providers.tencent_wsa.edition", p.TencentWSA.Edition},
		{"providers.exa.search_type", p.Exa.SearchType},
		{"providers.tavily.search_depth", p.Tavily.SearchDepth},
		{"providers.tavily.extract_depth", p.Tavily.ExtractDepth},
		{"providers.brave.mode", p.Brave.Mode},
		{"providers.keenable.mode", p.Keenable.Mode},
		{"providers.zhihu.scope", p.Zhihu.Scope},
		{"providers.zhihu.search_db", p.Zhihu.SearchDB},
		{"providers.linkup.depth", p.Linkup.Depth},
		{"providers.linkup.fetch_mode", p.Linkup.FetchMode},
		{"providers.parallel.mode", p.Parallel.Mode},
		{"providers.firecrawl.proxy", p.Firecrawl.Proxy},
	}
	for _, check := range enumChecks {
		if err := validateEnumValue(check[0], check[1]); err != nil {
			return err
		}
	}
	if p.Firecrawl.MaxPDFPages < 1 || p.Firecrawl.MaxPDFPages > spec.MaxFirecrawlPDFPages {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("providers.firecrawl.max_pdf_pages %d out of range [1, %d]", p.Firecrawl.MaxPDFPages, spec.MaxFirecrawlPDFPages))
	}

	return walkProviderFields(p, func(provName, fieldTag string, val reflect.Value) error {
		switch {
		case fieldTag == "weight" && val.Kind() == reflect.Float64:
			w := val.Float()
			if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("providers.%s.weight must be a positive finite number", provName))
			}
		case fieldTag == "qps" && val.Kind() == reflect.Float64:
			q := val.Float()
			if q < 0 || math.IsNaN(q) || math.IsInf(q, 0) {
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("providers.%s.%s must be a finite number >= 0", provName, fieldTag))
			}
		case fieldTag == "concurrency" && val.Kind() == reflect.Int:
			if val.Int() < 0 {
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("providers.%s.concurrency must be >= 0", provName))
			}
		case strings.HasPrefix(fieldTag, "price_") && val.Kind() == reflect.String:
			if priceSpec := strings.TrimSpace(val.String()); priceSpec != "" {
				if _, err := core.ParsePriceSpec(priceSpec); err != nil {
					return core.NewError(core.CodeInvalidArgument, err.Error())
				}
			}
		case isURLTOMLTag(fieldTag) && val.Kind() == reflect.String:
			if rawURL := strings.TrimSpace(val.String()); rawURL != "" {
				if err := validateProviderURL(provName, fieldTag, rawURL); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func validateProviderURL(provName, fieldTag, rawURL string) error {
	safeDisplay := redact.SanitizeURL(rawURL)
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid providers.%s.%s %q", provName, fieldTag, safeDisplay))
	}
	if u.User != nil {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("providers.%s.%s %q must not contain credentials", provName, fieldTag, safeDisplay))
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "https" || (scheme == "http" && isLoopbackHost(u.Hostname())) {
		return nil
	}
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("providers.%s.%s %q must use https (http is only allowed for loopback hosts)", provName, fieldTag, safeDisplay))
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip, err := netip.ParseAddr(h); err == nil && ip.Zone() == "" {
		return ip.Unmap().IsLoopback()
	}
	return false
}
