package textnorm

import (
	"testing"
	"time"
)

func TestCleanTextTruncateAndFlexibleTime(t *testing.T) {
	if CleanText("") != "" || CleanInline("   ") != "" || FirstNonEmpty("", "  ", " ok ") != "ok" || FirstNonEmpty("") != "" {
		t.Fatal("unexpected empty/FirstNonEmpty output")
	}
	dirty := "  Hello\u200b\u202e\u2066\ufeff &amp; World\r\nLine2\rLine3  "
	if got := CleanText(dirty); got != "Hello & World\nLine2\nLine3" {
		t.Fatalf("CleanText = %q", got)
	}
	if got := CleanInline(dirty); got != "Hello & World Line2 Line3" {
		t.Fatalf("CleanInline = %q", got)
	}
	plain := CleanPlainText("if a &lt; b &amp;&amp; c > 0\x00\u00AD\u2060\U000E0100\r\n")
	if plain != "if a &lt; b &amp;&amp; c > 0" {
		t.Fatalf("CleanPlainText must preserve literal entities and strip invisible controls, got %q", plain)
	}
	if TruncateRunes("hello", 0) != "hello" || TruncateRunes("hello", 10) != "hello" || TruncateRunes("hello", 1) != "…" {
		t.Fatal("unexpected TruncateRunes edge cases")
	}
	if got := TruncateRunes("你好世界测试", 4); got != "你好世…" || RuneCount("你好") != 2 {
		t.Fatalf("unexpected TruncateRunes = %q", got)
	}
	if *ClampUnitFloat(-0.5) != 0 || *ClampUnitFloat(1.5) != 1 || *ClampUnitFloat(0.4) != 0.4 {
		t.Fatal("unexpected ClampUnitFloat")
	}

	if ParseFlexibleTime("", nil) != nil || ParseFlexibleTime("not-a-date", nil) != nil {
		t.Fatal("expected nil for invalid dates")
	}
	t1 := ParseFlexibleTime("2026-09-21T08:32:25.0000000", ShanghaiLocation())
	if t1 == nil || t1.Format(time.RFC3339) != "2026-09-21T00:32:25Z" {
		t.Fatalf("unexpected Shanghai time: %v", t1)
	}
	t2 := ParseFlexibleTime("Thu, 18 Sep 2025 15:19:00 GMT", nil)
	if t2 == nil || t2.Year() != 2025 {
		t.Fatalf("unexpected RFC1123 time: %v", t2)
	}

	now := time.Date(2026, 3, 31, 14, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw      string
		wantDate string
		wantNil  bool
	}{
		{raw: "2026-03-15", wantDate: "2026-03-15"},
		{raw: "August 1, 2026", wantDate: "2026-08-01"},
		{raw: "Aug 1, 2026", wantDate: "2026-08-01"},
		{raw: "today", wantDate: "2026-03-31"},
		{raw: "yesterday", wantDate: "2026-03-30"},
		{raw: "just now", wantDate: "2026-03-31"},
		{raw: "2 days ago.", wantDate: "2026-03-29"},
		{raw: "107d ago", wantDate: "2025-12-14"},
		{raw: "a week ago", wantDate: "2026-03-24"},
		{raw: "1 month ago", wantDate: "2026-02-28"},
		{raw: "approx. 3 hours ago", wantDate: "2026-03-31"},
		{raw: "an hour ago", wantDate: "2026-03-31"},
		{raw: "2 years ago", wantDate: "2024-03-31"},
		{raw: "0001-01-01T00:00:00Z", wantNil: true},
		{raw: "999 years ago", wantNil: true},
		{raw: "not a date", wantNil: true},
		{raw: "100 ago", wantNil: true},
	} {
		got := ParseFlexibleTimeAt(tc.raw, time.UTC, now)
		if tc.wantNil {
			if got != nil {
				t.Fatalf("ParseFlexibleTimeAt(%q) = %v, want nil", tc.raw, got)
			}
			continue
		}
		if got == nil || got.Format("2006-01-02") != tc.wantDate {
			t.Fatalf("ParseFlexibleTimeAt(%q) = %v, want %s", tc.raw, got, tc.wantDate)
		}
	}
}
