package term

import (
	"fmt"
	"io"
	"strings"
	"unicode"
)

// DisplayWidth 计算纯文本字符串（不含 ANSI 转义序列）在等宽终端中的显示列宽。
// 中日韩表意文字与全角标点计 2 列，省略号（…）、中点（·）及 ASCII 字符计 1 列，组合音标与控制字符计 0 列。
func DisplayWidth(s string) int {
	width := 0
	for _, r := range s {
		width += runeDisplayWidth(r)
	}
	return width
}

// WriteTable 按终端显示列宽对齐渲染无边框表格，并在列宽计算完成后对单元格施加可选 ANSI 着色，防止颜色转义码破坏列对齐。
func WriteTable(w io.Writer, headers []string, rows [][]string, colorize func(col int, val string) string) {
	if w == nil || len(headers) == 0 {
		return
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = DisplayWidth(h)
	}
	for _, row := range rows {
		for i := range min(len(headers), len(row)) {
			widths[i] = max(widths[i], DisplayWidth(row[i]))
		}
	}

	writeRow := func(cells []string, isHeader bool) {
		var b strings.Builder
		for i := range headers {
			val := ""
			if i < len(cells) {
				val = cells[i]
			}
			rendered := val
			if !isHeader && colorize != nil {
				rendered = colorize(i, val)
			}
			b.WriteString(rendered)
			if i < len(headers)-1 {
				pad := max(widths[i]-DisplayWidth(val)+3, 1)
				b.WriteString(strings.Repeat(" ", pad))
			}
		}
		_, _ = fmt.Fprintln(w, b.String())
	}

	writeRow(headers, true)
	for _, row := range rows {
		writeRow(row, false)
	}
}

func runeDisplayWidth(r rune) int {
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return 0
	}
	if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || unicode.Is(unicode.Cf, r) {
		return 0
	}
	if isEastAsianWide(r) {
		return 2
	}
	return 1
}

func isEastAsianWide(r rune) bool {
	return (r >= 0x1100 && r <= 0x115f) ||
		r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0x303e) ||
		(r >= 0x3040 && r <= 0x3247) ||
		(r >= 0x3250 && r <= 0x4dbf) ||
		(r >= 0x4e00 && r <= 0xa4c6) ||
		(r >= 0xa960 && r <= 0xa97c) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe10 && r <= 0xfe19) ||
		(r >= 0xfe30 && r <= 0xfe6b) ||
		(r >= 0xff01 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1f9ff) ||
		(r >= 0x20000 && r <= 0x3fffd)
}
