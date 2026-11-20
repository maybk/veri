package htmlmd

import (
	"bytes"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"

	"veri/internal/mdscan"
)

const (
	// DefaultMaxNodes 是单次克隆或预处理的默认最大 DOM 节点上限。
	DefaultMaxNodes = 100000

	maxRenderDepth = 256
)

// Options 控制 HTML 预处理与 Markdown 渲染行为。
type Options struct {
	// BaseURL 用于将相对超链接与图片地址解析为绝对 URL（可选）。
	BaseURL *url.URL
	// CleanURL 可选钩子：在渲染超链接目标地址前执行追踪参数清洗（如 urlnorm.StripTrackingParams）。
	CleanURL func(string) string
	// SkipPrepare 为 true 时跳过 Prepare 原地预处理（适用于调用方已提前在整棵文档上执行过 Prepare 的场景）。
	SkipPrepare bool
}

// ConvertString 将原始 UTF-8 HTML 字符串转换为干净紧凑的 GFM Markdown。
func ConvertString(rawHTML string, opts Options) (string, error) {
	return ConvertBytes([]byte(rawHTML), opts)
}

// ConvertBytes 将原始 UTF-8 HTML 字节切片转换为干净紧凑的 GFM Markdown。
func ConvertBytes(rawHTML []byte, opts Options) (string, error) {
	if len(bytes.TrimSpace(rawHTML)) == 0 {
		return "", nil
	}
	doc, err := html.Parse(bytes.NewReader(rawHTML))
	if err != nil {
		return "", err
	}
	if !opts.SkipPrepare {
		Prepare(doc)
	}
	root := findBodyOrRoot(doc)
	renderOpts := opts
	renderOpts.SkipPrepare = true
	return RenderNode(root, renderOpts), nil
}

// RenderNode 将指定的已解析 HTML 节点子树渲染为干净紧凑的 GFM Markdown。
func RenderNode(root *html.Node, opts Options) string {
	if root == nil {
		return ""
	}
	if !opts.SkipPrepare {
		Prepare(root)
	}
	r := &renderer{
		baseURL:  opts.BaseURL,
		cleanURL: opts.CleanURL,
	}
	r.renderNode(root, 0)
	return finalizeMarkdown(r.buf.String())
}

func findBodyOrRoot(doc *html.Node) *html.Node {
	if b := findFirstTag(doc, "body"); b != nil {
		return b
	}
	return doc
}

type renderer struct {
	buf               strings.Builder
	baseURL           *url.URL
	cleanURL          func(string) string
	quoteDepth        int
	listDepth         int
	inHeading         bool
	inTableCell       bool
	inLink            bool
	inlineSub         bool // 行内子渲染器标记（不在首字符误判行首或剥离前导空格）
	afterAlertHeader  bool // 刚输出 GitHub Alert 标头，紧邻首个块不插入空引用行
	pendingPunctSpace bool // 上一个强调定界符以标点结尾，若紧跟非空白非标点字符需补空格以符合 CommonMark
}

func (r *renderer) lastByte() byte {
	s := r.buf.String()
	if len(s) == 0 {
		return 0
	}
	return s[len(s)-1]
}

func (r *renderer) lastRune() rune {
	s := r.buf.String()
	if len(s) == 0 {
		return 0
	}
	ru, _ := utf8.DecodeLastRuneInString(s)
	return ru
}

func (r *renderer) atLineStart() bool {
	if r.inlineSub || r.inHeading || r.inTableCell || r.inLink {
		return false
	}
	lb := r.lastByte()
	if lb == 0 || lb == '\n' {
		return true
	}
	if r.quoteDepth > 0 {
		s := r.buf.String()
		lineStart := strings.LastIndexByte(s, '\n') + 1
		prefix := strings.TrimSpace(s[lineStart:])
		if prefix != "" && strings.Trim(prefix, "> ") == "" {
			return true
		}
	}
	return false
}

func (r *renderer) writeLinePrefix() {
	if r.quoteDepth <= 0 || r.inTableCell {
		return
	}
	lb := r.lastByte()
	if lb == 0 || lb == '\n' {
		for range r.quoteDepth {
			r.buf.WriteString("> ")
		}
	}
}

func (r *renderer) writeRaw(s string) {
	if s == "" {
		return
	}
	if r.pendingPunctSpace {
		r.pendingPunctSpace = false
		first, _ := utf8.DecodeRuneInString(s)
		if first != utf8.RuneError && !unicode.IsSpace(first) && !isMarkdownPunct(first) {
			r.buf.WriteByte(' ')
		}
	}
	r.writeLinePrefix()
	r.buf.WriteString(s)
}

func (r *renderer) ensureNewline() {
	r.pendingPunctSpace = false
	if r.inTableCell || r.inHeading {
		if lb := r.lastByte(); lb != 0 && lb != ' ' && lb != '|' {
			r.buf.WriteByte(' ')
		}
		return
	}
	s := r.buf.String()
	if s == "" {
		return
	}
	trimmed := trimTrailingHorizontalSpace(s)
	if len(trimmed) != len(s) {
		r.buf.Reset()
		r.buf.WriteString(trimmed)
	}
	if r.lastByte() != '\n' {
		r.buf.WriteByte('\n')
	}
}

func (r *renderer) ensureBlankLine() {
	r.pendingPunctSpace = false
	if r.afterAlertHeader {
		r.afterAlertHeader = false
		r.ensureNewline()
		return
	}
	if r.inTableCell || r.inHeading {
		r.ensureNewline()
		return
	}
	s := trimTrailingHorizontalSpace(r.buf.String())
	if s == "" {
		return
	}
	r.buf.Reset()
	r.buf.WriteString(s)

	if r.quoteDepth > 0 {
		if !strings.HasSuffix(s, "\n") {
			r.buf.WriteByte('\n')
		}
		// 检查上一行是否已经是空引用分隔行（如 "> "）
		prev := r.buf.String()
		prevTrimmed := strings.TrimSuffix(prev, "\n")
		lastNL := strings.LastIndexByte(prevTrimmed, '\n')
		lastLine := strings.TrimSpace(prevTrimmed[lastNL+1:])
		if lastLine != "" && strings.Trim(lastLine, "> ") == "" {
			return
		}
		for i := range r.quoteDepth {
			r.buf.WriteString(">")
			if i+1 < r.quoteDepth {
				r.buf.WriteByte(' ')
			}
		}
		r.buf.WriteByte('\n')
		return
	}

	if strings.HasSuffix(s, "\n\n") {
		return
	}
	if strings.HasSuffix(s, "\n") {
		r.buf.WriteByte('\n')
		return
	}
	r.buf.WriteString("\n\n")
}

func (r *renderer) trimTrailingEmptyQuoteLine() {
	s := strings.TrimSuffix(r.buf.String(), "\n")
	if s == "" {
		return
	}
	lastNL := strings.LastIndexByte(s, '\n')
	lastLine := strings.TrimSpace(s[lastNL+1:])
	if lastLine != "" && strings.Trim(lastLine, "> ") == "" {
		r.buf.Reset()
		if lastNL >= 0 {
			r.buf.WriteString(s[:lastNL+1])
		}
	}
}

func (r *renderer) renderChildren(parent *html.Node, depth int) {
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		r.renderNode(c, depth+1)
	}
}

func (r *renderer) renderNode(n *html.Node, depth int) {
	if n == nil || depth > maxRenderDepth {
		return
	}
	switch n.Type {
	case html.DocumentNode:
		r.renderChildren(n, depth)
	case html.TextNode:
		r.renderTextNode(n.Data)
	case html.ElementNode:
		if mathMode := getAttr(n, attrMath); mathMode != "" {
			r.renderMathElement(n, mathMode == "block")
			return
		}
		tag := tagName(n)
		switch tag {
		case "head", "script", "style", "noscript", "template", "svg":
			return
		}
		if isBlockTag(tag) {
			r.renderBlockElement(n, tag, depth)
		} else {
			r.renderInlineElement(n, tag, depth)
		}
	}
}

func trimTrailingHorizontalSpace(s string) string {
	i := len(s)
	for i > 0 && (s[i-1] == ' ' || s[i-1] == '\t') {
		i--
	}
	return s[:i]
}

func finalizeMarkdown(raw string) string {
	if raw == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	var out strings.Builder
	blankRun := 0
	var fence mdscan.Tracker

	for _, line := range lines {
		switch fence.Next(line) {
		case mdscan.KindFenceBody:
			blankRun = 0
			out.WriteString(line)
			out.WriteByte('\n')
			continue
		case mdscan.KindFenceOpen, mdscan.KindFenceClose:
			blankRun = 0
			out.WriteString(trimTrailingHorizontalSpace(line))
			out.WriteByte('\n')
			continue
		}

		cleanLine := trimTrailingHorizontalSpace(line)
		if strings.TrimSpace(cleanLine) == "" {
			blankRun++
			if blankRun <= 1 && out.Len() > 0 {
				out.WriteByte('\n')
			}
			continue
		}
		blankRun = 0
		out.WriteString(cleanLine)
		out.WriteByte('\n')
	}
	return strings.TrimSpace(out.String())
}
