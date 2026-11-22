package core

import (
	"errors"
	"testing"
	"time"

	"veri/internal/textnorm"
)

func TestParseFreshness(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 3, 31, 15, 30, 0, 0, loc)

	tests := []struct {
		name       string
		raw        string
		wantNil    bool
		wantErr    bool
		wantUnit   FreshnessUnit
		wantN      int
		wantAfter  string
		wantBefore string
	}{
		{name: "empty", raw: "  ", wantNil: true},
		{name: "word_day", raw: "day", wantUnit: UnitDay, wantN: 1, wantAfter: "2026-03-30"},
		{name: "word_week", raw: "WEEK", wantUnit: UnitWeek, wantN: 1, wantAfter: "2026-03-24"},
		{name: "word_month_clamped", raw: "month", wantUnit: UnitMonth, wantN: 1, wantAfter: "2026-02-28"},
		{name: "word_year", raw: "year", wantUnit: UnitYear, wantN: 1, wantAfter: "2025-03-31"},
		{name: "suffix_seven_days", raw: "7d", wantUnit: UnitDay, wantN: 7, wantAfter: "2026-03-24"},
		{name: "suffix_two_weeks", raw: "2w", wantUnit: UnitWeek, wantN: 2, wantAfter: "2026-03-17"},
		{name: "suffix_three_months", raw: "3m", wantUnit: UnitMonth, wantN: 3, wantAfter: "2025-12-31"},
		{name: "suffix_one_year", raw: "1y", wantUnit: UnitYear, wantN: 1, wantAfter: "2025-03-31"},
		{name: "default_day", raw: "d", wantUnit: UnitDay, wantN: 1, wantAfter: "2026-03-30"},
		{name: "seven_days_legacy", raw: "d7", wantUnit: UnitDay, wantN: 7, wantAfter: "2026-03-24"},
		{name: "two_weeks_legacy", raw: "w2", wantUnit: UnitWeek, wantN: 2, wantAfter: "2026-03-17"},
		{name: "month_end_clamped", raw: "m1", wantUnit: UnitMonth, wantN: 1, wantAfter: "2026-02-28"},
		{name: "one_year_legacy", raw: "y1", wantUnit: UnitYear, wantN: 1, wantAfter: "2025-03-31"},
		{name: "open_end", raw: "2026-01-01..", wantAfter: "2026-01-01"},
		{name: "open_start", raw: "..2026-02-15", wantBefore: "2026-02-15"},
		{name: "closed_range", raw: "2026-02-01..2026-02-28", wantAfter: "2026-02-01", wantBefore: "2026-02-28"},
		{name: "invalid_unit", raw: "h5", wantErr: true},
		{name: "invalid_suffix_unit", raw: "5h", wantErr: true},
		{name: "invalid_plural_word", raw: "days", wantErr: true},
		{name: "zero_count", raw: "d0", wantErr: true},
		{name: "zero_suffix_count", raw: "0d", wantErr: true},
		{name: "negative_suffix", raw: "-7d", wantErr: true},
		{name: "over_max_count", raw: "d366", wantErr: true},
		{name: "over_max_suffix_count", raw: "366d", wantErr: true},
		{name: "future_after", raw: "2026-04-01..", wantErr: true},
		{name: "future_before", raw: "..2026-05-01", wantErr: true},
		{name: "pre_1970", raw: "1969-12-31..", wantErr: true},
		{name: "inverted_range", raw: "2026-03-01..2026-02-01", wantErr: true},
		{name: "empty_both_sides", raw: "..", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseFreshness(tc.raw, now)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tc.raw)
				}
				if se, ok := errors.AsType[*Error](err); !ok || se.Code != CodeInvalidArgument {
					t.Fatalf("expected CodeInvalidArgument, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantNil {
				if got != nil {
					t.Fatalf("expected nil, got %+v", got)
				}
				return
			}
			if got.Unit != tc.wantUnit || got.N != tc.wantN {
				t.Fatalf("unit/n = %q/%d, want %q/%d", got.Unit, got.N, tc.wantUnit, tc.wantN)
			}
			if tc.wantAfter != "" {
				if got.After.Location() != time.UTC || got.After.Format("2006-01-02") != tc.wantAfter {
					t.Fatalf("after = %s (%s), want %s UTC", got.After.Format("2006-01-02"), got.After.Location(), tc.wantAfter)
				}
			} else if !got.After.IsZero() {
				t.Fatalf("expected zero After, got %v", got.After)
			}
			if tc.wantBefore != "" {
				if got.Before.Location() != time.UTC || got.Before.Format("2006-01-02") != tc.wantBefore {
					t.Fatalf("before = %s, want %s", got.Before.Format("2006-01-02"), tc.wantBefore)
				}
				end := got.EndOfBeforeDay()
				if end.Hour() != 23 || end.Minute() != 59 || end.Second() != 59 {
					t.Fatalf("EndOfBeforeDay = %v, expected 23:59:59 UTC", end)
				}
			} else if !got.Before.IsZero() {
				t.Fatalf("expected zero Before, got %v", got.Before)
			}
		})
	}

	// 验证东八区凌晨时刻与 ParseFreshness 的日历日锚定严格对齐（不发生跨 UTC 日偏差）
	cstEarly := time.Date(2026, 11, 18, 2, 0, 0, 0, textnorm.ShanghaiLocation())
	freshD2, err := ParseFreshness("d2", cstEarly)
	if err != nil {
		t.Fatal(err)
	}
	twoDaysAgo := textnorm.ParseFlexibleTimeAt("2 days ago", nil, cstEarly)
	if twoDaysAgo == nil || !twoDaysAgo.Equal(freshD2.After) {
		t.Fatalf("expected '2 days ago' (%v) to align with ParseFreshness d2.After (%v)", twoDaysAgo, freshD2.After)
	}
}
