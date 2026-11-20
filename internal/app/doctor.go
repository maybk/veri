package app

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"time"

	"golang.org/x/sync/errgroup"

	"veri/internal/core"
	"veri/internal/httpx"
)

// LiveCheckResult 记录 doctor --live 单次真实请求连通性检测结果。
type LiveCheckResult struct {
	Provider  string `json:"provider"`
	Status    string `json:"status"` // ok | error
	LatencyMS int64  `json:"latency_ms"`
	Cost      string `json:"cost,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DoctorReport 汇总 veri doctor 的全部结构化检查结果。
type DoctorReport struct {
	Version          string            `json:"version"`
	GoVersion        string            `json:"go_version"`
	OS               string            `json:"os"`
	Arch             string            `json:"arch"`
	UILocale         string            `json:"ui_locale"`
	UILocaleSource   string            `json:"ui_locale_source"`
	ConfigPath       string            `json:"config_path"`
	ConfigExists     bool              `json:"config_exists"`
	ConfigMode       string            `json:"config_mode,omitempty"`
	PermWarning      bool              `json:"perm_warning"`
	VaultPermWarning bool              `json:"vault_perm_warning,omitzero"`
	AllowPrivate     bool              `json:"allow_private"`
	ProbeHost        string            `json:"probe_host"`
	ProbeIP          string            `json:"probe_ip,omitempty"`
	FakeIPDetected   bool              `json:"fake_ip_detected"`
	DNSError         string            `json:"dns_error,omitempty"`
	EnabledSearcher  int               `json:"enabled_searchers"`
	EnabledFetcher   int               `json:"enabled_fetchers"`
	SearchStrategy   string            `json:"search_strategy,omitempty"`
	SearchChain      []string          `json:"search_chain"`
	SearchChainEN    []string          `json:"search_chain_en,omitempty"`
	FusionChain      []string          `json:"fusion_chain,omitempty"`
	FetchStrategy    string            `json:"fetch_strategy,omitempty"`
	FetchChain       []string          `json:"fetch_chain"`
	FetchChainGlobal []string          `json:"fetch_chain_global,omitempty"`
	Providers        []ProviderStatus  `json:"providers"`
	LiveChecks       []LiveCheckResult `json:"live_checks,omitempty"`
	Issues           int               `json:"issues"`
}

// RunDoctor 执行配置、权限、Fake-IP DNS 环境及可选并发真实请求联调检查。
func (a *App) RunDoctor(ctx context.Context, uiLocale, uiSource string, live bool) DoctorReport {
	statuses := a.ProviderStatuses()
	enabledSearchers := a.EnabledSearchers()
	enabledFetchers := a.EnabledFetchers()
	if enabledSearchers == nil {
		enabledSearchers = []string{}
	}
	if enabledFetchers == nil {
		enabledFetchers = []string{}
	}
	rep := DoctorReport{
		Version:          a.version,
		GoVersion:        runtime.Version(),
		OS:               runtime.GOOS,
		Arch:             runtime.GOARCH,
		UILocale:         uiLocale,
		UILocaleSource:   uiSource,
		ConfigPath:       a.loaded.Path,
		ConfigExists:     a.loaded.FileExists,
		PermWarning:      a.loaded.PermWarning,
		VaultPermWarning: a.loaded.VaultPermWarning,
		AllowPrivate:     a.loaded.Config.Network.AllowPrivate,
		ProbeHost:        selectProbeHost(statuses),
		EnabledSearcher:  len(enabledSearchers),
		EnabledFetcher:   len(enabledFetchers),
		SearchStrategy:   string(a.cliRouter.DefaultSearchStrategy()),
		SearchChain:      enabledSearchers,
		SearchChainEN:    a.cliRouter.ActiveSearchChainEN(),
		FusionChain:      a.cliRouter.ActiveFusionChain(),
		FetchStrategy:    string(a.cliRouter.FetchStrategy()),
		FetchChain:       enabledFetchers,
		FetchChainGlobal: a.cliRouter.ActiveFetchChainGlobal(),
		Providers:        statuses,
	}
	if a.loaded.FileExists && runtime.GOOS != "windows" {
		rep.ConfigMode = fmt.Sprintf("%04o", a.loaded.FileMode.Perm())
	}
	if rep.PermWarning {
		rep.Issues++
	}
	if rep.VaultPermWarning {
		rep.Issues++
	}
	if rep.AllowPrivate {
		rep.Issues++
	}
	if rep.EnabledSearcher == 0 {
		rep.Issues++
	}

	dnsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	lookup := a.lookupNetIP
	if lookup == nil {
		lookup = net.DefaultResolver.LookupNetIP
	}
	addrs, err := lookup(dnsCtx, "ip", rep.ProbeHost)
	cancel()
	if err != nil {
		rep.DNSError = a.ScrubText(err.Error())
		rep.Issues++
	} else if len(addrs) > 0 {
		rep.ProbeIP = addrs[0].String()
		if fakeCIDRs, cErr := httpx.ParseCIDRs(a.loaded.Config.Network.FakeIPCIDRs); cErr == nil {
			rep.FakeIPDetected = httpx.IsFakeIP(addrs[0], fakeCIDRs)
		}
	}

	if live && len(enabledSearchers) > 0 {
		checks := make([]LiveCheckResult, len(enabledSearchers))
		var g errgroup.Group
		for idx, name := range enabledSearchers {
			g.Go(func() error {
				start := time.Now()
				env, sErr := a.Search(ctx, &core.SearchRequest{
					Query:     "golang",
					Count:     1,
					Detail:    core.DetailSnippet,
					Providers: []string{name},
					Strategy:  core.StrategySingle,
				})
				lat := time.Since(start).Milliseconds()
				if sErr != nil {
					checks[idx] = LiveCheckResult{
						Provider:  name,
						Status:    "error",
						LatencyMS: lat,
						Error:     a.ScrubText(sErr.Error()),
					}
					return nil
				}
				checks[idx] = LiveCheckResult{
					Provider:  name,
					Status:    "ok",
					LatencyMS: lat,
					Cost:      core.FormatCosts(env.Meta.Cost),
				}
				return nil
			})
		}
		_ = g.Wait()
		rep.LiveChecks = checks
		for _, c := range checks {
			if c.Status != "ok" {
				rep.Issues++
			}
		}
	}
	return rep
}

func selectProbeHost(statuses []ProviderStatus) string {
	for _, st := range statuses {
		if !st.Enabled || st.Endpoint == "" || st.Endpoint == "-" {
			continue
		}
		host := st.Endpoint
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host == "localhost" {
			continue
		}
		if _, err := netip.ParseAddr(host); err == nil {
			continue
		}
		return host
	}
	return "api.github.com"
}
