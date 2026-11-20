package htmlmd

import (
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

func isBlockTag(tag string) bool {
	switch tag {
	case "address", "article", "aside", "blockquote", "dd", "details", "div", "dl", "dt",
		"fieldset", "figcaption", "figure", "footer", "h1", "h2", "h3", "h4", "h5", "h6",
		"header", "hgroup", "hr", "li", "main", "nav", "ol", "p", "pre", "section", "summary",
		"table", "ul":
		return true
	default:
		return false
	}
}

func (r *renderer) renderBlockElement(n *html.Node, tag string, depth int) {
	if r.inTableCell {
		switch tag {
		case "p", "div", "section", "article", "li":
			if lb := r.lastByte(); lb != 0 && lb != ' ' && lb != '|' {
				r.writeRaw("<br>")
			}
			r.renderChildren(n, depth)
			return
		case "pre":
			code := strings.TrimSpace(collectPlainText(n))
			if code != "" {
				r.writeRaw(formatInlineCodeSpan(strings.ReplaceAll(code, "\n", " ")))
			}
			return
		}
	}

	switch tag {
	case "h1", "h2", "h3", "h4", "h5", "h6":
		level := int(tag[1] - '0')
		r.renderHeading(n, level, depth)

	case "p":
		r.ensureBlankLine()
		r.renderChildren(n, depth)
		r.ensureBlankLine()

	case "div", "section", "article", "main", "header", "footer", "aside", "nav", "figure", "hgroup", "fieldset", "address":
		if hasBlockChild(n) {
			r.ensureNewline()
			r.renderChildren(n, depth)
			r.ensureNewline()
		} else {
			if strings.TrimSpace(collectPlainText(n)) == "" && findFirstTag(n, "img") == nil {
				return
			}
			r.ensureBlankLine()
			r.renderChildren(n, depth)
			r.ensureBlankLine()
		}

	case "figcaption":
		text := strings.TrimSpace(r.renderSubtreeInline(n, depth))
		if text != "" {
			r.ensureNewline()
			r.writeRaw("*" + text + "*")
			r.ensureBlankLine()
		}

	case "blockquote":
		r.ensureBlankLine()
		alertType := detectAlertType(n)
		r.quoteDepth++
		if alertType != "" {
			r.writeRaw("[!" + alertType + "]")
			r.ensureNewline()
			r.afterAlertHeader = true
		}
		r.renderChildren(n, depth)
		r.afterAlertHeader = false
		r.trimTrailingEmptyQuoteLine()
		r.quoteDepth--
		r.ensureBlankLine()

	case "pre":
		r.renderPreBlock(n)

	case "ul":
		r.renderList(n, false, depth)

	case "ol":
		r.renderList(n, true, depth)

	case "li":
		r.ensureNewline()
		r.writeRaw("- ")
		r.renderChildren(n, depth)
		r.ensureNewline()

	case "hr":
		r.ensureBlankLine()
		r.writeRaw("---")
		r.ensureBlankLine()

	case "table":
		r.renderTable(n, depth)

	case "dl":
		r.ensureBlankLine()
		r.renderChildren(n, depth)
		r.ensureBlankLine()

	case "dt":
		term := strings.TrimSpace(r.renderSubtreeInline(n, depth))
		if term != "" {
			r.ensureBlankLine()
			r.writeRaw("**" + term + "**")
			r.ensureNewline()
		}

	case "dd":
		r.ensureNewline()
		r.writeRaw(": ")
		r.renderChildren(n, depth)
		r.ensureNewline()

	case "details":
		r.ensureBlankLine()
		r.renderChildren(n, depth)
		r.ensureBlankLine()

	case "summary":
		title := strings.TrimSpace(r.renderSubtreeInline(n, depth))
		if title != "" {
			r.ensureNewline()
			r.writeRaw("**" + title + "**")
			r.ensureBlankLine()
		}

	default:
		r.renderChildren(n, depth)
	}
}

func (r *renderer) renderHeading(n *html.Node, level int, depth int) {
	content := strings.TrimSpace(r.renderSubtreeHeading(n, depth))
	if content == "" {
		return
	}
	level = max(1, min(level, 6))
	r.ensureBlankLine()
	r.writeRaw(strings.Repeat("#", level) + " " + content)
	r.ensureBlankLine()
}

func (r *renderer) renderSubtreeHeading(n *html.Node, depth int) string {
	sub := &renderer{
		baseURL:   r.baseURL,
		cleanURL:  r.cleanURL,
		inHeading: true,
	}
	sub.renderChildren(n, depth)
	return collapseInlineSpaces(sub.buf.String())
}

func (r *renderer) renderSubtreeInline(n *html.Node, depth int) string {
	sub := &renderer{
		baseURL:     r.baseURL,
		cleanURL:    r.cleanURL,
		inTableCell: true,
	}
	sub.renderChildren(n, depth)
	return strings.TrimSpace(sub.buf.String())
}

func (r *renderer) renderPreBlock(pre *html.Node) {
	lang := getAttr(pre, attrLang)
	if lang == "" {
		lang = detectElementCodeLang(pre)
	}
	for c := pre.FirstChild; c != nil && lang == ""; c = c.NextSibling {
		if c.Type == html.ElementNode {
			lang = detectElementCodeLang(c)
		}
	}

	code := strings.Trim(strings.ReplaceAll(collectPlainText(pre), "\r\n", "\n"), "\n")
	if strings.TrimSpace(code) == "" {
		return
	}

	fenceLen := max(3, longestCharRun(code, '`')+1)
	fence := strings.Repeat("`", fenceLen)

	r.ensureBlankLine()
	r.writeRaw(fence + lang)
	r.ensureNewline()
	for line := range strings.SplitSeq(code, "\n") {
		r.writeLinePrefix()
		r.buf.WriteString(line)
		r.buf.WriteByte('\n')
	}
	r.writeRaw(fence)
	r.ensureBlankLine()
}

func (r *renderer) renderList(listNode *html.Node, ordered bool, depth int) {
	if r.listDepth == 0 {
		r.ensureBlankLine()
	} else {
		r.ensureNewline()
	}

	startNum := 1
	if ordered {
		if v, err := strconv.Atoi(strings.TrimSpace(getAttr(listNode, "start"))); err == nil && v >= 0 {
			startNum = v
		}
	}

	idx := startNum
	indent := strings.Repeat("  ", r.listDepth)
	r.listDepth++
	for c := listNode.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		t := tagName(c)
		if t == "ul" || t == "ol" {
			r.renderList(c, t == "ol", depth+1)
			continue
		}
		if t != "li" {
			continue
		}
		r.renderListItem(c, indent, ordered, idx, depth+1)
		idx++
	}
	r.listDepth--

	if r.listDepth == 0 {
		r.ensureBlankLine()
	}
}

func (r *renderer) renderListItem(li *html.Node, indent string, ordered bool, num int, depth int) {
	marker := "- "
	if ordered {
		marker = fmt.Sprintf("%d. ", num)
	}
	if checked, isTask := extractTaskCheckbox(li); isTask {
		if checked {
			marker += "[x] "
		} else {
			marker += "[ ] "
		}
	}

	sub := &renderer{
		baseURL:  r.baseURL,
		cleanURL: r.cleanURL,
	}
	for c := li.FirstChild; c != nil; c = c.NextSibling {
		if isDirectCheckboxInput(c) {
			continue
		}
		sub.renderNode(c, depth+1)
	}
	itemContent := strings.TrimSpace(sub.buf.String())
	if itemContent == "" {
		return
	}

	r.ensureNewline()
	lines := strings.Split(itemContent, "\n")
	contIndent := indent + "  "
	for i, line := range lines {
		if i == 0 {
			r.writeRaw(indent + marker + line)
		} else if strings.TrimSpace(line) == "" {
			r.ensureNewline()
		} else {
			r.ensureNewline()
			r.writeRaw(contIndent + line)
		}
	}
	r.ensureNewline()
}

func extractTaskCheckbox(li *html.Node) (checked bool, ok bool) {
	switch getAttr(li, attrTask) {
	case "checked":
		return true, true
	case "unchecked":
		return false, true
	}
	for c := li.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
			continue
		}
		if isDirectCheckboxInput(c) {
			return hasAttr(c, "checked"), true
		}
		if c.Type == html.ElementNode && tagName(c) == "p" {
			for pc := c.FirstChild; pc != nil; pc = pc.NextSibling {
				if pc.Type == html.TextNode && strings.TrimSpace(pc.Data) == "" {
					continue
				}
				if isDirectCheckboxInput(pc) {
					return hasAttr(pc, "checked"), true
				}
				break
			}
		}
		break
	}
	return false, false
}

func isDirectCheckboxInput(n *html.Node) bool {
	return n != nil && n.Type == html.ElementNode && tagName(n) == "input" && strings.EqualFold(strings.TrimSpace(getAttr(n, "type")), "checkbox")
}

func hasBlockChild(n *html.Node) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && isBlockTag(tagName(c)) {
			return true
		}
	}
	return false
}

func detectAlertType(bq *html.Node) string {
	return classifyAlertContainer(bq)
}

func longestCharRun(s string, target byte) int {
	maxRun, cur := 0, 0
	for i := 0; i < len(s); i++ {
		if s[i] == target {
			cur++
			if cur > maxRun {
				maxRun = cur
			}
		} else {
			cur = 0
		}
	}
	return maxRun
}
