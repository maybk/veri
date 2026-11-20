package htmlmd

import (
	"net/url"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestConvertElementsAndFormatting(t *testing.T) {
	baseURL, err := url.Parse("https://docs.example.com/guide/intro.html")
	if err != nil {
		t.Fatalf("parse base url: %v", err)
	}

	tests := []struct {
		name string
		html string
		want string
	}{
		{
			name: "headings_paragraphs_and_self_anchor_cleanup",
			html: `<h1>Getting Started <a href="#getting-started">#</a></h1><p>Hello   <strong>world</strong>!</p>`,
			want: "# Getting Started\n\nHello **world**!",
		},
		{
			name: "cjk_flanking_punctuation_emphasis",
			html: `<p>请仔细阅读<strong>（重要提示）</strong>后再继续，以及<strong>注意：</strong>紧跟正文与<em>「斜体」</em>测试。</p>`,
			want: "请仔细阅读 **（重要提示）** 后再继续，以及**注意：** 紧跟正文与 *「斜体」* 测试。",
		},
		{
			name: "emphasis_outer_whitespace_and_strikethrough",
			html: `<p>Keep<strong> trimmed </strong>and <del>deprecated</del> text.</p>`,
			want: "Keep **trimmed** and ~~deprecated~~ text.",
		},
		{
			name: "dynamic_backtick_inline_and_fenced_code",
			html: "<p>Use <code>``foo``</code> inline.</p><pre data-lang=\"markdown\"><code>```go\nfmt.Println(1)\n```</code></pre>",
			want: "Use ``` ``foo`` ``` inline.\n\n````markdown\n```go\nfmt.Println(1)\n```\n````",
		},
		{
			name: "nested_lists_and_gfm_task_list",
			html: `<ul>
				<li><input type="checkbox" checked> Done task</li>
				<li><input type="checkbox"> Pending task<ul><li>Nested bullet</li></ul></li>
			</ul>
			<ol start="3"><li>Third</li><li>Fourth</li></ol>`,
			want: "- [x] Done task\n- [ ] Pending task\n  - Nested bullet\n\n3. Third\n4. Fourth",
		},
		{
			name: "compact_gfm_table_with_alignment_and_pipe_escape",
			html: `<table>
				<thead>
					<tr>
						<th align="left">Option</th>
						<th style="text-align: center">Default</th>
						<th align="right">Notes</th>
					</tr>
				</thead>
				<tbody>
					<tr>
						<td><code>--format</code></td>
						<td>md</td>
						<td>Supports md | json | jsonl</td>
					</tr>
				</tbody>
			</table>`,
			want: "| Option | Default | Notes |\n| :--- | :---: | ---: |\n| `--format` | md | Supports md \\| json \\| jsonl |",
		},
		{
			name: "relative_links_lazy_images_and_dangerous_uri_filtering",
			html: `<p><a href="../api/search">Search API</a> and <a href="javascript:alert(1)">Safe Text</a></p>
			<p><img alt="Diagram" src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" data-src="/assets/arch.png"></p>
			<p><img alt="HugeDataURI" src="data:image/png;base64,` + strings.Repeat("A", 400) + `"></p>`,
			want: "[Search API](https://docs.example.com/api/search) and Safe Text\n\n![Diagram](https://docs.example.com/assets/arch.png)\n\nHugeDataURI",
		},
		{
			name: "blockquote_and_github_alert",
			html: `<blockquote class="markdown-alert markdown-alert-warning"><p>Watch out for SSRF redirects.</p></blockquote>`,
			want: "> [!WARNING]\n> Watch out for SSRF redirects.",
		},
		{
			name: "line_start_minimal_escaping",
			html: `<p># Not a heading</p><p>1. Not an ordered list</p><p>Normal # and 1. inside text stay clean.</p>`,
			want: "\\# Not a heading\n\n1\\. Not an ordered list\n\nNormal # and 1. inside text stay clean.",
		},
		{
			name: "table_caption_colspan_and_single_column_layout_unwrap",
			html: `<table><caption>API Quotas</caption><tr><th colspan="2">Tier</th></tr><tr><td>Free</td><td>1000</td></tr></table>
			<table><tr><td>Single column layout paragraph</td></tr></table>`,
			want: "**API Quotas**\n\n| Tier |  |\n| --- | --- |\n| Free | 1000 |\n\nSingle column layout paragraph",
		},
		{
			name: "layout_table_containing_multiline_pre_unwrapped",
			html: `<table role="presentation"><tr><td><p>Intro</p><pre><code class="language-sh">echo 1
echo 2</code></pre></td></tr></table>`,
			want: "Intro\n\n```sh\necho 1\necho 2\n```",
		},
		{
			name: "definition_list_details_figure_picture_and_srcset",
			html: `<dl><dt>CLI</dt><dd>Command line interface</dd></dl>
			<details><summary>More info</summary><p>Expanded details.</p></details>
			<figure><picture><img alt="Chart" srcset="/small.png 320w, /hd-(2x).png 1280w" src="/fallback.png"></picture><figcaption>Figure 1: Throughput</figcaption></figure>`,
			want: "**CLI**\n: Command line interface\n\n**More info**\n\nExpanded details.\n\n![Chart](<https://docs.example.com/hd-(2x).png>)\n*Figure 1: Throughput*",
		},
		{
			name: "mathjax_script_and_quotes",
			html: `<p>He said <q>check <script type="math/tex">x_1</script></q>.</p><script type="math/tex; mode=display">a^2 + b^2 = c^2</script>`,
			want: "He said \"check $x_1$\".\n\n$$\na^2 + b^2 = c^2\n$$",
		},
		{
			name: "real_github_div_alert_picture_source_permalink_and_badge_link",
			html: `<h2><a href="#install">Installation</a> <a class="header-anchor" href="#install" aria-label="Permalink to Installation"></a></h2>
			<div class="markdown-alert markdown-alert-important"><p class="markdown-alert-title"><svg></svg>Important</p><p>Use CGO_ENABLED=0 for static builds.</p></div>
			<p><a href="https://ci.example.com/build"><img src="/badge.svg" alt="Build Status"></a></p>
			<picture><source srcset="/hero-1x.webp 640w, /hero-2x.webp 1280w" type="image/webp"><img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=" alt="Hero"></picture>`,
			want: "## Installation\n\n> [!IMPORTANT]\n> Use CGO_ENABLED=0 for static builds.\n\n[![Build Status](https://docs.example.com/badge.svg)](https://ci.example.com/build)\n\n![Hero](https://docs.example.com/hero-2x.webp)",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, convErr := ConvertString(tc.html, Options{BaseURL: baseURL})
			if convErr != nil {
				t.Fatalf("ConvertString unexpected error: %v", convErr)
			}
			if got != tc.want {
				t.Fatalf("ConvertString mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, tc.want)
			}
		})
	}
}

func TestDeeplyNestedDOMSafety(t *testing.T) {
	// 1. 验证 html.Parse 栈限制错误被优雅返回且不发生崩溃
	var b strings.Builder
	for range 600 {
		b.WriteString("<div>")
	}
	b.WriteString("<p>deep leaf</p>")
	for range 600 {
		b.WriteString("</div>")
	}
	_, _ = ConvertString(b.String(), Options{})

	// 2. 验证直接传入 600 层编程构造的 *html.Node 树时 Prepare 与 RenderNode 受 maxRenderDepth 保护不发生栈溢出
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	cur := root
	for range 600 {
		next := &html.Node{Type: html.ElementNode, Data: "div"}
		cur.AppendChild(next)
		cur = next
	}
	cur.AppendChild(&html.Node{Type: html.TextNode, Data: "deep text"})
	_ = RenderNode(root, Options{})
}
