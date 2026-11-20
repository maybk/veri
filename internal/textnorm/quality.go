package textnorm

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"veri/internal/mdscan"
)

// strongWallPhrases 收录标题或短正文中出现即可确认的反爬盾页、PoW 校验与 SPA JS 空壳特征词（小写匹配）。
var strongWallPhrases = []string{
	"just a moment...",
	"attention required!",
	"checking your browser",
	"performing security verification",
	"verify you are human",
	"making sure you're not a bot",
	"vercel security checkpoint",
	"please enable javascript",
	"you need to enable javascript",
	"enable javascript and cookies",
	"javascript is required",
	"cf-browser-verification",
	"ddos-guard",
	"安全验证",
	"人机验证",
	"滑动验证",
	"请开启 javascript",
	"请求被拦截",
}

// shortWallPhrases 收录仅在短页面（去代码块后 < 1200 字符）下判定为拦截页的上下文敏感词，避免误伤讨论 HTTP 状态码的长技术文章。
var shortWallPhrases = []string{
	"access denied",
	"403 forbidden",
	"human verification",
	"cloudflare ray id",
	"enable cookies to continue",
	"pardon our interruption",
	"验证码",
	"访问受限",
}

// 页面质量闸门低质量原因分类常量。
const (
	QualityReasonBotOrJSWall    = "bot_or_js_wall"
	QualityReasonSoft404        = "soft_404"
	QualityReasonTooShort       = "too_short"
	QualityReasonLinkHeavyShell = "link_heavy_shell"
)

// PageQuality 描述网页标题与提取后 Markdown 正文的质量评估结论。
type PageQuality struct {
	LowQuality bool
	Reason     string // QualityReasonBotOrJSWall | QualityReasonSoft404 | QualityReasonTooShort | QualityReasonLinkHeavyShell
	IsWall     bool   // 命中反爬盾页或软 404 空壳时置 true（路由保底阶段绝不将其当有效正文返回）
}

// DetectChallengeWall 检测页面是否为反爬盾、PoW 验证、JS 空壳或软 404 错误页（供本地与云端提取结果共用）。
func DetectChallengeWall(title, markdown string) PageQuality {
	lowerTitle := strings.ToLower(strings.TrimSpace(title))
	for _, phrase := range strongWallPhrases {
		if strings.Contains(lowerTitle, phrase) {
			return PageQuality{LowQuality: true, Reason: QualityReasonBotOrJSWall, IsWall: true}
		}
	}

	proseOutsideCode := stripFencedCodeBlocks(markdown)
	nonCodeRunes := utf8.RuneCountInString(proseOutsideCode)

	if nonCodeRunes < 1200 {
		for _, phrase := range shortWallPhrases {
			if strings.Contains(lowerTitle, phrase) {
				return PageQuality{LowQuality: true, Reason: QualityReasonBotOrJSWall, IsWall: true}
			}
		}
		lowerBody := strings.ToLower(proseOutsideCode)
		for _, phrase := range strongWallPhrases {
			if strings.Contains(lowerBody, phrase) {
				return PageQuality{LowQuality: true, Reason: QualityReasonBotOrJSWall, IsWall: true}
			}
		}
		for _, phrase := range shortWallPhrases {
			if strings.Contains(lowerBody, phrase) {
				return PageQuality{LowQuality: true, Reason: QualityReasonBotOrJSWall, IsWall: true}
			}
		}
	}

	if nonCodeRunes < 400 && isSoft404Title(lowerTitle) {
		return PageQuality{LowQuality: true, Reason: QualityReasonSoft404, IsWall: true}
	}

	return PageQuality{}
}

// EvaluatePageQuality 对 HTML 转换后的标题与 Markdown 正文执行完整三级质量闸门检测。
func EvaluatePageQuality(title, markdown string, minContentRunes int) PageQuality {
	if wall := DetectChallengeWall(title, markdown); wall.LowQuality {
		return wall
	}

	scan := analyzeMarkdownContent(markdown)
	nonSpaceRunes := countNonSpaceRunes(scan.plainWithoutURLs)

	if minContentRunes > 0 && nonSpaceRunes < minContentRunes {
		structuredFloor := min(120, minContentRunes)
		if nonSpaceRunes >= structuredFloor &&
			(scan.hasCodeBlock || scan.hasTable) &&
			(strings.TrimSpace(title) != "" || scan.hasHeading) {
			return PageQuality{}
		}
		return PageQuality{LowQuality: true, Reason: QualityReasonTooShort}
	}

	if scan.linkCount > 0 && nonSpaceRunes > 0 {
		linkRatio := float64(scan.linkTextRunes) / float64(nonSpaceRunes)
		if linkRatio > 0.75 && !hasNaturalParagraph(scan.proseOnly, 80) {
			return PageQuality{LowQuality: true, Reason: QualityReasonLinkHeavyShell}
		}
	}

	return PageQuality{}
}

func isSoft404Title(lowerTitle string) bool {
	if lowerTitle == "" {
		return false
	}
	switch lowerTitle {
	case "404", "not found", "404 error", "error 404":
		return true
	}
	for _, prefix := range []string{
		"404 not found",
		"404 - ",
		"404 | ",
		"404:",
		"page not found",
		"页面不存在",
		"404 页面不存在",
		"找不到页面",
	} {
		if strings.HasPrefix(lowerTitle, prefix) {
			return true
		}
	}
	return false
}

type mdContentScan struct {
	plainWithoutURLs string
	proseOnly        string
	linkTextRunes    int
	linkCount        int
	hasCodeBlock     bool
	hasTable         bool
	hasHeading       bool
}

func analyzeMarkdownContent(markdown string) mdContentScan {
	var plainBuf strings.Builder
	var proseBuf strings.Builder
	plainBuf.Grow(len(markdown))
	proseBuf.Grow(len(markdown))

	var out mdContentScan
	var fence mdscan.Tracker
	codeBodyNonSpace := 0

	firstLine := true
	for line := range strings.SplitSeq(markdown, "\n") {
		if !firstLine {
			plainBuf.WriteByte('\n')
			if !fence.InFence() {
				proseBuf.WriteByte('\n')
			}
		}
		firstLine = false

		switch fence.Next(line) {
		case mdscan.KindFenceOpen:
			codeBodyNonSpace = 0
			continue
		case mdscan.KindFenceBody:
			plainBuf.WriteString(line)
			codeBodyNonSpace += countNonSpaceRunes(line)
			continue
		case mdscan.KindFenceClose:
			if codeBodyNonSpace > 0 {
				out.hasCodeBlock = true
			}
			continue
		}

		trimmedLeft := strings.TrimLeft(line, " \t")
		if _, _, ok := mdscan.ParseATXHeading(trimmedLeft, mdscan.MaxHeadingLevel); ok {
			out.hasHeading = true
		}
		if isGFMTableDelimiterLine(trimmedLeft) {
			out.hasTable = true
		}

		scanInlineLinks(line, &plainBuf, &proseBuf, &out)
	}

	out.plainWithoutURLs = plainBuf.String()
	out.proseOnly = proseBuf.String()
	return out
}

func stripFencedCodeBlocks(markdown string) string {
	if !strings.Contains(markdown, "```") && !strings.Contains(markdown, "~~~") {
		return markdown
	}
	var b strings.Builder
	b.Grow(len(markdown))
	var fence mdscan.Tracker

	for line := range strings.SplitSeq(markdown, "\n") {
		if fence.Next(line) != mdscan.KindText {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.String()
}

func isGFMTableDelimiterLine(trimmed string) bool {
	if !strings.Contains(trimmed, "|") || !strings.Contains(trimmed, "---") {
		return false
	}
	for _, r := range trimmed {
		switch r {
		case '|', '-', ':', ' ', '\t':
		default:
			return false
		}
	}
	return true
}

// scanInlineLinks 单遍线性扫描单行 Markdown，区分普通链接 [label](url) 与图片 ![alt](url)，兼容尖括号及平衡括号 URL。
func scanInlineLinks(line string, plainBuf, proseBuf *strings.Builder, out *mdContentScan) {
	n := len(line)
	for i := 0; i < n; {
		isImage := line[i] == '!' && i+1 < n && line[i+1] == '['
		if isImage || line[i] == '[' {
			bracketStart := i
			if isImage {
				bracketStart = i + 1
			}
			labelEnd := findClosingBracket(line, bracketStart+1)
			if labelEnd > bracketStart && labelEnd+1 < n && line[labelEnd+1] == '(' {
				if destEnd, ok := findClosingLinkParen(line, labelEnd+2); ok {
					label := line[bracketStart+1 : labelEnd]
					plainBuf.WriteString(label)
					if !isImage {
						out.linkCount++
						out.linkTextRunes += countNonSpaceRunes(label)
					}
					i = destEnd + 1
					continue
				}
			}
		}
		plainBuf.WriteByte(line[i])
		proseBuf.WriteByte(line[i])
		i++
	}
}

func findClosingBracket(s string, pos int) int {
	depth := 1
	for i := pos; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func findClosingLinkParen(s string, pos int) (int, bool) {
	for pos < len(s) && (s[pos] == ' ' || s[pos] == '\t') {
		pos++
	}
	if pos >= len(s) {
		return -1, false
	}
	if s[pos] == '<' {
		closeAngle := strings.IndexByte(s[pos+1:], '>')
		if closeAngle < 0 {
			return -1, false
		}
		after := pos + 1 + closeAngle + 1
		for after < len(s) && s[after] != ')' {
			after++
		}
		if after < len(s) && s[after] == ')' {
			return after, true
		}
		return -1, false
	}
	depth := 1
	for i := pos; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return -1, false
}

func countNonSpaceRunes(s string) int {
	n := 0
	for _, r := range s {
		if !unicode.IsSpace(r) {
			n++
		}
	}
	return n
}

func hasNaturalParagraph(text string, minRunes int) bool {
	for block := range strings.SplitSeq(text, "\n\n") {
		trimmed := strings.TrimSpace(block)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "- ") || strings.HasPrefix(trimmed, "* ") {
			continue
		}
		if countNonSpaceRunes(trimmed) >= minRunes {
			return true
		}
	}
	return false
}
