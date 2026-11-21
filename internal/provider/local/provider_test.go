package local

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"veri/internal/core"
	"veri/internal/provider/providertest"
)

func TestLocalExtractAndQualityGate(t *testing.T) {
	richArticle := providertest.LoadFixture(t, "article_ok.html")
	spaShell := providertest.LoadFixture(t, "spa_shell.html")
	shortStub := providertest.LoadFixture(t, "short_stub.html")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/rich":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write(richArticle)
		case "/spa":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(spaShell)
		case "/short":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write(shortStub)
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"hello":"world"}`))
		case "/pdf":
			w.Header().Set("Content-Type", "application/pdf")
			w.(http.Flusher).Flush()
			// 若客户端未在响应头阶段秒退而继续读流，此处会写入超出 MaxBytes 的大体积载荷触发不同错误
			_, _ = w.Write([]byte(strings.Repeat("%PDF-1.7-stream-chunk-", 1024)))
		}
	}))
	defer srv.Close()

	p, enabled, err := New(Options{MinContentChars: 300, MaxBytes: 4096}, providertest.NewTestFetchClient(t))
	if err != nil || !enabled {
		t.Fatalf("New: enabled=%v err=%v", enabled, err)
	}

	richOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/rich"})
	if err != nil || richOut.LowQuality || !strings.Contains(richOut.Page.Content, "Go memory model") {
		t.Fatalf("expected rich article to pass quality gate, got %+v err=%v", richOut, err)
	}

	spaOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/spa"})
	if err != nil || !spaOut.LowQuality || spaOut.QualityMsg != "bot_or_js_wall" {
		t.Fatalf("expected SPA shell to trigger bot_or_js_wall quality gate, got %+v err=%v", spaOut, err)
	}

	shortOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/short"})
	if err != nil || !shortOut.LowQuality || shortOut.QualityMsg != "too_short" {
		t.Fatalf("expected short page to trigger too_short quality gate, got %+v err=%v", shortOut, err)
	}

	jsonOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/json"})
	if err != nil || !strings.Contains(jsonOut.Page.Content, "```json") {
		t.Fatalf("unexpected json fetch: %+v err=%v", jsonOut, err)
	}

	_, err = p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/pdf"})
	if core.AsError(err).Code != core.CodeContentUnavailable {
		t.Fatalf("expected PDF to return content_unavailable, got %v", err)
	}
}

func TestLocalExtractModernDocsTablesCodeAndMath(t *testing.T) {
	const docHTML = `<!doctype html>
<html>
<head><title>API Reference</title></head>
<body>
  <nav><a href="/home">Home</a></nav>
  <main>
    <article>
      <h1>Vector Search Guide</h1>
      <p>The cosine similarity <span class="katex"><span class="katex-mathml"><math><annotation encoding="application/x-tex">\cos(\theta)</annotation></math></span><span class="katex-html" aria-hidden="true">cos(θ)</span></span> measures vector alignment across high-dimensional embeddings in modern retrieval pipelines, enabling semantic ranking with deterministic latency.</p>
      <table>
        <thead><tr><th align="left">Parameter</th><th align="right">Limit</th></tr></thead>
        <tbody><tr><td><code>top_k</code></td><td>50</td></tr></tbody>
      </table>
      <div class="language-go">
        <pre class="shiki"><button class="copy-button">Copy</button><code><span class="line">ctx := context.Background()</span><span class="line">res, err := client.Search(ctx, q)</span></code></pre>
      </div>
      <p>See <a href="/docs/limits">rate limits</a> for production deployment guidelines and best practices.</p>
    </article>
  </main>
</body>
</html>`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(docHTML))
	}))
	defer srv.Close()

	p, _, err := New(Options{MinContentChars: 120}, providertest.NewTestFetchClient(t))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	out, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/guide/vector"})
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	md := out.Page.Content
	for _, wantSub := range []string{
		"$\\cos(\\theta)$",
		"| Parameter | Limit |",
		"| :--- | ---: |",
		"| `top_k` | 50 |",
		"```go\nctx := context.Background()\nres, err := client.Search(ctx, q)\n```",
		"[rate limits](" + srv.URL + "/docs/limits)",
	} {
		if !strings.Contains(md, wantSub) {
			t.Fatalf("extracted markdown missing %q:\n%s", wantSub, md)
		}
	}
	if strings.Contains(md, "Copy") || strings.Contains(md, "cos(θ)") {
		t.Fatalf("extracted markdown leaked copy button or duplicate KaTeX visual layer:\n%s", md)
	}
}

func TestLocalJSONLDNoscriptMarkdownNegotiationAndAntiBotHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/jsonld":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><head>
				<script type="application/ld+json">{"@context":"https://schema.org","@type":"Article","headline":"Schema Headline","author":{"@type":"Person","name":"Ada Lovelace"},"datePublished":"2026-09-15T10:00:00Z","publisher":{"@type":"Organization","name":"Analytical Blog"}}</script>
			</head><body><article>
				<p>Detailed engineering article body with enough prose to clear readability thresholds cleanly while verifying JSON-LD and noscript lazy image preservation across the pipeline.</p>
				<noscript><img src="/assets/hero-hd.png" alt="Hero Diagram" /></noscript>
				<p><a href="/other-post"><time datetime="2020-01-01">Old Card</time></a></p>
			</article></body></html>`))
		case "/md-frontmatter":
			w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
			_, _ = w.Write([]byte("---\ntitle: \"Cloudflare Agents Spec\"\nauthor: \"Edge Team\"\ndate: 2026-10-02\n---\n\n# Cloudflare Agents Spec\n\nNative markdown negotiation delivers clean structured documentation without HTML parsing overhead for AI agents."))
		case "/md-weak":
			if strings.Contains(r.Header.Get("Accept"), "text/markdown") {
				w.Header().Set("Content-Type", "text/markdown")
				_, _ = w.Write([]byte("Short nav stub"))
				return
			}
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html><body><article><h1>Full HTML Fallback</h1><p>Complete article content recovered automatically when the markdown negotiation endpoint only emitted a broken short stub.</p><time datetime="2026-11-01">Nov 1</time></article></body></html>`))
		case "/cf-403":
			w.Header().Set("Cf-Mitigated", "challenge")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("Just a moment..."))
		}
	}))
	defer srv.Close()

	p, _, err := New(Options{MinContentChars: 80}, providertest.NewTestFetchClient(t))
	if err != nil {
		t.Fatal(err)
	}

	jOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/jsonld"})
	if err != nil {
		t.Fatalf("jsonld fetch: %v", err)
	}
	if jOut.Page.Author != "Ada Lovelace" || jOut.Page.Published == nil || jOut.Page.Published.Format("2006-01-02") != "2026-09-15" {
		t.Fatalf("expected JSON-LD author and published time preserved, got author=%q pub=%v", jOut.Page.Author, jOut.Page.Published)
	}
	if !strings.Contains(jOut.Page.Content, "/assets/hero-hd.png") {
		t.Fatalf("expected noscript lazy image preserved by readability, got:\n%s", jOut.Page.Content)
	}

	fmOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/md-frontmatter"})
	if err != nil || fmOut.LowQuality {
		t.Fatalf("md-frontmatter fetch: out=%+v err=%v", fmOut, err)
	}
	if fmOut.Page.Title != "Cloudflare Agents Spec" || fmOut.Page.Author != "Edge Team" || fmOut.Page.Published == nil || strings.Contains(fmOut.Page.Content, "author:") {
		t.Fatalf("expected frontmatter stripped and metadata populated, got page=%+v", fmOut.Page)
	}

	fbOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/md-weak"})
	if err != nil || fbOut.LowQuality || !strings.Contains(fbOut.Page.Content, "Complete article content recovered") {
		t.Fatalf("expected weak text/markdown to downgrade and recover HTML, got out=%+v err=%v", fbOut, err)
	}
	if fbOut.Page.Published == nil || fbOut.Page.Published.Format("2006-01-02") != "2026-11-01" {
		t.Fatalf("expected fallback <time datetime> extracted, got %v", fbOut.Page.Published)
	}

	_, cfErr := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/cf-403"})
	if se := core.AsError(cfErr); se.HintKey != core.HintKeyChallengeDetected {
		t.Fatalf("expected Cloudflare 403 challenge HintKeyChallengeDetected, got %+v", se)
	}
}

func TestLocalWikipediaMathThroughReadabilityAndPlainTextMarkdownSniff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/wiki-bayes":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html><html><head><title>Bayes' theorem</title></head><body><main><article>
				<h1>Bayes' theorem</h1>
				<p>In probability theory and statistics, Bayes' theorem describes the probability of an event based on prior knowledge of conditions that might be related to the event.</p>
				<p>Bayes' theorem is stated mathematically as the following equation: <math xmlns="http://www.w3.org/1998/Math/MathML" class="mwe-math-element mwe-math-element-inline" data-mw='{"name":"math","attrs":{},"body":{"extsrc":"P(A\\vert B) = \\frac{P(B \\vert A) P(A)}{P(B)}"}}'><mrow><mi>P</mi><mo>(</mo><mi>A</mi><mo>|</mo><mi>B</mi><mo>)</mo><mo>=</mo><mfrac><mrow><mi>P</mi><mo>(</mo><mi>B</mi><mo>|</mo><mi>A</mi><mo>)</mo><mi>P</mi><mo>(</mo><mi>A</mi><mo>)</mo></mrow><mrow><mi>P</mi><mo>(</mo><mi>B</mi><mo>)</mo></mrow></mfrac></mrow></math>.</p>
				<p>Pure MathML without annotation also preserves fractions like <math xmlns="http://www.w3.org/1998/Math/MathML"><mfrac><mi>a</mi><mi>b</mi></mfrac></math> across the full extraction pipeline.</p>
			</article></main></body></html>`))
		case "/react-plain-md":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("---\ntitle: Quick Start\nauthor: React Docs\n---\n\nWelcome to the React documentation. This page introduces 80 percent of the concepts you will use daily.\n"))
		case "/llms-txt":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("# Project LLMs Guide\n\nConcise documentation index for coding agents reading this repository directly over HTTP.\n"))
		case "/plain-source":
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("package main\n\n// # not a markdown heading at start\nfunc main() { println(\"hello world from plain source file\") }\n"))
		case "/not-found":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("404 page not found"))
		}
	}))
	defer srv.Close()

	p, _, err := New(Options{MinContentChars: 40}, providertest.NewTestFetchClient(t))
	if err != nil {
		t.Fatal(err)
	}

	wikiOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/wiki-bayes"})
	if err != nil {
		t.Fatalf("wiki-bayes fetch: %v", err)
	}
	if !strings.Contains(wikiOut.Page.Content, `\frac{P(B \vert A) P(A)}{P(B)}`) || !strings.Contains(wikiOut.Page.Content, `$\frac{a}{b}$`) {
		t.Fatalf("expected MediaWiki and MathML LaTeX preserved through Readability, got:\n%s", wikiOut.Page.Content)
	}

	reactOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/react-plain-md"})
	if err != nil {
		t.Fatalf("react-plain-md fetch: %v", err)
	}
	if reactOut.Page.Title != "Quick Start" || reactOut.Page.Author != "React Docs" || strings.Contains(reactOut.Page.Content, "author:") {
		t.Fatalf("expected text/plain YAML frontmatter parsed and stripped, got page=%+v", reactOut.Page)
	}

	llmsOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/llms-txt"})
	if err != nil || llmsOut.Page.Title != "Project LLMs Guide" {
		t.Fatalf("expected text/plain ATX heading title extracted, got out=%+v err=%v", llmsOut, err)
	}

	srcOut, err := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/plain-source"})
	if err != nil || srcOut.Page.Title != "" {
		t.Fatalf("plain Go source must not be misidentified as Markdown, got out=%+v err=%v", srcOut, err)
	}

	_, nfErr := p.Fetch(context.Background(), &core.FetchRequest{URL: srv.URL + "/not-found"})
	if se := core.AsError(nfErr); se.Code != core.CodeNotFound {
		t.Fatalf("expected HTTP 404 -> CodeNotFound, got %+v", se)
	}
}

func TestLocalHelpersAndRobotsWildcards(t *testing.T) {
	p, ok, err := New(Options{}, nil)
	if err != nil || !ok {
		t.Fatalf("New failed: ok=%v err=%v", ok, err)
	}
	if p.Name() != ProviderName || p.Capabilities().MaxCount != 0 || p.Capabilities().Sites != "" {
		t.Fatalf("unexpected provider identity/capabilities: name=%q cap=%+v", p.Name(), p.Capabilities())
	}

	// looksLikeHTML 保守探测 <!doctype html / <html 文档前缀
	for _, sample := range []string{"<!doctype html><html></html>", "  <HTML lang=\"en\"><body>ok</body></html>"} {
		if !looksLikeHTML([]byte(sample)) {
			t.Fatalf("expected looksLikeHTML=true for %q", sample)
		}
	}
	for _, sample := range []string{"", "plain text only", `{"json": true}`, "<article><p>fragment</p></article>"} {
		if looksLikeHTML([]byte(sample)) {
			t.Fatalf("expected looksLikeHTML=false for %q", sample)
		}
	}

	// robotsPatternMatches 通配符 * 与行尾锚点 $
	matchCases := []struct {
		target, pattern string
		want            bool
	}{
		{"/private/page", "/private/", true},
		{"/private", "/private$", true},
		{"/private/sub", "/private$", false},
		{"/a/b/c.pdf", "/a/*.pdf$", true},
		{"/a/b/c.pdf?x=1", "/a/*.pdf$", false},
		{"/search?q=go&page=2", "/search*page=2", true},
		{"/other/page", "/search*page", false},
		{"/docs/guide", "/docs/*", true},
	}
	for _, tc := range matchCases {
		if got := robotsPatternMatches(tc.target, tc.pattern); got != tc.want {
			t.Fatalf("robotsPatternMatches(%q, %q) = %v, want %v", tc.target, tc.pattern, got, tc.want)
		}
	}

	// expandedUTF8Limit 边界与 decodeToUTF8 正常/回退路径
	if expandedUTF8Limit(0) != DefaultMaxBytes*4 {
		t.Fatalf("expected default expanded limit, got %d", expandedUTF8Limit(0))
	}
	if expandedUTF8Limit(math.MaxInt64/2) != math.MaxInt64 {
		t.Fatalf("expected overflow clamp to MaxInt64")
	}
	if got := string(decodeToUTF8([]byte("hello"), "text/plain; charset=utf-8", 1024)); got != "hello" {
		t.Fatalf("unexpected utf-8 decode: %q", got)
	}
	if looksLikeMarkdownDocument("   \n\t  ") {
		t.Fatalf("blank text must not look like markdown")
	}
}
