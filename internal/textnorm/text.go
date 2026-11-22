package textnorm

import (
	"html"
	"strings"
	"unicode/utf8"
)

func isInvisibleControl(r rune) bool {
	switch {
	case r < 0x20 && r != '\n' && r != '\t':
		return true
	case r == 0x7F || r == 0x00AD: // DEL 与软连字符（常被用于拆分隐藏恶意指令词）
		return true
	case r >= 0x200B && r <= 0x200F:
		return true
	case r >= 0x202A && r <= 0x202E:
		return true
	case r >= 0x2060 && r <= 0x2064: // Word Joiner 与隐形数学算子
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	case r == 0xFEFF:
		return true
	case r >= 0xE0000 && r <= 0xE007F: // Unicode Tags 隐形注入区段
		return true
	case r >= 0xE0100 && r <= 0xE01EF: // Variation Selectors Supplement 隐写注入区段
		return true
	default:
		return false
	}
}

// CleanPlainText 归一换行、剥离不可见控制字符并裁剪首尾空白，不执行 HTML 实体反转义，
// 适用于已转换的 Markdown 正文、源代码文件与 JSON 文本，确保代码实体与转义字符不被篡改。
func CleanPlainText(raw string) string {
	if raw == "" {
		return ""
	}
	normalized := strings.ReplaceAll(raw, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")

	var b strings.Builder
	b.Grow(len(normalized))
	for _, r := range normalized {
		if isInvisibleControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// CleanText 解码 HTML 实体并执行控制字符清洗，适用于搜索引擎返回的摘要片段与标题文本。
func CleanText(raw string) string {
	if raw == "" {
		return ""
	}
	return CleanPlainText(html.UnescapeString(raw))
}

// CleanInline 对单行文本（标题、站点名、短摘要）执行 HTML 实体解码、控制字符清洗并折叠连续空白。
func CleanInline(raw string) string {
	return collapseFields(CleanText(raw))
}

func collapseFields(s string) string {
	if s == "" {
		return ""
	}
	return strings.Join(strings.Fields(s), " ")
}

// RuneCount 返回字符串的 Unicode 码点数量。
func RuneCount(s string) int {
	return utf8.RuneCountInString(s)
}

// TruncateRunes 流式按 Unicode 码点安全截断文本（零 []rune 全量分配），截断时在末尾附加省略号 "…"。
func TruncateRunes(s string, maxRunes int) string {
	if maxRunes <= 0 || s == "" {
		return s
	}
	count := 0
	cutByte := -1
	for idx := range s {
		if count == maxRunes-1 {
			cutByte = idx
		}
		count++
		if count > maxRunes {
			if maxRunes == 1 {
				return "…"
			}
			return strings.TrimSpace(s[:cutByte]) + "…"
		}
	}
	return s
}

// ClampUnitFloat 将归一化评分裁剪到 [0, 1] 区间并返回指针。
func ClampUnitFloat(val float64) *float64 {
	return new(min(max(val, 0.0), 1.0))
}

// FirstNonEmpty 返回首个去空白后非空的字符串。
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
