package core

import (
	"encoding/json/v2"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"veri/internal/spec"
)

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

	// 验证上游 .md 端点将 YAML frontmatter + 正文误灌入标题/摘要时能窄口清理，但 Content 必须原样保留 frontmatter
	fmBlock := "---\ntitle: 史上最大IPO刷新\nsource: 新浪财经\ncanonical_url: https://finance.sina.com.cn/a.shtml\n---\n正文内容很长很长"
	rFM := Result{
		Title:   "---title: 史上最大IPO刷新source: 新浪财经canonical_url: https://finance.sina.com.cn/a.shtml--- 正文内容很长很长",
		URL:     "https://finance.sina.com.cn/a.shtml.md",
		Snippet: fmBlock,
		Summary: fmBlock,
		Content: fmBlock,
	}
	AlignResultDetail(&rFM, DetailContent, false)
	if rFM.Title != "史上最大IPO刷新" || rFM.Summary != "正文内容很长很长" || rFM.Snippet != "正文内容很长很长" {
		t.Fatalf("expected frontmatter stripped from title/summary/snippet, got title=%q summary=%q snippet=%q", rFM.Title, rFM.Summary, rFM.Snippet)
	}
	if !strings.HasPrefix(rFM.Content, "---\ntitle:") {
		t.Fatalf("Result.Content must preserve raw frontmatter intact, got %q", rFM.Content)
	}
}
