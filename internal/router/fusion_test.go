package router

import (
	"slices"
	"strings"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

func TestSearchFusionRRFMergeAndPartialTimeout(t *testing.T) {
	relHigh := 0.95
	p1 := &fakeSearcher{
		name: "deepseek",
		caps: core.Capabilities{MaxCount: 50},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "Official SDK", URL: "https://github.com/modelcontextprotocol/go-sdk?utm_source=x", Snippet: "Short", Summary: "Short summary"},
				{Rank: 2, Title: "Unique DS", URL: "https://deepseek.com/blog", Summary: "DS blog"},
			},
			Cost: &core.Cost{Currency: core.CurrencyCNY, Micros: 5000, Estimated: true},
		},
	}
	p2 := &fakeSearcher{
		name: "exa",
		caps: core.Capabilities{MaxCount: 10},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "MCP Go SDK", URL: "http://www.github.com/modelcontextprotocol/go-sdk/", Summary: "Much longer merged summary from Exa highlights!", Relevance: &relHigh},
			},
			Cost: &core.Cost{Currency: core.CurrencyUSD, Micros: 4000, Estimated: false},
		},
	}
	slow := &fakeSearcher{
		name:  "tinyfish",
		caps:  core.Capabilities{MaxCount: 10},
		delay: 200 * time.Millisecond,
	}

	r := New(Options{
		SearchProviders: []string{"deepseek", "exa", "tinyfish"},
		SearchTimeout:   50 * time.Millisecond,
	}, []core.Provider{p1, p2, slow})

	env, err := r.Search(t.Context(), &core.SearchRequest{
		Query:    "mcp go sdk",
		Strategy: core.StrategyFusion,
		Count:    5,
	})
	if err != nil || !env.OK {
		t.Fatalf("Fusion failed: env=%+v err=%v", env, err)
	}
	if !env.Meta.Partial {
		t.Fatal("expected Meta.Partial=true when slow branch timed out")
	}
	if len(env.Results) != 2 {
		t.Fatalf("expected deduplicated 2 fused results, got %d: %+v", len(env.Results), env.Results)
	}
	top := env.Results[0]
	if len(top.Providers) != 2 || !strings.Contains(top.Summary, "Much longer merged summary") {
		t.Fatalf("expected RRF top result to merge providers & longest summary: %+v", top)
	}
	if len(env.Meta.Cost) != 2 {
		t.Fatalf("expected separate CNY and USD cost totals, got %+v", env.Meta.Cost)
	}
}

func TestCircuitSoftDegradationAndFusionFetchFloor(t *testing.T) {
	flaky := &fakeSearcher{
		name: "deepseek",
		caps: core.Capabilities{MaxCount: 50},
		err:  core.NewError(core.CodeUpstreamUnavailable, "503").WithProvider("deepseek"),
	}
	healthy := &fakeSearcher{
		name: "exa",
		caps: core.Capabilities{MaxCount: 10},
		outcome: &core.SearchOutcome{
			Results: []core.Result{{Rank: 1, Title: "OK", URL: "https://exa.ai/ok", Summary: "healthy"}},
		},
	}

	r := New(Options{SearchProviders: []string{"deepseek", "exa"}}, []core.Provider{flaky, healthy})

	// Trigger 3 consecutive 503 errors on primary provider to open soft circuit.
	for range defaultCircuitFailureThreshold {
		env, err := r.Search(t.Context(), &core.SearchRequest{Query: "中文路由测试", Count: 3})
		if err != nil || !env.OK {
			t.Fatalf("expected fallback to healthy exa, err=%v", err)
		}
	}

	// 4th call should route directly to healthy exa first (FallbackUsed == false)!
	env4, err := r.Search(t.Context(), &core.SearchRequest{Query: "中文路由测试", Count: 3})
	if err != nil || len(env4.Meta.Providers) != 1 || env4.Meta.Providers[0].Name != "exa" {
		t.Fatalf("expected degraded primary deprioritized after 3 failures, got providers=%+v err=%v", env4.Meta.Providers, err)
	}

	// Verify Fusion fetchFloor=10 expands small Count=3 to 10 on branches, then trims back to 3.
	s1 := &fakeSearcher{
		name: "brave",
		caps: core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "A", URL: "https://a.example.com"},
				{Rank: 2, Title: "B", URL: "https://b.example.com"},
				{Rank: 3, Title: "C", URL: "https://c.example.com"},
				{Rank: 4, Title: "Shared", URL: "https://shared.example.com"},
			},
		},
	}
	s2 := &fakeSearcher{
		name: "tavily",
		caps: core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "Shared", URL: "https://shared.example.com"},
			},
		},
	}
	rf := New(Options{FusionProviders: []string{"brave", "tavily"}}, []core.Provider{s1, s2})
	fEnv, err := rf.Search(t.Context(), &core.SearchRequest{Query: "test", Strategy: core.StrategyFusion, Count: 2})
	if err != nil || s1.lastReq.Count != defaultFusionFetchFloor || len(fEnv.Results) != 2 || fEnv.Results[0].URL != "https://shared.example.com" {
		t.Fatalf("expected fusion fetchFloor=%d and consensus winner #1, got s1.Count=%d results=%+v err=%v",
			defaultFusionFetchFloor, s1.lastReq.Count, fEnv.Results, err)
	}
}

func TestFusionProvidersGlobalSplitMatrix(t *testing.T) {
	mk := func(name string) *fakeSearcher {
		return &fakeSearcher{
			name: name,
			caps: core.Capabilities{MaxCount: 10},
			outcome: &core.SearchOutcome{
				Results: []core.Result{{Rank: 1, Title: "Hit " + name, URL: "https://example.com/" + name, Summary: "summary"}},
			},
		}
	}
	all := []core.Provider{
		mk(spec.ProviderDeepSeek),
		mk(spec.ProviderKeenable),
		mk(spec.ProviderTencentWSA),
		mk(spec.ProviderZhihu),
		mk(spec.ProviderParallel),
		mk(spec.ProviderExa),
		mk(spec.ProviderBrave),
	}
	ranProviders := func(env *core.Envelope) []string {
		out := make([]string, 0, len(env.Meta.Providers))
		for _, p := range env.Meta.Providers {
			out = append(out, p.Name)
		}
		return out
	}

	// 1. 双池独立配置（且 >3 个源时不被 DefaultFusionAutoLimit 误截断）
	rDual := New(Options{
		FusionProviders:       []string{spec.ProviderDeepSeek, spec.ProviderZhihu},
		FusionProvidersGlobal: []string{spec.ProviderExa, spec.ProviderParallel, spec.ProviderBrave, spec.ProviderKeenable},
	}, all)
	wantCN := []string{spec.ProviderDeepSeek, spec.ProviderZhihu}
	wantGlobal := []string{spec.ProviderExa, spec.ProviderParallel, spec.ProviderBrave, spec.ProviderKeenable}
	if got := rDual.ActiveFusionChain(); !slices.Equal(got, wantCN) {
		t.Fatalf("ActiveFusionChain() = %v, want %v", got, wantCN)
	}
	if got := rDual.ActiveFusionChainGlobal(); !slices.Equal(got, wantGlobal) {
		t.Fatalf("ActiveFusionChainGlobal() = %v, want %v", got, wantGlobal)
	}
	zhEnv, err := rDual.Search(t.Context(), &core.SearchRequest{Query: "Go 并发", Strategy: core.StrategyFusion})
	if err != nil || !slices.Equal(ranProviders(zhEnv), wantCN) {
		t.Fatalf("CN fusion providers = %v, want %v (err=%v)", ranProviders(zhEnv), wantCN, err)
	}
	enEnv, err := rDual.Search(t.Context(), &core.SearchRequest{Query: "Go concurrency", Strategy: core.StrategyFusion})
	if err != nil || !slices.Equal(ranProviders(enEnv), wantGlobal) {
		t.Fatalf("global fusion providers = %v, want %v (err=%v)", ranProviders(enEnv), wantGlobal, err)
	}

	// 2. 仅配 fusion_providers_global（主池保持默认前 3，非中文用自定义池）
	rGlobalOnly := New(Options{
		FusionProvidersGlobal: []string{spec.ProviderExa, spec.ProviderBrave},
	}, all)
	wantDefaultCN := []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA}
	if got := rGlobalOnly.ActiveFusionChain(); !slices.Equal(got, wantDefaultCN) {
		t.Fatalf("ActiveFusionChain() = %v, want %v", got, wantDefaultCN)
	}
	if got := rGlobalOnly.ActiveFusionChainGlobal(); !slices.Equal(got, []string{spec.ProviderExa, spec.ProviderBrave}) {
		t.Fatalf("ActiveFusionChainGlobal() = %v, want [exa brave]", got)
	}
	enOnlyEnv, err := rGlobalOnly.Search(t.Context(), &core.SearchRequest{Query: "Go言語の並行処理", Strategy: core.StrategyFusion})
	if err != nil || !slices.Equal(ranProviders(enOnlyEnv), []string{spec.ProviderExa, spec.ProviderBrave}) {
		t.Fatalf("global-only fusion providers = %v, want [exa brave] (err=%v)", ranProviders(enOnlyEnv), err)
	}

	// 3. 显式 fusion_providers_global = [] 时关闭非中文融合分池（即使存在 SearchProvidersGlobal）
	rDisableGlobalFusion := New(Options{
		FusionProvidersGlobalExplicit: true,
	}, all)
	if got := rDisableGlobalFusion.ActiveFusionChainGlobal(); got != nil {
		t.Fatalf("expected nil ActiveFusionChainGlobal when explicitly disabled, got %v", got)
	}
	enDisabledEnv, err := rDisableGlobalFusion.Search(t.Context(), &core.SearchRequest{Query: "golang memory model", Strategy: core.StrategyFusion})
	if err != nil || !slices.Equal(ranProviders(enDisabledEnv), wantDefaultCN) {
		t.Fatalf("disabled global fusion should run primary pool %v, got %v (err=%v)", wantDefaultCN, ranProviders(enDisabledEnv), err)
	}

	// 4. fusion_providers_global 全部未启用时，诊断返回 nil 且运行时平滑回退主融合池
	rFallback := New(Options{
		FusionProviders:       []string{spec.ProviderDeepSeek, spec.ProviderKeenable},
		FusionProvidersGlobal: []string{spec.ProviderExa, spec.ProviderParallel},
	}, []core.Provider{mk(spec.ProviderDeepSeek), mk(spec.ProviderKeenable)})
	if got := rFallback.ActiveFusionChainGlobal(); got != nil {
		t.Fatalf("expected nil ActiveFusionChainGlobal when global fusion providers are disabled, got %v", got)
	}
	fbEnv, err := rFallback.Search(t.Context(), &core.SearchRequest{Query: "golang generics", Strategy: core.StrategyFusion})
	if err != nil || !slices.Equal(ranProviders(fbEnv), []string{spec.ProviderDeepSeek, spec.ProviderKeenable}) {
		t.Fatalf("expected fallback to primary fusion pool, got %v (err=%v)", ranProviders(fbEnv), err)
	}
}

func TestFusionStragglerCancelDoesNotDegradeHealthAndNormalizesOptions(t *testing.T) {
	mkFast := func(name string) *fakeSearcher {
		return &fakeSearcher{
			name: name,
			caps: core.Capabilities{MaxCount: 10},
			outcome: &core.SearchOutcome{
				Results: []core.Result{{Rank: 1, Title: "Fast " + name, URL: "https://example.com/" + name, Summary: "ok"}},
			},
		}
	}
	fast1 := mkFast(spec.ProviderDeepSeek)
	fast2 := mkFast(spec.ProviderKeenable)
	slow := &fakeSearcher{
		name:  spec.ProviderExa,
		caps:  core.Capabilities{MaxCount: 10},
		delay: 2 * time.Second,
		outcome: &core.SearchOutcome{
			Results: []core.Result{{Rank: 1, Title: "Slow", URL: "https://example.com/exa", Summary: "slow"}},
		},
	}

	r := New(Options{
		FusionProviders: []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderExa},
	}, []core.Provider{fast1, fast2, slow})
	r.stragglerGrace = 20 * time.Millisecond

	// 连续触发 4 次宽限剪枝（> defaultCircuitFailureThreshold），慢源不应被误判故障而触发熔断降权
	for range defaultCircuitFailureThreshold + 1 {
		env, err := r.Search(t.Context(), &core.SearchRequest{
			Query:    "go concurrency",
			Strategy: core.StrategyFusion,
			Options:  map[string]string{" Exa.Category ": " news "},
		})
		if err != nil || !env.OK || !env.Meta.Partial {
			t.Fatalf("expected partial fusion success on straggler grace cancel, got env=%+v err=%v", env, err)
		}
	}
	if r.health.isDegraded(spec.ProviderExa, time.Now()) {
		t.Fatal("straggler grace cancel must not degrade healthy slow provider in circuit breaker")
	}
	// 验证路由入口已将大小写/空白 Options 键归一化后透传至底层 Provider
	if fast1.lastReq == nil || fast1.lastReq.Options["exa.category"] != "news" {
		t.Fatalf("expected normalized lowercase option key exa.category=news, got %+v", fast1.lastReq)
	}
}
