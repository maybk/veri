package router

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"veri/internal/core"
	"veri/internal/mdscan"
)

const maxOutlineHeadingLevel = 4

type rawSectionSpan struct {
	core.PageSection
	startByte int
	endByte   int
}

// ExtractOutline 扫描完整 Markdown 正文（自动跳过围栏代码块内部），提取最多 maxItems 条章节目录与码点偏移量。
func ExtractOutline(content string, maxItems int) []core.PageSection {
	spans := scanSectionSpans(content)
	if len(spans) == 0 {
		return nil
	}
	pruned := false
	if maxItems > 0 && len(spans) > maxItems {
		// 章节数较多时优先保留高层级标题（H1/H2/H3）
		for maxLevel := 3; maxLevel >= 2 && len(spans) > maxItems; maxLevel-- {
			var filtered []rawSectionSpan
			for _, sp := range spans {
				if sp.Level <= maxLevel {
					filtered = append(filtered, sp)
				}
			}
			if len(filtered) >= 2 && len(filtered) < len(spans) {
				spans = filtered
				pruned = true
			}
		}
	}
	if pruned {
		totalRunes := utf8.RuneCountInString(content)
		for i := range spans {
			if i+1 < len(spans) {
				spans[i].Chars = spans[i+1].Offset - spans[i].Offset
			} else {
				spans[i].Chars = totalRunes - spans[i].Offset
			}
		}
	}
	if maxItems > 0 && len(spans) > maxItems {
		spans = spans[:maxItems]
	}
	out := make([]core.PageSection, len(spans))
	for i, sp := range spans {
		out[i] = sp.PageSection
	}
	return out
}

func scanSectionSpans(content string) []rawSectionSpan {
	if content == "" {
		return nil
	}
	var spans []rawSectionSpan
	var fence mdscan.Tracker

	byteOffset := 0
	runeOffset := 0
	totalRunes := utf8.RuneCountInString(content)

	for line := range strings.SplitAfterSeq(content, "\n") {
		lineRunes := utf8.RuneCountInString(line)
		if fence.Next(line) == mdscan.KindText {
			if lvl, heading, ok := mdscan.ParseATXHeading(line, maxOutlineHeadingLevel); ok {
				if n := len(spans); n > 0 {
					spans[n-1].endByte = byteOffset
					spans[n-1].Chars = runeOffset - spans[n-1].Offset
				}
				spans = append(spans, rawSectionSpan{
					Heading:   cleanHeadingText(heading),
					Level:     lvl,
					Offset:    runeOffset,
					startByte: byteOffset,
				})
			}
		}

		byteOffset += len(line)
		runeOffset += lineRunes
	}

	if n := len(spans); n > 0 {
		spans[n-1].endByte = len(content)
		spans[n-1].Chars = totalRunes - spans[n-1].Offset
	}
	return spans
}

// ApplyPageView 在完整缓存或提取页面上按管线应用可选的 Section（章节截取）-> Match（页内匹配窗口）-> SlicePage 分页切片。
// Section 与 Match 同时给出时，先截取指定章节子树再在章节内匹配，且匹配锚点偏移始终相对全文。
func ApplyPageView(full *core.Page, req *core.FetchRequest) (*core.Page, []string) {
	if full == nil {
		return nil, nil
	}
	if req == nil {
		return full.Clone(), nil
	}
	sec := strings.TrimSpace(req.Section)
	m := strings.TrimSpace(req.Match)

	scopePage := full
	baseOffset := 0
	if sec != "" {
		extracted, secOffset, ok := extractSectionSubtree(full, sec)
		if !ok {
			paged := SlicePage(full, req.Offset, minPositive(req.MaxChars, defaultViewMissPreviewChars))
			if len(paged.Outline) == 0 {
				paged.Outline = ExtractOutline(full.Content, maxOutlineSections)
			}
			note := fmt.Sprintf("section %q not found; showing page preview and heading outline", sec)
			return paged, []string{note}
		}
		scopePage = extracted
		baseOffset = secOffset
	}

	if m != "" {
		return extractMatchView(scopePage, full.Content, sec, m, baseOffset, req.Offset, req.MaxChars)
	}

	paged := SlicePage(scopePage, req.Offset, req.MaxChars)
	if sec != "" && paged.NextOffset != nil {
		return paged, []string{sectionTruncatedNote(sec, *paged.NextOffset)}
	}
	return paged, nil
}

func sectionTruncatedNote(sec string, nextOffset int) string {
	return fmt.Sprintf("section %q truncated; keep the same section with offset %d to continue", sec, nextOffset)
}

// cleanHeadingText 去除文档站点标题首尾常见的装饰性自锚点（如裸 `¶` 或 `[¶](...)` / `[#](...)` / `[§](...)` / `[🔗](...)` / `[](#...)`），
// 保留正常业务链接与 `C#` 等合法末尾符号；若剥离后为空则原样返回。
func cleanHeadingText(raw string) string {
	s := strings.TrimSpace(raw)
	for {
		trimmed := strings.TrimRight(s, "¶ \t")
		if trimmed != s {
			s = trimmed
			continue
		}
		if next, ok := trimTrailingAnchorLink(s); ok {
			s = next
			continue
		}
		if next, ok := trimLeadingAnchorLink(s); ok {
			s = next
			continue
		}
		break
	}
	if s != "" {
		return s
	}
	return raw
}

// normalizeFetchedPage 在入视图与 MCP 缓存前，对普通网页（SourceGeneric）的围栏外 ATX 标题行执行克制的装饰性锚点净化。
// 跳过 GitHub 源码/Issue/PR 等结构化页面，绝不触碰代码块与普通正文段落，且同步维护 TotalChars 与偏移量一致性。
func normalizeFetchedPage(page *core.Page) *core.Page {
	if page == nil {
		return nil
	}
	if page.SourceType != "" && page.SourceType != core.SourceGeneric {
		return page
	}
	cleanedTitle := cleanHeadingText(page.Title)
	cleanedContent, contentChanged := normalizeGenericHeadingAnchors(page.Content)
	if !contentChanged && cleanedTitle == page.Title {
		return page
	}
	cp := page.Clone()
	cp.Title = cleanedTitle
	if contentChanged {
		cp.Content = cleanedContent
		cp.TotalChars = utf8.RuneCountInString(cleanedContent)
	}
	return cp
}

// normalizeGenericHeadingAnchors 仅清理围栏代码块之外的 ATX 标题行（H1-H6）首尾装饰性锚点；
// 若文档不含任何候选字符或未改写任何标题行，走零分配快路径原样返回。
func normalizeGenericHeadingAnchors(content string) (string, bool) {
	if content == "" || (!strings.Contains(content, "¶") && !strings.Contains(content, "](")) {
		return content, false
	}
	var tr mdscan.Tracker
	var b strings.Builder
	changed := false
	writtenByte := 0
	lineStart := 0

	for line := range strings.SplitAfterSeq(content, "\n") {
		lineEnd := lineStart + len(line)
		if tr.Next(line) == mdscan.KindText {
			if lvl, rawHeading, ok := mdscan.ParseATXHeading(line, mdscan.MaxHeadingLevel); ok {
				cleaned := cleanHeadingText(rawHeading)
				if cleaned != rawHeading {
					if !changed {
						b.Grow(len(content))
						changed = true
					}
					b.WriteString(content[writtenByte:lineStart])
					b.WriteString(strings.Repeat("#", lvl))
					b.WriteByte(' ')
					b.WriteString(cleaned)
					b.WriteString(lineEndingOf(line))
					writtenByte = lineEnd
				}
			}
		}
		lineStart = lineEnd
	}
	if !changed {
		return content, false
	}
	b.WriteString(content[writtenByte:])
	return b.String(), true
}

func lineEndingOf(line string) string {
	switch {
	case strings.HasSuffix(line, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(line, "\n"):
		return "\n"
	default:
		return ""
	}
}

func trimTrailingAnchorLink(s string) (string, bool) {
	if !strings.HasSuffix(s, ")") {
		return s, false
	}
	sep := strings.LastIndex(s, "](")
	if sep < 0 {
		return s, false
	}
	openBracket := strings.LastIndexByte(s[:sep], '[')
	if openBracket < 0 {
		return s, false
	}
	// 排除 Markdown 图片语法 ![alt](url#hash)，避免把图片拆碎留下孤立的 '!'。
	if openBracket > 0 && s[openBracket-1] == '!' {
		return s, false
	}
	label := s[openBracket+1 : sep]
	body := s[sep+2 : len(s)-1]
	if !isDecorativeAnchorLink(label, body) {
		return s, false
	}
	return strings.TrimRight(s[:openBracket], " \t"), true
}

func trimLeadingAnchorLink(s string) (string, bool) {
	if !strings.HasPrefix(s, "[") {
		return s, false
	}
	sep := strings.Index(s, "](")
	if sep < 0 {
		return s, false
	}
	closeParen := findBalancedClosingParen(s[sep+2:])
	if closeParen < 0 {
		return s, false
	}
	end := sep + 2 + closeParen + 1
	label := s[1:sep]
	body := s[sep+2 : end-1]
	if !isDecorativeAnchorLink(label, body) {
		return s, false
	}
	rest := s[end:]
	if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return s, false
	}
	return strings.TrimLeft(rest, " \t"), true
}

func isDecorativeAnchorLink(label, body string) bool {
	trimmedLabel := strings.TrimSpace(label)
	switch trimmedLabel {
	case "", "#", "§", "¶", "🔗":
	default:
		return false
	}
	body = strings.TrimSpace(body)
	if body == "" || !hasBalancedLinkParens(body) {
		return false
	}
	target, rest, hasRest := strings.Cut(body, " ")
	if target == "" {
		return false
	}
	if hasRest {
		title := strings.TrimSpace(rest)
		if len(title) < 2 {
			return false
		}
		q := title[0]
		if (q != '"' && q != '\'') || title[len(title)-1] != q {
			return false
		}
	}
	if strings.Contains(target, "#") {
		return true
	}
	// 明确装饰符（¶ / § / 🔗）允许无 fragment 的 permalink URL；空文本或 `#` 必须含 `#` 锚点防止误伤。
	return (trimmedLabel == "¶" || trimmedLabel == "§" || trimmedLabel == "🔗") &&
		(strings.HasPrefix(target, "http://") || strings.HasPrefix(target, "https://"))
}

func findBalancedClosingParen(s string) int {
	depth := 1
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r', '\n':
			return -1
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func hasBalancedLinkParens(s string) bool {
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\r', '\n':
			return false
		case '(':
			depth++
		case ')':
			depth--
			if depth < 0 {
				return false
			}
		}
	}
	return depth == 0
}

func extractSectionSubtree(full *core.Page, targetSection string) (*core.Page, int, bool) {
	spans := scanSectionSpans(full.Content)
	targetLower := strings.ToLower(strings.TrimSpace(targetSection))

	bestIdx := -1
	bestTier := 99 // 1: exact, 2: prefix, 3: contains
	for i, sp := range spans {
		hl := strings.ToLower(sp.Heading)
		tier := 0
		switch {
		case hl == targetLower:
			tier = 1
		case strings.HasPrefix(hl, targetLower):
			tier = 2
		case strings.Contains(hl, targetLower):
			tier = 3
		}
		if tier > 0 && tier < bestTier {
			bestTier = tier
			bestIdx = i
			if tier == 1 {
				break
			}
		}
	}

	if bestIdx < 0 {
		return nil, 0, false
	}

	startByte := spans[bestIdx].startByte
	endByte := len(full.Content)
	baseLevel := spans[bestIdx].Level
	for j := bestIdx + 1; j < len(spans); j++ {
		if spans[j].Level <= baseLevel {
			endByte = spans[j].startByte
			break
		}
	}

	sectionPage := full.Clone()
	sectionPage.Content = strings.TrimSpace(full.Content[startByte:endByte])
	return sectionPage, spans[bestIdx].Offset, true
}
