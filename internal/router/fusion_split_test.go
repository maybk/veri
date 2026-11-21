package router

import (
	"context"
	"slices"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

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
	zhEnv, err := rDual.Search(context.Background(), &core.SearchRequest{Query: "Go 并发", Strategy: core.StrategyFusion})
	if err != nil || !slices.Equal(ranProviders(zhEnv), wantCN) {
		t.Fatalf("CN fusion providers = %v, want %v (err=%v)", ranProviders(zhEnv), wantCN, err)
	}
	enEnv, err := rDual.Search(context.Background(), &core.SearchRequest{Query: "Go concurrency", Strategy: core.StrategyFusion})
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
	enOnlyEnv, err := rGlobalOnly.Search(context.Background(), &core.SearchRequest{Query: "Go言語の並行処理", Strategy: core.StrategyFusion})
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
	enDisabledEnv, err := rDisableGlobalFusion.Search(context.Background(), &core.SearchRequest{Query: "golang memory model", Strategy: core.StrategyFusion})
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
	fbEnv, err := rFallback.Search(context.Background(), &core.SearchRequest{Query: "golang generics", Strategy: core.StrategyFusion})
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
		env, err := r.Search(context.Background(), &core.SearchRequest{
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
