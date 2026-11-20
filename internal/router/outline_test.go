package router

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"veri/internal/core"
	"veri/internal/mdscan"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

func TestFenceAwareSliceOutlineSectionAndMatchViews(t *testing.T) {
	doc := strings.Join([]string{
		"# Architecture Guide",
		"",
		"Introductory overview of the system.",
		"",
		"## Authentication",
		"",
		"Use bearer tokens for all API calls. Rate limit headers include `Retry-After`.",
		"",
		"```go",
		"// # inside code fence must not become an outline heading",
		"req.Header.Set(\"Authorization\", \"Bearer \" + token)",
		"resp, err := client.Do(req)",
		"```",
		"",
		"## Pagination",
		"",
		"Pass `offset` and `max_chars` to read subsequent chunks safely without losing code fences.",
	}, "\n")

	full := &core.Page{
		URL:        "https://docs.example.com/guide",
		Title:      "Architecture Guide",
		Content:    doc,
		TotalChars: textnorm.RuneCount(doc),
		Provider:   spec.ProviderLocal,
		SourceType: core.SourceGeneric,
	}

	// 1. ExtractOutline skips `#` comments inside code fences.
	outline := ExtractOutline(doc, 10)
	if len(outline) != 3 || outline[1].Heading != "Authentication" || outline[2].Heading != "Pagination" {
		t.Fatalf("unexpected outline (code comment leaked?): %+v", outline)
	}

	// 2. Fence-aware SlicePage: if maxChars lands mid-codeblock started in the second half, backtrack before fence.
	before, _, _ := strings.Cut(doc, "```go")
	codeStartRunes := textnorm.RuneCount(before)
	chunk1 := SlicePage(full, 0, codeStartRunes+30)
	if chunk1.NextOffset == nil || *chunk1.NextOffset != codeStartRunes {
		t.Fatalf("expected SlicePage to backtrack cleanly before code fence at %d, got next_offset=%v\n%s", codeStartRunes, chunk1.NextOffset, chunk1.Content)
	}
	if len(chunk1.Outline) != 3 {
		t.Fatalf("expected truncated chunk to carry page outline, got %+v", chunk1.Outline)
	}
	chunk2 := SlicePage(full, *chunk1.NextOffset, 500)
	if !strings.HasPrefix(chunk2.Content, "```go\n") || !strings.Contains(chunk2.Content, "## Pagination") {
		t.Fatalf("expected chunk2 to start cleanly with intact code block:\n%s", chunk2.Content)
	}

	// 3. Oversized code block split across pages gets closing fence on page 1 and opening fence on page 2.
	giant := "```python\n" + strings.Repeat("print('line')\n", 40) + "```"
	giantPage := &core.Page{URL: "https://example.com/giant", Content: giant}
	g1 := SlicePage(giantPage, 0, 120)
	if g1.NextOffset == nil || !strings.HasSuffix(g1.Content, "```") {
		t.Fatalf("expected split giant code block to close fence on chunk 1:\n%s", g1.Content)
	}
	g2 := SlicePage(giantPage, *g1.NextOffset, 2000)
	if !strings.HasPrefix(g2.Content, "```python\n") {
		t.Fatalf("expected split giant code block to reopen ```python on chunk 2:\n%s", g2.Content)
	}

	// 4. Section view extracts target heading subtree.
	secView, notes := ApplyPageView(full, &core.FetchRequest{Section: "auth"})
	if len(notes) != 0 || !strings.HasPrefix(secView.Content, "## Authentication") || strings.Contains(secView.Content, "## Pagination") {
		t.Fatalf("unexpected section view: notes=%v content=\n%s", notes, secView.Content)
	}

	// 5. Match view extracts matching passages with heading context and offset.
	matchView, mNotes := ApplyPageView(full, &core.FetchRequest{Match: "Retry-After"})
	if len(mNotes) != 0 || !strings.Contains(matchView.Content, "Authentication") || !strings.Contains(matchView.Content, "Retry-After") {
		t.Fatalf("unexpected match view: notes=%v content=\n%s", mNotes, matchView.Content)
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
		env, err := r.Search(context.Background(), &core.SearchRequest{Query: "中文路由测试", Count: 3})
		if err != nil || !env.OK {
			t.Fatalf("expected fallback to healthy exa, err=%v", err)
		}
	}

	// 4th call should route directly to healthy exa first (FallbackUsed == false)!
	env4, err := r.Search(context.Background(), &core.SearchRequest{Query: "中文路由测试", Count: 3})
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
	fEnv, err := rf.Search(context.Background(), &core.SearchRequest{Query: "test", Strategy: core.StrategyFusion, Count: 2})
	if err != nil || s1.lastReq.Count != defaultFusionFetchFloor || len(fEnv.Results) != 2 || fEnv.Results[0].URL != "https://shared.example.com" {
		t.Fatalf("expected fusion fetchFloor=%d and consensus winner #1, got s1.Count=%d results=%+v err=%v",
			defaultFusionFetchFloor, s1.lastReq.Count, fEnv.Results, err)
	}
}

func TestEffectiveGoInlineBacktickRegression(t *testing.T) {
	// 复现 effective_go 第 31 行行首单反引号导致的围栏奇偶性翻转缺陷（P1-01）。
	doc := strings.Join([]string{
		"## Introduction",
		"Overview text.",
		"",
		"### Examples",
		"Sample text.",
		"",
		"## Formatting",
		"```",
		"type T struct {",
		"    name string",
		"}",
		"```",
		"",
		"`gofmt` will line up the columns:",
		"",
		"```",
		"type T struct {",
		"    name string // aligned",
		"}",
		"```",
		"",
		"All Go code in the standard packages has been formatted with `gofmt`.",
		"",
		"## Names",
		"~100ms is typical.",
		"",
		"### Defer",
		"Go's `defer` statement schedules a function call.",
		"",
		"## Concurrency",
		"### Share by communicating",
		"Do not communicate by sharing memory; instead, share memory by communicating.",
	}, "\n")

	full := &core.Page{URL: "https://go.dev/doc/effective_go", Title: "Effective Go", Content: doc}
	outline := ExtractOutline(doc, 24)
	if len(outline) != 7 {
		t.Fatalf("expected all 7 headings after inline backtick line, got %d: %+v", len(outline), outline)
	}

	secView, notes := ApplyPageView(full, &core.FetchRequest{Section: "Concurrency"})
	if len(notes) != 0 || !strings.HasPrefix(secView.Content, "## Concurrency") || !strings.Contains(secView.Content, "Share by communicating") {
		t.Fatalf("expected Concurrency section extracted cleanly, notes=%v content=\n%s", notes, secView.Content)
	}

	// 验证连续分页不会在 chunk2 页首产生孤立围栏
	p1 := SlicePage(full, 0, 180)
	if p1.NextOffset == nil {
		t.Fatal("expected first slice to paginate")
	}
	p2 := SlicePage(full, *p1.NextOffset, 200)
	if strings.HasPrefix(p2.Content, "```\n\nAll Go code") {
		t.Fatalf("orphan closing fence leaked at start of chunk2:\n%s", p2.Content)
	}
}

func TestApplyPageViewSectionMissCapsPreview(t *testing.T) {
	body := "## Intro\n\n" + strings.Repeat("Effective Go paragraph line.\n", 160) + "\n## Concurrency\n\nGoroutines.\n"
	full := &core.Page{URL: "https://go.dev/doc/effective_go", Title: "Effective Go", Content: body}

	// MaxChars=0 时未命中必须兜底截断至预览上限，绝不倾倒全文
	view, notes := ApplyPageView(full, &core.FetchRequest{Section: "NonExistentHeadingXYZ"})
	if len(notes) == 0 || !strings.Contains(notes[0], "not found") {
		t.Fatalf("expected section not found note, got %v", notes)
	}
	if view.NextOffset == nil {
		t.Fatalf("expected truncated preview with NextOffset, got nil (content runes=%d)", textnorm.RuneCount(view.Content))
	}
	if got := textnorm.RuneCount(view.Content); got > defaultViewMissPreviewChars {
		t.Fatalf("expected preview <= %d runes, got %d", defaultViewMissPreviewChars, got)
	}
	if len(view.Outline) < 2 {
		t.Fatalf("expected full outline on section miss, got %+v", view.Outline)
	}

	// 显式传更小 MaxChars 时遵循更小值；更大值仍受预览上限保护
	smallView, _ := ApplyPageView(full, &core.FetchRequest{Section: "NonExistentHeadingXYZ", MaxChars: 400})
	if smallView.NextOffset == nil || textnorm.RuneCount(smallView.Content) > 400 {
		t.Fatalf("expected preview <= 400 runes, got %d next=%v", textnorm.RuneCount(smallView.Content), smallView.NextOffset)
	}
	largeView, _ := ApplyPageView(full, &core.FetchRequest{Section: "NonExistentHeadingXYZ", MaxChars: 5000})
	if largeView.NextOffset == nil || textnorm.RuneCount(largeView.Content) > defaultViewMissPreviewChars {
		t.Fatalf("expected preview capped at %d runes even when MaxChars=5000, got %d", defaultViewMissPreviewChars, textnorm.RuneCount(largeView.Content))
	}
}

func TestExtractOutlineRecomputesCharsAfterDepthPruning(t *testing.T) {
	var b strings.Builder
	// 构造 10 个 H2，每个 H2 下挂 3 个 H3（总计 40 个标题 > 24），触发 H3 裁剪
	for i := 1; i <= 10; i++ {
		b.WriteString("## Section ")
		b.WriteString(string(rune('A' + i - 1)))
		b.WriteString("\n\n")
		for j := 1; j <= 3; j++ {
			b.WriteString("### Sub ")
			b.WriteString(string(rune('0' + j)))
			b.WriteString("\n\n")
			b.WriteString(strings.Repeat("Body content text here. ", 12))
			b.WriteString("\n\n")
		}
	}
	doc := b.String()

	outline := ExtractOutline(doc, 24)
	if len(outline) != 10 {
		t.Fatalf("expected 10 H2 headings after pruning H3s, got %d: %+v", len(outline), outline)
	}
	totalRunes := utf8.RuneCountInString(doc)
	for i, sec := range outline {
		if sec.Level != 2 {
			t.Fatalf("expected only H2 after pruning, got %+v", sec)
		}
		var wantChars int
		if i+1 < len(outline) {
			wantChars = outline[i+1].Offset - sec.Offset
		} else {
			wantChars = totalRunes - sec.Offset
		}
		if sec.Chars != wantChars {
			t.Fatalf("outline[%d] (%s) Chars=%d, want %d (must cover pruned H3 children)", i, sec.Heading, sec.Chars, wantChars)
		}
		if sec.Chars < 500 {
			t.Fatalf("outline[%d] (%s) suspiciously tiny Chars=%d", i, sec.Heading, sec.Chars)
		}
	}

	// 验证超过 maxItems 截断时，末项 Chars 仍只计算到下一同级标题而非全文末尾
	capped := ExtractOutline(doc, 5)
	if len(capped) != 5 {
		t.Fatalf("expected 5 capped headings, got %d", len(capped))
	}
	if capped[4].Chars != outline[4].Chars {
		t.Fatalf("capped last heading Chars=%d, want %d (must not absorb truncated tail)", capped[4].Chars, outline[4].Chars)
	}
}

func TestApplyPageViewSectionPaginationCoordinateConsistency(t *testing.T) {
	intro := "## Intro\n\n" + strings.Repeat("Intro filler line.\n", 80) + "\n"
	conc := strings.Join([]string{
		"## Concurrency",
		"",
		"### Share by communicating",
		"",
		strings.Repeat("Do not communicate by sharing memory. ", 15),
		"",
		"### Channels",
		"",
		strings.Repeat("Channels synchronize goroutines cleanly. ", 15),
		"",
	}, "\n")
	tail := "## Errors\n\nHandle errors explicitly.\n"
	doc := intro + conc + tail
	full := &core.Page{URL: "https://go.dev/doc/effective_go", Content: doc}

	// 1. 章节未截断时不附带冗余全文大纲
	whole, notes := ApplyPageView(full, &core.FetchRequest{Section: "Concurrency"})
	if len(notes) != 0 || len(whole.Outline) != 0 {
		t.Fatalf("expected clean un-truncated section view without full-doc outline, notes=%v outline=%+v", notes, whole.Outline)
	}

	// 2. 章节分页时，next_offset / total_chars / outline 全部使用同一章节坐标系
	p1, p1Notes := ApplyPageView(full, &core.FetchRequest{Section: "Concurrency", MaxChars: 400})
	if p1.NextOffset == nil {
		t.Fatal("expected section view to paginate")
	}
	if p1.TotalChars != textnorm.RuneCount(strings.TrimSpace(conc)) {
		t.Fatalf("expected section TotalChars=%d, got %d", textnorm.RuneCount(strings.TrimSpace(conc)), p1.TotalChars)
	}
	if len(p1Notes) == 0 || !strings.Contains(p1Notes[0], "section") || !strings.Contains(p1Notes[0], "offset") {
		t.Fatalf("expected continuation note explaining section + offset, got %v", p1Notes)
	}
	foundChannels := false
	for _, sec := range p1.Outline {
		if sec.Offset < 0 || sec.Offset >= p1.TotalChars {
			t.Fatalf("section outline offset %d out of section range [0, %d): %+v", sec.Offset, p1.TotalChars, sec)
		}
		if sec.Heading == "Channels" {
			foundChannels = true
		}
	}
	if !foundChannels {
		t.Fatalf("expected subsection 'Channels' in paginated section outline, got %+v", p1.Outline)
	}

	// 3. 使用相同 section + next_offset 续读必须平滑拼接剩余章节内容
	p2, _ := ApplyPageView(full, &core.FetchRequest{Section: "Concurrency", Offset: *p1.NextOffset, MaxChars: 2000})
	if !strings.Contains(p2.Content, "Channels synchronize goroutines") {
		t.Fatalf("expected continuation chunk to contain remaining Channels text:\n%s", p2.Content)
	}

	// 4. 文档站标题末尾装饰性锚点 `¶` 在大纲与匹配锚点中被清理，但不改写正文，纯符号标题不清空
	pilcrowDoc := "## Concurrency¶\n\nGoroutines coordinate via channel.\n\n## ¶\n\nEdge case.\n"
	pilcrowPage := &core.Page{URL: "https://go.dev/doc/effective_go", Content: pilcrowDoc}
	pOutline := ExtractOutline(pilcrowDoc, 10)
	if len(pOutline) != 2 || pOutline[0].Heading != "Concurrency" || pOutline[1].Heading != "¶" {
		t.Fatalf("unexpected pilcrow outline cleanup: %+v", pOutline)
	}
	pMatch, _ := ApplyPageView(pilcrowPage, &core.FetchRequest{Match: "channel"})
	if !strings.Contains(pMatch.Content, "· Concurrency]**") || strings.Contains(pMatch.Content, "Concurrency¶") {
		t.Fatalf("expected pilcrow stripped from match heading anchor:\n%s", pMatch.Content)
	}
	pSec, _ := ApplyPageView(pilcrowPage, &core.FetchRequest{Section: "Concurrency"})
	if !strings.HasPrefix(pSec.Content, "## Concurrency¶") {
		t.Fatalf("expected original markdown body preserved unmodified:\n%s", pSec.Content)
	}
}

func TestExtractMatchViewSlicesOversizedCodeFence(t *testing.T) {
	var code strings.Builder
	code.WriteString("# internal/provider/exa/search.go\n\n```go\n")
	for i := 1; i <= 260; i++ {
		switch i {
		case 60:
			code.WriteString("func helperFirst() { _ = req.IncludeExtra }\n")
		case 64:
			code.WriteString("// adjacent hit near line 60 should merge into same window: IncludeExtra\n")
		case 210:
			code.WriteString("cleaned := core.SanitizeResults(name, mapped, detail, req.IncludeExtra, keyFn)\n")
		default:
			code.WriteString("var fillerLinePayloadVariable = \"ordinary source line content for padding\"\n")
		}
	}
	code.WriteString("```\n")
	fullDoc := code.String()
	full := &core.Page{URL: "https://github.com/maybk/veri/blob/main/internal/provider/exa/search.go", Content: fullDoc}

	view, notes := ApplyPageView(full, &core.FetchRequest{Match: "IncludeExtra"})
	if len(notes) != 0 {
		t.Fatalf("unexpected notes: %v", notes)
	}
	// 严禁把整份 260 行（>19,000 runes）代码文件倾倒出来
	if got, fullLen := textnorm.RuneCount(view.Content), textnorm.RuneCount(fullDoc); got >= fullLen/3 {
		t.Fatalf("expected oversized code fence to be sliced into compact windows, got %d runes out of %d", got, fullLen)
	}
	if !strings.Contains(view.Content, "helperFirst") || !strings.Contains(view.Content, "core.SanitizeResults") {
		t.Fatalf("expected both matched regions in sliced view:\n%s", view.Content)
	}
	if !strings.Contains(view.Content, "lines ") || !strings.Contains(view.Content, "```go\n") {
		t.Fatalf("expected line-range anchor and preserved ```go fence:\n%s", view.Content)
	}
	// 围栏必须严格平衡闭合
	var tr mdscan.Tracker
	for line := range strings.SplitSeq(view.Content, "\n") {
		tr.Next(line)
	}
	if tr.InFence() {
		t.Fatalf("sliced code fence left unclosed fence:\n%s", view.Content)
	}
	if len(view.Outline) != 0 || view.TotalChars != textnorm.RuneCount(view.Content) {
		t.Fatalf("expected un-truncated match view to omit redundant outline and report match view TotalChars=%d, got total=%d outline=%+v",
			textnorm.RuneCount(view.Content), view.TotalChars, view.Outline)
	}
}

func FuzzSlicePage(f *testing.F) {
	f.Add("# Title\n\n`gofmt` inline line\n\n```go\nfunc main() {\n\tprintln(1)\n}\n```\n\n## Next\n\nParagraph text.\n", 40)
	f.Add("```python\n"+strings.Repeat("x = 1\n", 20)+"```\n\nTail\n", 35)
	f.Add("~~~sh\necho hi\n~~~\n~100ms\n## H2\nbody\n", 25)

	f.Fuzz(func(t *testing.T, raw string, maxChars int) {
		if !utf8.ValidString(raw) || raw == "" {
			return
		}
		if maxChars < 10 || maxChars > 400 {
			maxChars = 40
		}
		// 仅对本身围栏已平衡的文档断言每页围栏平衡不变量
		var initTracker mdscan.Tracker
		for line := range strings.SplitSeq(raw, "\n") {
			initTracker.Next(line)
		}
		balancedInput := !initTracker.InFence()

		full := &core.Page{URL: "https://example.com", Content: raw}
		offset := 0
		total := utf8.RuneCountInString(raw)
		for step := 0; step < 64 && offset < total; step++ {
			chunk := SlicePage(full, offset, maxChars)
			if balancedInput {
				var tr mdscan.Tracker
				for line := range strings.SplitSeq(chunk.Content, "\n") {
					tr.Next(line)
				}
				if tr.InFence() {
					t.Fatalf("chunk at offset=%d maxChars=%d left unclosed fence:\n%s", offset, maxChars, chunk.Content)
				}
			}
			if chunk.NextOffset == nil {
				break
			}
			if *chunk.NextOffset <= offset {
				t.Fatalf("NextOffset did not advance: offset=%d next=%d", offset, *chunk.NextOffset)
			}
			offset = *chunk.NextOffset
		}
	})
}

func TestCleanHeadingTextStripsMarkdownAnchorLinks(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Introduction [¶](https://go.dev/doc/effective_go\\#introduction)", "Introduction"},
		{"Text Types [¶](https://docs.python.org/3/library/stdtypes.html#textseq \"Permalink to this headline\")", "Text Types"},
		{"Errors [#](#errors)", "Errors"},
		{"Install [#](https://example.com/docs#install 'Direct link to Install')", "Install"},
		{"Usage [](#usage)", "Usage"},
		{"[¶](#intro) Introduction", "Introduction"},
		{"[](#sec) Overview ¶", "Overview"},
		{"C#", "C#"},
		{"Issue #42", "Issue #42"},
		{"See [RFC 9110](https://httpwg.org/specs/rfc9110.html#section-1)", "See [RFC 9110](https://httpwg.org/specs/rfc9110.html#section-1)"},
		{"Docs ![](https://cdn.example.com/icon.svg#gh-dark-mode-only)", "Docs ![](https://cdn.example.com/icon.svg#gh-dark-mode-only)"},
		{"History [¶](https://en.wikipedia.org/wiki/Go_(programming_language)#History)", "History"},
		{"[¶](https://en.wikipedia.org/wiki/Go_(programming_language)#History) History", "History"},
		{"¶", "¶"},
		{"[¶](#only)", "[¶](#only)"},
	}
	for _, tc := range cases {
		if got := cleanHeadingText(tc.in); got != tc.want {
			t.Errorf("cleanHeadingText(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
