package router

import (
	"slices"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

func TestSearchPlanMinimalKeyCombinationsAndCapabilityPriority(t *testing.T) {
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

	ds := mk(spec.ProviderDeepSeek, core.Capabilities{MaxCount: 50, QueryMaxRunes: 128, Sites: core.SiteMulti, ExcludeSites: 20, Freshness: core.TimeAbsolute, Content: core.ContentFree})
	kn := mk(spec.ProviderKeenable, core.Capabilities{MaxCount: 20, Sites: core.SiteMulti, ExcludeSites: 20, Freshness: core.TimeBoth, Content: core.ContentFree})
	zw := mk(spec.ProviderZhihuWeb, core.Capabilities{MaxCount: 20, Sites: core.SiteNone, Freshness: core.TimeBoth, Content: core.ContentFree})
	zh := mk(spec.ProviderZhihu, core.Capabilities{MaxCount: 10, Sites: core.SiteNone, ScopeDomains: []string{"zhihu.com"}, Freshness: core.TimeBoth, Content: core.ContentFree},
		core.Result{Rank: 1, Title: "ZH Q&A", URL: "https://www.zhihu.com/question/42"},
	)
	wsa := mk(spec.ProviderTencentWSA, core.Capabilities{MaxCount: 20, Sites: core.SiteSingle, ExcludeSites: 5, Freshness: core.TimeBoth, Content: core.ContentNone})
	exa := mk(spec.ProviderExa, core.Capabilities{MaxCount: 10, Sites: core.SiteMulti, ExcludeSites: 1200, Freshness: core.TimeAbsolute, Content: core.ContentFree},
		core.Result{Rank: 1, Title: "Exa Go", URL: "https://go.dev/doc/effective_go"},
	)
	tavily := mk(spec.ProviderTavily, core.Capabilities{MaxCount: 20, Sites: core.SiteMulti, ExcludeSites: 300, Freshness: core.TimeBoth, Lang: true, Content: core.ContentFree})

	// 1. 仅配 zhihu + exa（2 把 Key / 3 个检索源）：核心边界组合
	rZhihuExa := New(Options{}, []core.Provider{zh, zw, exa})
	if got := rZhihuExa.EnabledSearchers(); !slices.Equal(got, []string{spec.ProviderZhihuWeb, spec.ProviderExa, spec.ProviderZhihu}) {
		t.Fatalf("zhihu+exa auto chain = %v", got)
	}
	if got := rZhihuExa.ActiveSearchChainGlobal(); !slices.Equal(got, []string{spec.ProviderExa, spec.ProviderZhihuWeb, spec.ProviderZhihu}) {
		t.Fatalf("zhihu+exa global auto chain = %v", got)
	}
	if got := rZhihuExa.ActiveFusionChain(); !slices.Equal(got, []string{spec.ProviderZhihuWeb, spec.ProviderExa}) {
		t.Fatalf("zhihu+exa CN fusion chain = %v, want [zhihu_web exa]", got)
	}
	if got := rZhihuExa.ActiveFusionChainGlobal(); !slices.Equal(got, []string{spec.ProviderExa, spec.ProviderZhihuWeb}) {
		t.Fatalf("zhihu+exa EN fusion chain = %v, want [exa zhihu_web]", got)
	}

	// 中文 auto 非严格 -s go.dev 与 -x zhihu.com：直接命中原生支持过滤的 exa，不先白打 zhihu_web
	zwBefore := zw.calls.Load()
	envSiteAuto, err := rZhihuExa.Search(t.Context(), &core.SearchRequest{
		Query:   "Go 并发",
		Filters: core.Filters{Sites: []string{"go.dev"}},
	})
	if err != nil || !envSiteAuto.OK || zw.calls.Load() != zwBefore || !slices.Equal(ranNames(envSiteAuto), []string{spec.ProviderExa}) {
		t.Fatalf("zhihu+exa auto -s go.dev should hit native exa directly, got names=%v zwCalls=%d->%d err=%v", ranNames(envSiteAuto), zwBefore, zw.calls.Load(), err)
	}
	envExclAuto, err := rZhihuExa.Search(t.Context(), &core.SearchRequest{
		Query:   "Go 并发",
		Filters: core.Filters{ExcludeSites: []string{"zhihu.com"}},
	})
	if err != nil || !envExclAuto.OK || zw.calls.Load() != zwBefore || !slices.Equal(ranNames(envExclAuto), []string{spec.ProviderExa}) {
		t.Fatalf("zhihu+exa auto -x zhihu.com should hit native exa directly, got names=%v zwCalls=%d->%d err=%v", ranNames(envExclAuto), zwBefore, zw.calls.Load(), err)
	}

	// 英文 -F 不拉入纯知乎站内 zhihu；英文 -F -s zhihu.com 精确命中作用域时置顶 zhihu
	zhBefore := zh.calls.Load()
	envENFusion, err := rZhihuExa.Search(t.Context(), &core.SearchRequest{
		Query:    "Rust async runtime",
		Strategy: core.StrategyFusion,
	})
	if err != nil || !envENFusion.OK || zh.calls.Load() != zhBefore || !slices.Equal(ranNames(envENFusion), []string{spec.ProviderExa, spec.ProviderZhihuWeb}) {
		t.Fatalf("zhihu+exa EN fusion must exclude zhihu vertical, got names=%v zhCalls=%d->%d err=%v", ranNames(envENFusion), zhBefore, zh.calls.Load(), err)
	}
	envENZhihuSite, err := rZhihuExa.Search(t.Context(), &core.SearchRequest{
		Query:    "goroutine leak",
		Strategy: core.StrategyFusion,
		Filters:  core.Filters{Sites: []string{"zhihu.com"}},
	})
	if err != nil || !envENZhihuSite.OK || !slices.Equal(ranNames(envENZhihuSite), []string{spec.ProviderZhihu, spec.ProviderExa}) {
		t.Fatalf("zhihu+exa EN fusion -s zhihu.com should promote zhihu specialist, got names=%v err=%v", ranNames(envENZhihuSite), err)
	}

	// 2. 3 把 Key（deepseek + zhihu + exa / zhihu + exa + tavily）：同凭据组不占 2 席，独立厂商不被挤出中文 -F
	rDsZhExa := New(Options{}, []core.Provider{ds, zh, zw, exa})
	if got := rDsZhExa.ActiveFusionChain(); !slices.Equal(got, []string{spec.ProviderDeepSeek, spec.ProviderZhihu, spec.ProviderExa}) {
		t.Fatalf("ds+zhihu+exa CN fusion = %v, want [deepseek zhihu exa]", got)
	}
	if got := rDsZhExa.ActiveFusionChainGlobal(); !slices.Equal(got, []string{spec.ProviderExa, spec.ProviderDeepSeek, spec.ProviderZhihuWeb}) {
		t.Fatalf("ds+zhihu+exa EN fusion = %v, want [exa deepseek zhihu_web]", got)
	}
	rZhExaTav := New(Options{}, []core.Provider{zh, zw, exa, tavily})
	if got := rZhExaTav.ActiveFusionChain(); !slices.Equal(got, []string{spec.ProviderZhihu, spec.ProviderExa, spec.ProviderTavily}) {
		t.Fatalf("zhihu+exa+tavily CN fusion = %v, want [zhihu exa tavily]", got)
	}
	// 非严格 -F -s go.dev 在 ds + kn + zh + zw + exa 下优先选 3 家原生 SiteMulti（ds, kn, exa），不浪费席位给 emulated zhihu_web
	rNativeFusion := New(Options{}, []core.Provider{ds, kn, zh, zw, exa})
	envNativeSiteFusion, err := rNativeFusion.Search(t.Context(), &core.SearchRequest{
		Query:    "Go 并发",
		Strategy: core.StrategyFusion,
		Filters:  core.Filters{Sites: []string{"go.dev"}},
	})
	if err != nil || !envNativeSiteFusion.OK || !slices.Equal(ranNames(envNativeSiteFusion), []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderExa}) {
		t.Fatalf("non-strict -F -s go.dev should prefer 3 native providers over emulated zhihu_web, got names=%v err=%v", ranNames(envNativeSiteFusion), err)
	}

	// 3. 禁用 zhihu_web 仅剩 zhihu + exa：中文 -F 共存，英文 -F 抑制单站垂直源仅跑 exa
	rNoWeb := New(Options{}, []core.Provider{zh, exa})
	if got := rNoWeb.ActiveFusionChain(); !slices.Equal(got, []string{spec.ProviderZhihu, spec.ProviderExa}) {
		t.Fatalf("zhihu(no web)+exa CN fusion = %v, want [zhihu exa]", got)
	}
	if got := rNoWeb.ActiveFusionChainGlobal(); !slices.Equal(got, []string{spec.ProviderExa}) {
		t.Fatalf("zhihu(no web)+exa EN fusion = %v, want [exa]", got)
	}

	// 4. 仅配 zhihu 单 Key + -F -s zhihu.com：不拉入需 sites:emulated 的同组 zhihu_web
	rZhihuOnly := New(Options{}, []core.Provider{zh, zw})
	envZhihuOnlySite, err := rZhihuOnly.Search(t.Context(), &core.SearchRequest{
		Query:    "Go 并发",
		Strategy: core.StrategyFusion,
		Filters:  core.Filters{Sites: []string{"zhihu.com"}},
	})
	if err != nil || !envZhihuOnlySite.OK || !slices.Equal(ranNames(envZhihuOnlySite), []string{spec.ProviderZhihu}) {
		t.Fatalf("zhihu-only -F -s zhihu.com should run native zhihu without emulated zhihu_web, got names=%v err=%v", ranNames(envZhihuOnlySite), err)
	}

	// 5. 作用域保底边界：tencent_wsa (ContentNone) + zhihu (ContentFree, 垂直单站) 搜开放词 -d content，绝不把单站 zhihu 提到全网前
	rScopeFloor := New(Options{}, []core.Provider{wsa, zh})
	envContent, err := rScopeFloor.Search(t.Context(), &core.SearchRequest{
		Query:  "今天北京天气",
		Detail: core.DetailContent,
	})
	if err != nil || !envContent.OK || !slices.Equal(ranNames(envContent), []string{spec.ProviderTencentWSA}) {
		t.Fatalf("open-web -d content must keep general tencent_wsa ahead of vertical zhihu, got names=%v err=%v", ranNames(envContent), err)
	}
}

func TestSearchPlanHealthCooldownRotationInMinimalPool(t *testing.T) {
	ranNames := func(env *core.Envelope) []string {
		out := make([]string, 0, len(env.Meta.Providers))
		for _, p := range env.Meta.Providers {
			out = append(out, p.Name)
		}
		return out
	}

	// 1. zhihu + exa 中 exa 触发 402 冷却后，中文 -F 切至健康双源 [zhihu_web zhihu]，英文 -F 切至 [zhihu_web]，不反复空打 exa
	exaDead := &fakeSearcher{
		name: spec.ProviderExa,
		caps: core.Capabilities{MaxCount: 10, Sites: core.SiteMulti},
		err:  core.NewError(core.CodeInsufficientBalance, "out of credits").WithProvider(spec.ProviderExa),
	}
	zw := &fakeSearcher{
		name:    spec.ProviderZhihuWeb,
		caps:    core.Capabilities{MaxCount: 20},
		outcome: &core.SearchOutcome{Results: []core.Result{{Rank: 1, Title: "ZW", URL: "https://example.com/zw"}}},
	}
	zh := &fakeSearcher{
		name:    spec.ProviderZhihu,
		caps:    core.Capabilities{MaxCount: 10, ScopeDomains: []string{"zhihu.com"}},
		outcome: &core.SearchOutcome{Results: []core.Result{{Rank: 1, Title: "ZH", URL: "https://www.zhihu.com/question/1"}}},
	}
	rExaDown := New(Options{}, []core.Provider{zh, zw, exaDead})
	if env1, err := rExaDown.Search(t.Context(), &core.SearchRequest{Query: "中文一", Strategy: core.StrategyFusion}); err != nil || !env1.OK || !env1.Meta.Partial {
		t.Fatalf("first CN fusion should fail partially on exa 402: env=%+v err=%v", env1, err)
	}
	envCN2, err := rExaDown.Search(t.Context(), &core.SearchRequest{Query: "中文二", Strategy: core.StrategyFusion})
	if err != nil || !envCN2.OK || envCN2.Meta.Partial || exaDead.calls.Load() != 1 || !slices.Equal(ranNames(envCN2), []string{spec.ProviderZhihuWeb, spec.ProviderZhihu}) {
		t.Fatalf("CN fusion during exa cooldown should run healthy [zhihu_web zhihu], got names=%v exaCalls=%d err=%v", ranNames(envCN2), exaDead.calls.Load(), err)
	}
	envEN2, err := rExaDown.Search(t.Context(), &core.SearchRequest{Query: "golang", Strategy: core.StrategyFusion})
	if err != nil || !envEN2.OK || envEN2.Meta.Partial || exaDead.calls.Load() != 1 || !slices.Equal(ranNames(envEN2), []string{spec.ProviderZhihuWeb}) {
		t.Fatalf("EN fusion during exa cooldown should run healthy [zhihu_web], got names=%v exaCalls=%d err=%v", ranNames(envEN2), exaDead.calls.Load(), err)
	}

	// 2. zhihu + exa 中 zhihu_web 冷却时，健康 zhihu 不向不健康兄弟让位，中文 -F 自动切至 [zhihu exa]
	zwDead := &fakeSearcher{
		name: spec.ProviderZhihuWeb,
		caps: core.Capabilities{MaxCount: 20},
		err:  core.NewError(core.CodeInsufficientBalance, "daily web quota exhausted").WithProvider(spec.ProviderZhihuWeb),
	}
	exaOK := &fakeSearcher{
		name:    spec.ProviderExa,
		caps:    core.Capabilities{MaxCount: 10, Sites: core.SiteMulti},
		outcome: &core.SearchOutcome{Results: []core.Result{{Rank: 1, Title: "Exa", URL: "https://example.com/exa"}}},
	}
	rZWDown := New(Options{}, []core.Provider{zh, zwDead, exaOK})
	if env1, err := rZWDown.Search(t.Context(), &core.SearchRequest{Query: "中文一", Strategy: core.StrategyFusion}); err != nil || !env1.OK || !env1.Meta.Partial {
		t.Fatalf("first CN fusion should record zhihu_web quota error: env=%+v err=%v", env1, err)
	}
	env2, err := rZWDown.Search(t.Context(), &core.SearchRequest{Query: "中文二", Strategy: core.StrategyFusion})
	if err != nil || !env2.OK || env2.Meta.Partial || zwDead.calls.Load() != 1 || !slices.Equal(ranNames(env2), []string{spec.ProviderZhihu, spec.ProviderExa}) {
		t.Fatalf("healthy zhihu must not defer to cooled-down zhihu_web, got names=%v zwCalls=%d err=%v", ranNames(env2), zwDead.calls.Load(), err)
	}

	// 3. 仅配知乎单 Key 且 zhihu_web 冷却时，英文 -F 不被不健康全网源压死，回退健康 zhihu
	zwDeadSingle := &fakeSearcher{
		name: spec.ProviderZhihuWeb,
		caps: core.Capabilities{MaxCount: 20},
		err:  core.NewError(core.CodeInsufficientBalance, "daily web quota exhausted").WithProvider(spec.ProviderZhihuWeb),
	}
	rZhihuOnlyZWDown := New(Options{}, []core.Provider{zh, zwDeadSingle})
	if _, err := rZhihuOnlyZWDown.Search(t.Context(), &core.SearchRequest{Query: "golang", Strategy: core.StrategyFusion}); err == nil {
		t.Fatalf("first EN fusion with dead zhihu_web should fail and trigger cooldown")
	}
	envENFallback, err := rZhihuOnlyZWDown.Search(t.Context(), &core.SearchRequest{Query: "golang memory model", Strategy: core.StrategyFusion})
	if err != nil || !envENFallback.OK || zwDeadSingle.calls.Load() != 1 || !slices.Equal(ranNames(envENFallback), []string{spec.ProviderZhihu}) {
		t.Fatalf("EN fusion should fall back to healthy zhihu when zhihu_web is in cooldown, got names=%v zwCalls=%d err=%v", ranNames(envENFallback), zwDeadSingle.calls.Load(), err)
	}
}

func TestRRFCredentialGroupMaxPoolingAndIndependentQuorum(t *testing.T) {
	// 1. RRF 同凭据组最大池化：zhihu 与 zhihu_web 重合 URL 取组内最大单票（含 rrfTerm > prev 升级路径），不再叠加 2 票压制独立源 exa；跨厂商（exa + zhihu）仍正常双票共识
	branches := []branchResult{
		{
			chainIdx: 0,
			name:     spec.ProviderExa,
			results: []core.Result{
				{Rank: 1, Title: "Official Docs", URL: "https://go.dev/doc/spec", Summary: "official spec"},
				{Rank: 2, Title: "Cross Consensus", URL: "https://www.zhihu.com/question/consensus", Summary: "consensus"},
			},
		},
		{
			chainIdx: 1,
			name:     spec.ProviderZhihuWeb,
			results: []core.Result{
				{Rank: 1, Title: "Web Only", URL: "https://example.org/web-only", Summary: "web"},
				{Rank: 2, Title: "Same Group Only", URL: "https://www.zhihu.com/question/same-group", Summary: "zhihu web copy"},
				{Rank: 3, Title: "Cross Consensus", URL: "https://www.zhihu.com/question/consensus", Summary: "consensus"},
			},
		},
		{
			chainIdx: 2,
			name:     spec.ProviderZhihu,
			results: []core.Result{
				{Rank: 1, Title: "Same Group Only", URL: "https://www.zhihu.com/question/same-group", Summary: "zhihu community copy"},
				{Rank: 2, Title: "Cross Consensus", URL: "https://www.zhihu.com/question/consensus", Summary: "consensus"},
			},
		},
	}
	fused, _ := fuseRRF(branches, 60, func(string) float64 { return 1.0 }, 5)
	if len(fused) != 4 {
		t.Fatalf("expected 4 fused results, got %+v", fused)
	}
	// Rank 1 为跨独立厂商共识（exa + zhihu 组，2 独立组）；
	// Rank 2 为 exa Rank 1（chainIdx=0 胜出，未被同组 zhihu+zhihu_web 2x 压制）；
	// Rank 3 为 zhihu_web Rank 1（chainIdx=1 胜出）；
	// Rank 4 为仅知乎同组命中的链接（虽从 Rank 2 升级至 Rank 1 的 max 分 1/61，但仍仅计 1 组），且保留完整 providers 溯源。
	if fused[0].URL != "https://www.zhihu.com/question/consensus" {
		t.Fatalf("expected cross-vendor consensus #1, got %+v", fused)
	}
	if fused[1].URL != "https://go.dev/doc/spec" {
		t.Fatalf("same-vendor zhihu+zhihu_web must not outvote exa #1, got %+v", fused)
	}
	if fused[2].URL != "https://example.org/web-only" {
		t.Fatalf("expected zhihu_web #1 ahead of same-group rank-1/2 tie, got %+v", fused)
	}
	if fused[3].URL != "https://www.zhihu.com/question/same-group" || !slices.Equal(fused[3].Providers, []string{spec.ProviderZhihuWeb, spec.ProviderZhihu}) {
		t.Fatalf("expected same-group item #4 with full provenance providers, got %+v", fused[3])
	}

	// 2. 近重复聚类的规范代表页 URL 共识票数同样按独立凭据组去重：知乎双分支转载不以 2:1 压过单源原始页
	longBody := "Go 1.24 正式发布，全面支持泛型类型别名，并将内置 map 底层重构为基于 SwissTable 的哈希表实现，大幅降低高并发场景下的内存开销与探测延迟。同时标准库新增 os.Root 目录隔离访问能力、weak 弱引用包以及 testing.B.Loop 基准测试循环，进一步提升云原生服务端程序的安全性、性能与可维护性。"
	dupBranches := []branchResult{
		{
			chainIdx: 0,
			name:     spec.ProviderExa,
			results: []core.Result{
				{Rank: 1, Title: "Go 1.24 Release Notes", URL: "https://go.dev/doc/go1.24", Summary: longBody},
			},
		},
		{
			chainIdx: 1,
			name:     spec.ProviderZhihuWeb,
			results: []core.Result{
				{Rank: 1, Title: "Go 1.24 Release Notes", URL: "https://zhuanlan.zhihu.com/p/reprint124", Summary: longBody},
			},
		},
		{
			chainIdx: 2,
			name:     spec.ProviderZhihu,
			results: []core.Result{
				{Rank: 1, Title: "Go 1.24 Release Notes", URL: "https://zhuanlan.zhihu.com/p/reprint124", Summary: longBody},
			},
		},
	}
	dupFused, deduped := fuseRRF(dupBranches, 60, func(string) float64 { return 1.0 }, 5)
	if len(dupFused) != 1 || deduped != 1 || dupFused[0].URL != "https://go.dev/doc/go1.24" {
		t.Fatalf("same-vendor reprint must not outvote independent canonical URL 2:1, got deduped=%d res=%+v", deduped, dupFused)
	}

	// 3. 显式 -F -p zhihu,zhihu_web,exa 时，慢源法定数按独立凭据组计数：zhihu + zhihu_web 同组返回绝不提前截断慢 exa
	mkDelayed := func(name string, delay time.Duration, url string) *fakeSearcher {
		return &fakeSearcher{
			name:    name,
			caps:    core.Capabilities{MaxCount: 10},
			delay:   delay,
			outcome: &core.SearchOutcome{Results: []core.Result{{Rank: 1, Title: name, URL: url}}},
		}
	}
	rQuorum := New(Options{}, []core.Provider{
		mkDelayed(spec.ProviderZhihu, 0, "https://www.zhihu.com/question/1"),
		mkDelayed(spec.ProviderZhihuWeb, 0, "https://example.com/zw"),
		mkDelayed(spec.ProviderExa, 80*time.Millisecond, "https://go.dev/slow-exa"),
	})
	rQuorum.stragglerGrace = 15 * time.Millisecond
	envQ, err := rQuorum.Search(t.Context(), &core.SearchRequest{
		Query:     "Go 并发",
		Strategy:  core.StrategyFusion,
		Providers: []string{spec.ProviderZhihu, spec.ProviderZhihuWeb, spec.ProviderExa},
	})
	if err != nil || !envQ.OK || envQ.Meta.Partial {
		t.Fatalf("same-vendor zhihu+zhihu_web must not trigger straggler cancel on sole independent exa: env=%+v err=%v", envQ, err)
	}
	foundExa := false
	for _, res := range envQ.Results {
		if res.URL == "https://go.dev/slow-exa" {
			foundExa = true
		}
	}
	if !foundExa {
		t.Fatalf("expected slow exa result to be preserved, got %+v", envQ.Results)
	}

	// 4. searchEmptyQuorum 按独立凭据组计数：显式 -p zhihu_web,zhihu,exa 中知乎同组双空不会提前短路独立源 exa
	zwEmpty := &fakeSearcher{name: spec.ProviderZhihuWeb, caps: core.Capabilities{MaxCount: 20}, outcome: &core.SearchOutcome{Results: []core.Result{}}}
	zhEmpty := &fakeSearcher{name: spec.ProviderZhihu, caps: core.Capabilities{MaxCount: 10, ScopeDomains: []string{"zhihu.com"}}, outcome: &core.SearchOutcome{Results: []core.Result{}}}
	exaHit := &fakeSearcher{name: spec.ProviderExa, caps: core.Capabilities{MaxCount: 10}, outcome: &core.SearchOutcome{Results: []core.Result{{Rank: 1, Title: "Exa", URL: "https://go.dev/hit"}}}}
	rEmptyGroup := New(Options{FallbackOnEmpty: true}, []core.Provider{zwEmpty, zhEmpty, exaHit})
	envEmpty, err := rEmptyGroup.Search(t.Context(), &core.SearchRequest{
		Query:     "冷门词",
		Providers: []string{spec.ProviderZhihuWeb, spec.ProviderZhihu, spec.ProviderExa},
	})
	if err != nil || !envEmpty.OK || len(envEmpty.Results) != 1 || envEmpty.Results[0].URL != "https://go.dev/hit" {
		t.Fatalf("same-vendor double empty must not short-circuit independent exa: env=%+v err=%v", envEmpty, err)
	}
}
