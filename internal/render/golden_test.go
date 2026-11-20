package render

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

var updateGolden = flag.Bool("update", false, "update golden files in testdata/")

var untrustedIDRe = regexp.MustCompile(`<untrusted_web_content id="[a-z2-7]{8}">`)

func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to generate): %v", path, err)
	}
	if got != string(want) {
		t.Fatalf("golden mismatch for %s:\n--- GOT ---\n%s\n--- WANT ---\n%s", name, got, string(want))
	}
}

func TestRenderGoldenAndCoverageMatrix(t *testing.T) {
	pub := time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC)

	// 1. CLI Search Markdown（覆盖正常标题、空标题回退 Site、空标题+无 Site 回退 Host、Authors、Cost）
	searchEnv := &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     true,
		Kind:   spec.KindSearch,
		Query:  "golang context",
		Results: []core.Result{
			{
				Rank:      1,
				Title:     "Package context",
				URL:       "https://pkg.go.dev/context",
				Site:      "Go Packages",
				Authors:   []string{"Go Team"},
				Published: &pub,
				Summary:   "Defines the Context type.\nCarries deadlines and cancellation signals.",
			},
			{
				Rank:    2,
				Title:   "",
				URL:     "https://tip.golang.org/src/context/context.go?m=text",
				Site:    "The Go Programming Language",
				Snippet: "// Package context defines the Context type.",
			},
			{
				Rank:    3,
				Title:   "",
				URL:     "https://docs.example.org/guide",
				Content: "Full body text when detail=content.",
			},
		},
		Meta: core.Meta{
			Providers:  []core.ProviderReport{{Name: "deepseek", Status: core.ProviderStatusOK, Results: 3}, {Name: "deepseek", Status: core.ProviderStatusOK, Results: 1}},
			Cost:       []core.Cost{{Currency: "USD", Micros: 7000}},
			DurationMS: 450,
			Notes:      []string{"cache miss"},
		},
	}

	cliMD, err := FormatSearch(searchEnv, spec.FormatMarkdown, Palette{})
	if err != nil {
		t.Fatalf("FormatSearch md: %v", err)
	}
	assertGolden(t, "cli_search_md", cliMD)

	// 空结果、无成本、JSON、JSONL、URLs 与非法格式
	emptySearch := &core.Envelope{Schema: spec.SchemaVersion, OK: true, Kind: spec.KindSearch, Query: "none", Meta: core.Meta{DurationMS: 120}}
	if out, err := FormatSearch(emptySearch, spec.FormatMarkdown, Palette{}); err != nil || !strings.Contains(out, "0 results") || !strings.Contains(out, "none") {
		t.Fatalf("unexpected empty md: %q err=%v", out, err)
	}
	if out, err := FormatSearch(emptySearch, spec.FormatURLs, Palette{}); err != nil || out != "" {
		t.Fatalf("unexpected empty urls: %q err=%v", out, err)
	}
	if out, err := FormatSearch(searchEnv, spec.FormatJSON, Palette{}); err != nil || !strings.Contains(out, `"schema": "veri/v1"`) {
		t.Fatalf("unexpected search json: err=%v", err)
	}
	if out, err := FormatSearch(searchEnv, spec.FormatJSONL, Palette{}); err != nil || len(strings.Split(strings.TrimSpace(out), "\n")) != 3 {
		t.Fatalf("unexpected search jsonl: %q err=%v", out, err)
	}
	if _, err := FormatSearch(searchEnv, "xml", Palette{}); err == nil {
		t.Fatal("expected error for unsupported search format")
	}

	// 2. CLI Fetch（单页、批量含错误与空标题、JSON、JSONL、nil 与非法格式）
	batchFetchEnv := &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     true,
		Kind:   spec.KindFetch,
		Pages: []core.Page{
			{URL: "https://example.com/a", Title: "Alpha", Content: "# Alpha\n\nBody A.", Provider: "tinyfish", SourceType: core.SourceGeneric},
			{URL: "https://example.com/missing", Error: &core.ErrorInfo{Code: core.CodeNotFound, Message: "404 not found"}},
			{URL: "https://example.com/notitle", Content: "Plain content without title.", Provider: "tinyfish", SourceType: core.SourceGeneric},
		},
		Meta: core.Meta{
			Providers: []core.ProviderReport{
				{Name: "tinyfish", Status: core.ProviderStatusOK, Results: 1},
				{Name: "tinyfish", Status: core.ProviderStatusOK, Results: 1},
			},
			Cost: []core.Cost{{Currency: "USD", Micros: 2000}},
		},
	}
	batchMD, err := FormatFetch(batchFetchEnv, spec.FormatMarkdown)
	if err != nil {
		t.Fatalf("FormatFetch batch md: %v", err)
	}
	assertGolden(t, "cli_fetch_batch_md", batchMD)

	singleFetchEnv := &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     true,
		Kind:   spec.KindFetch,
		Page:   &core.Page{URL: "https://example.com/a", Content: "Single line"},
	}
	if out, err := FormatFetch(singleFetchEnv, spec.FormatMarkdown); err != nil || out != "Single line\n" {
		t.Fatalf("unexpected single fetch md: %q err=%v", out, err)
	}
	if out, err := FormatFetch(&core.Envelope{Page: &core.Page{Content: "Ends with newline\n"}}, spec.FormatMarkdown); err != nil || out != "Ends with newline\n" {
		t.Fatalf("unexpected trailing newline fetch md: %q err=%v", out, err)
	}
	if out, err := FormatFetch(nil, spec.FormatMarkdown); err != nil || out != "" {
		t.Fatalf("unexpected nil fetch md: %q err=%v", out, err)
	}
	if out, err := FormatFetch(&core.Envelope{}, spec.FormatMarkdown); err != nil || out != "" {
		t.Fatalf("unexpected empty fetch md: %q err=%v", out, err)
	}
	if out, err := FormatFetch(batchFetchEnv, spec.FormatJSON); err != nil || !strings.Contains(out, `"pages"`) {
		t.Fatalf("unexpected fetch json: err=%v", err)
	}
	if out, err := FormatFetch(batchFetchEnv, spec.FormatJSONL); err != nil || len(strings.Split(strings.TrimSpace(out), "\n")) != 3 {
		t.Fatalf("unexpected batch fetch jsonl: %q err=%v", out, err)
	}
	if out, err := FormatFetch(singleFetchEnv, spec.FormatJSONL); err != nil || !strings.Contains(out, `"Single line"`) {
		t.Fatalf("unexpected single fetch jsonl: %q err=%v", out, err)
	}
	if out, err := FormatFetch(nil, spec.FormatJSONL); err != nil || out != "" {
		t.Fatalf("unexpected nil fetch jsonl: %q err=%v", out, err)
	}
	if _, err := FormatFetch(singleFetchEnv, "urls"); err == nil {
		t.Fatal("expected error for unsupported fetch format")
	}

	// 3. MCP Search & Fetch（Markdown + JSON + 批量去重标签 + 大纲预览 + 错误信封）
	mcpSearchMD, err := FormatMCPSearchText(searchEnv, spec.TextFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "mcp_search_md", untrustedIDRe.ReplaceAllString(mcpSearchMD, `<untrusted_web_content id="TEST">`))

	if out, err := FormatMCPSearchText(nil, spec.TextFormatMarkdown); err != nil || out != "" {
		t.Fatalf("nil mcp search: %q err=%v", out, err)
	}
	if out, err := FormatMCPSearchText(emptySearch, spec.TextFormatMarkdown); err != nil || !strings.Contains(out, "No results.") {
		t.Fatalf("empty mcp search: %q err=%v", out, err)
	}
	if out, err := FormatMCPSearchText(searchEnv, spec.TextFormatJSON); err != nil || !strings.HasPrefix(out, "<untrusted_web_content") {
		t.Fatalf("json mcp search: %q err=%v", out, err)
	}
	errEnv := &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     false,
		Kind:   spec.KindSearch,
		Error:  &core.ErrorInfo{Code: core.CodeNotFound, Provider: "local", Message: "404", Hint: "Check URL."},
	}
	if out, err := FormatMCPSearchText(errEnv, spec.TextFormatMarkdown); err != nil || !strings.Contains(out, "Error [not_found]") || !strings.Contains(out, "Hint: Check URL.") {
		t.Fatalf("error mcp search md: %q err=%v", out, err)
	}
	if out, err := FormatMCPSearchText(errEnv, spec.TextFormatJSON); err != nil || strings.HasPrefix(out, "<untrusted_web_content") {
		t.Fatalf("error mcp search json must not wrap untrusted: %q err=%v", out, err)
	}

	mcpBatchMD, err := FormatMCPFetchText(batchFetchEnv, spec.TextFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "mcp_fetch_batch_md", untrustedIDRe.ReplaceAllString(mcpBatchMD, `<untrusted_web_content id="TEST">`))

	nextOff := 400
	richFetchEnv := &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     true,
		Kind:   spec.KindFetch,
		Page: &core.Page{
			URL:        "https://go.dev/doc/effective_go",
			FinalURL:   "https://go.dev/doc/effective_go",
			Title:      "Effective Go",
			Author:     "Go Authors",
			Site:       "go.dev",
			Published:  &pub,
			Content:    "# Effective Go\n\nBody content.",
			TotalChars: 10000,
			NextOffset: &nextOff,
			Outline: []core.PageSection{
				{Heading: "Introduction", Level: 2, Offset: 10},
				{Heading: "Formatting", Level: 2, Offset: 120},
				{Heading: "Concurrency", Level: 2, Offset: 800},
			},
			Provider:   "local",
			SourceType: core.SourceGeneric,
		},
		Meta: core.Meta{
			Cost:  []core.Cost{{Currency: "USD", Micros: 1000}},
			Notes: []string{"section hint"},
		},
	}
	richMCP, err := FormatMCPFetchText(richFetchEnv, spec.TextFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(richMCP, "Title: Effective Go") || !strings.Contains(richMCP, "Outline: [10] ## Introduction") || !strings.Contains(richMCP, "Notes: section hint") {
		t.Fatalf("unexpected rich MCP fetch text:\n%s", richMCP)
	}
	if out, err := FormatMCPFetchText(richFetchEnv, spec.TextFormatJSON); err != nil || !strings.HasPrefix(out, "<untrusted_web_content") {
		t.Fatalf("json mcp fetch: %q err=%v", out, err)
	}
	if out, err := FormatMCPFetchText(errEnv, spec.TextFormatMarkdown); err != nil || !strings.Contains(out, "Error [not_found]") {
		t.Fatalf("err mcp fetch md: %q err=%v", out, err)
	}
	if out, err := FormatMCPFetchText(errEnv, spec.TextFormatJSON); err != nil || strings.HasPrefix(out, "<untrusted_web_content") {
		t.Fatalf("err mcp fetch json: %q err=%v", out, err)
	}
	if out, err := FormatMCPFetchText(&core.Envelope{OK: true}, spec.TextFormatMarkdown); err != nil || !strings.Contains(out, "No page content") {
		t.Fatalf("nil page mcp fetch: %q err=%v", out, err)
	}
	if out, err := FormatMCPFetchText(nil, spec.TextFormatMarkdown); err != nil || out != "" {
		t.Fatalf("nil env mcp fetch: %q err=%v", out, err)
	}

	// 4. Display helpers & Palette
	if got := displayResultTitle(core.Result{}); got != "(untitled)" {
		t.Fatalf("empty result title got %q", got)
	}
	if got := displayResultTitle(core.Result{URL: "://bad-url"}); got != "://bad-url" {
		t.Fatalf("unparseable url title fallback got %q", got)
	}
	if got := FormatOutlinePreview(richFetchEnv.Page.Outline, 2); got != "Introduction (10) | Formatting (120) | ...(+1)" {
		t.Fatalf("unexpected FormatOutlinePreview: %q", got)
	}
	if got := FormatOutlinePreview(nil, 5); got != "" {
		t.Fatalf("empty outline preview got %q", got)
	}
	var buf bytes.Buffer
	_ = DetectPalette(&buf, true)
	_ = DetectPaletteWithEnv(&buf, false, func(string) string { return "" })
}
