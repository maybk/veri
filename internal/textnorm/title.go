package textnorm

import (
	"slices"
	"strings"
	"unicode"
)

const (
	jointTitleMinTokens             = 8
	jointTitleMinContain            = 0.85
	jointTitleMinJaccard            = 0.70
	minHeadlineTokensForSuffixStrip = 6
	maxSiteSuffixTokens             = 4

	maxFrontmatterBlockBytes = 1024
	maxCleanTitleRunes       = 220
)

var (
	titleSeparators = []string{" - ", " | ", " — ", " – ", " _ "}

	// 常见 YAML Frontmatter 元数据键（用于识别并切分被上游剥离换行符的单行 frontmatter 脏标题）。
	frontmatterMetaKeys = []string{
		"source:", "author:", "authors:", "date:", "datetime:",
		"published:", "published_at:", "updated:", "updated_at:",
		"canonical_url:", "url:", "importance:", "description:", "summary:",
		"tags:", "categories:", "category:", "slug:", "layout:",
		"lang:", "language:", "draft:", "site:", "keywords:", "subtitle:",
	}
)

// CleanResultTitle 对检索结果标题执行单行清洗；若上游将完整 YAML frontmatter 与正文误灌入标题，则窄口还原其中的 title 字段。
func CleanResultTitle(raw string) string {
	cleaned := CleanText(raw)
	if cleaned == "" {
		return ""
	}
	if extracted, ok := extractFrontmatterTitleFromCleaned(cleaned); ok {
		return extracted
	}
	return collapseFields(cleaned)
}

// CleanResultSummary 清洗检索摘要文本，并窄口剥离上游 .md 镜像页头部泄露的机器 YAML frontmatter 元数据块。
func CleanResultSummary(raw string) string {
	cleaned := CleanText(raw)
	if cleaned == "" {
		return ""
	}
	return StripLeadingFrontmatter(cleaned)
}

// CleanResultSnippet 清洗检索短摘要并折叠为单行，同时窄口剥离头部完整的 YAML frontmatter 元数据块。
func CleanResultSnippet(raw string) string {
	cleaned := CleanText(raw)
	if cleaned == "" {
		return ""
	}
	return collapseFields(StripLeadingFrontmatter(cleaned))
}

// IsCleanResultTitle 判断标题是否干净可读（非空、非重复拼接、非 YAML frontmatter 泄露且未超长灌入正文）。
func IsCleanResultTitle(title string) bool {
	cleaned := CleanInline(title)
	if cleaned == "" {
		return false
	}
	if RuneCount(cleaned) > maxCleanTitleRunes {
		return false
	}
	if IsDuplicatedTitle(cleaned) {
		return false
	}
	if block, _, ok := parseFrontmatterBlock(cleaned); ok && looksLikeFrontmatterMetadata(block) {
		return false
	}
	return true
}

// ExtractFrontmatterTitle 从以 "--- ... ---" 包裹的 YAML frontmatter 脏标题中窄口提取真实 title 字段。
func ExtractFrontmatterTitle(raw string) (string, bool) {
	cleaned := CleanText(raw)
	if cleaned == "" {
		return "", false
	}
	return extractFrontmatterTitleFromCleaned(cleaned)
}

func extractFrontmatterTitleFromCleaned(cleaned string) (string, bool) {
	block, _, ok := parseFrontmatterBlock(cleaned)
	if !ok {
		return "", false
	}
	if strings.Contains(block, "\n") {
		if !firstNonEmptyLineHasYAMLKey(block) {
			return "", false
		}
		for line := range strings.SplitSeq(block, "\n") {
			trimmed := strings.TrimSpace(line)
			if len(trimmed) > 6 && strings.EqualFold(trimmed[:6], "title:") {
				if title := normalizeExtractedTitle(trimmed[6:]); title != "" {
					return title, true
				}
			}
		}
		return "", false
	}

	if !startsWithKnownFrontmatterKey(strings.TrimSpace(block), false) {
		return "", false
	}
	keyIdx := findInlineFrontmatterKey(block, "title:")
	if keyIdx < 0 {
		return "", false
	}
	tail := block[keyIdx+len("title:"):]
	endIdx := len(tail)
	for _, key := range frontmatterMetaKeys {
		if idx := findInlineFrontmatterKey(tail, key); idx > 0 && idx < endIdx {
			endIdx = idx
		}
	}
	if title := normalizeExtractedTitle(tail[:endIdx]); title != "" {
		return title, true
	}
	return "", false
}

// StripLeadingFrontmatter 剥离正文开头的 YAML frontmatter 元数据块，避免站点元信息稀释 Shingle 指纹重合度。
func StripLeadingFrontmatter(raw string) string {
	block, rest, ok := parseFrontmatterBlock(raw)
	if !ok || !looksLikeFrontmatterMetadata(block) {
		return raw
	}
	trimmedRest := strings.TrimSpace(rest)
	if trimmedRest == "" {
		return raw
	}
	return trimmedRest
}

// IsDuplicatedTitle 判断标题是否属于形如 "A - A" 或 "A | A" 的门户重复拼接脏标题。
func IsDuplicatedTitle(title string) bool {
	cleaned := CleanInline(title)
	if cleaned == "" {
		return false
	}
	_, dup := splitDuplicatedTitle(cleaned)
	return dup
}

// NormalizeTitleForDedup 清洗重复拼接与常见站点尾缀，返回用于去重比对的标题 Token 序列。
func NormalizeTitleForDedup(title, site string) []string {
	cleaned := CleanResultTitle(title)
	if cleaned == "" {
		return nil
	}
	if head, dup := splitDuplicatedTitle(cleaned); dup {
		cleaned = head
	}
	cleaned = stripTrailingSiteSuffix(cleaned, CleanInline(site))
	return tokenizeForFingerprint(cleaned, 128)
}

func titlesHighlySimilar(a, b []string) bool {
	if len(a) < jointTitleMinTokens || len(b) < jointTitleMinTokens {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, tok := range a {
		counts[tok]++
	}
	overlap := 0
	for _, tok := range b {
		if n := counts[tok]; n > 0 {
			overlap++
			counts[tok] = n - 1
		}
	}
	minLen := min(len(a), len(b))
	unionLen := len(a) + len(b) - overlap
	containment := float64(overlap) / float64(minLen)
	jaccard := float64(overlap) / float64(unionLen)
	return containment >= jointTitleMinContain && jaccard >= jointTitleMinJaccard
}

func splitDuplicatedTitle(cleaned string) (string, bool) {
	for _, sep := range titleSeparators {
		left, right, ok := strings.Cut(cleaned, sep)
		if !ok {
			continue
		}
		lt := tokenizeForFingerprint(left, 64)
		rt := tokenizeForFingerprint(right, 64)
		if len(lt) >= 4 && slices.Equal(lt, rt) {
			return strings.TrimSpace(left), true
		}
	}
	return cleaned, false
}

func stripTrailingSiteSuffix(title, site string) string {
	siteTokens := tokenizeForFingerprint(site, 16)
	bestIdx := -1
	bestSepLen := 0
	for _, sep := range titleSeparators {
		if idx := strings.LastIndex(title, sep); idx > bestIdx {
			bestIdx = idx
			bestSepLen = len(sep)
		}
	}
	if bestIdx <= 0 {
		return title
	}
	head := strings.TrimSpace(title[:bestIdx])
	tail := strings.TrimSpace(title[bestIdx+bestSepLen:])
	if head == "" || tail == "" {
		return title
	}
	headTokens := tokenizeForFingerprint(head, 128)
	tailTokens := tokenizeForFingerprint(tail, 32)
	if len(headTokens) < minHeadlineTokensForSuffixStrip || len(tailTokens) == 0 {
		return title
	}
	if len(siteTokens) > 0 && slices.Equal(tailTokens, siteTokens) {
		return head
	}
	if len(tailTokens) <= maxSiteSuffixTokens {
		return head
	}
	return title
}

func parseFrontmatterBlock(raw string) (block, rest string, ok bool) {
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "---") || strings.HasPrefix(s, "----") {
		return "", "", false
	}
	afterOpen := s[3:]
	search := afterOpen[:min(len(afterOpen), maxFrontmatterBlockBytes)]
	closeIdx := -1
	if strings.Contains(search, "\n") {
		for offset := 0; offset < len(search); {
			rel := strings.Index(search[offset:], "\n---")
			if rel < 0 {
				break
			}
			cand := offset + rel
			afterClose := cand + 4
			if afterClose == len(afterOpen) || (afterClose < len(afterOpen) && (afterOpen[afterClose] == '\n' || afterOpen[afterClose] == '\r' || afterOpen[afterClose] == ' ' || afterOpen[afterClose] == '\t')) {
				closeIdx = cand
				break
			}
			offset = afterClose
		}
		if closeIdx < 0 {
			return "", "", false
		}
		block = afterOpen[:closeIdx]
		rest = afterOpen[closeIdx+4:]
	} else {
		closeIdx = strings.Index(search, "---")
		if closeIdx <= 0 {
			return "", "", false
		}
		block = afterOpen[:closeIdx]
		rest = afterOpen[closeIdx+3:]
	}
	if !strings.Contains(block, ":") {
		return "", "", false
	}
	return block, rest, true
}

func looksLikeFrontmatterMetadata(block string) bool {
	if strings.Contains(block, "\n") {
		if !firstNonEmptyLineHasYAMLKey(block) {
			return false
		}
		for line := range strings.SplitSeq(block, "\n") {
			if startsWithKnownFrontmatterKey(strings.TrimSpace(line), true) {
				return true
			}
		}
		return false
	}

	// 单行折叠形态须同时满足：块首即已知 YAML 键名 + 至少出现 2 个不同元数据键，彻底杜绝普通散文误剥。
	if !startsWithKnownFrontmatterKey(strings.TrimSpace(block), false) {
		return false
	}
	matchedKeys := 0
	if findInlineFrontmatterKey(block, "title:") >= 0 {
		matchedKeys++
	}
	for _, key := range frontmatterMetaKeys {
		if findInlineFrontmatterKey(block, key) >= 0 {
			matchedKeys++
			if matchedKeys >= 2 {
				return true
			}
		}
	}
	return false
}

func firstNonEmptyLineHasYAMLKey(block string) bool {
	for line := range strings.SplitSeq(block, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		return hasYAMLKeyPrefix(trimmed)
	}
	return false
}

func hasYAMLKeyPrefix(trimmed string) bool {
	colon := strings.IndexByte(trimmed, ':')
	if colon <= 0 {
		return false
	}
	for i := range colon {
		c := trimmed[i]
		isAlpha := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
		isDigitOrDash := (c >= '0' && c <= '9') || c == '-'
		if i == 0 && !isAlpha {
			return false
		}
		if i > 0 && !isAlpha && !isDigitOrDash {
			return false
		}
	}
	return true
}

func startsWithKnownFrontmatterKey(trimmed string, ignoreCase bool) bool {
	if len(trimmed) >= 6 {
		if (ignoreCase && strings.EqualFold(trimmed[:6], "title:")) || (!ignoreCase && strings.HasPrefix(trimmed, "title:")) {
			return true
		}
	}
	for _, key := range frontmatterMetaKeys {
		if len(trimmed) < len(key) {
			continue
		}
		if (ignoreCase && strings.EqualFold(trimmed[:len(key)], key)) || (!ignoreCase && strings.HasPrefix(trimmed, key)) {
			return true
		}
	}
	return false
}

// findInlineFrontmatterKey 在去换行符的 frontmatter 文本中查找独立元数据键；
// 要求键前为起始位置、空白、数字或非 ASCII 字节，避免误切 "resource:"、"open-source:" 等正常英文词。
func findInlineFrontmatterKey(s, key string) int {
	for offset := 0; offset < len(s); {
		rel := strings.Index(s[offset:], key)
		if rel < 0 {
			return -1
		}
		idx := offset + rel
		if idx == 0 || isFrontmatterKeyBoundary(s[idx-1]) {
			return idx
		}
		offset = idx + len(key)
	}
	return -1
}

func isFrontmatterKeyBoundary(prev byte) bool {
	return prev == ' ' || prev == '\t' || prev >= 0x80 || (prev >= '0' && prev <= '9')
}

func normalizeExtractedTitle(raw string) string {
	val := strings.TrimSpace(raw)
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			val = strings.TrimSpace(val[1 : len(val)-1])
		}
	}
	cleaned := collapseFields(val)
	if cleaned == "" || RuneCount(cleaned) > maxCleanTitleRunes {
		return ""
	}
	return cleaned
}

// tokenizeForFingerprint 将标题文本切分为混合粒度 Token：单个 CJK 字符（繁简折叠后）为 1 Token，连续字母/数字串（小写）为 1 Token。
func tokenizeForFingerprint(s string, maxTokens int) []string {
	if s == "" || maxTokens <= 0 {
		return nil
	}
	tokens := make([]string, 0, min(maxTokens, 32))
	var word strings.Builder

	flushWord := func() bool {
		if word.Len() == 0 {
			return len(tokens) < maxTokens
		}
		tokens = append(tokens, word.String())
		word.Reset()
		return len(tokens) < maxTokens
	}

	for _, r := range s {
		if isCJKUnit(r) {
			if !flushWord() {
				break
			}
			tokens = append(tokens, string(foldCJKForFingerprint(r)))
			if len(tokens) >= maxTokens {
				break
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			word.WriteRune(unicode.ToLower(r))
			continue
		}
		if !flushWord() {
			break
		}
	}
	if len(tokens) < maxTokens {
		flushWord()
	}
	return tokens
}
