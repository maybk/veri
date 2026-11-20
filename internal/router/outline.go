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

// cleanHeadingText 去除文档站点标题末尾常见的装饰性锚点符号（如 Sphinx/GoDoc 的 `¶`），不修改正文原文。
func cleanHeadingText(raw string) string {
	if cleaned := strings.TrimRight(raw, "¶ \t"); cleaned != "" {
		return cleaned
	}
	return raw
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
