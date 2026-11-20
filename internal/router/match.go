package router

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"veri/internal/core"
	"veri/internal/mdscan"
)

type passageCandidate struct {
	idx       int
	subIdx    int
	heading   string
	lineRange string
	offset    int
	text      string
	score     int
}

type rawPassage struct {
	heading string
	offset  int
	text    string
	isFence bool
}

func extractMatchView(scope *core.Page, fullContent, sectionName, query string, baseOffset, offset, maxChars int) (*core.Page, []string) {
	qLower := strings.ToLower(strings.TrimSpace(query))
	terms := uniqueSearchTerms(qLower)
	if qLower == "" || len(terms) == 0 {
		paged := SlicePage(scope, offset, maxChars)
		if sectionName != "" && paged.NextOffset != nil {
			return paged, []string{sectionTruncatedNote(sectionName, *paged.NextOffset)}
		}
		return paged, nil
	}

	blocks := splitMarkdownPassages(scope.Content)
	var hits []passageCandidate
	for i, blk := range blocks {
		score := scoreMatchText(blk.text, qLower, terms)
		if score == 0 {
			continue
		}
		if blk.isFence && utf8.RuneCountInString(blk.text) > maxWholeFenceMatchRunes {
			if sliced := sliceOversizedFencePassage(i, blk, baseOffset, qLower, terms); len(sliced) > 0 {
				hits = append(hits, sliced...)
				continue
			}
		}
		text := blk.text
		// 若前一紧邻块是简短属性标签行（<60 chars，非标题），合并以保留上下文语义
		if i > 0 && !blocks[i-1].isFence && !strings.HasPrefix(blocks[i-1].text, "#") && utf8.RuneCountInString(blocks[i-1].text) < 60 {
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
		paged := SlicePage(scope, offset, minPositive(maxChars, defaultViewMissPreviewChars))
		if sectionName != "" {
			if len(paged.Outline) == 0 {
				paged.Outline = ExtractOutline(scope.Content, maxOutlineSections)
			}
			return paged, []string{fmt.Sprintf("match %q had 0 hits in section %q; showing section preview and outline", query, sectionName)}
		}
		if len(paged.Outline) == 0 {
			paged.Outline = ExtractOutline(fullContent, maxOutlineSections)
		}
		return paged, []string{fmt.Sprintf("match %q had 0 hits; showing top preview and outline", query)}
	}

	slices.SortStableFunc(hits, func(a, b passageCandidate) int {
		if a.score != b.score {
			return b.score - a.score
		}
		if a.idx != b.idx {
			return a.idx - b.idx
		}
		return a.subIdx - b.subIdx
	})
	if len(hits) > maxMatchSnippets {
		hits = hits[:maxMatchSnippets]
	}
	// 恢复文档原始阅读顺序输出
	slices.SortFunc(hits, func(a, b passageCandidate) int {
		if a.idx != b.idx {
			return a.idx - b.idx
		}
		return a.subIdx - b.subIdx
	})

	var b strings.Builder
	for i, h := range hits {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		writeMatchAnchor(&b, h)
		b.WriteString(h.text)
	}

	matchedPage := scope.Clone()
	matchedPage.Content = b.String()
	paged := SlicePage(matchedPage, offset, maxChars)
	// Match 视图输出的是按锚点拼接的匹配片段，清除 SlicePage 自动提取的片段内大纲，保持 TotalChars / NextOffset 严格对齐当前匹配视图。
	paged.Outline = nil
	if paged.NextOffset != nil {
		if sectionName != "" {
			return paged, []string{fmt.Sprintf("match %q in section %q truncated; keep the same section and match with offset %d to continue", query, sectionName, *paged.NextOffset)}
		}
		return paged, []string{fmt.Sprintf("match %q truncated; keep the same match with offset %d to continue", query, *paged.NextOffset)}
	}
	return paged, nil
}

func writeMatchAnchor(b *strings.Builder, h passageCandidate) {
	parts := []string{fmt.Sprintf("offset=%d", h.offset)}
	if h.heading != "" {
		parts = append(parts, h.heading)
	}
	if h.lineRange != "" {
		parts = append(parts, h.lineRange)
	}
	fmt.Fprintf(b, "> **[%s]**\n\n", strings.Join(parts, " · "))
}

func scoreMatchText(text, qLower string, terms []string) int {
	lower := strings.ToLower(text)
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
		return 0
	}
	if matchedTerms == len(terms) && len(terms) > 1 {
		score += 40
	}
	return score
}

type lineWindow struct {
	start int
	end   int
	score int
}

// sliceOversizedFencePassage 对超大单围栏代码块（如 GitHub 整文件源码）按命中行切出合并后的上下文窗口，保留原始围栏语言与行号锚点。
func sliceOversizedFencePassage(blockIdx int, blk rawPassage, baseOffset int, qLower string, terms []string) []passageCandidate {
	var rawLines []string
	var lineOffsets []int
	runePos := 0
	for line := range strings.SplitAfterSeq(blk.text, "\n") {
		rawLines = append(rawLines, line)
		lineOffsets = append(lineOffsets, runePos)
		runePos += utf8.RuneCountInString(line)
	}
	if n := len(rawLines); n > 0 && rawLines[n-1] == "" {
		rawLines = rawLines[:n-1]
		lineOffsets = lineOffsets[:n-1]
	}
	if len(rawLines) < 3 {
		return nil
	}

	var tr mdscan.Tracker
	if tr.Next(rawLines[0]) != mdscan.KindFenceOpen {
		return nil
	}
	openSpec, ok := tr.Open()
	if !ok {
		return nil
	}
	openLine := openSpec.OpenLine()
	closeLine := openSpec.CloseLine()

	bodyEnd := len(rawLines)
	if last := rawLines[len(rawLines)-1]; tr.Next(last) == mdscan.KindFenceClose {
		bodyEnd = len(rawLines) - 1
	}
	bodyLines := rawLines[1:bodyEnd]
	bodyOffsets := lineOffsets[1:bodyEnd]
	if len(bodyLines) == 0 {
		return nil
	}

	var windows []lineWindow
	for i, line := range bodyLines {
		sc := scoreMatchText(line, qLower, terms)
		if sc == 0 {
			continue
		}
		start := max(0, i-codeMatchContextLines)
		end := min(len(bodyLines)-1, i+codeMatchContextLines)
		if n := len(windows); n > 0 && start <= windows[n-1].end+2 {
			if end > windows[n-1].end {
				windows[n-1].end = end
			}
			windows[n-1].score += sc
		} else {
			windows = append(windows, lineWindow{start: start, end: end, score: sc})
		}
	}
	if len(windows) == 0 {
		return nil
	}

	if len(windows) > maxCodeMatchWindows {
		slices.SortStableFunc(windows, func(a, b lineWindow) int {
			if a.score != b.score {
				return b.score - a.score
			}
			return a.start - b.start
		})
		windows = windows[:maxCodeMatchWindows]
		slices.SortFunc(windows, func(a, b lineWindow) int { return a.start - b.start })
	}

	out := make([]passageCandidate, 0, len(windows))
	for wi, w := range windows {
		var chunk strings.Builder
		chunk.WriteString(openLine)
		chunk.WriteByte('\n')
		for li := w.start; li <= w.end; li++ {
			chunk.WriteString(bodyLines[li])
		}
		if !strings.HasSuffix(bodyLines[w.end], "\n") {
			chunk.WriteByte('\n')
		}
		chunk.WriteString(closeLine)

		out = append(out, passageCandidate{
			idx:       blockIdx,
			subIdx:    wi,
			heading:   blk.heading,
			lineRange: fmt.Sprintf("lines %d-%d", w.start+1, w.end+1),
			offset:    baseOffset + blk.offset + bodyOffsets[w.start],
			text:      chunk.String(),
			score:     w.score,
		})
	}
	return out
}

func splitMarkdownPassages(content string) []rawPassage {
	var out []rawPassage
	var curLines []string
	curHeading := ""
	curStartRune := 0
	curIsFence := false
	runePos := 0
	var fence mdscan.Tracker

	flush := func() {
		if len(curLines) == 0 {
			return
		}
		txt := strings.TrimSpace(strings.Join(curLines, ""))
		isFence := curIsFence
		curLines = curLines[:0]
		curIsFence = false
		if txt != "" {
			out = append(out, rawPassage{heading: curHeading, offset: curStartRune, text: txt, isFence: isFence})
		}
	}

	for line := range strings.SplitAfterSeq(content, "\n") {
		lineRunes := utf8.RuneCountInString(line)

		switch fence.Next(line) {
		case mdscan.KindFenceOpen:
			flush()
			curStartRune = runePos
			curIsFence = true
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
			curHeading = cleanHeadingText(heading)
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
