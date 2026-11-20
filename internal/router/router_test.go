package router

import (
	"context"
	"math"
	"net/url"
	"strings"
	"testing"
	"time"

	"veri/internal/core"
)

type fakeSearcher struct {
	name    string
	caps    core.Capabilities
	delay   time.Duration
	outcome *core.SearchOutcome
	err     error
	lastReq *core.SearchRequest
}

func (f *fakeSearcher) Name() string                    { return f.name }
func (f *fakeSearcher) Capabilities() core.Capabilities { return f.caps }
func (f *fakeSearcher) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	cp := *req
	f.lastReq = &cp
	if f.delay > 0 {
		select {
		case <-time.After(f.delay):
		case <-ctx.Done():
			return nil, core.WrapError(core.CodeUpstreamTimeout, "timed out", ctx.Err()).WithProvider(f.name)
		}
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.outcome, nil
}

type fakeFetcher struct {
	name    string
	outcome *core.FetchOutcome
	err     error
}

func (f *fakeFetcher) Name() string                    { return f.name }
func (f *fakeFetcher) Capabilities() core.Capabilities { return core.Capabilities{} }
func (f *fakeFetcher) Fetch(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.outcome, nil
}

type fakeSpecialistFetcher struct {
	fakeFetcher
	matchHost string
}

func (f *fakeSpecialistFetcher) Match(u *url.URL) bool {
	return u != nil && strings.EqualFold(u.Hostname(), f.matchHost)
}

func TestSearchAutoFallbackLanguageRoutingAndEmulation(t *testing.T) {
	ds := &fakeSearcher{
		name: "deepseek",
		caps: core.Capabilities{MaxCount: 50, Sites: core.SiteMulti, Freshness: core.TimeAbsolute},
		err:  core.NewError(core.CodeInsufficientBalance, "402").WithProvider("deepseek"),
	}
	wsa := &fakeSearcher{
		name: "tencent_wsa",
		caps: core.Capabilities{MaxCount: 50, CountSteps: []int{10, 20}, Sites: core.SiteSingle, Freshness: core.TimeBoth},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Rank: 1, Title: "Keep Go", URL: "https://go.dev/doc", Summary: "Go documentation"},
				{Rank: 2, Title: "Drop Other", URL: "https://unrelated.org/a", Summary: "Other site"},
			},
			Cost: &core.Cost{Currency: core.CurrencyCNY, Micros: 20000, Estimated: true},
		},
	}
	exa := &fakeSearcher{
		name: "exa",
		caps: core.Capabilities{MaxCount: 10, Sites: core.SiteMulti},
		outcome: &core.SearchOutcome{
			Results: []core.Result{{Rank: 1, Title: "Exa Hit", URL: "https://exa.ai/p", Summary: "English fast result"}},
			Cost:    &core.Cost{Currency: core.CurrencyUSD, Micros: 4000, Estimated: false},
		},
	}

	r := New(Options{
		SearchProviders:   []string{"deepseek", "tencent_wsa", "exa"},
		SearchProvidersEN: []string{"exa", "deepseek"},
		FallbackOnEmpty:   true,
	}, []core.Provider{ds, wsa, exa})

	// 1. 中文查询走默认链：deepseek 402 失败 -> 自动回退 tencent_wsa；且 2 个站点在 SiteSingle 上触发本地模拟后过滤！
	env, err := r.Search(context.Background(), &core.SearchRequest{
		Query:   "Go 并发编程",
		Count:   5,
		Filters: core.Filters{Sites: []string{"go.dev", "github.com"}},
	})
	if err != nil || !env.OK || !env.Meta.FallbackUsed {
		t.Fatalf("expected fallback to succeed: env=%+v err=%v", env, err)
	}
	if len(env.Results) != 1 || env.Results[0].URL != "https://go.dev/doc" {
		t.Fatalf("expected emulated site post-filter to keep only go.dev, got %+v", env.Results)
	}
	if wsa.lastReq.Count != 10 {
		t.Fatalf("expected discrete CountSteps upward rounding to 10, got %d", wsa.lastReq.Count)
	}

	// 2. 纯英文查询命中配置的 SearchProvidersEN（直接命中 exa，不触发 deepseek）
	enEnv, err := r.Search(context.Background(), &core.SearchRequest{Query: "golang context cancellation"})
	if err != nil || len(enEnv.Meta.Providers) != 1 || enEnv.Meta.Providers[0].Name != "exa" {
		t.Fatalf("expected English query to route to exa first, got %+v err=%v", enEnv, err)
	}
}

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

	env, err := r.Search(context.Background(), &core.SearchRequest{
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

func TestSmartLocalEscalationFallbackAndBudgetPagination(t *testing.T) {
	gh := &fakeSpecialistFetcher{
		name: "github",
		outcome: &core.FetchOutcome{
			Page: &core.Page{URL: "https://github.com/a/b", Title: "GH Readme", Content: "# Repo", Provider: "github", SourceType: "github_readme"},
		},
		matchHost: "github.com",
	}
	locLow := &fakeFetcher{
		name: "local",
		outcome: &core.FetchOutcome{
			Page:       &core.Page{URL: "https://spa.test/doc", Title: "Partial Doc", Content: "Short partial text.", Provider: "local", SourceType: "generic"},
			LowQuality: true,
			QualityMsg: "too_short",
		},
	}
	tf := &fakeFetcher{
		name: "tinyfish",
		outcome: &core.FetchOutcome{
			Page: &core.Page{
				URL:        "https://spa.test/doc",
				Title:      "Full Rendered SPA",
				Content:    "Paragraph one line.\n\nParagraph two with full dynamic content rendered cleanly.",
				Provider:   "tinyfish",
				SourceType: "generic",
			},
			Cost: &core.Cost{Currency: core.CurrencyUSD, Micros: 0},
		},
	}

	r := New(Options{
		FetchStrategy:  core.FetchSmartLocal,
		FetchProviders: []string{"tinyfish"},
	}, []core.Provider{gh, locLow, tf})

	// 1. GitHub URL 直接走专用提取器
	ghEnv, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/a/b"})
	if err != nil || ghEnv.Page.Provider != "github" {
		t.Fatalf("expected github specialist, got %+v err=%v", ghEnv, err)
	}

	// 2. 普通 SPA 页面：local 返回 LowQuality -> 自动升档 tinyfish 成功返回完整内容！
	spaEnv, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://spa.test/doc", MaxChars: 25})
	if err != nil || spaEnv.Page.Provider != "tinyfish" || !spaEnv.Meta.FallbackUsed {
		t.Fatalf("expected smart_local escalation to tinyfish, got %+v err=%v", spaEnv, err)
	}
	if spaEnv.Page.NextOffset == nil {
		t.Fatal("expected paginated NextOffset when MaxChars=25")
	}
	contEnv, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://spa.test/doc", Offset: *spaEnv.Page.NextOffset})
	if err != nil || !strings.Contains(contEnv.Page.Content, "Paragraph two") {
		t.Fatalf("unexpected continuation page: %+v err=%v", contEnv, err)
	}

	// 3. 无 API 提取器时，非反爬盾页的 too_short 本地正文应优雅保底返回（不丢数据）
	rNoAPI := New(Options{FetchStrategy: core.FetchSmartLocal}, []core.Provider{locLow})
	fbEnv, err := rNoAPI.Fetch(context.Background(), &core.FetchRequest{URL: "https://spa.test/doc"})
	if err != nil || !fbEnv.OK || fbEnv.Page.Content != "Short partial text." {
		t.Fatalf("expected graceful fallback to non-wall local text, got %+v err=%v", fbEnv, err)
	}

	// 4. 验证四级渐进式字符预算截断
	longResults := []core.Result{
		{Rank: 1, Title: "R1", URL: "https://a.com/1", Snippet: "S1", Summary: strings.Repeat("A", 500), Content: strings.Repeat("C", 1000)},
		{Rank: 2, Title: "R2", URL: "https://a.com/2", Snippet: "S2", Summary: strings.Repeat("B", 500), Content: strings.Repeat("D", 1000)},
	}
	trimmed, truncated, notes := applyFieldCapsAndBudget(longResults, 800, 0, 400)
	if !truncated || len(notes) == 0 || trimmed[1].Content != "" {
		t.Fatalf("expected budget truncation, truncated=%v trimmed=%+v", truncated, trimmed)
	}

	// 5. 验证 SlicePage 在非法 UTF-8 字节与超大 maxChars 下不越界 panic、不发生整数溢出
	badUTF8 := SlicePage(&core.Page{Content: "\xff\xff\xff\xff"}, 0, 3)
	if badUTF8 == nil || len(badUTF8.Content) != 3 || badUTF8.NextOffset == nil || *badUTF8.NextOffset != 3 {
		t.Fatalf("SlicePage invalid UTF-8 mismatch: %+v", badUTF8)
	}
	maxPage := SlicePage(&core.Page{Content: "hello world"}, 5, math.MaxInt)
	if maxPage.Content != " world" || maxPage.NextOffset != nil {
		t.Fatalf("SlicePage math.MaxInt overflow regression: %+v", maxPage)
	}
}

func TestSearchAutoSingleAndMultiFailureDiagnostics(t *testing.T) {
	wsaFail := &fakeSearcher{
		name: "tencent_wsa",
		caps: core.Capabilities{MaxCount: 50},
		err:  core.NewError(core.CodeAuthFailed, "未授权操作。").WithProvider("tencent_wsa"),
	}
	rSingle := New(Options{SearchProviders: []string{"exa", "tencent_wsa"}}, []core.Provider{wsaFail})
	if _, err := rSingle.Search(context.Background(), &core.SearchRequest{Query: "golang"}); err == nil {
		t.Fatal("expected error when single enabled provider fails")
	} else {
		se := core.AsError(err)
		if se.Provider != "tencent_wsa" || !strings.Contains(se.Message, "未授权操作") {
			t.Fatalf("expected single enabled provider failure to preserve provider & message, got %+v", se)
		}
	}

	dsFail := &fakeSearcher{
		name: "deepseek",
		caps: core.Capabilities{MaxCount: 50},
		err:  core.NewError(core.CodeRateLimited, "429").WithProvider("deepseek"),
	}
	rMulti := New(Options{SearchProviders: []string{"deepseek", "tencent_wsa"}}, []core.Provider{dsFail, wsaFail})
	if _, err := rMulti.Search(context.Background(), &core.SearchRequest{Query: "golang"}); err == nil {
		t.Fatal("expected error when all providers fail")
	} else {
		se := core.AsError(err)
		if !strings.Contains(se.Message, "deepseek: rate_limited") || !strings.Contains(se.Message, "tencent_wsa: auth_failed") {
			t.Fatalf("expected multi-provider failure summary, got %+v", se)
		}
	}

	localChallenge := &fakeFetcher{
		name:    "local",
		outcome: &core.FetchOutcome{Page: &core.Page{URL: "https://cf.test", Content: "Verify"}, LowQuality: true, QualityMsg: "bot_or_js_wall"},
	}
	exaAuthFail := &fakeFetcher{
		name: "exa",
		err:  core.NewError(core.CodeAuthFailed, "invalid api key").WithProvider("exa"),
	}
	rFetch := New(Options{FetchStrategy: core.FetchSmartLocal, FetchProviders: []string{"exa"}}, []core.Provider{localChallenge, exaAuthFail})
	if _, err := rFetch.Fetch(context.Background(), &core.FetchRequest{URL: "https://cf.test"}); err == nil {
		t.Fatal("expected fetch error when local hits bot wall and single API extractor fails")
	} else if se := core.AsError(err); se.Provider != "exa" || !strings.Contains(se.Message, "invalid api key") {
		t.Fatalf("expected fetch failure to preserve single failing API extractor attribution, got %+v", se)
	}
}

type fakeDynamicSearcher struct {
	fakeSearcher
	capsFor func(req *core.SearchRequest) core.Capabilities
}

func (f *fakeDynamicSearcher) CapabilitiesFor(req *core.SearchRequest) core.Capabilities {
	return f.capsFor(req)
}

func TestDynamicSearcherCapabilityNarrowingAndEmulation(t *testing.T) {
	now := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	fresh, _ := core.ParseFreshness("d7", now)
	oldDate := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	newDate := time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC)

	dyn := &fakeDynamicSearcher{
		name: "exa",
		caps: core.Capabilities{MaxCount: 10, ExcludeSites: 1200, Freshness: core.TimeAbsolute},
		outcome: &core.SearchOutcome{
			Results: []core.Result{
				{Title: "Drop Domain", URL: "https://blocked.com/profile", Published: &newDate},
				{Title: "Drop Old", URL: "https://ok.com/old", Published: &oldDate},
				{Title: "Keep Hit", URL: "https://ok.com/new", Published: &newDate},
			},
		},
		capsFor: func(req *core.SearchRequest) core.Capabilities {
			c := core.Capabilities{MaxCount: 10, ExcludeSites: 1200, Freshness: core.TimeAbsolute}
			if req.Options["exa.category"] == "people" {
				c.ExcludeSites = 0
				c.Freshness = core.TimeNone
			}
			return c
		},
	}

	r := New(Options{SearchProviders: []string{"exa"}}, []core.Provider{dyn})
	env, err := r.Search(context.Background(), &core.SearchRequest{
		Query:   "researcher",
		Options: map[string]string{"exa.category": "people"},
		Filters: core.Filters{ExcludeSites: []string{"blocked.com"}, Freshness: fresh},
	})
	if err != nil {
		t.Fatalf("unexpected error in lenient mode: %v", err)
	}
	if dyn.lastReq.Filters.Freshness != nil || len(dyn.lastReq.Filters.ExcludeSites) != 0 {
		t.Fatalf("expected conflicting filters stripped before calling provider, got %+v", dyn.lastReq.Filters)
	}
	if len(env.Results) != 1 || env.Results[0].Title != "Keep Hit" {
		t.Fatalf("expected emulated post-filtering to keep only matching result, got %+v", env.Results)
	}

	// Strict 模式下必须拒绝不兼容组合
	if _, err := r.Search(context.Background(), &core.SearchRequest{
		Query:         "researcher",
		Providers:     []string{"exa"},
		StrictFilters: true,
		Options:       map[string]string{"exa.category": "people"},
		Filters:       core.Filters{Freshness: fresh},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument under strict mode, got %v", err)
	}
}
