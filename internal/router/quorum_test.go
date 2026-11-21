package router

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"veri/internal/core"
	"veri/internal/spec"
)

type spySpecialistFetcher struct {
	spyFetcher
	matchFn func(*url.URL) bool
}

func (s *spySpecialistFetcher) Match(u *url.URL) bool { return s.matchFn != nil && s.matchFn(u) }

func TestFetchNotFoundQuorumAndMostDecisive(t *testing.T) {
	// 1. api_first 下云端首源返回 not_found -> 自动提权免费 local 确认 -> 立即终止，不烧后续付费源
	var order []string
	tf := &spyFetcher{
		name: spec.ProviderTinyFish,
		fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, spec.ProviderTinyFish)
			return nil, core.NewError(core.CodeNotFound, "404").WithProvider(spec.ProviderTinyFish).WithStatus(404)
		},
	}
	tv := &spyFetcher{
		name: spec.ProviderTavily,
		fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, spec.ProviderTavily)
			return nil, core.NewError(core.CodeNotFound, "404").WithProvider(spec.ProviderTavily).WithStatus(404)
		},
	}
	ds := &spyFetcher{
		name: spec.ProviderDeepSeek,
		fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, spec.ProviderDeepSeek)
			return nil, core.NewError(core.CodeUpstreamTimeout, "timeout").WithProvider(spec.ProviderDeepSeek)
		},
	}
	loc := &spyFetcher{
		name: spec.ProviderLocal,
		fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, spec.ProviderLocal)
			return nil, core.NewError(core.CodeNotFound, "404").WithProvider(spec.ProviderLocal).WithStatus(404)
		},
	}

	r := New(Options{
		FetchStrategy:  core.FetchAPIFirst,
		FetchProviders: []string{spec.ProviderTinyFish, spec.ProviderTavily, spec.ProviderDeepSeek},
	}, []core.Provider{tf, tv, ds, loc})

	env, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.org/dead"})
	if err == nil || core.AsError(err).Code != core.CodeNotFound || env.OK {
		t.Fatalf("expected api_first 404 quorum -> not_found, got env=%+v err=%v", env, err)
	}
	if strings.Join(order, ",") != "tinyfish,local" {
		t.Fatalf("expected [tinyfish, local] without burning tavily/deepseek, got %v", order)
	}

	// 2. api_only（无 local）下连续 2 家云端 not_found 达法定数立即停止，不触发第 3 家超时
	order = nil
	env2, err2 := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.org/dead", Strategy: core.FetchAPIOnly})
	if err2 == nil || core.AsError(err2).Code != core.CodeNotFound || env2.OK {
		t.Fatalf("expected api_only 2-cloud quorum -> not_found, got env=%+v err=%v", env2, err2)
	}
	if strings.Join(order, ",") != "tinyfish,tavily" {
		t.Fatalf("expected [tinyfish, tavily] stopping before deepseek, got %v", order)
	}

	// 3. 专用源（如 github）返回 404/not_found 不计入法定数，允许云端接管成功
	order = nil
	gh := &spySpecialistFetcher{
		name: spec.ProviderGitHub,
		fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, spec.ProviderGitHub)
			return nil, core.NewError(core.CodeNotFound, "release not found").WithProvider(spec.ProviderGitHub).WithStatus(404)
		},
		matchFn: func(u *url.URL) bool { return u.Host == "github.com" },
	}
	tfOK := &spyFetcher{
		name: spec.ProviderTinyFish,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, spec.ProviderTinyFish)
			return &core.FetchOutcome{Page: &core.Page{URL: req.URL, Content: strings.Repeat("release notes ", 30), Provider: spec.ProviderTinyFish}}, nil
		},
	}
	rGH := New(Options{FetchStrategy: core.FetchAPIFirst, FetchProviders: []string{spec.ProviderTinyFish}}, []core.Provider{gh, tfOK})
	ghEnv, ghErr := rGH.Fetch(context.Background(), &core.FetchRequest{URL: "https://github.com/org/repo/releases/tag/v0.1.0"})
	if ghErr != nil || !ghEnv.OK || ghEnv.Page.Provider != spec.ProviderTinyFish {
		t.Fatalf("expected github 404 to fall through to tinyfish, got env=%+v err=%v", ghEnv, ghErr)
	}

	// 4. MostDecisive：前序 blocked_by_policy 不被链尾 upstream_timeout 掩盖
	pBlocked := &spyFetcher{
		name: spec.ProviderTinyFish,
		fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
			return nil, core.NewError(core.CodeBlockedByPolicy, "403").WithProvider(spec.ProviderTinyFish)
		},
	}
	rDec := New(Options{FetchStrategy: core.FetchAPIOnly, FetchProviders: []string{spec.ProviderTinyFish, spec.ProviderDeepSeek}}, []core.Provider{pBlocked, ds})
	if _, decErr := rDec.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.org/waf"}); core.AsError(decErr).Code != core.CodeBlockedByPolicy {
		t.Fatalf("expected MostDecisive to prefer blocked_by_policy over trailing timeout, got %v", decErr)
	}

	// 5. 同时指定 providers 与 strategy 应返回本地化冲突错误
	_, confErr := r.Fetch(context.Background(), &core.FetchRequest{
		URL:       "https://example.org/doc",
		Providers: []string{spec.ProviderTinyFish},
		Strategy:  core.FetchLocalOnly,
	})
	se := core.AsError(confErr)
	if se == nil || se.Code != core.CodeInvalidArgument || se.MsgKey != "err.fetch_providers_strategy_conflict" {
		t.Fatalf("expected fetch_providers_strategy_conflict, got %+v", se)
	}
}

func TestApplyPageViewSectionAndMatchPipeline(t *testing.T) {
	doc := strings.Join([]string{
		"## Intro",
		"This intro also mentions channel once.",
		"",
		"## Concurrency",
		"Goroutines coordinate via select and channel primitives.",
		"",
		"Another unrelated paragraph.",
		"",
		"## Errors",
		"Always check errors; never ignore channel close errors.",
	}, "\n")

	full := &core.Page{URL: "https://go.dev/doc/effective_go", Content: doc}
	view, notes := ApplyPageView(full, &core.FetchRequest{Section: "Concurrency", Match: "channel"})
	if len(notes) != 0 {
		t.Fatalf("unexpected notes: %v", notes)
	}
	if strings.Contains(view.Content, "This intro") || strings.Contains(view.Content, "Always check errors") {
		t.Fatalf("match leaked outside target section:\n%s", view.Content)
	}
	if !strings.Contains(view.Content, "Goroutines coordinate via select and channel") {
		t.Fatalf("expected matching passage within Concurrency section:\n%s", view.Content)
	}
	wantFullOffset := utf8.RuneCountInString(doc[:strings.Index(doc, "Goroutines coordinate")])
	if !strings.Contains(view.Content, fmt.Sprintf("offset=%d", wantFullOffset)) {
		t.Fatalf("expected global offset=%d in anchor, got:\n%s", wantFullOffset, view.Content)
	}
	// 章节内 0 命中时返回章节预览 + 明确提示
	missView, missNotes := ApplyPageView(full, &core.FetchRequest{Section: "Concurrency", Match: "nonexistent_term_xyz"})
	if len(missNotes) == 0 || !strings.Contains(missNotes[0], `in section "Concurrency"`) || !strings.Contains(missView.Content, "## Concurrency") {
		t.Fatalf("unexpected section match miss fallback: notes=%v content=%s", missNotes, missView.Content)
	}
	// mergeAuthors 保序去重验证
	if got := mergeAuthors([]string{"Alice", ""}, []string{"alice", "Bob"}); strings.Join(got, ",") != "Alice,Bob" {
		t.Fatalf("mergeAuthors got %v, want [Alice Bob]", got)
	}
}

func TestSearchEmptyFallbackQuorum(t *testing.T) {
	mkEmpty := func(name string, siteCap core.SiteSupport) *fakeSearcher {
		return &fakeSearcher{
			name:    name,
			caps:    core.Capabilities{MaxCount: 20, Sites: siteCap},
			outcome: &core.SearchOutcome{Results: []core.Result{}},
		}
	}
	// s1 需后过滤模拟（Sites=SiteNone），不计入原生否定法定数；s2 与 s3 原生支持 Sites，连续 2 次 empty 触发法定数停止，不调用 s4
	s1 := mkEmpty("emulated_src", core.SiteNone)
	s2 := mkEmpty("native_a", core.SiteMulti)
	s3 := mkEmpty("native_b", core.SiteMulti)
	s4 := &fakeSearcher{
		name: "native_c",
		caps: core.Capabilities{MaxCount: 20, Sites: core.SiteMulti},
		outcome: &core.SearchOutcome{
			Results: []core.Result{{Rank: 1, Title: "ShouldNotBeCalled", URL: "https://rare.example/1"}},
		},
	}

	r := New(Options{
		SearchProviders: []string{"emulated_src", "native_a", "native_b", "native_c"},
		FallbackOnEmpty: true,
	}, []core.Provider{s1, s2, s3, s4})

	env, err := r.Search(context.Background(), &core.SearchRequest{
		Query:   "nonexistent_token",
		Count:   3,
		Filters: core.Filters{Sites: []string{"rare.example"}},
	})
	if err != nil || !env.OK {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(env.Meta.Providers) != 3 {
		t.Fatalf("expected 3 calls (emulated + 2 native empties) stopping before native_c, got %+v", env.Meta.Providers)
	}
	if len(env.Results) != 0 {
		t.Fatalf("expected 0 results after native quorum, got %+v", env.Results)
	}
}

func TestLocalOnlyNotesAndWallHintAccuracy(t *testing.T) {
	shortLocal := &spyFetcher{
		name: spec.ProviderLocal,
		fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
			return &core.FetchOutcome{
				Page:       &core.Page{URL: "https://example.com", Title: "Example", Content: "Short legitimate page.", Provider: spec.ProviderLocal},
				LowQuality: true,
				QualityMsg: "too_short",
			}, nil
		},
	}
	rShort := New(Options{FetchStrategy: core.FetchAPIFirst}, []core.Provider{shortLocal})
	env, err := rShort.Fetch(context.Background(), &core.FetchRequest{URL: "https://example.com", Strategy: core.FetchLocalOnly})
	if err != nil || !env.OK {
		t.Fatalf("local_only too_short should gracefully succeed: env=%+v err=%v", env, err)
	}
	if len(env.Meta.Notes) == 0 || !strings.Contains(env.Meta.Notes[0], "local_only") || strings.Contains(env.Meta.Notes[0], "configure an API") {
		t.Fatalf("unexpected local_only fallback note: %+v", env.Meta.Notes)
	}

	wallLocal := &spyFetcher{
		name: spec.ProviderLocal,
		fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
			return &core.FetchOutcome{
				Page:       &core.Page{URL: "https://spa.example", Title: "Just a moment...", Content: "Enable JavaScript", Provider: spec.ProviderLocal},
				LowQuality: true,
				QualityMsg: "bot_or_js_wall",
			}, nil
		},
	}
	rWall := New(Options{FetchStrategy: core.FetchAPIFirst}, []core.Provider{wallLocal})
	_, wallErr := rWall.Fetch(context.Background(), &core.FetchRequest{URL: "https://spa.example", Strategy: core.FetchLocalOnly})
	se := core.AsError(wallErr)
	if se.Code != core.CodeContentUnavailable || se.HintKey != "hint.local_only_js_wall" {
		t.Fatalf("expected hint.local_only_js_wall under local_only, got %+v", se)
	}
}

func TestApplyPageViewOffsetExceededNote(t *testing.T) {
	doc := "# Intro\n\nHello world.\n\n## Concurrency\n\nGoroutines communicate via channels.\n"
	full := &core.Page{URL: "https://example.com/doc", Title: "Doc", Content: doc}
	total := utf8.RuneCountInString(doc)

	// 1. 全文越界（包含刚好等于总长与超大偏移）均安全返回空正文并附带明确 note
	for _, off := range []int{total, 99999999} {
		view, notes := ApplyPageView(full, &core.FetchRequest{Offset: off, MaxChars: 1000})
		if view.Content != "" || view.NextOffset != nil || view.TotalChars != total {
			t.Fatalf("offset=%d unexpected view: %+v", off, view)
		}
		wantNote := fmt.Sprintf("offset %d exceeds available content length (%d chars)", off, total)
		if !slices.Contains(notes, wantNote) {
			t.Fatalf("offset=%d expected note %q, got %v", off, wantNote, notes)
		}
	}

	// 2. offset=0 的空页面及正常末页（0 < offset < total）均不误报越界 note
	emptyView, emptyNotes := ApplyPageView(&core.Page{URL: "https://example.com/empty", Content: ""}, &core.FetchRequest{Offset: 0})
	if emptyView.Content != "" || len(emptyNotes) != 0 {
		t.Fatalf("empty page at offset 0 should not emit offset note, got %v", emptyNotes)
	}
	tailView, tailNotes := ApplyPageView(full, &core.FetchRequest{Offset: total - 5, MaxChars: 100})
	if tailView.Content == "" || tailView.NextOffset != nil || len(tailNotes) != 0 {
		t.Fatalf("valid tail page should not emit offset note: view=%+v notes=%v", tailView, tailNotes)
	}

	// 3. --section 与 --match 作用域视图内越界同样提示对应视图长度
	secView, secNotes := ApplyPageView(full, &core.FetchRequest{Section: "Concurrency", Offset: 9999})
	if secView.Content != "" || !slices.Contains(secNotes, fmt.Sprintf("offset 9999 exceeds available content length (%d chars)", secView.TotalChars)) {
		t.Fatalf("section offset overflow mismatch: view=%+v notes=%v", secView, secNotes)
	}
	matchView, matchNotes := ApplyPageView(full, &core.FetchRequest{Match: "channels", Offset: 9999})
	if matchView.Content != "" || !slices.Contains(matchNotes, fmt.Sprintf("offset 9999 exceeds available content length (%d chars)", matchView.TotalChars)) {
		t.Fatalf("match offset overflow mismatch: view=%+v notes=%v", matchView, matchNotes)
	}
}
