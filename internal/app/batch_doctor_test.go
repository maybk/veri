package app

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/router"
	"veri/internal/spec"
)

func newTestAppWithServer(t *testing.T, extraTOML string, handler http.HandlerFunc) (*App, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	cacheDir := filepath.Join(tmp, "cache")
	cfgBody := fmt.Sprintf(`
[search]
providers = ["deepseek"]

[fetch]
strategy = "api_first"
providers = ["deepseek"]

[providers.deepseek]
api_key = "sk-appsecret999988887777"
base_url = %q

[providers.local]
enabled = true

[network]
allow_private = true
%s
`, srv.URL, extraTOML)
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == spec.EnvCacheDir {
			return cacheDir
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	application, err := NewWithStderr(loaded, "1.0.0", nil)
	if err != nil {
		t.Fatalf("NewWithStderr: %v", err)
	}
	return application, srv
}

func TestFetchBatchCLIAndMCPPaths(t *testing.T) {
	application, srv := newTestAppWithServer(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/fetch":
			var body struct {
				URL string `json:"url"`
			}
			_ = json.UnmarshalRead(r.Body, &body)
			switch {
			case strings.Contains(body.URL, "/ok1"):
				_, _ = w.Write([]byte(`{"title":"Page One","content":"Content one with secret sk-appsecret999988887777 redacted and sufficient length for quality gate."}`))
			case strings.Contains(body.URL, "/ok2"):
				_, _ = w.Write([]byte(`{"title":"Page Two","content":"Content two with enough text for extraction verification."}`))
			default:
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"not found for sk-appsecret999988887777"}}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	// 1. 空 URL 列表 -> invalid_argument
	if env, err := application.FetchBatch(context.Background(), []string{" ", ""}, nil); err == nil || env.OK || core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for empty batch, got env=%+v err=%v", env, err)
	}

	// 2. 超过最大上限 -> invalid_argument
	tooMany := make([]string, spec.MaxMCPBatchFetchURLs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("https://example.org/p%d", i)
	}
	if _, err := application.FetchBatchMCP(context.Background(), tooMany, nil); err == nil || core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument when exceeding batch max, got %v", err)
	}

	// 3. 单 URL 自动委托单页 Fetch / FetchMCP（返回 Page 而非 Pages）
	singleEnv, err := application.FetchBatch(context.Background(), []string{"https://example.org/ok1"}, &core.FetchRequest{Providers: []string{spec.ProviderDeepSeek}})
	if err != nil || !singleEnv.OK || singleEnv.Page == nil || len(singleEnv.Pages) != 0 {
		t.Fatalf("expected single-URL batch delegation to Page, got env=%+v err=%v", singleEnv, err)
	}

	singleMCP, err := application.FetchBatchMCP(context.Background(), []string{"https://example.org/ok1"}, &core.FetchRequest{Providers: []string{spec.ProviderDeepSeek}})
	if err != nil || !singleMCP.OK || singleMCP.Page == nil {
		t.Fatalf("expected single-URL MCP batch delegation, got env=%+v err=%v", singleMCP, err)
	}

	// 4. 多 URL 混合部分失败：严格保序、partial=true、报错信息脱敏生效、含空行/去重
	urls := []string{"https://example.org/ok1", "https://example.org/missing", "https://example.org/ok2", "https://example.org/ok1"}
	batchEnv, err := application.FetchBatch(context.Background(), urls, &core.FetchRequest{
		Providers: []string{spec.ProviderDeepSeek},
		MaxChars:  4000,
	})
	if err != nil || !batchEnv.OK || !batchEnv.Meta.Partial || len(batchEnv.Pages) != 3 {
		t.Fatalf("expected 3 deduped ordered pages with partial=true, got env=%+v err=%v", batchEnv, err)
	}
	if batchEnv.Pages[0].Title != "Page One" || batchEnv.Pages[1].Error == nil || batchEnv.Pages[2].Title != "Page Two" {
		t.Fatalf("batch order or error mismatch: %+v", batchEnv.Pages)
	}
	if strings.Contains(batchEnv.Pages[1].Error.Message, "sk-appsecret999988887777") {
		t.Fatalf("secret leaked in batch page error message: %s", batchEnv.Pages[1].Error.Message)
	}

	// 5. MCP 批量抓取复用缓存与预算均摊
	mcpBatch, err := application.FetchBatchMCP(context.Background(), []string{"https://example.org/ok1", "https://example.org/ok2"}, &core.FetchRequest{
		Providers: []string{spec.ProviderDeepSeek},
	})
	if err != nil || !mcpBatch.OK || len(mcpBatch.Pages) != 2 {
		t.Fatalf("MCP batch failed: env=%+v err=%v", mcpBatch, err)
	}

	// 6. 全部 URL 均失败时返回 ok=false 与错误
	allFailEnv, allFailErr := application.FetchBatch(context.Background(), []string{"https://example.org/m1", "https://example.org/m2"}, &core.FetchRequest{
		Providers: []string{spec.ProviderDeepSeek},
	})
	if allFailErr == nil || allFailEnv.OK || len(allFailEnv.Pages) != 2 {
		t.Fatalf("expected all-fail batch error with per-page errors, got env=%+v err=%v", allFailEnv, allFailErr)
	}
	_ = srv
}

func TestRunDoctorResolverStatesAndLiveChecks(t *testing.T) {
	mode := "ok"
	application, srv := newTestAppWithServer(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/search":
			if mode == "fail" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"bad key sk-appsecret999988887777"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev","snippet":"Go lang"}]}`))
		case "/local-probe":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html><head><title>Probe</title></head><body><main><h1>Probe OK</h1><p>Local fetch live probe works cleanly.</p></main></body></html>"))
		}
	})
	application.liveLocalProbeURL = srv.URL + "/local-probe"
	application.liveGitHubProbeURL = liveProbeDisabledSentinel

	// 1. 静态 Fake-IP DNS 识别与凭据库路径展示
	application.lookupNetIP = func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("198.18.0.42")}, nil
	}
	repFake := application.RunDoctor(context.Background(), "zh-CN", "flag", false)
	wantMode := "0600"
	if runtime.GOOS == "windows" {
		wantMode = ""
	}
	if !repFake.FakeIPDetected || repFake.ProbeIP != "198.18.0.42" || repFake.EnabledSearcher != 1 || repFake.ConfigMode != wantMode || repFake.SecretsPath == "" {
		t.Fatalf("unexpected Fake-IP doctor report: %+v", repFake)
	}
	// allow_private=true 计 1 项 issue
	if repFake.Issues != 1 {
		t.Fatalf("expected 1 issue for allow_private=true, got %d", repFake.Issues)
	}

	// 1b. 自动探测 127.255.0.0/16 透明代理，与关闭自动探测时的 FakeIPBlocked 告警
	winMap := map[string]string{
		"api.github.com": "127.255.0.1",
		"example.com":    "127.255.0.2",
		"github.com":     "127.255.0.3",
		"cloudflare.com": "127.255.0.4",
	}
	application.lookupNetIP = func(_ context.Context, _, host string) ([]netip.Addr, error) {
		if ip, ok := winMap[host]; ok {
			return []netip.Addr{netip.MustParseAddr(ip)}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.255.0.9")}, nil
	}
	repAuto := application.RunDoctor(context.Background(), "zh-CN", "flag", false)
	if !repAuto.FakeIPDetected || len(repAuto.DetectedFakeIPCIDRs) != 1 || repAuto.DetectedFakeIPCIDRs[0] != "127.255.0.0/16" || repAuto.FakeIPBlocked {
		t.Fatalf("expected auto-detected 127.255.0.0/16 in doctor report, got %+v", repAuto)
	}
	application.loaded.Config.Network.AllowPrivate = false
	application.loaded.Config.Network.FakeIPAuto = false
	repBlocked := application.RunDoctor(context.Background(), "zh-CN", "flag", false)
	if !repBlocked.FakeIPBlocked || repBlocked.SuggestedFakeIPCIDR != "127.255.0.0/16" || repBlocked.Issues != 1 {
		t.Fatalf("expected FakeIPBlocked=true with suggested 127.255.0.0/16 and Issues=1, got %+v", repBlocked)
	}
	application.loaded.Config.Network.AllowPrivate = true
	application.loaded.Config.Network.FakeIPAuto = true

	// 2. 公网 IP + live=true 成功（含 search + 零成本 local 提取探针）
	application.lookupNetIP = func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	repLiveOK := application.RunDoctor(context.Background(), "en-US", "env", true)
	if repLiveOK.FakeIPDetected || len(repLiveOK.LiveChecks) != 2 ||
		repLiveOK.LiveChecks[0].Status != "ok" || repLiveOK.LiveChecks[0].Kind != spec.KindSearch ||
		repLiveOK.LiveChecks[1].Provider != spec.ProviderLocal || repLiveOK.LiveChecks[1].Kind != spec.KindFetch || repLiveOK.LiveChecks[1].Status != "ok" {
		t.Fatalf("unexpected live-ok doctor report: %+v", repLiveOK)
	}
	if ci, cErr := application.CacheInfo(); cErr != nil || ci.Disk.Entries != 0 {
		t.Fatalf("doctor --live must not pollute disk cache, got %+v err=%v", ci, cErr)
	}

	// 3. DNS 解析失败 + live=true 失败（含密钥脱敏）
	mode = "fail"
	application.lookupNetIP = func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return nil, errors.New("dns failed for sk-appsecret999988887777")
	}
	repFail := application.RunDoctor(context.Background(), "en-US", "env", true)
	if repFail.DNSError == "" || strings.Contains(repFail.DNSError, "sk-appsecret999988887777") {
		t.Fatalf("expected scrubbed DNS error, got %q", repFail.DNSError)
	}
	if len(repFail.LiveChecks) != 2 || repFail.LiveChecks[0].Status != "error" || strings.Contains(repFail.LiveChecks[0].Error, "sk-appsecret999988887777") {
		t.Fatalf("expected scrubbed live error, got %+v", repFail.LiveChecks)
	}
	if repFail.Issues < 3 {
		t.Fatalf("expected issues >= 3 (allow_private + dns + live), got %d", repFail.Issues)
	}

	// 4. selectProbeHost 过滤 localhost（含大小写与 *.localhost）、IP 字面量与禁用项
	if got := selectProbeHost([]ProviderStatus{
		{Name: "a", Enabled: false, Endpoint: "disabled.example.com"},
		{Name: "b", Enabled: true, Endpoint: "127.0.0.1:8080"},
		{Name: "c", Enabled: true, Endpoint: "LOCALHOST:9000"},
		{Name: "c2", Enabled: true, Endpoint: "firecrawl.localhost:3002"},
		{Name: "d", Enabled: true, Endpoint: "api.exa.ai:443"},
	}); got != "api.exa.ai" {
		t.Fatalf("selectProbeHost got %q, want api.exa.ai", got)
	}
	if got := selectProbeHost([]ProviderStatus{{Name: "local", Enabled: true, Endpoint: "-"}}); got != "api.github.com" {
		t.Fatalf("selectProbeHost fallback got %q, want api.github.com", got)
	}
	if application.Config() == nil {
		t.Fatal("App.Config() must not be nil")
	}
}

type panicAwareFetcher struct {
	name      string
	panicHost string
}

func (f *panicAwareFetcher) Name() string                    { return f.name }
func (f *panicAwareFetcher) Capabilities() core.Capabilities { return core.Capabilities{} }
func (f *panicAwareFetcher) Fetch(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	if strings.Contains(req.URL, f.panicHost) {
		panic("unexpected extractor panic with sk-batch-secret-9999")
	}
	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        req.URL,
			Title:      "OK Page",
			Content:    "# OK\n\nClean body content from extractor.",
			TotalChars: 40,
			Provider:   f.name,
			SourceType: core.SourceGeneric,
		},
		Cost: &core.Cost{Currency: core.CurrencyUSD, Micros: 1000},
	}, nil
}

func TestBatchFetchPanicRecoveryAndMCPDoubleCheckCache(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "batch-panic.toml")
	cacheDir := t.TempDir()
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		switch k {
		case "DEEPSEEK_API_KEY":
			return "sk-batch-secret-9999"
		case "VERI_CACHE_DIR":
			return cacheDir
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := New(loaded, "0.1.2")
	if err != nil {
		t.Fatal(err)
	}
	pf := &panicAwareFetcher{name: spec.ProviderDeepSeek, panicHost: "panic.example.com"}
	application.enabledProviders = []core.Provider{pf}
	application.loaded.Config.Fetch.Strategy = string(core.FetchAPIOnly)
	application.baseRouterOpts.FetchProviders = []string{spec.ProviderDeepSeek}
	application.baseRouterOpts.FetchProvidersGlobal = nil
	application.baseRouterOpts.FetchStrategy = core.FetchAPIOnly
	application.cliRouter = router.New(application.baseRouterOpts, application.enabledProviders)

	// 1. 批量抓取中单页 goroutine 发生 panic 时必须被隔离恢复、脱敏且不影响同批正常页
	env, batchErr := application.FetchBatchMCP(t.Context(), []string{
		"https://ok.example.com/a",
		"https://panic.example.com/boom",
	}, nil)
	if batchErr != nil || !env.OK || !env.Meta.Partial || len(env.Pages) != 2 {
		t.Fatalf("expected partial batch recovery, got env=%+v err=%v", env, batchErr)
	}
	if env.Pages[0].Error != nil || env.Pages[0].Title != "OK Page" {
		t.Fatalf("expected healthy page unaffected, got %+v", env.Pages[0])
	}
	if env.Pages[1].Error == nil || env.Pages[1].Error.Code != core.CodeInternal || strings.Contains(env.Pages[1].Error.Message, "sk-batch-secret-9999") {
		t.Fatalf("expected sanitized internal error on panic branch, got %+v", env.Pages[1].Error)
	}

	// 2. FetchMCP 共享航班 double-check 命中缓存时，必须标 cached=true 且清零 Cost/DurationMS，同时不破坏原缓存页
	targetURL := "https://ok.example.com/double-check"
	cacheKey := application.fetchCacheKey(targetURL, core.FetchAPIOnly, nil, nil)
	orig := &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     true,
		Kind:   spec.KindFetch,
		Page: &core.Page{
			URL:        targetURL,
			Title:      "Cached Doc",
			Content:    "# Cached Doc\n\n" + strings.Repeat("alpha beta gamma ", 100),
			TotalChars: 1715,
			Provider:   spec.ProviderDeepSeek,
			SourceType: core.SourceGeneric,
		},
		Meta: core.Meta{
			Strategy:   string(core.FetchAPIOnly),
			Providers:  []core.ProviderReport{{Name: spec.ProviderDeepSeek, Status: core.ProviderStatusOK, Results: 1, LatencyMS: 320, Cost: &core.Cost{Currency: core.CurrencyUSD, Micros: 1000}}},
			Cost:       []core.Cost{{Currency: core.CurrencyUSD, Micros: 1000}},
			DurationMS: 420,
		},
	}
	application.ensureMCP()
	application.fetchCache.Set(cacheKey, orig)
	// 模拟进入 sfFetch 回调后的 double-check 路径
	viewed := application.buildViewedFetchEnvelope(orig, &core.FetchRequest{URL: targetURL, MaxChars: 100}, true)
	if !viewed.Meta.Cached || len(viewed.Meta.Cost) != 0 || viewed.Meta.DurationMS != 0 || viewed.Meta.Providers[0].Cost != nil || viewed.Meta.Providers[0].LatencyMS != 0 {
		t.Fatalf("expected cached view to zero cost/duration/provider latency and mark cached=true, got %+v", viewed.Meta)
	}
	if orig.Meta.Cached || len(orig.Meta.Cost) != 1 || orig.Meta.DurationMS != 420 || orig.Meta.Providers[0].LatencyMS != 320 {
		t.Fatalf("cached view mutated original cached envelope: %+v", orig.Meta)
	}
	// 再次直接走 FetchMCP 验证 double-check 与缓存命中路径一致
	dcEnv, dcErr := application.FetchMCP(t.Context(), &core.FetchRequest{URL: targetURL, MaxChars: 120})
	if dcErr != nil || !dcEnv.OK || !dcEnv.Meta.Cached || len(dcEnv.Meta.Cost) != 0 || dcEnv.Meta.DurationMS != 0 {
		t.Fatalf("expected FetchMCP cache hit with cached=true and zero cost, got meta=%+v err=%v", dcEnv.Meta, dcErr)
	}

	// 3. 批量全热命中（MCP 内存缓存与 CLI 磁盘缓存）均须与单页口径一致：cached=true 且 DurationMS/Cost/Providers[].LatencyMS 归零
	hotMCP, hotMCPErr := application.FetchBatchMCP(t.Context(), []string{"https://ok.example.com/a", targetURL}, nil)
	if hotMCPErr != nil || !hotMCP.OK || !hotMCP.Meta.Cached || hotMCP.Meta.DurationMS != 0 || len(hotMCP.Meta.Cost) != 0 {
		t.Fatalf("expected all-hot MCP batch to zero duration/cost and mark cached=true, got meta=%+v err=%v", hotMCP.Meta, hotMCPErr)
	}
	for _, rep := range hotMCP.Meta.Providers {
		if rep.LatencyMS != 0 || rep.Cost != nil {
			t.Fatalf("expected all-hot MCP batch provider telemetry zeroed, got %+v", rep)
		}
	}
	cliBatchURLs := []string{"https://ok.example.com/cli-a", "https://ok.example.com/cli-b"}
	if coldCLI, coldErr := application.FetchBatch(t.Context(), cliBatchURLs, nil); coldErr != nil || !coldCLI.OK || coldCLI.Meta.Cached {
		t.Fatalf("expected initial CLI batch cold miss, got cached=%v err=%v", coldCLI != nil && coldCLI.Meta.Cached, coldErr)
	}
	hotCLI, hotCLIErr := application.FetchBatch(t.Context(), cliBatchURLs, nil)
	if hotCLIErr != nil || !hotCLI.OK || !hotCLI.Meta.Cached || hotCLI.Meta.DurationMS != 0 || len(hotCLI.Meta.Cost) != 0 {
		t.Fatalf("expected all-hot CLI batch to zero duration/cost and mark cached=true, got meta=%+v err=%v", hotCLI.Meta, hotCLIErr)
	}
	for _, rep := range hotCLI.Meta.Providers {
		if rep.LatencyMS != 0 || rep.Cost != nil {
			t.Fatalf("expected all-hot CLI batch provider telemetry zeroed, got %+v", rep)
		}
	}
}
