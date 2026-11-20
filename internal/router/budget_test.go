package router

import (
	"context"
	"testing"
	"time"

	"veri/internal/core"
)

func TestRouterStrictDegradationAndAllFetchStrategies(t *testing.T) {
	oldPub := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	recentPub := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)

	limited := &fakeSearcher{
		name: "limited",
		caps: core.Capabilities{
			MaxCount:     5,
			Sites:        core.SiteNone,
			ExcludeSites: 1,
			Freshness:    core.TimeNone,
			Lang:         false,
			Content:      core.ContentNone,
			Question:     false,
		},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "Keep Recent", URL: "https://go.dev/a", Published: &recentPub, Summary: "ok"},
				{Rank: 2, Title: "Drop Old", URL: "https://go.dev/old", Published: &oldPub, Summary: "old"},
				{Rank: 3, Title: "Keep Undated", URL: "https://go.dev/undated", Summary: "no date"},
				{Rank: 4, Title: "Drop Excluded", URL: "https://bad2.com/x", Published: &recentPub, Summary: "bad"},
			},
		},
	}

	r := New(Options{
		SearchProviders: []string{"limited"},
		Weights:         map[string]float64{"limited": 1.5},
	}, []core.Provider{limited})

	if len(r.EnabledSearchers()) != 1 {
		t.Fatalf("expected 1 enabled searcher, got %v", r.EnabledSearchers())
	}

	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	fresh, _ := core.ParseFreshness("d7", now)

	// 1. 非严格模式：Sites / ExcludeSites 超限 / Freshness / Lang / Content 全部降级模拟并记录 degraded
	env, err := r.Search(context.Background(), &core.SearchRequest{
		Query:    "go",
		Question: "ignored question",
		Count:    20,
		Detail:   core.DetailContent,
		Filters: core.Filters{
			Sites:        []string{"go.dev", "bad2.com"},
			ExcludeSites: []string{"bad1.com", "bad2.com"},
			Lang:         []string{"en"},
			Freshness:    fresh,
		},
	})
	if err != nil || !env.OK {
		t.Fatalf("emulated search failed: env=%+v err=%v", env, err)
	}
	if len(env.Results) != 2 {
		t.Fatalf("expected Keep Recent + Keep Undated (2 results), got %+v", env.Results)
	}

	// 2. Strict 模式在 single 策略下直接拒绝不支持的能力
	_, err = r.Search(context.Background(), &core.SearchRequest{
		Query:         "go",
		Providers:     []string{"limited"},
		StrictFilters: true,
		Filters:       core.Filters{Sites: []string{"go.dev"}},
	})
	if core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument in strict single mode, got %v", err)
	}

	// 3. 测试 api_first、local_only、api_only 与反爬盾页不保底
	locWall := &fakeFetcher{
		name: "local",
		outcome: &core.FetchOutcome{
			Page:       &core.Page{URL: "https://cf.test", Title: "Just a moment...", Content: "Checking your browser", Provider: "local"},
			LowQuality: true,
			QualityMsg: "bot_or_js_wall",
		},
	}
	apiFail := &fakeFetcher{
		name: "deepseek",
		err:  core.NewError(core.CodeContentUnavailable, "404").WithProvider("deepseek"),
	}
	rWall := New(Options{
		FetchStrategy:  core.FetchSmartLocal,
		FetchProviders: []string{"deepseek"},
	}, []core.Provider{locWall, apiFail})

	if len(rWall.EnabledFetchers()) != 2 {
		t.Fatalf("expected 2 fetchers, got %v", rWall.EnabledFetchers())
	}
	// bot_or_js_wall 在后续 API 也失败时绝不能把盾页废话当成功返回！
	if _, err := rWall.Fetch(context.Background(), &core.FetchRequest{URL: "https://cf.test"}); err == nil {
		t.Fatal("expected bot_or_js_wall without working API to return error, not shield text")
	}

	// api_first：API 失败后回退 local
	locGood := &fakeFetcher{
		name: "local",
		outcome: &core.FetchOutcome{
			Page: &core.Page{URL: "https://ok.test", Title: "OK", Content: "Good local content", Provider: "local"},
		},
	}
	rAPIFirst := New(Options{}, []core.Provider{locGood, apiFail})
	afEnv, err := rAPIFirst.Fetch(context.Background(), &core.FetchRequest{
		URL:      "https://ok.test",
		Strategy: core.FetchAPIFirst,
	})
	if err != nil || afEnv.Page.Provider != "local" || !afEnv.Meta.FallbackUsed {
		t.Fatalf("expected api_first to fallback to local, got %+v err=%v", afEnv, err)
	}

	// local_only
	loEnv, err := rAPIFirst.Fetch(context.Background(), &core.FetchRequest{
		URL:      "https://ok.test",
		Strategy: core.FetchLocalOnly,
	})
	if err != nil || loEnv.Page.Provider != "local" {
		t.Fatalf("expected local_only to succeed, got %+v err=%v", loEnv, err)
	}

	// 显式指定未启用的提取器
	if _, err := rAPIFirst.Fetch(context.Background(), &core.FetchRequest{
		URL:       "https://ok.test",
		Providers: []string{"nonexistent"},
	}); core.AsError(err).Code != core.CodeNoProvider {
		t.Fatalf("expected CodeNoProvider for nonexistent explicit fetcher, got %v", err)
	}

	// 4. 覆盖 nil 请求、非法参数、api_only、第 4 级极限字符预算截断、以及 RRF 全字段合并与 Capability 剩余分支
	if _, err := r.Search(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatal("expected nil SearchRequest error")
	}
	if _, err := r.Search(context.Background(), &core.SearchRequest{Query: "   "}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatal("expected empty query error")
	}
	if _, err := r.Fetch(context.Background(), nil); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatal("expected nil FetchRequest error")
	}
	if _, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://ok.test", Offset: -1}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatal("expected negative offset error")
	}

	// TimeRelative 遇到绝对区间自动模拟，SiteMulti 超 MaxSites 自动模拟
	relCapSearcher := &fakeSearcher{
		name: "relcap",
		caps: core.Capabilities{
			MaxCount:  10,
			Sites:     core.SiteMulti,
			MaxSites:  1,
			Freshness: core.TimeRelative,
		},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "Hit", URL: "https://a.com/x", Published: &recentPub, Snippet: "snip", Summary: "longer summary text", Content: "body"},
			},
		},
	}
	absWindow, _ := core.ParseFreshness("2026-11-01..2026-11-16", now)
	rRel := New(Options{SearchProviders: []string{"relcap"}}, []core.Provider{relCapSearcher})
	envExtreme, err := rRel.Search(context.Background(), &core.SearchRequest{
		Query:    "test",
		Detail:   core.DetailContent,
		MaxChars: 10, // 极小预算，强制跑完第 1~4 步全部截断！
		Filters:  core.Filters{Sites: []string{"a.com", "b.com"}, Freshness: absWindow},
	})
	if err != nil || !envExtreme.Meta.Truncated || envExtreme.Results[0].Summary != "" || envExtreme.Results[0].Snippet != "" {
		t.Fatalf("expected stage-4 budget strip to title+url only, got %+v err=%v", envExtreme, err)
	}

	// RRF 合并全字段（Published 更早值、Authority 最大值、Content 最长值、Site/Icon 补全）
	tEarly := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	tLate := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	auth1, auth2 := 0.5, 0.9
	fA := &fakeSearcher{
		name: "pA",
		caps: core.Capabilities{MaxCount: 10},
		outcome: &core.SearchOutcome{Results: []core.Result{
			{Rank: 2, Title: "", URL: "https://same.org/doc", Published: &tLate, Authority: &auth1, Content: "short"},
		}},
	}
	fB := &fakeSearcher{
		name: "pB",
		caps: core.Capabilities{MaxCount: 10},
		outcome: &core.SearchOutcome{Results: []core.Result{
			{Rank: 1, Title: "Better Title", URL: "https://same.org/doc", Site: "SameOrg", Icon: "https://same.org/i.png", Published: &tEarly, Snippet: "Better Snip", Authority: &auth2, Content: "much longer content"},
		}},
	}
	rFuse := New(Options{SearchProviders: []string{"pA", "pB"}, Weights: map[string]float64{"pA": 1.0, "pB": 1.2}}, []core.Provider{fA, fB})
	fEnv, err := rFuse.Search(context.Background(), &core.SearchRequest{Query: "merge", Strategy: core.StrategyFusion, Detail: core.DetailContent})
	if err != nil || fEnv.Results[0].Title != "Better Title" || *fEnv.Results[0].Authority != 0.9 || !fEnv.Results[0].Published.Equal(tEarly) {
		t.Fatalf("unexpected RRF field merge: %+v err=%v", fEnv, err)
	}

	// SlicePage 换行回退与超出总长分支
	longLines := "line1\nline2\nline3\nline4\n"
	sliced := SlicePage(&core.Page{URL: "https://a.com", Content: longLines}, 0, 14)
	if sliced.NextOffset == nil || *sliced.NextOffset != 12 {
		t.Fatalf("expected line-boundary cut at 12, got %+v", sliced.NextOffset)
	}
	pastEnd := SlicePage(&core.Page{URL: "https://a.com", Content: "abc"}, 10, 5)
	if pastEnd.Content != "" {
		t.Fatal("expected empty content when offset >= total")
	}

	// 覆盖 Fusion 全部失败、Auto 严格模式全部跳过、以及 api_only 无提取器分支
	failS := &fakeSearcher{
		name: "fails",
		caps: core.Capabilities{MaxCount: 10, Sites: core.SiteNone},
		err:  core.NewError(core.CodeRateLimited, "429").WithProvider("fails"),
	}
	rFail := New(Options{SearchProviders: []string{"fails"}}, []core.Provider{failS})
	if _, err := rFail.Search(context.Background(), &core.SearchRequest{Query: "q", Strategy: core.StrategyFusion}); err == nil {
		t.Fatal("expected all-failed fusion to return error")
	}
	if _, err := rFail.Search(context.Background(), &core.SearchRequest{
		Query: "q", StrictFilters: true, Filters: core.Filters{Sites: []string{"a.com"}},
	}); core.AsError(err).Code != core.CodeNoProvider {
		t.Fatal("expected all-skipped auto search to return CodeNoProvider")
	}
	if _, err := rFail.Fetch(context.Background(), &core.FetchRequest{URL: "https://a.com", Strategy: core.FetchAPIOnly}); core.AsError(err).Code != core.CodeNoProvider {
		t.Fatal("expected api_only with no fetchers to return CodeNoProvider")
	}

	// 验证检索出口自动剥离营销追踪参数同时保留功能参数与锚点
	tracked, _, _ := applyFieldCapsAndBudget([]core.Result{
		{Rank: 1, Title: "Go 1.24", URL: "https://go.dev/blog/go1.24?utm_source=tldr&fbclid=abc#intro"},
		{Rank: 2, Title: "Source", URL: "https://tip.golang.org/src/context/context.go?m=text&utm_medium=rss"},
	}, 800, 0, 0)
	if tracked[0].URL != "https://go.dev/blog/go1.24#intro" {
		t.Fatalf("expected tracking params stripped keeping fragment, got %q", tracked[0].URL)
	}
	if tracked[1].URL != "https://tip.golang.org/src/context/context.go?m=text" {
		t.Fatalf("expected functional param m=text preserved, got %q", tracked[1].URL)
	}
}
