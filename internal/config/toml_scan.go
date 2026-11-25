package config

import "strings"

func detectLineEnding(raw string) string {
	if strings.Contains(raw, "\r\n") {
		return "\r\n"
	}
	return "\n"
}

func normalizeNewlines(raw string) string {
	// 剥离 Windows 编辑器可能写入的 UTF-8 BOM，避免首行 [section] 匹配失败。
	cleaned := strings.TrimPrefix(raw, "\uFEFF")
	return strings.ReplaceAll(strings.ReplaceAll(cleaned, "\r\n", "\n"), "\r", "\n")
}

func restoreLineEnding(text, eol string) string {
	if eol == "\r\n" {
		return strings.ReplaceAll(text, "\n", "\r\n")
	}
	return text
}

func parseSectionHeader(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "[") || strings.HasPrefix(trimmed, "[[") {
		return "", false
	}
	closeIdx := strings.IndexByte(trimmed, ']')
	if closeIdx <= 1 {
		return "", false
	}
	rest := strings.TrimSpace(trimmed[closeIdx+1:])
	if rest != "" && !strings.HasPrefix(rest, "#") {
		return "", false
	}
	sec := strings.TrimSpace(trimmed[1:closeIdx])
	if sec == "" {
		return "", false
	}
	return sec, true
}

func parseKeyAssignment(lines []string, startIdx int) (key, trailingComment string, endIdx int, ok bool) {
	line := lines[startIdx]
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
		return "", "", startIdx, false
	}
	origEq := strings.IndexByte(line, '=')
	if origEq <= 0 {
		return "", "", startIdx, false
	}
	rawKey := strings.TrimSpace(line[:origEq])
	if !isBareTOMLKey(rawKey) {
		return "", "", startIdx, false
	}
	afterEq := line[origEq+1:]
	valTrim := strings.TrimLeft(afterEq, " \t")
	if strings.HasPrefix(valTrim, `"""`) || strings.HasPrefix(valTrim, "'''") {
		delim := valTrim[:3]
		var closed bool
		endIdx, trailingComment, closed = scanMultilineTOMLString(lines, startIdx, origEq+1, delim)
		if !closed {
			return "", "", startIdx, false
		}
		return rawKey, trailingComment, endIdx, true
	}

	depth, commentIdx := scanTOMLValueLine(afterEq, 0)
	endIdx = startIdx
	for depth > 0 && endIdx+1 < len(lines) {
		endIdx++
		depth, _ = scanTOMLValueLine(lines[endIdx], depth)
	}
	if endIdx == startIdx && commentIdx >= 0 {
		// 保留原始赋值行上 '#' 之前的对齐空格与行尾注释。
		trailingComment = sliceTrailingComment(afterEq, commentIdx)
	}
	return rawKey, trailingComment, endIdx, true
}

func sliceTrailingComment(s string, commentIdx int) string {
	prefixEnd := commentIdx
	for prefixEnd > 0 && (s[prefixEnd-1] == ' ' || s[prefixEnd-1] == '\t') {
		prefixEnd--
	}
	return s[prefixEnd:]
}

func scanMultilineTOMLString(lines []string, startIdx, valStart int, delim string) (int, string, bool) {
	openOff := strings.Index(lines[startIdx][valStart:], delim)
	if openOff < 0 {
		return startIdx, "", false
	}
	col := valStart + openOff + len(delim)
	for lineIdx := startIdx; lineIdx < len(lines); lineIdx++ {
		end, closed := findMultilineStringClose(lines[lineIdx], col, delim)
		if closed {
			var comment string
			if lineIdx == startIdx && end < len(lines[lineIdx]) {
				rest := lines[lineIdx][end:]
				if hashIdx := strings.IndexByte(rest, '#'); hashIdx >= 0 {
					comment = sliceTrailingComment(rest, hashIdx)
				}
			}
			return lineIdx, comment, true
		}
		col = 0
	}
	return startIdx, "", false
}

func findMultilineStringClose(line string, startCol int, delim string) (int, bool) {
	quote := delim[0]
	basic := quote == '"'
	escaped := false
	for j := startCol; j < len(line); j++ {
		if basic && escaped {
			escaped = false
			continue
		}
		if basic && line[j] == '\\' {
			escaped = true
			continue
		}
		if strings.HasPrefix(line[j:], delim) {
			end := j + len(delim)
			// TOML 1.0 允许紧邻闭合三引号前额外存在 1～2 个同类引号，需一并消费。
			for extra := 0; extra < 2 && end < len(line) && line[end] == quote; extra++ {
				end++
			}
			return end, true
		}
	}
	return -1, false
}

func leadingWhitespace(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	return line[:i]
}

func isBareTOMLKey(k string) bool {
	if k == "" {
		return false
	}
	for i := range len(k) {
		c := k[i]
		ok := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// scanTOMLValueLine 在跳过字符串字面量的前提下统计方括号嵌套深度，并返回行内注释 '#' 起始偏移（未出现返回 -1）。
func scanTOMLValueLine(line string, depth int) (int, int) {
	inBasic := false
	inLiteral := false
	escaped := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if inBasic {
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inBasic = false
			}
			continue
		}
		if inLiteral {
			if c == '\'' {
				inLiteral = false
			}
			continue
		}
		switch c {
		case '"':
			inBasic = true
		case '\'':
			inLiteral = true
		case '#':
			return depth, i
		case '[':
			depth++
		case ']':
			if depth > 0 {
				depth--
			}
		}
	}
	return depth, -1
}
