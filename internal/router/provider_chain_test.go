package router

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

type profiledFakeFetcher struct {
	name    string
	profile core.FetchProfile
	fn      func(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error)
}

func (f *profiledFakeFetcher) Name() string                    { return f.name }
func (f *profiledFakeFetcher) Capabilities() core.Capabilities { return core.Capabilities{} }
func (f *profiledFakeFetcher) FetchProfile() core.FetchProfile { return f.profile }
func (f *profiledFakeFetcher) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	return f.fn(ctx, req)
}

func TestNewProvidersFetchGlobalChainPDFPriorityAndEscalation(t *testing.T) {
	var order []string
	mk := func(name string, pdf, dyn, wall bool) core.Provider {
		return &profiledFakeFetcher{
			name:    name,
			profile: core.FetchProfile{PDF: pdf, DynamicRender: dyn},
			fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
				order = append(order, name)
				if wall {
					return nil, core.NewError(core.CodeContentUnavailable, "blocked").
						WithProvider(name).
						WithStatus(403).
						WithHintKey(core.HintKeyChallengeDetected)
				}
				return &core.FetchOutcome{
					Page: &core.Page{
						URL:      req.URL,
						Title:    "OK " + name,
						Content:  "# OK\n\nExtracted by " + name,
						Provider: name,
					},
				}, nil
			},
		}
	}

	// 1. 海外 PDF：稳定排序保证 exa -> parallel -> linkup 均排在按页计费的 firecrawl 之前，且未声明 PDF 的 keenable 不抢 PDF 优先权
	rPDF := New(Options{FetchStrategy: core.FetchAPIFirst}, []core.Provider{
		mk(spec.ProviderDeepSeek, false, false, false),
		mk(spec.ProviderKeenable, false, false, false),
		mk(spec.ProviderExa, true, false, false),
		mk(spec.ProviderLinkup, true, true, false),
		mk(spec.ProviderParallel, true, true, false),
		mk(spec.ProviderFirecrawl, true, true, false),
	})
	if got := rPDF.ActiveFetchChainGlobal(); !slices.Equal(got, []string{spec.ProviderExa, spec.ProviderParallel, spec.ProviderKeenable, spec.ProviderLinkup, spec.ProviderFirecrawl, spec.ProviderDeepSeek}) {
		t.Fatalf("ActiveFetchChainGlobal = %v", got)
	}
	order = nil
	envPDF, err := rPDF.Fetch(context.Background(), &core.FetchRequest{URL: "https://arxiv.org/pdf/2601.00001.pdf"})
	if err != nil || !envPDF.OK || envPDF.Page.Provider != spec.ProviderExa {
		t.Fatalf("expected overseas PDF to hit exa before firecrawl, got env=%+v err=%v order=%v", envPDF, err, order)
	}

	// 2. 反爬盾升档：首源 exa 遇到无 HTTP 状态码（Status=0）的挑战盾后，路由器置 DynamicRenderHint=true 并升档 linkup（跳过纯静态 keenable），且日志不输出 "(HTTP 0)"
	var linkupSawHint bool
	rWall := New(Options{
		FetchStrategy:        core.FetchAPIFirst,
		FetchProviders:       []string{spec.ProviderExa, spec.ProviderKeenable, spec.ProviderLinkup, spec.ProviderFirecrawl},
		FetchProvidersGlobal: []string{spec.ProviderExa, spec.ProviderKeenable, spec.ProviderLinkup, spec.ProviderFirecrawl},
	}, []core.Provider{
		&profiledFakeFetcher{
			name:    spec.ProviderExa,
			profile: core.FetchProfile{PDF: true, DynamicRender: false},
			fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
				order = append(order, spec.ProviderExa)
				return nil, core.NewError(core.CodeContentUnavailable, "blocked by bot challenge").
					WithProvider(spec.ProviderExa).
					WithHintKey(core.HintKeyChallengeDetected)
			},
		},
		mk(spec.ProviderKeenable, false, false, false),
		&profiledFakeFetcher{
			name:    spec.ProviderLinkup,
			profile: core.FetchProfile{PDF: true, DynamicRender: true},
			fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
				order = append(order, spec.ProviderLinkup)
				linkupSawHint = req.DynamicRenderHint
				return &core.FetchOutcome{
					Page: &core.Page{URL: req.URL, Title: "Rendered", Content: "# Rendered\n\nBody.", Provider: spec.ProviderLinkup},
				}, nil
			},
		},
		mk(spec.ProviderFirecrawl, true, true, false),
	})
	order = nil
	envWall, err := rWall.Fetch(context.Background(), &core.FetchRequest{URL: "https://protected.example.com/article"})
	if err != nil || !envWall.OK || envWall.Page.Provider != spec.ProviderLinkup || !linkupSawHint {
		t.Fatalf("expected anti-bot challenge to escalate to linkup with DynamicRenderHint=true, got env=%+v err=%v order=%v hint=%v", envWall, err, order, linkupSawHint)
	}
	if !slices.Equal(order, []string{spec.ProviderExa, spec.ProviderLinkup}) {
		t.Fatalf("expected escalation order [exa linkup], got %v", order)
	}
	if strings.Contains(strings.Join(envWall.Meta.Notes, "\n"), "HTTP 0") {
		t.Fatalf("challenge note must not print HTTP 0: %v", envWall.Meta.Notes)
	}

	// 3. 软 404 不应触发 DynamicRenderHint=true（避免对普通缺失页强行升档高成本渲染）
	var soft404NextSawHint bool
	rSoft404 := New(Options{
		FetchStrategy:  core.FetchAPIFirst,
		FetchProviders: []string{spec.ProviderDeepSeek, spec.ProviderLinkup},
	}, []core.Provider{
		&profiledFakeFetcher{
			name: spec.ProviderDeepSeek,
			fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
				return &core.FetchOutcome{
					Page: &core.Page{URL: req.URL, Title: "404 Not Found", Content: "Page not found.", Provider: spec.ProviderDeepSeek},
				}, nil
			},
		},
		&profiledFakeFetcher{
			name:    spec.ProviderLinkup,
			profile: core.FetchProfile{DynamicRender: true},
			fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
				soft404NextSawHint = req.DynamicRenderHint
				return &core.FetchOutcome{
					Page: &core.Page{URL: req.URL, Title: "Real Page", Content: "# Real\n\nSubstantial page body.", Provider: spec.ProviderLinkup},
				}, nil
			},
		},
	})
	if envSoft, err := rSoft404.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com/maybe-missing"}); err != nil || !envSoft.OK || soft404NextSawHint {
		t.Fatalf("soft-404 must NOT set DynamicRenderHint=true, got ok=%v hint=%v err=%v", envSoft != nil && envSoft.OK, soft404NextSawHint, err)
	}
}

type hostOnlyFakeSearcher struct {
	name      string
	caps      core.Capabilities
	lastSites []string
	lastExcl  []string
	results   []core.Result
}

func (s *hostOnlyFakeSearcher) Name() string                    { return s.name }
func (s *hostOnlyFakeSearcher) Capabilities() core.Capabilities { return s.caps }
func (s *hostOnlyFakeSearcher) Search(_ context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	s.lastSites = slices.Clone(req.Filters.Sites)
	s.lastExcl = slices.Clone(req.Filters.ExcludeSites)
	return &core.SearchOutcome{Results: slices.Clone(s.results)}, nil
}

func TestSiteHostOnlyCapabilityPreservesPathFilteringAndPreventsExcludeOverbroadening(t *testing.T) {
	s := &hostOnlyFakeSearcher{
		name: spec.ProviderLinkup,
		caps: core.Capabilities{
			MaxCount:     10,
			Sites:        core.SiteMulti,
			MaxSites:     100,
			ExcludeSites: 100,
			SiteHostOnly: true,
		},
		results: []core.Result{
			{Title: "Go Doc", URL: "https://go.dev/doc/effective_go", Summary: "doc"},
			{Title: "Go Blog", URL: "https://go.dev/blog/slices", Summary: "blog"},
			{Title: "Spam", URL: "https://github.com/spam-org/repo", Summary: "spam"},
			{Title: "Good", URL: "https://github.com/good-org/repo", Summary: "good"},
		},
	}
	r := New(Options{SearchProviders: []string{spec.ProviderLinkup}}, []core.Provider{s})

	// 1. 非严格模式下 -s go.dev/doc 下发主机名 go.dev 并在本地精确后过滤路径前缀
	envInc, err := r.Search(context.Background(), &core.SearchRequest{
		Query:   "golang",
		Count:   5,
		Filters: core.Filters{Sites: []string{"go.dev/doc"}},
	})
	if err != nil || !envInc.OK || len(envInc.Results) != 1 || envInc.Results[0].URL != "https://go.dev/doc/effective_go" {
		t.Fatalf("expected path-filtered go.dev/doc only, got results=%+v err=%v", envInc.Results, err)
	}
	if !slices.Equal(s.lastSites, []string{"go.dev"}) {
		t.Fatalf("expected host-only Sites=[go.dev] sent upstream, got %v", s.lastSites)
	}

	// 2. 非严格模式下 -x reddit.com -x github.com/spam-org 仅下发 reddit.com，绝不将 github.com/spam-org 扩成整站 github.com
	envExc, err := r.Search(context.Background(), &core.SearchRequest{
		Query:   "golang",
		Count:   5,
		Filters: core.Filters{ExcludeSites: []string{"reddit.com", "github.com/spam-org"}},
	})
	if err != nil || !envExc.OK {
		t.Fatalf("exclude search failed: err=%v", err)
	}
	if !slices.Equal(s.lastExcl, []string{"reddit.com"}) {
		t.Fatalf("expected ONLY pure host exclude [reddit.com] sent upstream, got %v", s.lastExcl)
	}
	for _, res := range envExc.Results {
		if strings.Contains(res.URL, "spam-org") {
			t.Fatalf("expected github.com/spam-org filtered locally, got %+v", envExc.Results)
		}
	}
	if !slices.ContainsFunc(envExc.Results, func(res core.Result) bool { return strings.Contains(res.URL, "good-org") }) {
		t.Fatalf("expected github.com/good-org preserved, got %+v", envExc.Results)
	}

	// 3. 严格模式下含路径的 -s 与 -x 均按不支持跳过/拒绝
	if _, err := r.Search(context.Background(), &core.SearchRequest{
		Query:         "golang",
		Count:         5,
		Providers:     []string{spec.ProviderLinkup},
		StrictFilters: true,
		Filters:       core.Filters{Sites: []string{"go.dev/doc"}},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("strict path Sites on SiteHostOnly must fail, got %v", err)
	}
	if _, err := r.Search(context.Background(), &core.SearchRequest{
		Query:         "golang",
		Count:         5,
		Providers:     []string{spec.ProviderLinkup},
		StrictFilters: true,
		Filters:       core.Filters{ExcludeSites: []string{"github.com/spam-org"}},
	}); core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("strict path ExcludeSites on SiteHostOnly must fail, got %v", err)
	}
}

func TestRouterNormalizesGenericHeadingAnchorsAndPreservesCodeAndGitHub(t *testing.T) {
	doc := strings.Join([]string{
		"# Effective Go [¶](https://go.dev/doc/effective_go\\#top)",
		"",
		"## Introduction [¶](https://go.dev/doc/effective_go\\#introduction \"Permalink\")",
		"",
		"Paragraph with literal [¶](#keep-body) stays intact.",
		"",
		"```markdown",
		"## Inside Fence [¶](#keep-fence)",
		"```",
		"",
		"## C# [#](#csharp)",
		"",
		"Body.",
	}, "\n")

	// 1. 普通云端网页（DeepSeek / Firecrawl 等 SourceGeneric）统一清理围栏外标题锚点并同步 TotalChars，保留正文与代码块内文本
	rGeneric := New(Options{FetchStrategy: core.FetchAPIOnly}, []core.Provider{
		&profiledFakeFetcher{
			name: spec.ProviderDeepSeek,
			fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
				return &core.FetchOutcome{
					Page: &core.Page{
						URL:        req.URL,
						Title:      "Effective Go¶",
						Content:    doc,
						TotalChars: len([]rune(doc)),
						Provider:   spec.ProviderDeepSeek,
						SourceType: core.SourceGeneric,
					},
				}, nil
			},
		},
	})
	env, err := rGeneric.Fetch(context.Background(), &core.FetchRequest{URL: "https://go.dev/doc/effective_go"})
	if err != nil || !env.OK {
		t.Fatalf("generic fetch failed: env=%+v err=%v", env, err)
	}
	if env.Page.Title != "Effective Go" {
		t.Fatalf("expected cleaned title, got %q", env.Page.Title)
	}
	if !strings.Contains(env.Page.Content, "## Introduction\n") || !strings.Contains(env.Page.Content, "## C#\n") {
		t.Fatalf("expected cleaned ATX heading lines in generic content:\n%s", env.Page.Content)
	}
	if !strings.Contains(env.Page.Content, "Paragraph with literal [¶](#keep-body) stays intact.") ||
		!strings.Contains(env.Page.Content, "## Inside Fence [¶](#keep-fence)") {
		t.Fatalf("must preserve non-heading body and fenced code block intact:\n%s", env.Page.Content)
	}
	if env.Page.TotalChars != len([]rune(env.Page.Content)) {
		t.Fatalf("TotalChars=%d must match normalized content runes=%d", env.Page.TotalChars, len([]rune(env.Page.Content)))
	}

	// 2. GitHub 源码文件（SourceGitHubFile）必须原封不动保留一字不改
	rGitHub := New(Options{FetchStrategy: core.FetchAPIOnly}, []core.Provider{
		&profiledFakeFetcher{
			name: spec.ProviderGitHub,
			fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
				return &core.FetchOutcome{
					Page: &core.Page{
						URL:        req.URL,
						Title:      "doc.md",
						Content:    doc,
						TotalChars: len([]rune(doc)),
						Provider:   spec.ProviderGitHub,
						SourceType: core.SourceGitHubFile,
					},
				}, nil
			},
		},
	})
	ghEnv, err := rGitHub.Fetch(context.Background(), &core.FetchRequest{
		URL:       "https://github.com/org/repo/blob/main/doc.md",
		Providers: []string{spec.ProviderGitHub},
	})
	if err != nil || !ghEnv.OK || ghEnv.Page.Content != doc {
		t.Fatalf("SourceGitHubFile content must remain untouched, got err=%v content=%q", err, ghEnv.Page.Content)
	}
}

type afterOnlyFakeSearcher struct {
	name          string
	lastFreshness *core.Freshness
	results       []core.Result
}

func (s *afterOnlyFakeSearcher) Name() string { return s.name }
func (s *afterOnlyFakeSearcher) Capabilities() core.Capabilities {
	return core.Capabilities{
		MaxCount:  20,
		Sites:     core.SiteMulti,
		MaxSites:  100,
		Freshness: core.TimeAfterOnly,
		Content:   core.ContentFree,
	}
}

func (s *afterOnlyFakeSearcher) Search(_ context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	s.lastFreshness = req.Filters.Freshness
	return &core.SearchOutcome{Results: slices.Clone(s.results)}, nil
}

func TestRouterTimeAfterOnlyFreshnessMatching(t *testing.T) {
	inRange := time.Date(2025, 4, 15, 0, 0, 0, 0, time.UTC)
	tooNew := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	s := &afterOnlyFakeSearcher{
		name: spec.ProviderParallel,
		results: []core.Result{
			{Title: "InRange", URL: "https://example.com/in", Published: &inRange},
			{Title: "TooNew", URL: "https://example.com/new", Published: &tooNew},
		},
	}
	r := New(Options{SearchProviders: []string{spec.ProviderParallel}}, []core.Provider{s})

	now := time.Date(2026, 11, 21, 0, 0, 0, 0, time.UTC)

	// 1. 相对时间窗与单下界绝对区间均 100% 原生支持，strict 模式直接通过且不降级
	for _, expr := range []string{"7d", "2025-01-01.."} {
		f, err := core.ParseFreshness(expr, now)
		if err != nil {
			t.Fatal(err)
		}
		env, err := r.Search(context.Background(), &core.SearchRequest{
			Query:         "golang",
			Count:         5,
			Providers:     []string{spec.ProviderParallel},
			StrictFilters: true,
			Filters:       core.Filters{Freshness: f},
		})
		if err != nil || !env.OK {
			t.Fatalf("strict TimeAfterOnly(%q) failed: env=%+v err=%v", expr, env, err)
		}
		if s.lastFreshness == nil || s.lastFreshness.After.IsZero() || !s.lastFreshness.Before.IsZero() {
			t.Fatalf("expected native after-only freshness forwarded for %q, got %+v", expr, s.lastFreshness)
		}
	}

	// 2. 双界区间（2025-01-01..2025-06-30）：非严格模式向下游发仅含 After 的下界收窄召回，并在本地精滤掉超过 Before 的结果
	fBoth, err := core.ParseFreshness("2025-01-01..2025-06-30", now)
	if err != nil {
		t.Fatal(err)
	}
	envBoth, err := r.Search(context.Background(), &core.SearchRequest{
		Query:     "golang",
		Count:     5,
		Providers: []string{spec.ProviderParallel},
		Filters:   core.Filters{Freshness: fBoth},
	})
	if err != nil || !envBoth.OK {
		t.Fatalf("non-strict bounded range failed: env=%+v err=%v", envBoth, err)
	}
	if s.lastFreshness == nil || s.lastFreshness.After.Format("2006-01-02") != "2025-01-01" || !s.lastFreshness.Before.IsZero() {
		t.Fatalf("expected upstream freshness narrowed to after-only 2025-01-01, got %+v", s.lastFreshness)
	}
	if len(envBoth.Results) != 1 || envBoth.Results[0].URL != "https://example.com/in" {
		t.Fatalf("expected local emulation to filter out tooNew result, got %+v", envBoth.Results)
	}

	// 3. 含 Before 上界的区间在 strict 模式下必须拒绝/跳过；纯上界非严格模式不向上游发 Freshness
	for _, expr := range []string{"2025-01-01..2025-06-30", "..2025-06-30"} {
		f, err := core.ParseFreshness(expr, now)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.Search(context.Background(), &core.SearchRequest{
			Query:         "golang",
			Count:         5,
			Providers:     []string{spec.ProviderParallel},
			StrictFilters: true,
			Filters:       core.Filters{Freshness: f},
		}); core.AsError(err).Code != core.CodeInvalidArgument {
			t.Fatalf("strict bounded/upper freshness %q on TimeAfterOnly must fail, got %v", expr, err)
		}
	}
	fUpper, _ := core.ParseFreshness("..2025-06-30", now)
	envUpper, err := r.Search(context.Background(), &core.SearchRequest{
		Query:     "golang",
		Count:     5,
		Providers: []string{spec.ProviderParallel},
		Filters:   core.Filters{Freshness: fUpper},
	})
	if err != nil || !envUpper.OK || s.lastFreshness != nil || len(envUpper.Results) != 1 {
		t.Fatalf("upper-only non-strict failed: lastFreshness=%+v env=%+v err=%v", s.lastFreshness, envUpper, err)
	}
}
