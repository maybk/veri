package core

import (
	"encoding/json/v2"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"veri/internal/spec"
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

func TestCostAndSumCosts(t *testing.T) {
	cny, err := ParsePriceSpec("CNY:0.005")
	if err != nil || cny.Currency != CurrencyCNY || cny.Micros != 5000 || !cny.Estimated {
		t.Fatalf("unexpected ParsePriceSpec CNY: %+v, err=%v", cny, err)
	}
	if cny.Format() != "¥0.005" {
		t.Fatalf("cny.Format() = %q, want ¥0.005", cny.Format())
	}
	neg := Cost{Currency: CurrencyUSD, Micros: -5000}
	if neg.Format() != "-$0.005" {
		t.Fatalf("neg.Format() = %q, want -$0.005", neg.Format())
	}
	if _, err := ParsePriceSpec("USD:999999999999999"); err == nil {
		t.Fatal("expected absurd price spec to fail")
	}

	usd := Cost{Currency: CurrencyUSD, Micros: 7000, Estimated: false}
	if usd.Format() != "$0.007" {
		t.Fatalf("usd.Format() = %q, want $0.007", usd.Format())
	}

	zero := Cost{Currency: CurrencyUSD, Micros: 0, Estimated: false}
	if zero.Format() != "$0" {
		t.Fatalf("zero.Format() = %q, want $0", zero.Format())
	}

	sum := SumCosts([]*Cost{
		&cny,
		{Currency: CurrencyCNY, Micros: 20000, Estimated: false},
		&usd,
		nil,
	})
	if len(sum) != 2 {
		t.Fatalf("len(sum) = %d, want 2", len(sum))
	}
	if sum[0].Currency != CurrencyCNY || sum[0].Micros != 25000 || !sum[0].Estimated {
		t.Fatalf("unexpected CNY sum: %+v", sum[0])
	}
	if sum[1].Currency != CurrencyUSD || sum[1].Micros != 7000 || sum[1].Estimated {
		t.Fatalf("unexpected USD sum: %+v", sum[1])
	}
	if got := FormatCosts(sum); got != "¥0.025 + $0.007" {
		t.Fatalf("FormatCosts = %q, want '¥0.025 + $0.007'", got)
	}
}

func TestErrorExitCodesAndFallback(t *testing.T) {
	if got := CodeInvalidArgument.ExitCode(); got != 2 {
		t.Fatalf("invalid_argument exit = %d, want 2", got)
	}
	if got := CodeAuthFailed.ExitCode(); got != 3 {
		t.Fatalf("auth_failed exit = %d, want 3", got)
	}
	if got := CodeRateLimited.ExitCode(); got != 4 {
		t.Fatalf("rate_limited exit = %d, want 4", got)
	}
	if got := CodeBlockedByPolicy.ExitCode(); got != 5 {
		t.Fatalf("blocked_by_policy exit = %d, want 5", got)
	}
	if got := CodeInternal.ExitCode(); got != 1 {
		t.Fatalf("internal exit = %d, want 1", got)
	}

	globalArgErr := NewError(CodeInvalidArgument, "empty query")
	if ShouldFallback(globalArgErr) {
		t.Fatal("global invalid_argument should not fallback")
	}
	provArgErr := NewError(CodeInvalidArgument, "query exceeds 128 runes").WithProvider("deepseek")
	if !ShouldFallback(provArgErr) {
		t.Fatal("provider-scoped invalid_argument should fallback")
	}
	policyErr := NewError(CodeBlockedByPolicy, "content blocked").WithProvider("deepseek")
	if ShouldFallback(policyErr) {
		t.Fatal("blocked_by_policy should not fallback")
	}
	rateErr := NewError(CodeRateLimited, "429").WithProvider("deepseek")
	if !ShouldFallback(rateErr) {
		t.Fatal("rate_limited should fallback")
	}
}

func TestParseEnumsCloneSanitizeAndEnvelopeJSON(t *testing.T) {
	if d, err := ParseDetail("CONTENT"); err != nil || d != DetailContent {
		t.Fatalf("ParseDetail: %v, %v", d, err)
	}
	if _, err := ParseDetail("bad"); err == nil {
		t.Fatal("expected error for invalid detail")
	}
	if s, err := ParseStrategy("fusion"); err != nil || s != StrategyFusion {
		t.Fatalf("ParseStrategy: %v, %v", s, err)
	}
	if fs, err := ParseFetchStrategy(""); err != nil || fs != FetchAPIFirst {
		t.Fatalf("ParseFetchStrategy empty default: %v, %v", fs, err)
	}
	if fs, err := ParseFetchStrategy("smart_local"); err != nil || fs != FetchSmartLocal {
		t.Fatalf("ParseFetchStrategy smart_local: %v, %v", fs, err)
	}

	if _, err := ValidateTargetURL("", "p", 100); err == nil {
		t.Fatal("expected empty URL error")
	}
	if _, err := ValidateTargetURL("https://example.com/long", "p", 10); err == nil {
		t.Fatal("expected maxLen error")
	}
	if _, err := ValidateTargetURL("ftp://example.com/a", "p", 100); err == nil {
		t.Fatal("expected unsupported scheme error")
	}
	if _, err := ValidateTargetURL("https://user:pw@example.com/a", "p", 100); err == nil {
		t.Fatal("expected userinfo error")
	}

	now := time.Now()
	items := []Result{
		{Title: "A", URL: "https://example.com/1", Summary: "Long summary text", Content: "Full body", Published: &now, Extra: map[string]any{"x": 1}},
		{Title: "Dup", URL: "https://example.com/1", Summary: "Duplicate"},
		{Title: "Bad", URL: "javascript:void(0)"},
		{Title: "OnlySnippet", URL: "https://example.com/2", Snippet: "Just snippet"},
	}
	s1, warns := SanitizeResults("demo", items, DetailSnippet, false, nil)
	if len(s1) != 2 || len(warns) != 1 || s1[0].Snippet == "" || s1[0].Summary != "" || s1[0].Extra != nil {
		t.Fatalf("unexpected DetailSnippet sanitize: %+v warns=%v", s1, warns)
	}
	s2, _ := SanitizeResults("demo", items, DetailContent, true, nil)
	if s2[0].Content != "Full body" || s2[0].Extra == nil || s2[1].Summary != "Just snippet" {
		t.Fatalf("unexpected DetailContent sanitize: %+v", s2)
	}
	s3, _ := SanitizeResults("demo", items, DetailSummary, false, func(u string) string { return strings.ToLower(u) })
	if s3[0].Content != "" || s3[1].Summary != "Just snippet" {
		t.Fatalf("unexpected DetailSummary sanitize: %+v", s3)
	}

	pub := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	rel := 0.9
	orig := Result{
		Rank:      1,
		Title:     "T",
		URL:       "https://example.com",
		Published: &pub,
		Relevance: &rel,
		Providers: []string{"deepseek"},
		Extra:     map[string]any{"nested": []any{"a", "b"}},
	}
	cloned := orig.Clone()
	cloned.Providers[0] = "mutated"
	cloned.Extra["nested"].([]any)[0] = "mutated"
	if orig.Providers[0] != "deepseek" || orig.Extra["nested"].([]any)[0] != "a" {
		t.Fatal("Result.Clone did not deep copy nested slices/maps")
	}

	env := Envelope{
		Schema:  spec.SchemaVersion,
		Kind:    spec.KindSearch,
		OK:      true,
		Results: nil,
	}
	rawJSON, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("json.Marshal(Envelope): %v", err)
	}
	if !strings.Contains(string(rawJSON), `"results":[]`) {
		t.Fatalf("expected empty search envelope to serialize results:[], got %s", string(rawJSON))
	}
	v1CompatJSON, err := env.MarshalJSON()
	if err != nil || string(v1CompatJSON) != string(rawJSON) {
		t.Fatalf("Envelope.MarshalJSON() drifted from json/v2 Marshal: v1=%s v2=%s err=%v", string(v1CompatJSON), string(rawJSON), err)
	}

	errEnv := Envelope{
		Schema: spec.SchemaVersion,
		Kind:   spec.KindFetch,
		OK:     false,
		Error:  &ErrorInfo{Code: CodeInvalidArgument, Message: "bad url", Status: 0},
	}
	errJSON, err := json.Marshal(errEnv)
	if err != nil || strings.Contains(string(errEnv.Error.Code), " ") || strings.Contains(string(errJSON), `"status"`) || strings.Contains(string(errJSON), `"results"`) {
		t.Fatalf("expected Status=0 and fetch results to be omitted under json/v2, got %s (err=%v)", string(errJSON), err)
	}
}

func TestModelErrorAndFreshnessEdges(t *testing.T) {
	if AmountToMicros(-1) != 0 || AmountToMicros(math.NaN()) != 0 || AmountToMicros(math.Inf(1)) != 0 {
		t.Fatal("unexpected AmountToMicros edge behavior")
	}
	if _, err := ParsePriceSpec("bad"); err == nil {
		t.Fatal("expected missing colon error")
	}
	if _, err := ParsePriceSpec("EUR:1.0"); err == nil {
		t.Fatal("expected unsupported currency error")
	}
	if _, err := ParsePriceSpec("USD:-1"); err == nil {
		t.Fatal("expected negative price error")
	}
	if FormatCosts(nil) != "" {
		t.Fatal("expected empty string for empty costs")
	}
	optNil, err := ParseOptionalPriceSpec("   ")
	if err != nil || optNil != nil {
		t.Fatalf("expected nil optional price for blank input, got (%+v, %v)", optNil, err)
	}
	optPrice, err := ParseOptionalPriceSpec("CNY:0.005")
	if err != nil || optPrice == nil || optPrice.Micros != 5000 || optPrice.Currency != CurrencyCNY {
		t.Fatalf("unexpected ParseOptionalPriceSpec: (%+v, %v)", optPrice, err)
	}
	cloned := CloneCost(optPrice)
	cloned.Micros = 999
	if optPrice.Micros != 5000 || CloneCost(nil) != nil {
		t.Fatal("CloneCost failed deep copy")
	}
	eur := Cost{Currency: "EUR", Micros: 1500000}
	if eur.Format() != "EUR 1.5" {
		t.Fatalf("unexpected other currency format: %s", eur.Format())
	}

	var nilPage *Page
	if nilPage.Clone() != nil {
		t.Fatal("expected nil Page clone")
	}
	next := 42
	now := time.Now()
	p := &Page{URL: "https://a.com", Published: &now, NextOffset: &next}
	pc := p.Clone()
	*pc.NextOffset = 99
	if *p.NextOffset != 42 {
		t.Fatal("Page.Clone did not deep copy NextOffset")
	}

	for _, tc := range []struct {
		unit FreshnessUnit
		n    int
		days int
	}{
		{UnitDay, 3, 3},
		{UnitWeek, 2, 14},
		{UnitMonth, 2, 60},
		{UnitYear, 1, 365},
		{"x", 5, 5},
	} {
		f := &Freshness{Unit: tc.unit, N: tc.n}
		if f.ApproximateDays() != tc.days {
			t.Fatalf("ApproximateDays(%s,%d) = %d, want %d", tc.unit, tc.n, f.ApproximateDays(), tc.days)
		}
	}
	var nilFresh *Freshness
	if nilFresh.IsRelative() || nilFresh.ApproximateDays() != 0 {
		t.Fatal("expected zero values for nil Freshness")
	}

	if AsError(nil) != nil || ShouldFallback(nil) {
		t.Fatal("nil error checks failed")
	}
	var nilErr *Error
	if nilErr.Error() != "" || nilErr.Unwrap() != nil || nilErr.ToErrorInfo() != nil {
		t.Fatal("nil *Error methods failed")
	}
	cause := errors.New("root io")
	wrapped := WrapError(CodeUpstreamUnavailable, "", cause).
		WithProvider("exa").
		WithHint("check network").
		WithStatus(503).
		WithRetryable(true).
		WithParam("k", "v")
	if !errors.Is(wrapped, cause) || !strings.Contains(wrapped.Error(), "root io") {
		t.Fatalf("unexpected wrapped error string: %s", wrapped.Error())
	}
	plainWrapped := AsError(cause)
	if plainWrapped.Code != CodeInternal {
		t.Fatalf("expected plain error to map to CodeInternal, got %s", plainWrapped.Code)
	}
	if len(AllCodes()) != 13 {
		t.Fatalf("expected 13 error codes, got %d", len(AllCodes()))
	}
	if CodeNotFound.ExitCode() != 5 || CodeNotFound.IsRetryable() {
		t.Fatalf("unexpected CodeNotFound properties: exit=%d retryable=%t", CodeNotFound.ExitCode(), CodeNotFound.IsRetryable())
	}
	if got := MostDecisiveCode([]Code{CodeUpstreamTimeout, CodeNotFound, CodeRateLimited}); got != CodeNotFound {
		t.Fatalf("MostDecisiveCode expected not_found, got %s", got)
	}
	if got := MostDecisiveCode([]Code{CodeUpstreamTimeout, CodeBlockedByPolicy, CodeUpstreamUnavailable}); got != CodeBlockedByPolicy {
		t.Fatalf("MostDecisiveCode expected blocked_by_policy, got %s", got)
	}
	if got := MostDecisiveCode([]Code{CodeUpstreamTimeout, CodeAuthFailed}); got != CodeAuthFailed {
		t.Fatalf("MostDecisiveCode expected auth_failed, got %s", got)
	}
	for _, c := range AllCodes() {
		if c.DefaultHint() == "" {
			t.Fatalf("code %s missing DefaultHint", c)
		}
		if MostDecisiveCode([]Code{"", CodeInternal, c}) != c {
			t.Fatalf("MostDecisiveCode failed to prioritize %s over internal", c)
		}
	}
	if MostDecisiveCode([]Code{CodeUpstreamTimeout, CodeUpstreamBadResponse, CodeRateLimited, CodeInvalidArgument}) != CodeInvalidArgument {
		t.Fatal("expected invalid_argument to rank above transient errors")
	}
	if MostDecisiveCode([]Code{CodeUpstreamTimeout, CodeUpstreamBadResponse, CodeUpstreamUnavailable}) != CodeUpstreamUnavailable {
		t.Fatal("expected upstream_unavailable to rank above bad_response/timeout")
	}
	if MostDecisiveCode([]Code{CodeUpstreamTimeout, CodeUpstreamBadResponse}) != CodeUpstreamBadResponse {
		t.Fatal("expected upstream_bad_response to rank above timeout")
	}
	if MostDecisiveCode([]Code{CodeUpstreamTimeout, CodeRateLimited}) != CodeRateLimited {
		t.Fatal("expected rate_limited to rank above timeout")
	}

	// 验证 Authors 清洗去重与 Result.Clone 深拷贝隔离
	r := Result{
		Title:   "Paper",
		URL:     "https://example.org/p",
		Authors: []string{" Alice ", "alice", "", "Bob"},
		Extra:   map[string]any{"tags": []string{"ml", "go"}},
	}
	AlignResultDetail(&r, DetailSummary, true)
	if len(r.Authors) != 2 || r.Authors[0] != "Alice" || r.Authors[1] != "Bob" {
		t.Fatalf("unexpected cleaned authors: %+v", r.Authors)
	}
	clonedRes := r.Clone()
	clonedRes.Authors[0] = "Mutated"
	if r.Authors[0] != "Alice" {
		t.Fatal("Result.Clone did not deep-copy Authors")
	}

	// 验证默认不输出 icon/extra 以节省 Token，仅在 keepExtra=true 时保留
	rIcon := Result{
		Title: "Doc",
		URL:   "https://example.org/doc",
		Icon:  "https://cdn.example.org/favicon.ico",
		Extra: map[string]any{"image": "https://cdn.example.org/og.png"},
	}
	AlignResultDetail(&rIcon, DetailSummary, false)
	if rIcon.Icon != "" || rIcon.Extra != nil {
		t.Fatalf("expected Icon and Extra stripped when keepExtra=false, got icon=%q extra=%v", rIcon.Icon, rIcon.Extra)
	}
	rIconKeep := Result{
		Title: "Doc",
		URL:   "https://example.org/doc",
		Icon:  " https://cdn.example.org/favicon.ico ",
		Extra: map[string]any{"image": "https://cdn.example.org/og.png"},
	}
	AlignResultDetail(&rIconKeep, DetailSummary, true)
	if rIconKeep.Icon != "https://cdn.example.org/favicon.ico" || rIconKeep.Extra["image"] != "https://cdn.example.org/og.png" {
		t.Fatalf("expected Icon and Extra preserved when keepExtra=true, got icon=%q extra=%v", rIconKeep.Icon, rIconKeep.Extra)
	}
	rBadIcon := Result{
		Title: "Doc",
		URL:   "https://example.org/doc",
		Icon:  "javascript:alert(1)",
	}
	AlignResultDetail(&rBadIcon, DetailSummary, true)
	if rBadIcon.Icon != "" {
		t.Fatalf("expected non-http(s) Icon rejected even when keepExtra=true, got %q", rBadIcon.Icon)
	}

	// 验证空标题按 URL 末段或主机名自动兜底，不覆盖已有标题
	rEmptyGo := Result{Title: "", URL: "https://tip.golang.org/src/context/context.go"}
	AlignResultDetail(&rEmptyGo, DetailSummary, false)
	if rEmptyGo.Title != "context.go" {
		t.Fatalf("expected fallback title context.go, got %q", rEmptyGo.Title)
	}
	rEmptyMD := Result{Title: "   ", URL: "https://modelcontextprotocol.io/specification/2026-07-28.md"}
	AlignResultDetail(&rEmptyMD, DetailSummary, false)
	if rEmptyMD.Title != "2026-07-28.md" {
		t.Fatalf("expected fallback title 2026-07-28.md, got %q", rEmptyMD.Title)
	}
	rEmptyRoot := Result{Title: "", URL: "https://go.dev/"}
	AlignResultDetail(&rEmptyRoot, DetailSummary, false)
	if rEmptyRoot.Title != "go.dev" {
		t.Fatalf("expected fallback title go.dev, got %q", rEmptyRoot.Title)
	}
}
