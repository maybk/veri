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
