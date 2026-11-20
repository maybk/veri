package router

import (
	"strings"
	"unicode/utf8"

	"veri/internal/core"
	"veri/internal/mdscan"
)

// SlicePage 对完整页面正文按 Unicode 码点执行零 []rune 拷贝的围栏感知流式分页切片：
//  1. 优先在段落或换行边界切分；
//  2. 若切点落在窗口后半段开启的代码块内，自动回退至代码块起点前切分以保持代码完整；
//  3. 若巨型代码块必须跨页切分，自动在页尾补闭合围栏并在下一页页首补开启围栏，且 NextOffset 严格对齐原始文本码点；
//  4. 当发生分页截断且页面尚未携带大纲时，自动提取并附带章节大纲 Outline。
func SlicePage(full *core.Page, offset, maxChars int) *core.Page {
	if full == nil {
		return nil
	}
	cp := full.Clone()
	content := cp.Content
	total := utf8.RuneCountInString(content)
	cp.TotalChars = total
	cp.NextOffset = nil

	if offset >= total {
		cp.Content = ""
		return cp
	}
	if offset < 0 {
		offset = 0
	}
	needSliceEnd := maxChars > 0 && maxChars < total-offset
	if offset == 0 && !needSliceEnd {
		return cp
	}

	end := 0
	minBacktrack := 0
	if needSliceEnd {
		end = offset + maxChars
		minBacktrack = max(offset+1, end-240)
	}

	startByte := 0
	endByte := len(content)
	lastParaCutRune, lastParaCutByte := 0, 0
	lastLineCutRune, lastLineCutByte := 0, 0
	lastLineInFence := false
	var lastLineCloseFence string

	var fence mdscan.Tracker
	fenceStartRune, fenceStartByte := 0, 0
	startedInFence := false
	var prefixFence string

	lineStartByte, lineStartRune := 0, 0
	runeIdx := 0
	var prevRune rune

	for bytePos, r := range content {
		if runeIdx == offset {
			startByte = bytePos
			if open, ok := fence.Open(); ok {
				startedInFence = true
				prefixFence = open.OpenLine()
			}
			if !needSliceEnd {
				break
			}
		}
		_, width := utf8.DecodeRuneInString(content[bytePos:])
		nextRune := runeIdx + 1
		nextByte := bytePos + width

		if r == '\n' {
			if fence.Next(content[lineStartByte:bytePos]) == mdscan.KindFenceOpen {
				fenceStartRune = lineStartRune
				fenceStartByte = lineStartByte
			}
			lineStartByte = nextByte
			lineStartRune = nextRune
		}

		if needSliceEnd && nextRune >= minBacktrack && nextRune <= end {
			if r == '\n' {
				lastLineCutRune = nextRune
				lastLineCutByte = nextByte
				if open, ok := fence.Open(); ok {
					lastLineInFence = true
					lastLineCloseFence = open.CloseLine()
				} else {
					lastLineInFence = false
					lastLineCloseFence = ""
				}
				if !fence.InFence() && nextRune >= 2 && prevRune == '\n' {
					lastParaCutRune = nextRune
					lastParaCutByte = nextByte
				}
			}
			if nextRune == end {
				endByte = nextByte
				break
			}
		}
		prevRune = r
		runeIdx = nextRune
	}

	midLineStart := startByte > 0 && content[startByte-1] != '\n'

	if !needSliceEnd {
		sliced := content[startByte:]
		if startedInFence && prefixFence != "" {
			sliced = prefixFence + "\n" + sliced
		}
		if midLineStart {
			sliced = closeUnclosedFence(sliced)
		}
		cp.Content = sliced
		return cp
	}

	cutRune := end
	cutByte := endByte
	openAtEnd, endedInFence := fence.Open()
	suffixFence := openAtEnd.CloseLine()
	midLineCut := false

	switch {
	case endedInFence && fenceStartRune > offset+maxChars/2:
		// 代码块在窗口后半段才开启：回退至围栏起点前切分，将完整代码块留给下一页
		cutRune = fenceStartRune
		cutByte = fenceStartByte
		endedInFence = false
		suffixFence = ""
	case !endedInFence && lastParaCutRune > 0:
		cutRune = lastParaCutRune
		cutByte = lastParaCutByte
		endedInFence = false
		suffixFence = ""
	case lastLineCutRune > 0:
		cutRune = lastLineCutRune
		cutByte = lastLineCutByte
		endedInFence = lastLineInFence
		suffixFence = lastLineCloseFence
	default:
		midLineCut = cutByte > lineStartByte
	}

	sliced := content[startByte:cutByte]
	if startedInFence && prefixFence != "" {
		sliced = prefixFence + "\n" + sliced
	}
	if midLineStart || midLineCut {
		// 超长无换行行被从中间硬切时，按本页实际内容扫描围栏状态，避免误加或漏补闭合围栏
		sliced = closeUnclosedFence(sliced)
	} else if endedInFence && suffixFence != "" {
		sliced = appendClosingFence(sliced, suffixFence)
	}

	cp.Content = strings.TrimRight(sliced, "\n")
	cp.NextOffset = &cutRune
	if len(cp.Outline) == 0 {
		cp.Outline = ExtractOutline(content, maxOutlineSections)
	}
	return cp
}

func appendClosingFence(s, closeFence string) string {
	if strings.HasSuffix(s, "\n") {
		return s + closeFence
	}
	return s + "\n" + closeFence
}

func closeUnclosedFence(s string) string {
	var tr mdscan.Tracker
	for line := range strings.SplitSeq(s, "\n") {
		tr.Next(line)
	}
	if open, ok := tr.Open(); ok {
		return appendClosingFence(s, open.CloseLine())
	}
	return s
}
