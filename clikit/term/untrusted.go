package term

import (
	"crypto/rand"
	"regexp"
	"strings"
	"sync"
)

var (
	validTagNameRe      = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	validBoundaryIDRe   = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)
	untrustedRegexCache sync.Map
)

// SanitizeUntrusted 清洗外部文本中伪造的同名开闭标签（含带属性、自闭合或末尾截断形式），防止闭合逃逸。
func SanitizeUntrusted(tagName, content string) string {
	tag := normalizeTagName(tagName)
	re := cachedBreakoutRegex(tag)
	return re.ReplaceAllString(content, "[removed-tag]")
}

// WrapUntrusted 将外部不可信内容清洗后包裹在确定性的 <tagName>...</tagName> 边界标签中。
func WrapUntrusted(tagName, content string) string {
	return WrapUntrustedWithID(tagName, "", content)
}

// WrapUntrustedWithID 将外部不可信内容清洗后包裹在带可选边界标识 id 的标签中。
func WrapUntrustedWithID(tagName, boundaryID, content string) string {
	tag := normalizeTagName(tagName)
	sanitized := strings.TrimSpace(SanitizeUntrusted(tag, content))
	id := strings.TrimSpace(boundaryID)
	if id != "" && validBoundaryIDRe.MatchString(id) {
		return `<` + tag + ` id="` + id + `">` + "\n" + sanitized + "\n</" + tag + ">"
	}
	return "<" + tag + ">\n" + sanitized + "\n</" + tag + ">"
}

// WrapUntrustedRandom 生成调用级随机 8 位边界标识包裹外部不可信内容，防御针对已知静态边界的提示注入。
func WrapUntrustedRandom(tagName, content string) string {
	return WrapUntrustedWithID(tagName, strings.ToLower(rand.Text()[:8]), content)
}

func normalizeTagName(tagName string) string {
	tag := strings.TrimSpace(tagName)
	if tag == "" || !validTagNameRe.MatchString(tag) {
		return "untrusted_content"
	}
	return tag
}

func cachedBreakoutRegex(tag string) *regexp.Regexp {
	key := strings.ToLower(tag)
	if v, ok := untrustedRegexCache.Load(key); ok {
		return v.(*regexp.Regexp)
	}
	re := regexp.MustCompile(`(?i)</?\s*` + regexp.QuoteMeta(tag) + `(?:\b[^>]*>?|/\s*>?|$)`)
	untrustedRegexCache.Store(key, re)
	return re
}
