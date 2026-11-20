package htmlmd

import (
	"strings"

	"golang.org/x/net/html"
)

func extractCodeTable(table *html.Node) *html.Node {
	var rows []*html.Node
	stack := []*html.Node{table}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if cur != table && cur.Type == html.ElementNode && tagName(cur) == "table" {
			return nil
		}
		if cur.Type == html.ElementNode && tagName(cur) == "tr" {
			rows = append(rows, cur)
			if len(rows) > 1 {
				return nil
			}
			continue
		}
		for c := cur.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	if len(rows) != 1 {
		return nil
	}

	var cells []*html.Node
	for c := rows[0].FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (tagName(c) == "td" || tagName(c) == "th") {
			cells = append(cells, c)
		}
	}
	if len(cells) != 2 {
		return nil
	}

	left, right := cells[0], cells[1]
	if !isLineNumberCell(left) {
		return nil
	}
	rightPre := findFirstTag(right, "pre")
	if rightPre == nil {
		return nil
	}
	if lang := detectCodeLanguageChain(table); lang != "" && getAttr(rightPre, attrLang) == "" {
		setAttr(rightPre, attrLang, lang)
	}
	detachNode(rightPre)
	return rightPre
}

func normalizePreElement(pre *html.Node) {
	lang := detectCodeLanguageChain(pre)
	for c := pre.FirstChild; c != nil; c = c.NextSibling {
		if lang == "" && c.Type == html.ElementNode {
			lang = detectElementCodeLang(c)
		}
	}

	var b strings.Builder
	extractPreCodeText(pre, &b)
	codeText := strings.Trim(strings.ReplaceAll(b.String(), "\r\n", "\n"), "\n")

	for pre.FirstChild != nil {
		pre.RemoveChild(pre.FirstChild)
	}
	codeEl := newElement("code")
	if lang != "" {
		setAttr(pre, attrLang, lang)
		setAttr(codeEl, attrLang, lang)
		setAttr(codeEl, "class", "language-"+lang)
	}
	codeEl.AppendChild(&html.Node{
		Type: html.TextNode,
		Data: codeText,
	})
	pre.AppendChild(codeEl)
}

// unwrapSinglePreWrapper 将仅包裹单个 <pre> 的语法高亮外层 <div> 扁平化为直接 <pre>，避免打断外层正文容器评分。
func unwrapSinglePreWrapper(pre *html.Node) {
	parent := pre.Parent
	if parent == nil || parent.Type != html.ElementNode || tagName(parent) != "div" || parent.Parent == nil {
		return
	}
	for c := parent.FirstChild; c != nil; c = c.NextSibling {
		if c == pre {
			continue
		}
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		return
	}
	detachNode(pre)
	replaceNode(parent, pre)
}

func extractPreCodeText(root *html.Node, b *strings.Builder) {
	type op struct {
		node      *html.Node
		afterLine bool
	}
	stack := []op{{node: root}}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]

		if cur.afterLine {
			ensureTrailingNewline(b)
			continue
		}
		n := cur.node
		if n == nil {
			continue
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			continue
		}
		if n.Type != html.ElementNode {
			continue
		}

		t := tagName(n)
		if isPreNoiseNode(n, t) {
			continue
		}
		if t == "br" {
			b.WriteByte('\n')
			continue
		}

		isLineWrap := n != root && isCodeLineContainer(n, t)
		if isLineWrap && b.Len() > 0 {
			ensureTrailingNewline(b)
		}
		if isLineWrap {
			stack = append(stack, op{afterLine: true})
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, op{node: c})
		}
	}
}

func ensureTrailingNewline(b *strings.Builder) {
	s := b.String()
	if s != "" && !strings.HasSuffix(s, "\n") {
		b.WriteByte('\n')
	}
}

func isPreNoiseNode(n *html.Node, t string) bool {
	if t == "button" || t == "svg" || t == "style" || t == "script" {
		return true
	}
	if hasAttr(n, "data-line-number") || strings.EqualFold(getAttr(n, "aria-hidden"), "true") {
		return true
	}
	for _, sub := range []string{"line-number", "lineno", "line-num", "gutter", "copy-button", "codeblock-button"} {
		if hasClassSubstr(n, sub) {
			return true
		}
	}
	if style := strings.ToLower(strings.ReplaceAll(getAttr(n, "style"), " ", "")); strings.Contains(style, "user-select:none") {
		return true
	}
	return false
}

func isCodeLineContainer(n *html.Node, t string) bool {
	switch t {
	case "div", "p", "li", "tr":
		return true
	case "span":
		if hasAttr(n, "data-line") {
			return true
		}
		for tok := range strings.FieldsSeq(getAttr(n, "class")) {
			lower := strings.ToLower(tok)
			if lower == "line" || lower == "ec-line" || lower == "code-line" || strings.HasSuffix(lower, "-line") {
				return true
			}
		}
	}
	return false
}

func detectCodeLanguageChain(n *html.Node) string {
	for cur, up := n, 0; cur != nil && up <= 2; cur, up = cur.Parent, up+1 {
		if cur.Type == html.ElementNode {
			if lang := detectElementCodeLang(cur); lang != "" {
				return lang
			}
		}
	}
	return ""
}

func detectElementCodeLang(n *html.Node) string {
	for _, attr := range []string{attrLang, "data-lang", "data-language", "data-code-language", "lang"} {
		if v := cleanLangToken(getAttr(n, attr)); v != "" {
			if attr == "lang" && (v == "en" || v == "zh" || strings.HasPrefix(v, "en-") || strings.HasPrefix(v, "zh-")) {
				continue
			}
			return v
		}
	}
	for tok := range strings.FieldsSeq(getAttr(n, "class")) {
		lower := strings.ToLower(tok)
		for _, prefix := range []string{"language-", "lang-", "highlight-source-", "highlight-", "syntax-", "brush:"} {
			if after, ok := strings.CutPrefix(lower, prefix); ok {
				if lang := cleanLangToken(after); lang != "" && lang != "text" && lang != "plain" && lang != "none" {
					return lang
				}
			}
		}
	}
	return ""
}

func cleanLangToken(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '+' || r == '#' || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			break
		}
	}
	return strings.Trim(b.String(), "-_")
}
