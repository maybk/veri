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
		mk(spec.ProviderZhihuWeb),
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
	wantDefaultCN := []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihu}
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

func TestFusionAutoCapabilityBackfillAndHealthRotation(t *testing.T) {
	mkCap := func(name string, caps core.Capabilities) *fakeSearcher {
		return &fakeSearcher{
			name: name,
			caps: caps,
			outcome: &core.SearchOutcome{
				Results: []core.Result{{Rank: 1, Title: "Hit " + name, URL: "https://go.dev/" + name, Summary: "summary " + name}},
			},
		}
	}
	ranNames := func(env *core.Envelope) []string {
		out := make([]string, 0, len(env.Meta.Providers))
		for _, p := range env.Meta.Providers {
			out = append(out, p.Name)
		}
		return out
	}

	// 1. 英文自动 -F + --lang en --strict：全球链前 3 均不支持 Lang，自动顺延递补 brave / tavily / tinyfish
	rGlobal := New(Options{}, []core.Provider{
		mkCap(spec.ProviderKeenable, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti}),
		mkCap(spec.ProviderParallel, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti}),
		mkCap(spec.ProviderExa, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti}),
		mkCap(spec.ProviderBrave, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti, Lang: true}),
		mkCap(spec.ProviderLinkup, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti}),
		mkCap(spec.ProviderTavily, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti, Lang: true}),
		mkCap(spec.ProviderDeepSeek, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti, QueryMaxRunes: 128}),
		mkCap(spec.ProviderTinyFish, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti, Lang: true}),
	})
	enLangEnv, err := rGlobal.Search(t.Context(), &core.SearchRequest{
		Query:         "golang memory model",
		Strategy:      core.StrategyFusion,
		StrictFilters: true,
		Filters:       core.Filters{Lang: []string{"en"}},
	})
	wantLangPool := []string{spec.ProviderBrave, spec.ProviderTavily, spec.ProviderTinyFish}
	if err != nil || !enLangEnv.OK || !slices.Equal(ranNames(enLangEnv), wantLangPool) {
		t.Fatalf("expected strict --lang auto-fusion backfill %v, got names=%v env=%+v err=%v", wantLangPool, ranNames(enLangEnv), enLangEnv, err)
	}

	// 2. 中文自动 -F + -s go.dev --strict：zhihu_web 声明 SiteNone，自动递补第 4 位 tencent_wsa；超长查询同样跳过 deepseek 自动递补
	rCN := New(Options{}, []core.Provider{
		mkCap(spec.ProviderDeepSeek, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti, QueryMaxRunes: 128}),
		mkCap(spec.ProviderKeenable, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti}),
		mkCap(spec.ProviderZhihuWeb, core.Capabilities{MaxCount: 10, Sites: core.SiteNone}),
		mkCap(spec.ProviderTencentWSA, core.Capabilities{MaxCount: 10, Sites: core.SiteSingle}),
	})
	cnSiteEnv, err := rCN.Search(t.Context(), &core.SearchRequest{
		Query:         "Go 并发",
		Strategy:      core.StrategyFusion,
		StrictFilters: true,
		Filters:       core.Filters{Sites: []string{"go.dev"}},
	})
	wantSitePool := []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA}
	if err != nil || !cnSiteEnv.OK || !slices.Equal(ranNames(cnSiteEnv), wantSitePool) {
		t.Fatalf("expected strict -s auto-fusion backfill %v, got names=%v err=%v", wantSitePool, ranNames(cnSiteEnv), err)
	}

	longCNQuery := strings.Repeat("并发模型", 40) // 160 runes > deepseek 128 上限
	longEnv, err := rCN.Search(t.Context(), &core.SearchRequest{
		Query:    longCNQuery,
		Strategy: core.StrategyFusion,
	})
	wantLongPool := []string{spec.ProviderKeenable, spec.ProviderZhihuWeb, spec.ProviderTencentWSA}
	if err != nil || !longEnv.OK || !slices.Equal(ranNames(longEnv), wantLongPool) {
		t.Fatalf("expected query_length auto-fusion backfill %v, got names=%v err=%v", wantLongPool, ranNames(longEnv), err)
	}

	// 3. 全部候选源均不满足 --strict 时，返回与 auto 模式一致的 strict_filter_empty 提示
	noMatchEnv, noMatchErr := rCN.Search(t.Context(), &core.SearchRequest{
		Query:         "Go 并发",
		Strategy:      core.StrategyFusion,
		StrictFilters: true,
		Filters:       core.Filters{Lang: []string{"zh"}},
	})
	se := core.AsError(noMatchErr)
	if noMatchErr == nil || noMatchEnv.OK || se.Code != core.CodeNoProvider || se.HintKey != "hint.strict_filter_empty" {
		t.Fatalf("expected strict_filter_empty no_provider error, got env=%+v err=%+v", noMatchEnv, se)
	}

	// 4. 显式配置 FusionProviders 时严格尊重用户池边界，不越权拉入未列出的源
	rPinned := New(Options{
		FusionProviders: []string{spec.ProviderDeepSeek, spec.ProviderZhihuWeb},
	}, []core.Provider{
		mkCap(spec.ProviderDeepSeek, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti}),
		mkCap(spec.ProviderZhihuWeb, core.Capabilities{MaxCount: 10, Sites: core.SiteNone}),
		mkCap(spec.ProviderTencentWSA, core.Capabilities{MaxCount: 10, Sites: core.SiteSingle}),
	})
	pinnedEnv, err := rPinned.Search(t.Context(), &core.SearchRequest{
		Query:         "Go 并发",
		Strategy:      core.StrategyFusion,
		StrictFilters: true,
		Filters:       core.Filters{Sites: []string{"go.dev"}},
	})
	if err != nil || !pinnedEnv.OK || !slices.Equal(ranNames(pinnedEnv), []string{spec.ProviderDeepSeek, spec.ProviderZhihuWeb}) {
		t.Fatalf("explicit fusion pool must not pull in unlisted providers, got names=%v err=%v", ranNames(pinnedEnv), err)
	}

	// 5. 配额/鉴权错误（CodeInsufficientBalance）立即软降权后置，下一次自动 -F 与 auto 自动轮换健康源，恢复成功后即时复权
	zhihuWebQuota := &fakeSearcher{
		name: spec.ProviderZhihuWeb,
		caps: core.Capabilities{MaxCount: 10},
		err:  core.NewError(core.CodeInsufficientBalance, "daily quota exceeded").WithProvider(spec.ProviderZhihuWeb),
	}
	rQuota := New(Options{}, []core.Provider{
		mkCap(spec.ProviderDeepSeek, core.Capabilities{MaxCount: 10}),
		mkCap(spec.ProviderKeenable, core.Capabilities{MaxCount: 10}),
		zhihuWebQuota,
		mkCap(spec.ProviderTencentWSA, core.Capabilities{MaxCount: 10}),
	})
	firstEnv, err := rQuota.Search(t.Context(), &core.SearchRequest{Query: "中文测试", Strategy: core.StrategyFusion})
	if err != nil || !firstEnv.OK || !firstEnv.Meta.Partial || !slices.Equal(ranNames(firstEnv), []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihuWeb}) {
		t.Fatalf("first call should try default CN top3 with partial zhihu_web quota failure, got names=%v env=%+v err=%v", ranNames(firstEnv), firstEnv, err)
	}
	secondEnv, err := rQuota.Search(t.Context(), &core.SearchRequest{Query: "中文测试二", Strategy: core.StrategyFusion})
	if err != nil || !secondEnv.OK || secondEnv.Meta.Partial || !slices.Equal(ranNames(secondEnv), []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA}) {
		t.Fatalf("subsequent auto-fusion must rotate healthy tencent_wsa ahead of quota-exhausted zhihu_web, got names=%v env=%+v err=%v", ranNames(secondEnv), secondEnv, err)
	}
	// 单源恢复成功后立即清零降权状态，重回默认前 3
	zhihuWebQuota.err = nil
	zhihuWebQuota.outcome = &core.SearchOutcome{Results: []core.Result{{Rank: 1, Title: "Recovered", URL: "https://example.com/1", Summary: "ok"}}}
	if _, err := rQuota.Search(t.Context(), &core.SearchRequest{Query: "恢复验证", Providers: []string{spec.ProviderZhihuWeb}}); err != nil {
		t.Fatalf("single zhihu_web recovery call failed: %v", err)
	}
	recoveredEnv, err := rQuota.Search(t.Context(), &core.SearchRequest{Query: "中文测试三", Strategy: core.StrategyFusion})
	if err != nil || !recoveredEnv.OK || !slices.Equal(ranNames(recoveredEnv), []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihuWeb}) {
		t.Fatalf("recovered zhihu_web should rejoin default CN fusion top3, got names=%v err=%v", ranNames(recoveredEnv), err)
	}
}

func TestZhihuSplitAndDomainScopeRouting(t *testing.T) {
	mk := func(name string, caps core.Capabilities, results ...core.Result) *fakeSearcher {
		if len(results) == 0 {
			results = []core.Result{{Rank: 1, Title: "Hit " + name, URL: "https://example.com/" + name, Summary: "summary " + name}}
		}
		return &fakeSearcher{
			name:    name,
			caps:    caps,
			outcome: &core.SearchOutcome{Results: results},
		}
	}
	ranNames := func(env *core.Envelope) []string {
		out := make([]string, 0, len(env.Meta.Providers))
		for _, p := range env.Meta.Providers {
			out = append(out, p.Name)
		}
		return out
	}

	ds := mk(spec.ProviderDeepSeek, core.Capabilities{MaxCount: 50, QueryMaxRunes: 128, Sites: core.SiteMulti, ExcludeSites: 20},
		core.Result{Rank: 1, Title: "DS Go", URL: "https://go.dev/ds"},
		core.Result{Rank: 2, Title: "DS Zhihu", URL: "https://www.zhihu.com/question/1"},
	)
	kn := mk(spec.ProviderKeenable, core.Capabilities{MaxCount: 20, Sites: core.SiteMulti, ExcludeSites: 20},
		core.Result{Rank: 1, Title: "KN Go", URL: "https://go.dev/kn"},
	)
	zw := mk(spec.ProviderZhihuWeb, core.Capabilities{MaxCount: 20, Sites: core.SiteNone},
		core.Result{Rank: 1, Title: "ZW Go", URL: "https://go.dev/zw"},
		core.Result{Rank: 2, Title: "ZW Zhihu", URL: "https://zhuanlan.zhihu.com/p/77"},
	)
	zh := mk(spec.ProviderZhihu, core.Capabilities{MaxCount: 10, Sites: core.SiteNone, ScopeDomains: []string{"zhihu.com"}},
		core.Result{Rank: 1, Title: "ZH Q&A", URL: "https://www.zhihu.com/question/42"},
		core.Result{Rank: 2, Title: "ZH Column", URL: "https://zhuanlan.zhihu.com/p/99"},
	)
	wsa := mk(spec.ProviderTencentWSA, core.Capabilities{MaxCount: 20, Sites: core.SiteSingle},
		core.Result{Rank: 1, Title: "WSA Go", URL: "https://go.dev/wsa"},
	)

	// 1. 单链顺序（search.providers 中 zhihu 第 3、zhihu_web 第 4）：
	//    - auto 通用搜按 ScopeDomains 自动将全网源排在单站垂直源之前；
	//    - -F 融合直接按 search.providers 顺序取前 3（零特化表）。
	rAll := New(Options{}, []core.Provider{ds, kn, zw, zh, wsa})
	wantAuto := []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihuWeb, spec.ProviderTencentWSA, spec.ProviderZhihu}
	if got := rAll.EnabledSearchers(); !slices.Equal(got, wantAuto) {
		t.Fatalf("default auto chain = %v, want %v", got, wantAuto)
	}
	wantFusionFull := []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihu}
	if got := rAll.ActiveFusionChain(); !slices.Equal(got, wantFusionFull) {
		t.Fatalf("default fusion chain (all) = %v, want %v", got, wantFusionFull)
	}

	// 2 把 Key（deepseek + zhihu 共享组）：auto 通用搜为 deepseek -> zhihu_web -> zhihu（超长词跳 deepseek 后走全网 zhihu_web），-F 为 deepseek + zhihu + zhihu_web
	rTwoKeys := New(Options{}, []core.Provider{ds, zw, zh})
	if got := rTwoKeys.EnabledSearchers(); !slices.Equal(got, []string{spec.ProviderDeepSeek, spec.ProviderZhihuWeb, spec.ProviderZhihu}) {
		t.Fatalf("2-key auto chain = %v", got)
	}
	if got := rTwoKeys.ActiveFusionChain(); !slices.Equal(got, []string{spec.ProviderDeepSeek, spec.ProviderZhihu, spec.ProviderZhihuWeb}) {
		t.Fatalf("2-key fusion chain = %v", got)
	}
	longAutoEnv, err := rTwoKeys.Search(t.Context(), &core.SearchRequest{Query: strings.Repeat("并发模型", 40)})
	if err != nil || !longAutoEnv.OK || len(longAutoEnv.Meta.Providers) < 2 || longAutoEnv.Meta.Providers[1].Name != spec.ProviderZhihuWeb {
		t.Fatalf("long auto query skipping deepseek should fall back to general-web zhihu_web, got env=%+v err=%v", longAutoEnv, err)
	}

	// 仅 1 把 Key（仅 ZHIHU_API_KEY）：auto 优先全网 zhihu_web（且与非中文 auto 顺序一致故 Global 诊断返回 nil），-F 同时启用 zhihu + zhihu_web
	rZhihuOnly := New(Options{}, []core.Provider{zw, zh})
	if got := rZhihuOnly.EnabledSearchers(); !slices.Equal(got, []string{spec.ProviderZhihuWeb, spec.ProviderZhihu}) {
		t.Fatalf("zhihu-only auto chain = %v", got)
	}
	if got := rZhihuOnly.ActiveSearchChainGlobal(); got != nil {
		t.Fatalf("expected nil ActiveSearchChainGlobal when auto order matches global, got %v", got)
	}
	if got := rZhihuOnly.ActiveFusionChain(); !slices.Equal(got, []string{spec.ProviderZhihu, spec.ProviderZhihuWeb}) {
		t.Fatalf("zhihu-only fusion chain = %v", got)
	}
	weatherEnv, err := rZhihuOnly.Search(t.Context(), &core.SearchRequest{Query: "北京今日天气"})
	if err != nil || !weatherEnv.OK || weatherEnv.Meta.Providers[0].Name != spec.ProviderZhihuWeb {
		t.Fatalf("zhihu-only general auto query must hit zhihu_web first, got env=%+v err=%v", weatherEnv, err)
	}

	// 2. auto 专长源置顶：-s zhihu.com 无论中文还是英文查询词均优先直连 zhihu（不触发前面的 deepseek）；
	//    但子域名/多站点（-s zhuanlan.zhihu.com、-s go.dev,zhihu.com）与显式 -p 不越权抢跑。
	dsBefore := ds.calls.Load()
	zhSpecCN, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:   "Go 并发",
		Filters: core.Filters{Sites: []string{"zhihu.com"}},
	})
	if err != nil || !zhSpecCN.OK || ds.calls.Load() != dsBefore || zhSpecCN.Meta.Providers[0].Name != spec.ProviderZhihu {
		t.Fatalf("auto -s zhihu.com (CN) must prioritize zhihu specialist, got env=%+v dsCalls=%d->%d err=%v", zhSpecCN, dsBefore, ds.calls.Load(), err)
	}
	zhSpecEN, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:   "golang goroutine leak",
		Filters: core.Filters{Sites: []string{"zhihu.com"}},
	})
	if err != nil || !zhSpecEN.OK || ds.calls.Load() != dsBefore || zhSpecEN.Meta.Providers[0].Name != spec.ProviderZhihu {
		t.Fatalf("auto -s zhihu.com (EN) must prioritize zhihu specialist, got env=%+v dsCalls=%d->%d err=%v", zhSpecEN, dsBefore, ds.calls.Load(), err)
	}
	subAutoEnv, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:   "Go 并发",
		Filters: core.Filters{Sites: []string{"zhuanlan.zhihu.com"}},
	})
	if err != nil || !subAutoEnv.OK || subAutoEnv.Meta.Providers[0].Name != spec.ProviderDeepSeek {
		t.Fatalf("auto -s zhuanlan.zhihu.com should keep native site searcher deepseek first, got env=%+v err=%v", subAutoEnv, err)
	}
	multiSiteAutoEnv, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:   "Go 并发",
		Filters: core.Filters{Sites: []string{"go.dev", "zhihu.com"}},
	})
	if err != nil || !multiSiteAutoEnv.OK || multiSiteAutoEnv.Meta.Providers[0].Name != spec.ProviderDeepSeek {
		t.Fatalf("auto -s go.dev,zhihu.com should keep native multi-site searcher deepseek first, got env=%+v err=%v", multiSiteAutoEnv, err)
	}
	// 专长源配额耗尽后，orderByHealth 仍自动避让切回健康通用源 deepseek
	zhQuota := &fakeSearcher{
		name: spec.ProviderZhihu,
		caps: core.Capabilities{MaxCount: 10, Sites: core.SiteNone, ScopeDomains: []string{"zhihu.com"}},
		err:  core.NewError(core.CodeInsufficientBalance, "daily quota exceeded").WithProvider(spec.ProviderZhihu),
	}
	rSpecHealth := New(Options{}, []core.Provider{ds, zhQuota})
	if env1, err1 := rSpecHealth.Search(t.Context(), &core.SearchRequest{Query: "测试", Filters: core.Filters{Sites: []string{"zhihu.com"}}}); err1 != nil || !env1.OK || env1.Meta.Providers[0].Name != spec.ProviderZhihu || env1.Meta.Providers[1].Name != spec.ProviderDeepSeek {
		t.Fatalf("expected first specialist call to fail over to deepseek, got env=%+v err=%v", env1, err1)
	}
	if env2, err2 := rSpecHealth.Search(t.Context(), &core.SearchRequest{Query: "测试二", Filters: core.Filters{Sites: []string{"zhihu.com"}}}); err2 != nil || !env2.OK || env2.Meta.Providers[0].Name != spec.ProviderDeepSeek {
		t.Fatalf("expected degraded specialist to be deprioritized by health order, got env=%+v err=%v", env2, err2)
	}

	// 3. -F -s go.dev（非严格）：自动跳过 zhihu（site_scope）并顺延递补 zhihu_web
	zhBefore := zh.calls.Load()
	envGo, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:    "Go 并发",
		Strategy: core.StrategyFusion,
		Filters:  core.Filters{Sites: []string{"go.dev"}},
	})
	if err != nil || !envGo.OK || zh.calls.Load() != zhBefore || !slices.Equal(ranNames(envGo), []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihuWeb}) {
		t.Fatalf("non-strict -s go.dev failed: names=%v zhCalls=%d->%d err=%v", ranNames(envGo), zhBefore, zh.calls.Load(), err)
	}

	// 4. -F --strict -s go.dev：跳过 zhihu（site_scope）与 zhihu_web（SiteNone），递补 tencent_wsa
	envGoStrict, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:         "Go 并发",
		Strategy:      core.StrategyFusion,
		StrictFilters: true,
		Filters:       core.Filters{Sites: []string{"go.dev"}},
	})
	if err != nil || !envGoStrict.OK || !slices.Equal(ranNames(envGoStrict), []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA}) {
		t.Fatalf("strict -s go.dev failed: names=%v err=%v", ranNames(envGoStrict), err)
	}

	// 5. -F --strict -s zhihu.com：zhihu 原生满足作用域（零 sites:emulated），保留 [deepseek keenable zhihu]
	zwBefore := zw.calls.Load()
	envZhihuSite, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:         "Go 并发",
		Strategy:      core.StrategyFusion,
		StrictFilters: true,
		Filters:       core.Filters{Sites: []string{"zhihu.com"}},
	})
	if err != nil || !envZhihuSite.OK || zw.calls.Load() != zwBefore || !slices.Equal(ranNames(envZhihuSite), []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihu}) {
		t.Fatalf("strict -s zhihu.com failed: names=%v zwCalls=%d->%d err=%v", ranNames(envZhihuSite), zwBefore, zw.calls.Load(), err)
	}
	for _, rep := range envZhihuSite.Meta.Providers {
		if rep.Name == spec.ProviderZhihu && len(rep.Degraded) != 0 {
			t.Fatalf("expected zhihu to satisfy -s zhihu.com natively without degradation, got %+v", rep)
		}
	}

	// 6. -F -x zhihu.com（非严格）：跳过 zhihu（site_scope），顺延递补 zhihu_web 并后过滤剥离 zhihu.com 条目
	zhBefore = zh.calls.Load()
	envExclZhihu, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:    "Go 并发",
		Strategy: core.StrategyFusion,
		Filters:  core.Filters{ExcludeSites: []string{"zhihu.com"}},
	})
	if err != nil || !envExclZhihu.OK || zh.calls.Load() != zhBefore || !slices.Equal(ranNames(envExclZhihu), []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihuWeb}) {
		t.Fatalf("non-strict -x zhihu.com failed: names=%v zhCalls=%d->%d err=%v", ranNames(envExclZhihu), zhBefore, zh.calls.Load(), err)
	}
	for _, res := range envExclZhihu.Results {
		if res.URL == "https://zhuanlan.zhihu.com/p/77" {
			t.Fatalf("expected -x zhihu.com to strip zhihu.com URL from zhihu_web results, got %q", res.URL)
		}
	}
	// 严格模式 -F --strict -x zhihu.com：zhihu（site_scope）与 zhihu_web（ExcludeSites=0）均跳过
	envExclStrict, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:         "Go 并发",
		Strategy:      core.StrategyFusion,
		StrictFilters: true,
		Filters:       core.Filters{ExcludeSites: []string{"zhihu.com"}},
	})
	if err != nil || !envExclStrict.OK || !slices.Equal(ranNames(envExclStrict), []string{spec.ProviderDeepSeek, spec.ProviderKeenable}) {
		t.Fatalf("strict -x zhihu.com failed: names=%v err=%v", ranNames(envExclStrict), err)
	}

	// 7. -F --strict -x csdn.net：zhihu 作用域与 csdn.net 无交集，空真满足并保留在融合池内
	envExclCSDN, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:         "Go 并发",
		Strategy:      core.StrategyFusion,
		StrictFilters: true,
		Filters:       core.Filters{ExcludeSites: []string{"csdn.net"}},
	})
	if err != nil || !envExclCSDN.OK || !slices.Equal(ranNames(envExclCSDN), []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihu}) {
		t.Fatalf("strict -x csdn.net failed: names=%v err=%v", ranNames(envExclCSDN), err)
	}

	// 8. 显式单源与站点作用域冲突：直接返回 CodeInvalidArgument，不向上游发请求
	zhBefore = zh.calls.Load()
	if _, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:     "Go 并发",
		Providers: []string{spec.ProviderZhihu},
		Filters:   core.Filters{Sites: []string{"go.dev"}},
	}); core.AsError(err) == nil || core.AsError(err).Code != core.CodeInvalidArgument || zh.calls.Load() != zhBefore {
		t.Fatalf("expected invalid_argument without calling zhihu for -p zhihu -s go.dev, got err=%v calls=%d->%d", err, zhBefore, zh.calls.Load())
	}
	if _, err := rAll.Search(t.Context(), &core.SearchRequest{
		Query:     "Go 并发",
		Providers: []string{spec.ProviderZhihu},
		Filters:   core.Filters{ExcludeSites: []string{"zhihu.com"}},
	}); core.AsError(err) == nil || core.AsError(err).Code != core.CodeInvalidArgument || zh.calls.Load() != zhBefore {
		t.Fatalf("expected invalid_argument without calling zhihu for -p zhihu -x zhihu.com, got err=%v calls=%d->%d", err, zhBefore, zh.calls.Load())
	}

	// 9. 用户显式定制 search.providers 时严格按用户顺序，不越权拉入未列出的源
	rCustomOrder := New(Options{
		SearchProviders:         []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA},
		SearchProvidersExplicit: true,
	}, []core.Provider{ds, kn, zw, zh, wsa})
	if got := rCustomOrder.ActiveFusionChain(); !slices.Equal(got, []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA}) {
		t.Fatalf("explicit SearchProviders must not pull zhihu, got %v", got)
	}
}

func TestZhihuOnlyKeyFailureAndEdgeCases(t *testing.T) {
	// 1. 仅配知乎单 Key（zhihu_web + zhihu）+ FallbackOnEmpty=true：
	//    auto -s go.dev 时 zhihu_web 后过滤为 0 条、zhihu 因 site_scope 跳过，必须返回 OK=true 空结果而非误报 no_provider。
	zwNoGo := &fakeSearcher{
		name: spec.ProviderZhihuWeb,
		caps: core.Capabilities{MaxCount: 20, Sites: core.SiteNone},
		outcome: &core.SearchOutcome{
			Results: []core.Result{{Rank: 1, Title: "Other", URL: "https://example.com/1"}},
		},
	}
	zhValid := &fakeSearcher{
		name: spec.ProviderZhihu,
		caps: core.Capabilities{MaxCount: 10, Sites: core.SiteNone, ScopeDomains: []string{"zhihu.com"}},
		outcome: &core.SearchOutcome{
			Results: []core.Result{{Rank: 1, Title: "ZH", URL: "https://www.zhihu.com/question/1"}},
		},
	}
	rEmptySkip := New(Options{FallbackOnEmpty: true}, []core.Provider{zwNoGo, zhValid})
	envEmpty, err := rEmptySkip.Search(t.Context(), &core.SearchRequest{
		Query:   "Go 并发",
		Filters: core.Filters{Sites: []string{"go.dev"}},
	})
	if err != nil || !envEmpty.OK || len(envEmpty.Results) != 0 || len(envEmpty.Meta.Providers) != 2 ||
		envEmpty.Meta.Providers[0].Status != core.ProviderStatusEmpty ||
		envEmpty.Meta.Providers[1].Status != core.ProviderStatusSkipped {
		t.Fatalf("expected clean empty OK envelope when zhihu_web empty + zhihu skipped, got env=%+v err=%v", envEmpty, err)
	}

	// 2. zhihu_web 返回 0 条（合法空结果）后回退 zhihu 遇瞬态 503：应保留合法空结果并标记 Partial=true，不误报全链失败。
	zwEmpty := &fakeSearcher{
		name:    spec.ProviderZhihuWeb,
		caps:    core.Capabilities{MaxCount: 20, Sites: core.SiteNone},
		outcome: &core.SearchOutcome{Results: []core.Result{}},
	}
	zhErr := &fakeSearcher{
		name: spec.ProviderZhihu,
		caps: core.Capabilities{MaxCount: 10, Sites: core.SiteNone, ScopeDomains: []string{"zhihu.com"}},
		err:  core.NewError(core.CodeUpstreamUnavailable, "503").WithProvider(spec.ProviderZhihu),
	}
	rEmptyThenErr := New(Options{FallbackOnEmpty: true}, []core.Provider{zwEmpty, zhErr})
	envPartialEmpty, err := rEmptyThenErr.Search(t.Context(), &core.SearchRequest{Query: "极冷门词"})
	if err != nil || !envPartialEmpty.OK || !envPartialEmpty.Meta.Partial || len(envPartialEmpty.Results) != 0 {
		t.Fatalf("expected clean empty partial envelope when fallback errors after empty success, got env=%+v err=%v", envPartialEmpty, err)
	}

	// 3. 仅启用 zhihu（用户禁用 zhihu_web）且未开 --strict 时查 -s go.dev：报错提示不应误导“请关闭 --strict”。
	rZhihuSingleOnly := New(Options{}, []core.Provider{zhValid})
	for _, strat := range []core.Strategy{core.StrategyAuto, core.StrategyFusion} {
		_, capErr := rZhihuSingleOnly.Search(t.Context(), &core.SearchRequest{
			Query:    "Go 并发",
			Strategy: strat,
			Filters:  core.Filters{Sites: []string{"go.dev"}},
		})
		se := core.AsError(capErr)
		if se == nil || se.Code != core.CodeNoProvider || se.HintKey != "hint.provider_capability_mismatch" {
			t.Fatalf("strategy=%s expected hint.provider_capability_mismatch without --strict, got %+v", strat, se)
		}
	}

	// 4. 配额独立与故障接管：zhihu_web 配额耗尽（30002）时自动切 zhihu，后续长驻调用直接跳过已降权的 zhihu_web。
	zwQuota := &fakeSearcher{
		name: spec.ProviderZhihuWeb,
		caps: core.Capabilities{MaxCount: 20, Sites: core.SiteNone},
		err:  core.NewError(core.CodeInsufficientBalance, "quota exceeded").WithProvider(spec.ProviderZhihuWeb),
	}
	rQuota := New(Options{}, []core.Provider{zwQuota, zhValid})
	envQ1, err := rQuota.Search(t.Context(), &core.SearchRequest{Query: "中文测试"})
	if err != nil || !envQ1.OK || len(envQ1.Meta.Providers) != 2 || envQ1.Meta.Providers[1].Name != spec.ProviderZhihu {
		t.Fatalf("expected auto failover from quota-exhausted zhihu_web to zhihu, got env=%+v err=%v", envQ1, err)
	}
	envQ2, err := rQuota.Search(t.Context(), &core.SearchRequest{Query: "中文测试二"})
	if err != nil || !envQ2.OK || len(envQ2.Meta.Providers) != 1 || envQ2.Meta.Providers[0].Name != spec.ProviderZhihu || zwQuota.calls.Load() != 1 {
		t.Fatalf("expected subsequent call to skip degraded zhihu_web, got env=%+v zwCalls=%d err=%v", envQ2, zwQuota.calls.Load(), err)
	}

	// 5. -s zhihu.com 专长源配额耗尽时自动回退至 zhihu_web 并后过滤仅保留 zhihu.com 结果。
	zwMixed := &fakeSearcher{
		name: spec.ProviderZhihuWeb,
		caps: core.Capabilities{MaxCount: 20, Sites: core.SiteNone},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "External", URL: "https://example.com/1"},
				{Rank: 2, Title: "Zhihu From Web", URL: "https://zhuanlan.zhihu.com/p/88"},
			},
		},
	}
	zhQuota := &fakeSearcher{
		name: spec.ProviderZhihu,
		caps: core.Capabilities{MaxCount: 10, Sites: core.SiteNone, ScopeDomains: []string{"zhihu.com"}},
		err:  core.NewError(core.CodeInsufficientBalance, "quota exceeded").WithProvider(spec.ProviderZhihu),
	}
	rSpecFailover := New(Options{}, []core.Provider{zwMixed, zhQuota})
	envSpecFB, err := rSpecFailover.Search(t.Context(), &core.SearchRequest{
		Query:   "Go 并发",
		Filters: core.Filters{Sites: []string{"zhihu.com"}},
	})
	if err != nil || !envSpecFB.OK || len(envSpecFB.Results) != 1 || envSpecFB.Results[0].URL != "https://zhuanlan.zhihu.com/p/88" {
		t.Fatalf("expected -s zhihu.com failover to zhihu_web with emulated site filter, got env=%+v err=%v", envSpecFB, err)
	}

	// 6. 风控拦截（30003 CodeBlockedByPolicy）绝不跨源回退到 zhihu，防止重复触发风控。
	zwBlocked := &fakeSearcher{
		name: spec.ProviderZhihuWeb,
		caps: core.Capabilities{MaxCount: 20, Sites: core.SiteNone},
		err:  core.NewError(core.CodeBlockedByPolicy, "blocked").WithProvider(spec.ProviderZhihuWeb).WithRetryable(false),
	}
	zhBefore := zhValid.calls.Load()
	rBlocked := New(Options{}, []core.Provider{zwBlocked, zhValid})
	if _, bErr := rBlocked.Search(t.Context(), &core.SearchRequest{Query: "风控词"}); core.AsError(bErr) == nil || core.AsError(bErr).Code != core.CodeBlockedByPolicy || zhValid.calls.Load() != zhBefore {
		t.Fatalf("blocked_by_policy must stop immediately without calling zhihu, got err=%v zhCalls=%d->%d", bErr, zhBefore, zhValid.calls.Load())
	}
}
