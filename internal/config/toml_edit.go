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
			newLine := leadingWhitespace(lines[i]) + field + " = " + literal + comment
			return joinLinesWithNewline(replaceLineSpan(lines, i, endIdx, newLine))
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

// applyUnsetLineEdit 从指定 [section] 移除 field 赋值（含多行数组与多行字符串），并在段内完全为空时连带清理空表头。
func applyUnsetLineEdit(content, section, field string) (string, bool) {
	return applyCleanSecretLineEdit(content, section, field, "")
}

// applyCleanSecretLineEdit 清理指定 [section] 下的键值赋值：
// 当 defaultRef 非空且该行带行尾注释或所在段落包含紧贴段内注释（典型如 config init 参考模板）时，
// 将其原地还原为注释默认引用 `# <field> = "<defaultRef>"`（段内再无其他生效项时将表头一并注释回 `# [section]`）；
// 否则按稀疏配置语义移除该行与空表头。
func applyCleanSecretLineEdit(content, section, field, defaultRef string) (string, bool) {
	lines := splitLinesKeepNoTrailingEmpty(content)
	curSection := ""
	sectionStart := -1

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
		k, trailingComment, endIdx, ok := parseKeyAssignment(lines, i)
		if !ok {
			continue
		}
		if k != field {
			i = endIdx
			continue
		}

		sectionEnd := findSectionEnd(lines, sectionStart)
		restoreComment := defaultRef != "" && (trailingComment != "" || sectionHasBlockComment(lines, sectionStart, sectionEnd))
		if restoreComment {
			indent := leadingWhitespace(lines[i])
			commentedLine := indent + "# " + field + " = " + strconv.Quote(defaultRef) + trailingComment
			lines = replaceLineSpan(lines, i, endIdx, commentedLine)

			if sectionStart >= 0 && !sectionHasActiveAssignment(lines, sectionStart) {
				hdr := lines[sectionStart]
				lines[sectionStart] = leadingWhitespace(hdr) + "# " + strings.TrimLeft(hdr, " \t")
			}
			return joinLinesWithNewline(lines), true
		}

		lines = append(lines[:i], lines[endIdx+1:]...)
		if sectionStart >= 0 && sectionStart < len(lines) {
			lines = pruneEmptySection(lines, sectionStart)
		}
		lines = trimBlankEdgesAndCollapse(lines)
		if len(lines) == 0 {
			return "", true
		}
		return joinLinesWithNewline(lines), true
	}
	return content, false
}

func findSectionEnd(lines []string, sectionStart int) int {
	if sectionStart < 0 {
		return len(lines)
	}
	for i := sectionStart + 1; i < len(lines); i++ {
		if _, ok := parseSectionHeader(lines[i]); ok {
			return i
		}
	}
	return len(lines)
}

// sectionHasBlockComment 判断当前 section 紧贴块内（遇到首个空行之前）是否包含 '#' 注释行，
// 避免将空行之后属于下一个 section 的前置注释误判为本段注释。
func sectionHasBlockComment(lines []string, sectionStart, sectionEnd int) bool {
	if sectionStart < 0 {
		return false
	}
	for i := sectionStart + 1; i < sectionEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			return false
		}
		if strings.HasPrefix(trimmed, "#") {
			return true
		}
	}
	return false
}

func sectionHasActiveAssignment(lines []string, sectionStart int) bool {
	sectionEnd := findSectionEnd(lines, sectionStart)
	for i := sectionStart + 1; i < sectionEnd; i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return true
	}
	return false
}

func pruneEmptySection(lines []string, sectionStart int) []string {
	sectionEnd := findSectionEnd(lines, sectionStart)
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

func replaceLineSpan(lines []string, startIdx, endIdx int, replacement string) []string {
	out := make([]string, 0, len(lines)-(endIdx-startIdx))
	out = append(out, lines[:startIdx]...)
	out = append(out, replacement)
	out = append(out, lines[endIdx+1:]...)
	return out
}
