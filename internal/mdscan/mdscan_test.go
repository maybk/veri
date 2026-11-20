package mdscan

import (
	"strings"
	"testing"
)

func TestParseFenceOpenAndClose(t *testing.T) {
	openCases := []struct {
		name       string
		line       string
		wantOK     bool
		wantMarker byte
		wantLen    int
		wantInfo   string
	}{
		{"plain_backticks", "```", true, '`', 3, ""},
		{"go_backticks", "```go\n", true, '`', 3, "go"},
		{"four_backticks_with_info", "  ````markdown\r\n", true, '`', 4, "markdown"},
		{"tilde_fence", "~~~python", true, '~', 3, "python"},
		{"indented_list_fence", "    ```json", true, '`', 3, "json"},
		{"single_backtick_inline_code", "`gofmt` will line up the columns:", false, 0, 0, ""},
		{"double_backtick_inline_code", "``foo`` bar", false, 0, 0, ""},
		{"triple_backtick_inline_code", "```inline``` text", false, 0, 0, ""},
		{"single_tilde_approx", "~100ms latency", false, 0, 0, ""},
		{"double_tilde_strikethrough", "~~deleted~~ text", false, 0, 0, ""},
		{"tilde_info_allows_tilde", "~~~a~b", true, '~', 3, "a~b"},
		{"too_short", "``", false, 0, 0, ""},
		{"plain_text", "hello world", false, 0, 0, ""},
	}

	for _, tc := range openCases {
		t.Run(tc.name, func(t *testing.T) {
			f, ok := ParseFenceOpen(tc.line)
			if ok != tc.wantOK {
				t.Fatalf("ParseFenceOpen(%q) ok=%t, want %t (fence=%+v)", tc.line, ok, tc.wantOK, f)
			}
			if !ok {
				return
			}
			if f.Marker != tc.wantMarker || f.Len != tc.wantLen || f.Info != tc.wantInfo {
				t.Fatalf("ParseFenceOpen(%q) = %+v, want marker=%q len=%d info=%q", tc.line, f, tc.wantMarker, tc.wantLen, tc.wantInfo)
			}
		})
	}

	f4 := Fence{Marker: '`', Len: 4, Info: "go"}
	if f4.OpenLine() != "````go" || f4.CloseLine() != "````" {
		t.Fatalf("unexpected open/close lines: %q / %q", f4.OpenLine(), f4.CloseLine())
	}
	if f4.Closes("```") {
		t.Fatal("3 backticks must not close a 4-backtick fence")
	}
	if f4.Closes("```` trailing") {
		t.Fatal("closing fence with trailing non-space text must not close")
	}
	if f4.Closes("~~~~") {
		t.Fatal("tilde fence must not close backtick fence")
	}
	if !f4.Closes("  `````  \r\n") {
		t.Fatal("5 backticks with whitespace should close 4-backtick fence")
	}

	var empty Fence
	if empty.OpenLine() != "" || empty.CloseLine() != "" || empty.Closes("```") {
		t.Fatal("zero Fence should return empty lines and never close")
	}
}

func TestParseATXHeading(t *testing.T) {
	cases := []struct {
		line      string
		maxLevel  int
		wantOK    bool
		wantLevel int
		wantText  string
	}{
		{"## Concurrency\n", 4, true, 2, "Concurrency"},
		{"### Examples ###", 4, true, 3, "Examples"},
		{"## C# Programming", 6, true, 2, "C# Programming"},
		{"## C#", 6, true, 2, "C#"},
		{"## C# ##", 6, true, 2, "C#"},
		{"## ###", 6, false, 0, ""},
		{"##### Level Five", 4, false, 0, ""},
		{"##### Level Five", 0, true, 5, "Level Five"},
		{"####### Seven", 6, false, 0, ""},
		{"#NoSpace", 6, false, 0, ""},
		{"  #\tTab Heading  ", 6, true, 1, "Tab Heading"},
		{"Not a heading", 6, false, 0, ""},
	}
	for _, tc := range cases {
		lvl, text, ok := ParseATXHeading(tc.line, tc.maxLevel)
		if ok != tc.wantOK || lvl != tc.wantLevel || text != tc.wantText {
			t.Errorf("ParseATXHeading(%q, %d) = (%d, %q, %t), want (%d, %q, %t)",
				tc.line, tc.maxLevel, lvl, text, ok, tc.wantLevel, tc.wantText, tc.wantOK)
		}
	}
}

func TestTrackerEffectiveGoInlineBacktickRegression(t *testing.T) {
	doc := strings.Join([]string{
		"## Formatting",
		"```",
		"type T struct {}",
		"```",
		"`gofmt` will line up the columns:",
		"```go",
		"// # comment inside fence",
		"type T struct {}",
		"```",
		"## Concurrency",
		"~100ms is fast.",
		"### Share by communicating",
	}, "\n")

	var tr Tracker
	var headings []string
	for line := range strings.SplitSeq(doc, "\n") {
		kind := tr.Next(line)
		if kind == KindText {
			if _, txt, ok := ParseATXHeading(line, 4); ok {
				headings = append(headings, txt)
			}
		}
	}
	if tr.InFence() {
		t.Fatal("tracker should end outside fence")
	}
	want := []string{"Formatting", "Concurrency", "Share by communicating"}
	if strings.Join(headings, "|") != strings.Join(want, "|") {
		t.Fatalf("headings=%v, want %v", headings, want)
	}

	// Verify nested fence: 4-backtick outer contains 3-backtick inner without closing prematurely
	tr.Reset()
	nested := []string{"````md", "```go", "## Fake Inside Nested", "```", "````", "## Real After Nested"}
	var gotKinds []LineKind
	for _, l := range nested {
		gotKinds = append(gotKinds, tr.Next(l))
	}
	wantKinds := []LineKind{KindFenceOpen, KindFenceBody, KindFenceBody, KindFenceBody, KindFenceClose, KindText}
	for i := range wantKinds {
		if gotKinds[i] != wantKinds[i] {
			t.Fatalf("nested line %d (%q): kind=%d, want %d", i, nested[i], gotKinds[i], wantKinds[i])
		}
	}
}

func TestFirstH1(t *testing.T) {
	md := "```sh\n# not a heading\n```\n\n## Subheading\n\n# Real Title C# ###\n# Second Title\n"
	if got := FirstH1(md); got != "Real Title C#" {
		t.Fatalf("FirstH1 = %q, want %q", got, "Real Title C#")
	}
	if got := FirstH1("```bash\n# inside code\n```\nplain text"); got != "" {
		t.Fatalf("FirstH1 expected empty, got %q", got)
	}
}

func FuzzTracker(f *testing.F) {
	f.Add("```go\nfmt.Println()\n```\n`inline` text\n## Heading\n~~~sh\necho hi\n~~~\n")
	f.Add("````\n```\n````\n~approx\n```inline```\n")
	f.Fuzz(func(t *testing.T, input string) {
		var tr Tracker
		var active Fence
		inFence := false

		for line := range strings.SplitSeq(input, "\n") {
			kind := tr.Next(line)
			if !inFence {
				if opened, ok := ParseFenceOpen(line); ok {
					if kind != KindFenceOpen || !tr.InFence() {
						t.Fatalf("expected KindFenceOpen for %q, got %d", line, kind)
					}
					inFence = true
					active = opened
				} else if kind != KindText || tr.InFence() {
					t.Fatalf("expected KindText for %q, got %d", line, kind)
				}
			} else {
				if active.Closes(line) {
					if kind != KindFenceClose || tr.InFence() {
						t.Fatalf("expected KindFenceClose for %q, got %d", line, kind)
					}
					inFence = false
					active = Fence{}
				} else if kind != KindFenceBody || !tr.InFence() {
					t.Fatalf("expected KindFenceBody for %q, got %d", line, kind)
				}
			}
			gotOpen, gotIn := tr.Open()
			if gotIn != inFence || gotOpen != active {
				t.Fatalf("Open() mismatch: got (%+v, %t), want (%+v, %t)", gotOpen, gotIn, active, inFence)
			}
		}
	})
}
