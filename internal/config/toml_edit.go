package config

import (
	"fmt"
	"strconv"
	"strings"
)

func formatTOMLLiteral(vt ValueType, val any) string {
	switch vt {
	case TypeString:
		if s, ok := val.(string); ok {
			return strconv.Quote(s)
		}
		return strconv.Quote(fmt.Sprint(val))
	case TypeBool:
		if b, ok := val.(bool); ok {
			return strconv.FormatBool(b)
		}
		return strconv.Quote(fmt.Sprint(val))
	case TypeInt:
		if n, ok := val.(int); ok {
			return strconv.Itoa(n)
		}
		return strconv.Quote(fmt.Sprint(val))
	case TypeFloat:
		f, ok := val.(float64)
		if !ok {
			return strconv.Quote(fmt.Sprint(val))
		}
		s := strconv.FormatFloat(f, 'f', -1, 64)
		if !strings.ContainsAny(s, ".eE") {
			s += ".0"
		}
		return s
	case TypeDuration:
		switch v := val.(type) {
		case Duration:
			return strconv.Quote(v.String())
		case string:
			return strconv.Quote(v)
		default:
			return strconv.Quote(fmt.Sprint(val))
		}
	case TypeByteSize:
		switch v := val.(type) {
		case ByteSize:
			raw, _ := v.MarshalText()
			return strconv.Quote(string(raw))
		case string:
			return strconv.Quote(v)
		default:
			return strconv.Quote(fmt.Sprint(val))
		}
	case TypeStringSlice:
		items, _ := val.([]string)
		if len(items) == 0 {
			return "[]"
		}
		quoted := make([]string, len(items))
		for i, item := range items {
			quoted[i] = strconv.Quote(item)
		}
		return "[" + strings.Join(quoted, ", ") + "]"
	default:
		return strconv.Quote(fmt.Sprint(val))
	}
}

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

// applySetLineEdit 在保留注释、空白与既有排版的前提下，对指定 [section] 下的 field 执行原地替换、段末追加或文件末新建。
func applySetLineEdit(content, section, field, literal string) string {
	if strings.TrimSpace(content) == "" {
		return "[" + section + "]\n" + field + " = " + literal + "\n"
	}
	lines := splitLinesKeepNoTrailingEmpty(content)
	curSection := ""
	sectionStart := -1
	sectionEnd := -1
	lastAssignEnd := -1
	sectionIndent := ""

	for i := 0; i < len(lines); i++ {
		if sec, ok := parseSectionHeader(lines[i]); ok {
			if curSection == section && sectionEnd < 0 {
				sectionEnd = i
			}
			curSection = sec
			if sec == section && sectionStart < 0 {
				sectionStart = i
			}
			continue
		}
		if curSection != section {
			continue
		}
		if k, comment, endIdx, ok := parseKeyAssignment(lines, i); ok && k == field {
			newLine := leadingWhitespace(lines[i]) + field + " = " + literal
			if comment != "" {
				newLine += comment
			}
			out := make([]string, 0, len(lines)-(endIdx-i))
			out = append(out, lines[:i]...)
			out = append(out, newLine)
			out = append(out, lines[endIdx+1:]...)
			return joinLinesWithNewline(out)
		} else if ok {
			sectionIndent = leadingWhitespace(lines[i])
			lastAssignEnd = endIdx
			i = endIdx
		}
	}

	if sectionStart >= 0 {
		if sectionEnd < 0 {
			sectionEnd = len(lines)
		}
		insertAt := sectionStart + 1
		if lastAssignEnd >= 0 {
			insertAt = lastAssignEnd + 1
		} else {
			// 空段仅跳过紧贴表头的段内注释，绝不越过空行拆散下一个 section 的前置注释。
			for insertAt < sectionEnd && strings.HasPrefix(strings.TrimSpace(lines[insertAt]), "#") {
				insertAt++
			}
		}
		out := make([]string, 0, len(lines)+1)
		out = append(out, lines[:insertAt]...)
		out = append(out, sectionIndent+field+" = "+literal)
		out = append(out, lines[insertAt:]...)
		return joinLinesWithNewline(out)
	}

	// 目标 section 不存在，在文件末尾以空行分隔干净追加。
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > 0 {
		lines = append(lines, "")
	}
	lines = append(lines, "["+section+"]", field+" = "+literal)
	return joinLinesWithNewline(lines)
}

// applyUnsetLineEdit 从指定 [section] 移除 field 赋值（含多行数组），并在段内完全为空时连带清理空表头。
func applyUnsetLineEdit(content, section, field string) (string, bool) {
	lines := splitLinesKeepNoTrailingEmpty(content)
	curSection := ""
	sectionStart := -1
	removed := false

	for i := 0; i < len(lines); i++ {
		if sec, ok := parseSectionHeader(lines[i]); ok {
			curSection = sec
			if sec == section {
				sectionStart = i
			}
			continue
		}
		if curSection != section {
			continue
		}
		if k, _, endIdx, ok := parseKeyAssignment(lines, i); ok {
			if k == field {
				lines = append(lines[:i], lines[endIdx+1:]...)
				removed = true
				break
			}
			i = endIdx
		}
	}
	if !removed {
		return content, false
	}

	if sectionStart >= 0 && sectionStart < len(lines) {
		lines = pruneEmptySection(lines, sectionStart)
	}
	lines = trimBlankEdgesAndCollapse(lines)
	if len(lines) == 0 {
		return "", true
	}
	return joinLinesWithNewline(lines), true
}

func pruneEmptySection(lines []string, sectionStart int) []string {
	sectionEnd := len(lines)
	for i := sectionStart + 1; i < len(lines); i++ {
		if _, ok := parseSectionHeader(lines[i]); ok {
			sectionEnd = i
			break
		}
	}
	// 段内若仍有任何赋值行，或存在紧贴表头的段内注释，则保留表头。
	seenBlank := false
	for i := sectionStart + 1; i < sectionEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			seenBlank = true
			continue
		}
		if !strings.HasPrefix(trimmed, "#") || !seenBlank {
			return lines
		}
	}
	// 仅移除空表头行本身，保留空行之后可能属于下一个 section 的前置注释。
	return append(lines[:sectionStart], lines[sectionStart+1:]...)
}

func splitLinesKeepNoTrailingEmpty(content string) []string {
	trimmed := strings.TrimSuffix(content, "\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

func joinLinesWithNewline(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func trimBlankEdgesAndCollapse(lines []string) []string {
	out := make([]string, 0, len(lines))
	prevBlank := true
	for _, line := range lines {
		blank := strings.TrimSpace(line) == ""
		if blank && prevBlank {
			continue
		}
		out = append(out, line)
		prevBlank = blank
	}
	for len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
		out = out[:len(out)-1]
	}
	return out
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
	trimmed := strings.TrimSpace(lines[startIdx])
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
		return "", "", startIdx, false
	}
	eqIdx := strings.IndexByte(trimmed, '=')
	if eqIdx <= 0 {
		return "", "", startIdx, false
	}
	rawKey := strings.TrimSpace(trimmed[:eqIdx])
	if !isBareTOMLKey(rawKey) {
		return "", "", startIdx, false
	}
	valPart := trimmed[eqIdx+1:]
	depth, commentIdx := scanTOMLValueLine(valPart, 0)
	endIdx = startIdx
	for depth > 0 && endIdx+1 < len(lines) {
		endIdx++
		depth, _ = scanTOMLValueLine(lines[endIdx], depth)
	}
	if endIdx == startIdx && commentIdx >= 0 {
		// 保留原始赋值行上 '#' 之前的对齐空格与行尾注释。
		origEq := strings.IndexByte(lines[startIdx], '=')
		if origEq >= 0 {
			afterEq := lines[startIdx][origEq+1:]
			_, origCommentIdx := scanTOMLValueLine(afterEq, 0)
			if origCommentIdx >= 0 {
				prefixEnd := origCommentIdx
				for prefixEnd > 0 && (afterEq[prefixEnd-1] == ' ' || afterEq[prefixEnd-1] == '\t') {
					prefixEnd--
				}
				trailingComment = afterEq[prefixEnd:]
			}
		}
	}
	return rawKey, trailingComment, endIdx, true
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
