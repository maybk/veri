package router

import (
	"fmt"
	"slices"
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
	if maxItems > 0 && len(spans) > maxItems {
		// 章节数较多时优先保留高层级标题（H1/H2/H3）
		for maxLevel := 3; maxLevel >= 2 && len(spans) > maxItems; maxLevel-- {
			var filtered []rawSectionSpan
			for _, sp := range spans {
				if sp.Level <= maxLevel {
					filtered = append(filtered, sp)
				}
			}
			if len(filtered) >= 2 {
				spans = filtered
			}
		}
		if len(spans) > maxItems {
			spans = spans[:maxItems]
		}
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
					Heading:   heading,
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
// Section 与 Match 同时给出时，先截取指定章节子树再在章节内匹配，且输出偏移锚点始终相对全文。
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
			paged := SlicePage(full, req.Offset, req.MaxChars)
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
	if sec != "" {
		paged.Outline = ExtractOutline(full.Content, maxOutlineSections)
	}
	return paged, nil
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

type passageCandidate struct {
	idx     int
	heading string
	offset  int
	text    string
	score   int
}

func extractMatchView(scope *core.Page, fullContent, sectionName, query string, baseOffset, offset, maxChars int) (*core.Page, []string) {
	qLower := strings.ToLower(strings.TrimSpace(query))
	terms := uniqueSearchTerms(qLower)
	if qLower == "" || len(terms) == 0 {
		paged := SlicePage(scope, offset, maxChars)
		if sectionName != "" {
			paged.Outline = ExtractOutline(fullContent, maxOutlineSections)
		}
		return paged, nil
	}

	blocks := splitMarkdownPassages(scope.Content)
	var hits []passageCandidate
	for i, blk := range blocks {
		lower := strings.ToLower(blk.text)
		score := 0
		if strings.Contains(lower, qLower) {
			score += 100
		}
		matchedTerms := 0
		for _, t := range terms {
			if strings.Contains(lower, t) {
				matchedTerms++
				score += 10
			}
		}
		if matchedTerms == 0 && score == 0 {
			continue
		}
		if matchedTerms == len(terms) && len(terms) > 1 {
			score += 40
		}
		text := blk.text
		// 若前一紧邻块是简短属性标签行（<60 chars，非标题），合并以保留上下文语义
		if i > 0 && !strings.HasPrefix(blocks[i-1].text, "#") && utf8.RuneCountInString(blocks[i-1].text) < 60 {
			text = blocks[i-1].text + "\n\n" + text
		}
		hits = append(hits, passageCandidate{
			idx:     i,
			heading: blk.heading,
			offset:  baseOffset + blk.offset,
			text:    text,
			score:   score,
		})
	}

	if len(hits) == 0 {
		paged := SlicePage(scope, offset, minPositive(maxChars, 2000))
		paged.Outline = ExtractOutline(fullContent, maxOutlineSections)
		if sectionName != "" {
			return paged, []string{fmt.Sprintf("match %q had 0 hits in section %q; showing section preview and outline", query, sectionName)}
		}
		return paged, []string{fmt.Sprintf("match %q had 0 hits; showing top preview and outline", query)}
	}

	slices.SortStableFunc(hits, func(a, b passageCandidate) int {
		if a.score != b.score {
			return b.score - a.score
		}
		return a.idx - b.idx
	})
	if len(hits) > maxMatchSnippets {
		hits = hits[:maxMatchSnippets]
	}
	// 恢复文档原始阅读顺序输出
	slices.SortFunc(hits, func(a, b passageCandidate) int { return a.idx - b.idx })

	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		if h.heading != "" {
			fmt.Fprintf(&b, "> **[offset=%d · %s]**\n\n", h.offset, h.heading)
		} else {
			fmt.Fprintf(&b, "> **[offset=%d]**\n\n", h.offset)
		}
		b.WriteString(h.text)
	}

	matchedPage := scope.Clone()
	matchedPage.Content = b.String()
	paged := SlicePage(matchedPage, offset, maxChars)
	paged.TotalChars = utf8.RuneCountInString(fullContent)
	paged.Outline = ExtractOutline(fullContent, maxOutlineSections)
	return paged, nil
}

type rawPassage struct {
	heading string
	offset  int
	text    string
}

func splitMarkdownPassages(content string) []rawPassage {
	var out []rawPassage
	var curLines []string
	curHeading := ""
	curStartRune := 0
	runePos := 0
	var fence mdscan.Tracker

	flush := func() {
		if len(curLines) == 0 {
			return
		}
		txt := strings.TrimSpace(strings.Join(curLines, ""))
		curLines = curLines[:0]
		if txt != "" {
			out = append(out, rawPassage{heading: curHeading, offset: curStartRune, text: txt})
		}
	}

	for line := range strings.SplitAfterSeq(content, "\n") {
		lineRunes := utf8.RuneCountInString(line)

		switch fence.Next(line) {
		case mdscan.KindFenceOpen:
			flush()
			curStartRune = runePos
			curLines = append(curLines, line)
			runePos += lineRunes
			continue
		case mdscan.KindFenceBody:
			curLines = append(curLines, line)
			runePos += lineRunes
			continue
		case mdscan.KindFenceClose:
			curLines = append(curLines, line)
			runePos += lineRunes
			flush()
			continue
		}

		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flush()
			runePos += lineRunes
			continue
		}

		if _, heading, ok := mdscan.ParseATXHeading(line, mdscan.MaxHeadingLevel); ok {
			flush()
			curHeading = heading
			runePos += lineRunes
			continue
		}

		if len(curLines) == 0 {
			curStartRune = runePos
		}
		curLines = append(curLines, line)
		runePos += lineRunes
	}
	flush()
	return out
}

func uniqueSearchTerms(qLower string) []string {
	seen := make(map[string]struct{})
	var out []string
	for f := range strings.FieldsSeq(qLower) {
		t := strings.Trim(f, `"'.,;:!?()[]{}`)
		if t == "" {
			continue
		}
		if _, dup := seen[t]; !dup {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	return out
}

func minPositive(a, b int) int {
	if a > 0 && a < b {
		return a
	}
	return b
}
