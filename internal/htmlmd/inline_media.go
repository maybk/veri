package htmlmd

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

var (
	bracketEscaper   = strings.NewReplacer("[", `\[`, "]", `\]`)
	angleDestEscaper = strings.NewReplacer("<", "%3C", ">", "%3E", " ", "%20")
)

func (r *renderer) renderLink(a *html.Node, depth int) {
	if r.inLink {
		r.renderChildren(a, depth)
		return
	}
	rawHref := strings.TrimSpace(getAttr(a, "href"))
	href := resolveSafeURL(rawHref, r.baseURL, false)
	isFragment := strings.HasPrefix(rawHref, "#") || strings.HasPrefix(href, "#")
	if href != "" && !isFragment && r.cleanURL != nil {
		href = r.cleanURL(href)
	}

	sub := &renderer{
		baseURL:     r.baseURL,
		cleanURL:    r.cleanURL,
		inHeading:   r.inHeading,
		inTableCell: r.inTableCell,
		inLink:      true,
		inlineSub:   true,
	}
	sub.renderChildren(a, depth)
	label := collapseInlineSpaces(strings.ReplaceAll(sub.buf.String(), "\n", " "))
	trimmedLabel := strings.TrimSpace(label)

	// 标题内的页内 #fragment 锚点：符号/空锚点直接丢弃（不回退 aria-label），包裹标题正文的自指链接剥壳保留纯文本
	if isFragment && isPermalinkSymbol(trimmedLabel) {
		return
	}
	if r.inHeading && isFragment {
		if trimmedLabel != "" {
			r.writeRaw(trimmedLabel)
		}
		return
	}

	if trimmedLabel == "" {
		if isPermalinkClassOrTitle(a) {
			return
		}
		trimmedLabel = collapseInlineSpaces(firstNonEmpty(getAttr(a, "aria-label"), getAttr(a, "title")))
	}
	if trimmedLabel == "" {
		return
	}
	if href == "" {
		r.writeRaw(trimmedLabel)
		return
	}

	if hasLeadingSpace(label) && r.lastByte() != 0 && r.lastByte() != ' ' && r.lastByte() != '\n' {
		r.writeRaw(" ")
	}
	if findFirstTag(a, "img") == nil && isSelfReferentialURL(trimmedLabel, href) && !strings.ContainsAny(href, " \t<>") {
		r.writeRaw("<" + href + ">")
	} else {
		// 若链接内部包含已渲染的 Markdown 图片（如 [![alt](img)](url)），保留其内部图片语法不重复转义方括号
		escapedLabel := trimmedLabel
		if findFirstTag(a, "img") == nil {
			escapedLabel = bracketEscaper.Replace(trimmedLabel)
		}
		r.writeRaw("[" + escapedLabel + "](" + formatLinkDestination(href) + ")")
	}
	if hasTrailingSpace(label) {
		r.writeRaw(" ")
	}
}

func isSelfReferentialURL(label, href string) bool {
	if !strings.HasPrefix(strings.ToLower(href), "http://") && !strings.HasPrefix(strings.ToLower(href), "https://") {
		return false
	}
	return strings.TrimRight(label, "/") == strings.TrimRight(href, "/")
}

func isPermalinkSymbol(s string) bool {
	switch s {
	case "", "#", "§", "¶", "🔗", "\ufeff":
		return true
	default:
		return false
	}
}

func isPermalinkClassOrTitle(a *html.Node) bool {
	lowerTitle := strings.ToLower(getAttr(a, "title") + " " + getAttr(a, "aria-label"))
	if strings.Contains(lowerTitle, "permalink") {
		return true
	}
	for _, sub := range []string{"permalink", "header-anchor", "heading-anchor", "anchor-link", "hash-link"} {
		if hasClassSubstr(a, sub) {
			return true
		}
	}
	return false
}

func (r *renderer) renderPicture(pic *html.Node) {
	if img := findFirstTag(pic, "img"); img != nil {
		r.renderImage(img)
	}
}

func (r *renderer) renderImage(img *html.Node) {
	alt := collapseInlineSpaces(strings.TrimSpace(getAttr(img, "alt")))
	rawSrc := bestImageSource(img)
	src := resolveSafeURL(rawSrc, r.baseURL, true)

	if src == "" {
		if alt != "" {
			r.writeRaw(alt)
		}
		return
	}
	escapedAlt := bracketEscaper.Replace(alt)
	r.writeRaw("![" + escapedAlt + "](" + formatLinkDestination(src) + ")")
}

func bestImageSource(img *html.Node) string {
	for _, attr := range []string{"data-src", "data-original", "data-lazy-src"} {
		if v := strings.TrimSpace(getAttr(img, attr)); v != "" && !strings.HasPrefix(strings.ToLower(v), "data:") {
			return v
		}
	}
	if srcset := strings.TrimSpace(getAttr(img, "srcset")); srcset != "" {
		if best := pickLargestSrcsetURL(srcset); best != "" {
			return best
		}
	}
	return strings.TrimSpace(getAttr(img, "src"))
}

func pickLargestSrcsetURL(srcset string) string {
	bestURL := ""
	bestWidth := -1
	for part := range strings.SplitSeq(srcset, ",") {
		fields := strings.Fields(strings.TrimSpace(part))
		if len(fields) == 0 {
			continue
		}
		u := fields[0]
		if strings.HasPrefix(strings.ToLower(u), "data:") {
			continue
		}
		w := 0
		desc := fields[len(fields)-1]
		if len(fields) >= 2 {
			if numStr, ok := strings.CutSuffix(desc, "w"); ok {
				if parsed, err := strconv.Atoi(numStr); err == nil {
					w = parsed
				}
			}
		}
		if w > bestWidth {
			bestWidth = w
			bestURL = u
		}
	}
	return bestURL
}

func formatLinkDestination(href string) string {
	if strings.ContainsAny(href, " \t()") {
		return "<" + angleDestEscaper.Replace(href) + ">"
	}
	return href
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}
