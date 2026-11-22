package textnorm

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	fingerprintShingleSize   = 4
	maxFingerprintTokens     = 600
	maxFingerprintInputBytes = 32 * 1024
	maxInlineLinkLabelBytes  = 512
	maxInlineLinkDestBytes   = 2048

	fnvOffset64 uint64 = 14695981039346656037
	fnvPrime64  uint64 = 1099511628211

	// 正文强命中阈值（覆盖改写标题的同稿转载）
	bodyOnlyMinShingles = 28
	bodyOnlyMinOverlap  = 28
	bodyOnlyMinJaccard  = 0.52
	bodyOnlyMinContain  = 0.78

	// 标题 + 正文联合命中阈值（覆盖短摘要与前缀差异，绝不仅凭标题误杀异文）
	jointBodyMinShingles = 12
	jointBodyMinOverlap  = 10
	jointBodyMinContain  = 0.45
)

// TextFingerprint 表示短文本（检索摘要/正文）经标点清洗与混合分词后的 4-token Shingle 哈希指纹。
type TextFingerprint struct {
	shingles map[uint64]struct{}
}

// Matchable 报告正文指纹是否达到近重复判定的最低信息量门槛。
func (f TextFingerprint) Matchable() bool {
	return len(f.shingles) >= jointBodyMinShingles
}

// BuildTextFingerprint 剥离头部 YAML frontmatter 与 Markdown 链接目标，流式提取中英混合 4-token Shingle 哈希集合。
func BuildTextFingerprint(text string) TextFingerprint {
	bounded := trimUTF8Bytes(text, maxFingerprintInputBytes)
	bounded = StripLeadingFrontmatter(bounded)
	plain := stripMarkdownLinkDestinations(bounded)
	if plain == "" {
		return TextFingerprint{}
	}

	var (
		shingles map[uint64]struct{}
		window   [fingerprintShingleSize]uint64
		count    int
	)
	emitToken := func(tokHash uint64) bool {
		window[count%fingerprintShingleSize] = tokHash
		count++
		if count >= fingerprintShingleSize {
			if shingles == nil {
				shingles = make(map[uint64]struct{}, min(len(plain)/3+1, maxFingerprintTokens))
			}
			base := count % fingerprintShingleSize
			shingles[hashShingle4(
				window[base],
				window[(base+1)%fingerprintShingleSize],
				window[(base+2)%fingerprintShingleSize],
				window[(base+3)%fingerprintShingleSize],
			)] = struct{}{}
		}
		return count < maxFingerprintTokens
	}

	var (
		wordHash = fnvOffset64
		inWord   bool
		runeBuf  [utf8.UTFMax]byte
	)
	flushWord := func() bool {
		if !inWord {
			return count < maxFingerprintTokens
		}
		h := wordHash
		wordHash = fnvOffset64
		inWord = false
		return emitToken(h)
	}

	for _, r := range plain {
		if isCJKUnit(r) {
			if !flushWord() {
				break
			}
			n := utf8.EncodeRune(runeBuf[:], foldCJKForFingerprint(r))
			if !emitToken(fnvHashBytes(fnvOffset64, runeBuf[:n])) {
				break
			}
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			n := utf8.EncodeRune(runeBuf[:], unicode.ToLower(r))
			wordHash = fnvHashBytes(wordHash, runeBuf[:n])
			inWord = true
			continue
		}
		if !flushWord() {
			break
		}
	}
	if count < maxFingerprintTokens {
		flushWord()
	}
	return TextFingerprint{shingles: shingles}
}

// FingerprintsNearDuplicate 使用双闸门判定两份检索结果是否为跨链同稿转载：
//  1. 正文强命中（要求足够长正文与高 4-gram 重合，可捕获改标题转载）；
//  2. 标题 + 正文联合命中（要求长标题高一致且正文实质重合，绝不误杀同名短标题或同主题独立报道）。
func FingerprintsNearDuplicate(titleA []string, bodyA TextFingerprint, titleB []string, bodyB TextFingerprint) bool {
	lenA, lenB := len(bodyA.shingles), len(bodyB.shingles)
	if lenA < jointBodyMinShingles || lenB < jointBodyMinShingles {
		return false
	}

	overlap := countShingleOverlap(bodyA.shingles, bodyB.shingles)
	if overlap < jointBodyMinOverlap {
		return false
	}

	minLen := min(lenA, lenB)
	unionLen := lenA + lenB - overlap
	containment := float64(overlap) / float64(minLen)
	jaccard := float64(overlap) / float64(unionLen)

	// 闸门 1：正文强命中
	if minLen >= bodyOnlyMinShingles && overlap >= bodyOnlyMinOverlap {
		if jaccard >= bodyOnlyMinJaccard || containment >= bodyOnlyMinContain {
			return true
		}
	}

	// 闸门 2：长标题一致 + 正文实质重合
	if containment >= jointBodyMinContain && titlesHighlySimilar(titleA, titleB) {
		return true
	}
	return false
}

func countShingleOverlap(a, b map[uint64]struct{}) int {
	if len(a) > len(b) {
		a, b = b, a
	}
	overlap := 0
	for h := range a {
		if _, ok := b[h]; ok {
			overlap++
		}
	}
	return overlap
}

// stripMarkdownLinkDestinations 将行内 Markdown 链接 [label](url) 还原为纯文本 label；
// 对括号查找施加单链接长度上界，保证最坏情况下仍为严格线性 O(N) 扫描。
func stripMarkdownLinkDestinations(raw string) string {
	if !strings.Contains(raw, "](") {
		return raw
	}
	var b strings.Builder
	b.Grow(len(raw))
	n := len(raw)
	for i := 0; i < n; {
		isImg := raw[i] == '!' && i+1 < n && raw[i+1] == '['
		if isImg || raw[i] == '[' {
			bs := i
			if isImg {
				bs = i + 1
			}
			labelLimit := min(n, bs+1+maxInlineLinkLabelBytes)
			labelEnd := findClosingBracket(raw[:labelLimit], bs+1)
			if labelEnd > bs && labelEnd+1 < n && raw[labelEnd+1] == '(' {
				destLimit := min(n, labelEnd+2+maxInlineLinkDestBytes)
				if destEnd, ok := findClosingLinkParen(raw[:destLimit], labelEnd+2); ok {
					b.WriteString(raw[bs+1 : labelEnd])
					i = destEnd + 1
					continue
				}
			}
		}
		b.WriteByte(raw[i])
		i++
	}
	return b.String()
}

// trimUTF8Bytes 在不超过 maxBytes 字节的前提下，回退至合法 UTF-8 字符起始边界截断，避免切碎多字节码点。
func trimUTF8Bytes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

func isCJKUnit(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func hashShingle4(t0, t1, t2, t3 uint64) uint64 {
	h := fnvOffset64
	h = fnvAddUint64(h, t0)
	h = fnvAddUint64(h, t1)
	h = fnvAddUint64(h, t2)
	h = fnvAddUint64(h, t3)
	return h
}

func fnvHashBytes(h uint64, p []byte) uint64 {
	for _, b := range p {
		h ^= uint64(b)
		h *= fnvPrime64
	}
	return h
}

func fnvAddUint64(h, v uint64) uint64 {
	for range 8 {
		h ^= v & 0xff
		h *= fnvPrime64
		v >>= 8
	}
	return h
}
