package config

import (
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"reflect"
	"strings"

	"veri/clikit/redact"
	"veri/internal/core"
	"veri/internal/spec"
)

func validateConfig(cfg *Config) error {
	if !isValidUILangSetting(cfg.UI.Lang) {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid ui.lang %q: must be auto, zh, or en", cfg.UI.Lang))
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
	if cfg.Search.SummaryMaxRunes < 0 || cfg.Search.ContentMaxRunes < 0 || cfg.Search.MaxChars < 0 {
		return core.NewError(core.CodeInvalidArgument, "search rune/char budgets must be >= 0")
	}
	var err error
	if cfg.Search.Providers, err = normalizeProviderList("search.providers", cfg.Search.Providers, spec.RoleSearch); err != nil {
		return err
	}
	if cfg.Search.ProvidersEN, err = normalizeProviderList("search.providers_en", cfg.Search.ProvidersEN, spec.RoleSearch); err != nil {
		return err
	}
	if cfg.Search.FusionProviders, err = normalizeProviderList("search.fusion_providers", cfg.Search.FusionProviders, spec.RoleSearch); err != nil {
		return err
	}

	if _, err := core.ParseFetchStrategy(cfg.Fetch.Strategy); err != nil {
		return err
	}
	if cfg.Fetch.MaxBytes <= 0 {
		return core.NewError(core.CodeInvalidArgument, "fetch.max_bytes must be > 0")
	}
	if cfg.Fetch.MinContentRunes < 0 || cfg.Fetch.MaxChars < 0 {
		return core.NewError(core.CodeInvalidArgument, "fetch.min_content_runes and fetch.max_chars must be >= 0")
	}
	if cfg.Fetch.Providers, err = normalizeProviderList("fetch.providers", cfg.Fetch.Providers, spec.RoleFetch); err != nil {
		return err
	}
	if cfg.Fetch.ProvidersGlobal, err = normalizeProviderList("fetch.providers_global", cfg.Fetch.ProvidersGlobal, spec.RoleFetch); err != nil {
		return err
	}

	switch strings.ToLower(strings.TrimSpace(cfg.MCP.TextFormat)) {
	case spec.TextFormatMarkdown, spec.TextFormatJSON:
	default:
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid mcp.text_format %q: must be markdown or json", cfg.MCP.TextFormat))
	}
	if cfg.MCP.SearchMaxChars < 0 || cfg.MCP.FetchMaxChars < 0 || cfg.MCP.ContentMaxRunes < 0 || cfg.MCP.MaxCount < 0 || cfg.MCP.CacheSize < 0 {
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
			return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown provider %q in %s", raw, field))
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
	switch strings.ToLower(strings.TrimSpace(p.TencentWSA.Edition)) {
	case spec.WSAEditionLite, spec.WSAEditionStandard, spec.WSAEditionFlagship:
	default:
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid providers.tencent_wsa.edition %q: must be lite, standard, or flagship", p.TencentWSA.Edition))
	}

	switch strings.ToLower(strings.TrimSpace(p.Exa.SearchType)) {
	case spec.ExaTypeLite, spec.ExaTypeAuto, spec.ExaTypeFast, spec.ExaTypeInstant, spec.ExaTypeDeepLite, spec.ExaTypeDeep, spec.ExaTypeDeepReasoning:
	default:
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid providers.exa.search_type %q: must be lite, auto, fast, instant, deep-lite, deep, or deep-reasoning", p.Exa.SearchType))
	}

	switch strings.ToLower(strings.TrimSpace(p.Tavily.SearchDepth)) {
	case spec.TavilyDepthUltraFast, spec.TavilyDepthFast, spec.TavilyDepthBasic, spec.TavilyDepthAdvanced:
	default:
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid providers.tavily.search_depth %q", p.Tavily.SearchDepth))
	}
	switch strings.ToLower(strings.TrimSpace(p.Tavily.ExtractDepth)) {
	case spec.TavilyDepthBasic, spec.TavilyDepthAdvanced:
	default:
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid providers.tavily.extract_depth %q", p.Tavily.ExtractDepth))
	}

	switch strings.ToLower(strings.TrimSpace(p.Brave.Mode)) {
	case spec.BraveModeAuto, spec.BraveModeContext, spec.BraveModeWeb:
	default:
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid providers.brave.mode %q: must be auto, context, or web", p.Brave.Mode))
	}

	return walkProviderFields(p, func(provName, fieldTag string, val reflect.Value) error {
		switch {
		case fieldTag == "weight" && val.Kind() == reflect.Float64:
			w := val.Float()
			if w <= 0 || math.IsNaN(w) || math.IsInf(w, 0) {
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("providers.%s.weight must be a positive finite number", provName))
			}
		case (fieldTag == "qps" || strings.HasSuffix(fieldTag, "_qps")) && val.Kind() == reflect.Float64:
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
		}
		return nil
	})
}
