package app

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
)

const (
	defaultLiveLocalProbeURL  = "https://example.com"
	defaultLiveGitHubProbeURL = "https://github.com/octocat/Hello-World/blob/master/README"
	liveProbeDisabledSentinel = "-"
)

type lookupFuncResolver func(ctx context.Context, network, host string) ([]netip.Addr, error)

func (fn lookupFuncResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	return fn(ctx, network, host)
}

// LiveCheckResult 记录 doctor --live 单次真实请求连通性检测结果。
type LiveCheckResult struct {
	Provider  string `json:"provider"`
	Kind      string `json:"kind,omitempty"` // search | fetch
	Status    string `json:"status"`         // ok | error
	LatencyMS int64  `json:"latency_ms"`
	Cost      string `json:"cost,omitempty"`
	Error     string `json:"error,omitempty"`
}

// DoctorReport 汇总 veri doctor 的全部结构化检查结果。
type DoctorReport struct {
	Version             string            `json:"version"`
	GoVersion           string            `json:"go_version"`
	OS                  string            `json:"os"`
	Arch                string            `json:"arch"`
	UILocale            string            `json:"ui_locale"`
	UILocaleSource      string            `json:"ui_locale_source"`
	ConfigPath          string            `json:"config_path"`
	ConfigExists        bool              `json:"config_exists"`
	ConfigMode          string            `json:"config_mode,omitempty"`
	SecretsPath         string            `json:"secrets_path"`
	SecretsExists       bool              `json:"secrets_exists"`
	PermWarning         bool              `json:"perm_warning"`
	VaultPermWarning    bool              `json:"vault_perm_warning"`
	PlaintextProviders  []string          `json:"plaintext_providers,omitempty"`
	AllowPrivate        bool              `json:"allow_private"`
	FakeIPAuto          bool              `json:"fake_ip_auto"`
	ProbeHost           string            `json:"probe_host"`
	ProbeIP             string            `json:"probe_ip,omitempty"`
	FakeIPDetected      bool              `json:"fake_ip_detected"`
	DetectedFakeIPCIDRs []string          `json:"detected_fake_ip_cidrs,omitempty"`
	FakeIPBlocked       bool              `json:"fake_ip_blocked,omitempty"`
	SuggestedFakeIPCIDR string            `json:"suggested_fake_ip_cidr,omitempty"`
	DNSError            string            `json:"dns_error,omitempty"`
	EnabledSearcher     int               `json:"enabled_searchers"`
	EnabledFetcher      int               `json:"enabled_fetchers"`
	SearchStrategy      string            `json:"search_strategy,omitempty"`
	SearchChain         []string          `json:"search_chain"`
	SearchChainGlobal   []string          `json:"search_chain_global,omitempty"`
	FusionChain         []string          `json:"fusion_chain,omitempty"`
	FusionChainGlobal   []string          `json:"fusion_chain_global,omitempty"`
	FetchStrategy       string            `json:"fetch_strategy,omitempty"`
	FetchChain          []string          `json:"fetch_chain"`
	FetchChainGlobal    []string          `json:"fetch_chain_global,omitempty"`
	Providers           []ProviderStatus  `json:"providers"`
	LiveChecks          []LiveCheckResult `json:"live_checks,omitempty"`
	Issues              int               `json:"issues"`
}

// RunDoctor 执行配置、凭据路径、权限、透明代理 Fake-IP 环境及可选并发真实请求联调检查。
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
		Version:            a.version,
		GoVersion:          runtime.Version(),
		OS:                 runtime.GOOS,
		Arch:               runtime.GOARCH,
		UILocale:           uiLocale,
		UILocaleSource:     uiSource,
		ConfigPath:         a.loaded.Path,
		ConfigExists:       a.loaded.FileExists,
		SecretsPath:        a.loaded.SecretsPath,
		SecretsExists:      a.loaded.SecretsExists,
		PermWarning:        a.loaded.PermWarning,
		VaultPermWarning:   a.loaded.VaultPermWarning,
		PlaintextProviders: slices.Clone(a.loaded.PlaintextProviders),
		AllowPrivate:       a.loaded.Config.Network.AllowPrivate,
		FakeIPAuto:         a.loaded.Config.Network.FakeIPAuto,
		ProbeHost:          selectProbeHost(statuses),
		EnabledSearcher:    len(enabledSearchers),
		EnabledFetcher:     len(enabledFetchers),
		SearchStrategy:     string(a.cliRouter.DefaultSearchStrategy()),
		SearchChain:        enabledSearchers,
		SearchChainGlobal:  a.cliRouter.ActiveSearchChainGlobal(),
		FusionChain:        a.cliRouter.ActiveFusionChain(),
		FusionChainGlobal:  a.cliRouter.ActiveFusionChainGlobal(),
		FetchStrategy:      string(a.cliRouter.FetchStrategy()),
		FetchChain:         enabledFetchers,
		FetchChainGlobal:   a.cliRouter.ActiveFetchChainGlobal(),
		Providers:          statuses,
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
	fakeCIDRs, _ := httpx.ParseCIDRs(a.loaded.Config.Network.FakeIPCIDRs)
	diag := httpx.DiagnoseNetwork(dnsCtx, lookupFuncResolver(lookup), rep.ProbeHost, fakeCIDRs, rep.FakeIPAuto)
	cancel()

	rep.ProbeHost = diag.ProbeHost
	rep.ProbeIP = diag.ProbeIP
	switch diag.Status {
	case httpx.NetworkDiagDNSError:
		rep.DNSError = a.ScrubText(diag.DNSError)
		rep.Issues++
	case httpx.NetworkDiagFakeIPConfigured:
		rep.FakeIPDetected = true
	case httpx.NetworkDiagFakeIPAuto:
		rep.FakeIPDetected = true
		rep.DetectedFakeIPCIDRs = slices.Clone(diag.DetectedFakeIPCIDRs)
	case httpx.NetworkDiagNonPublicBlocked:
		rep.SuggestedFakeIPCIDR = diag.SuggestedFakeIPCIDR
		if !rep.AllowPrivate {
			rep.FakeIPBlocked = true
			rep.Issues++
		}
	}

	if live {
		type liveTask struct {
			provider string
			kind     string
			fetchURL string
		}
		tasks := make([]liveTask, 0, len(enabledSearchers)+2)
		for _, name := range enabledSearchers {
			tasks = append(tasks, liveTask{provider: name, kind: spec.KindSearch})
		}
		if slices.Contains(enabledFetchers, spec.ProviderGitHub) && a.liveGitHubProbeURL != liveProbeDisabledSentinel {
			u := a.liveGitHubProbeURL
			if u == "" {
				u = defaultLiveGitHubProbeURL
			}
			tasks = append(tasks, liveTask{provider: spec.ProviderGitHub, kind: spec.KindFetch, fetchURL: u})
		}
		if slices.Contains(enabledFetchers, spec.ProviderLocal) && a.liveLocalProbeURL != liveProbeDisabledSentinel {
			u := a.liveLocalProbeURL
			if u == "" {
				u = defaultLiveLocalProbeURL
			}
			tasks = append(tasks, liveTask{provider: spec.ProviderLocal, kind: spec.KindFetch, fetchURL: u})
		}

		if len(tasks) > 0 {
			checks := make([]LiveCheckResult, len(tasks))
			var g errgroup.Group
			g.SetLimit(spec.DefaultBatchFetchConcurrency)
			for idx, task := range tasks {
				g.Go(func() error {
					start := time.Now()
					var (
						env  *core.Envelope
						cErr error
					)
					if task.kind == spec.KindFetch {
						// 直接走 runCLIFetch，避免诊断探针将临时测试页写入用户本地磁盘缓存。
						env, cErr = a.runCLIFetch(ctx, &core.FetchRequest{
							URL:       task.fetchURL,
							Providers: []string{task.provider},
							MaxChars:  512,
							Options:   map[string]string{spec.OptFetchFresh: "true"},
						})
					} else {
						env, cErr = a.Search(ctx, &core.SearchRequest{
							Query:     "golang",
							Count:     1,
							Detail:    core.DetailSnippet,
							Providers: []string{task.provider},
							Strategy:  core.StrategySingle,
							NoCache:   true,
						})
					}
					lat := time.Since(start).Milliseconds()
					if cErr != nil {
						checks[idx] = LiveCheckResult{
							Provider:  task.provider,
							Kind:      task.kind,
							Status:    "error",
							LatencyMS: lat,
							Error:     a.ScrubText(cErr.Error()),
						}
						return nil
					}
					checks[idx] = LiveCheckResult{
						Provider:  task.provider,
						Kind:      task.kind,
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
		lower := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
		if lower == "" || lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
			continue
		}
		if _, err := netip.ParseAddr(lower); err == nil {
			continue
		}
		return host
	}
	return "api.github.com"
}
