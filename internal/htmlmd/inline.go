package htmlmd

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/html"
)

func (r *renderer) renderInlineElement(n *html.Node, tag string, depth int) {
	switch tag {
	case "br":
		if r.inTableCell || r.inHeading {
			r.writeRaw(" ")
		} else {
			r.ensureNewline()
		}

	case "strong", "b":
		r.renderDelimitedInline(n, "**", depth)

	case "em", "i":
		r.renderDelimitedInline(n, "*", depth)

	case "del", "s", "strike":
		r.renderDelimitedInline(n, "~~", depth)

	case "mark":
		r.renderDelimitedInline(n, "==", depth)

	case "sup":
		inner := strings.TrimSpace(r.renderInlineChildrenString(n, depth))
		if inner != "" {
			if strings.Contains(inner, "](") {
				r.writeRaw(inner)
			} else {
				r.writeRaw("^" + inner + "^")
			}
		}

	case "sub":
		inner := strings.TrimSpace(r.renderInlineChildrenString(n, depth))
		if inner != "" {
			r.writeRaw("~" + inner + "~")
		}

	case "rp":
		return

	case "rt":
		inner := strings.TrimSpace(r.renderInlineChildrenString(n, depth))
		if inner != "" {
			r.writeRaw("(" + inner + ")")
		}

	case "code", "kbd", "samp", "tt":
		text := collapseInlineSpaces(strings.ReplaceAll(collectPlainText(n), "\n", " "))
		if text != "" {
			r.writeRaw(formatInlineCodeSpan(text))
		}

	case "a":
		r.renderLink(n, depth)

	case "img":
		r.renderImage(n)

	case "picture":
		r.renderPicture(n)

	case "q":
		inner := strings.TrimSpace(r.renderInlineChildrenString(n, depth))
		if inner != "" {
			r.writeRaw(`"` + inner + `"`)
		}

	default:
		if needsNumericGlueSpace(r.lastRune(), firstNonSpaceRune(n)) {
			r.writeRaw(" ")
		}
		r.renderChildren(n, depth)
	}
}

func (r *renderer) renderMathElement(n *html.Node, isBlock bool) {
	latex := strings.TrimSpace(collectPlainText(n))
	if latex == "" {
		return
	}
	if isBlock && !r.inTableCell && !r.inHeading {
		r.ensureBlankLine()
		r.writeRaw("$$")
		r.ensureNewline()
		for line := range strings.SplitSeq(latex, "\n") {
			r.writeLinePrefix()
			r.buf.WriteString(strings.TrimRight(line, " \t\r"))
			r.buf.WriteByte('\n')
		}
		r.writeRaw("$$")
		r.ensureBlankLine()
		return
	}
	flat := collapseInlineSpaces(strings.ReplaceAll(latex, "\n", " "))
	r.writeRaw("$" + flat + "$")
}

// renderDelimitedInline 渲染 **粗体** / *斜体* / ~~删除线~~：
//  1. 自动将内部首尾空白提取到定界符外侧（防止 "** text **" 非闭合语法）；
//  2. 处理 CommonMark Flanking 标点规则（如中文 "请看**（重点）**说明"），当内部首尾为标点且紧邻非空白非标点字符时自动补半角保护空格。
func (r *renderer) renderDelimitedInline(n *html.Node, marker string, depth int) {
	raw := r.renderInlineChildrenString(n, depth)
	if strings.TrimSpace(raw) == "" {
		if raw != "" {
			r.renderTextNode(raw)
		}
		return
	}

	leadSpace := hasLeadingSpace(raw)
	trailSpace := hasTrailingSpace(raw)
	coreText := strings.TrimSpace(raw)

	if leadSpace {
		if lb := r.lastByte(); lb != 0 && lb != ' ' && lb != '\n' {
			r.writeRaw(" ")
		}
	}

	if marker == "**" || marker == "*" {
		firstRune, _ := utf8.DecodeRuneInString(coreText)
		prevRune := r.lastRune()
		if isMarkdownPunct(firstRune) && prevRune != 0 && !unicode.IsSpace(prevRune) && !isMarkdownPunct(prevRune) {
			r.buf.WriteByte(' ')
		}
	}

	r.writeRaw(marker + coreText + marker)

	if trailSpace {
		r.writeRaw(" ")
	} else if marker == "**" || marker == "*" {
		lastRune, _ := utf8.DecodeLastRuneInString(coreText)
		if isMarkdownPunct(lastRune) {
			r.pendingPunctSpace = true
		}
	}
}

func (r *renderer) renderInlineChildrenString(n *html.Node, depth int) string {
	sub := &renderer{
		baseURL:     r.baseURL,
		cleanURL:    r.cleanURL,
		inHeading:   r.inHeading,
		inTableCell: r.inTableCell,
		inLink:      r.inLink,
		inlineSub:   true,
	}
	sub.renderChildren(n, depth)
	return sub.buf.String()
}

func needsNumericGlueSpace(prev, next rune) bool {
	if prev == 0 || next == 0 {
		return false
	}
	prevNum := unicode.IsDigit(prev) || prev == '%'
	nextNumOrSign := unicode.IsDigit(next) || next == '+' || next == '-' || strings.ContainsRune("$€£¥₹", next)
	return prevNum && nextNumOrSign
}

func firstNonSpaceRune(root *html.Node) rune {
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			for _, ru := range c.Data {
				if !unicode.IsSpace(ru) {
					return ru
				}
			}
		case html.ElementNode:
			if ru := firstNonSpaceRune(c); ru != 0 {
				return ru
			}
		}
	}
	return 0
}

func formatInlineCodeSpan(text string) string {
	ticks := strings.Repeat("`", longestCharRun(text, '`')+1)
	if strings.HasPrefix(text, "`") || strings.HasSuffix(text, "`") {
		return ticks + " " + text + " " + ticks
	}
	return ticks + text + ticks
}

func (r *renderer) renderTextNode(raw string) {
	if raw == "" {
		return
	}
	normalized := collapseHTMLTextWhitespace(raw)
	if normalized == "" {
		return
	}
	if normalized == " " {
		lb := r.lastByte()
		if lb == 0 || lb == ' ' || lb == '\n' {
			return
		}
		r.buf.WriteByte(' ')
		return
	}

	atStart := r.atLineStart()
	if atStart {
		normalized = strings.TrimLeft(normalized, " ")
		if normalized == "" {
			return
		}
		normalized = escapeLineStartConstructs(normalized)
	}
	r.writeRaw(normalized)
}

func collapseHTMLTextWhitespace(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inSpace := false
	for _, ru := range s {
		if ru == '\u00a0' {
			b.WriteByte(' ')
			inSpace = false
			continue
		}
		if unicode.IsSpace(ru) {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		inSpace = false
		b.WriteRune(ru)
	}
	return b.String()
}

// escapeLineStartConstructs 仅对行首可能被误解析为块级标题、引用、无序或有序列表的纯文本做最小转义。
func escapeLineStartConstructs(s string) string {
	if s == "" {
		return s
	}
	switch s[0] {
	case '#':
		run := 0
		for run < len(s) && run < 7 && s[run] == '#' {
			run++
		}
		if run <= 6 && (run == len(s) || s[run] == ' ') {
			return `\` + s
		}
	case '>':
		return `\` + s
	case '-', '+', '*':
		if len(s) == 1 || s[1] == ' ' {
			return `\` + s
		}
	}
	// 检查行首 "1. " 或 "1) " 有序列表触发模式
	i := 0
	for i < len(s) && i < 9 && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')') && (i+1 == len(s) || s[i+1] == ' ') {
		return s[:i] + `\` + s[i:]
	}
	return s
}

func collapseInlineSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func hasLeadingSpace(s string) bool {
	if s == "" {
		return false
	}
	ru, _ := utf8.DecodeRuneInString(s)
	return unicode.IsSpace(ru)
}

func hasTrailingSpace(s string) bool {
	if s == "" {
		return false
	}
	ru, _ := utf8.DecodeLastRuneInString(s)
	return unicode.IsSpace(ru)
}

func isMarkdownPunct(ru rune) bool {
	return unicode.IsPunct(ru) || unicode.IsSymbol(ru)
}
