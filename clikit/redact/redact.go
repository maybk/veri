package redact

import (
	"net/url"
	"regexp"
	"slices"
	"strings"
)

var sensitiveHeaders = map[string]struct{}{
	"authorization":        {},
	"proxy-authorization":  {},
	"x-subscription-token": {},
	"x-api-key":            {},
	"api-key":              {},
	"cookie":               {},
	"set-cookie":           {},
}

var sensitiveQueryKeys = map[string]struct{}{
	"key":                  {},
	"api_key":              {},
	"apikey":               {},
	"access_key":           {},
	"token":                {},
	"access_token":         {},
	"refresh_token":        {},
	"private_token":        {},
	"auth_token":           {},
	"auth":                 {},
	"secret":               {},
	"client_secret":        {},
	"password":             {},
	"passwd":               {},
	"pwd":                  {},
	"signature":            {},
	"sig":                  {},
	"x-amz-signature":      {},
	"x-amz-credential":     {},
	"x-amz-security-token": {},
	"x-goog-signature":     {},
	"x-tc-signature":       {},
}

var (
	embeddedURLRe = regexp.MustCompile(`(?i)(?:https?|socks5h?|socks4a?|ftp)://[^\s"'<>]+`)
	bearerTokenRe = regexp.MustCompile(`(?i)((?:Proxy-)?Authorization\s*:\s*Bearer\s+)([A-Za-z0-9._\-+/=]{9,})`)
	basicAuthRe   = regexp.MustCompile(`(?i)((?:Proxy-)?Authorization\s*:\s*Basic\s+)([A-Za-z0-9+/=]{4,})`)
)

// DefaultPrefixes 是常见云端与开发者平台令牌前缀集合，脱敏时保留前缀以辅助排障。
var DefaultPrefixes = []string{"github_pat_", "tvly-", "keen_", "exa_", "ghp_", "gho_", "ghu_", "ghs_", "ghr_", "sk-", "tp-", "fc-"}

// LooksLikeSecret 判断单个无空白字符串是否疑似常见 API Key 或访问令牌，用于防误将凭据当作查询词或路径回显。
func LooksLikeSecret(raw string) bool {
	s := strings.TrimSpace(raw)
	if len(s) < 16 || strings.ContainsAny(s, " \t\r\n") {
		return false
	}
	for _, p := range DefaultPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return strings.HasPrefix(s, "BSA") && len(s) >= 24
}

// MaskKey 使用默认前缀表将密钥脱敏为「前缀 + … + 末 4 位」（如 "sk-…a1b2"）。
// 短密钥统一返回 "****"，"env:VAR" 引用表达式原样保留，空字符串返回 ""。
func MaskKey(raw string) string {
	return MaskKeyWithPrefixes(raw, DefaultPrefixes)
}

// MaskKeyWithPrefixes 使用自定义令牌前缀表将密钥脱敏，保证至少隐藏 6 个字符。
func MaskKeyWithPrefixes(raw string, prefixes []string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(trimmed), "env:") && !strings.ContainsAny(trimmed, ",\r\n") {
		return trimmed
	}
	runes := []rune(trimmed)
	if len(runes) < 12 {
		return "****"
	}
	suffix := string(runes[len(runes)-4:])
	for _, p := range prefixes {
		if p == "" || !strings.HasPrefix(trimmed, p) {
			continue
		}
		pLen := len([]rune(p))
		if len(runes) >= pLen+10 {
			return p + "…" + suffix
		}
		return "****"
	}
	return string(runes[:2]) + "…" + suffix
}

// IsSensitiveHeader 判断 HTTP 头名称是否承载认证凭据或会话 Cookie。
func IsSensitiveHeader(name string) bool {
	_, ok := sensitiveHeaders[strings.ToLower(strings.TrimSpace(name))]
	return ok
}

// SanitizeURL 移除 URL 中的用户名密码，并将敏感查询参数值替换为 [REDACTED]。
// 即使面对缺少协议前缀（如 user:pass@host:8080）或含非法字符无法被 url.Parse 解析的畸形 URL，也会兜底剥离内嵌凭据并清洗查询串。
func SanitizeURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if hasSchemeLessAuthorityAt(trimmed) {
		u, err := url.Parse("//" + trimmed)
		if err != nil {
			return stripMalformedURLUserinfo(trimmed)
		}
		sanitizeParsedURL(u)
		return strings.TrimPrefix(u.String(), "//")
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return stripMalformedURLUserinfo(trimmed)
	}
	sanitizeParsedURL(u)
	return u.String()
}

func hasSchemeLessAuthorityAt(raw string) bool {
	if strings.Contains(raw, "://") || strings.HasPrefix(raw, "//") {
		return false
	}
	if len(raw) >= 7 && strings.EqualFold(raw[:7], "mailto:") {
		return false
	}
	authority := raw
	if cut := strings.IndexAny(raw, "/?#"); cut >= 0 {
		authority = raw[:cut]
	}
	return strings.Contains(authority, "@")
}

func sanitizeParsedURL(u *url.URL) {
	u.User = nil
	if u.RawQuery == "" {
		return
	}
	q := u.Query()
	changed := false
	for k := range q {
		if _, ok := sensitiveQueryKeys[strings.ToLower(k)]; ok {
			q.Set(k, "[REDACTED]")
			changed = true
		}
	}
	if changed {
		u.RawQuery = q.Encode()
	}
}

func stripMalformedURLUserinfo(raw string) string {
	prefix, rest := "", raw
	if scheme, after, ok := strings.Cut(raw, "://"); ok {
		prefix, rest = scheme+"://", after
	} else if after, ok := strings.CutPrefix(raw, "//"); ok {
		prefix, rest = "//", after
	}
	authority, suffix := rest, ""
	if cut := strings.IndexAny(rest, "/?#"); cut >= 0 {
		authority, suffix = rest[:cut], rest[cut:]
	}
	if _, hostPart, hasAt := strings.CutLast(authority, "@"); hasAt {
		authority = hostPart
	}
	if beforeQ, afterQ, hasQ := strings.Cut(suffix, "?"); hasQ && afterQ != "" {
		if _, frag, hasFrag := strings.Cut(afterQ, "#"); hasFrag {
			suffix = beforeQ + "?[REDACTED]#" + frag
		} else {
			suffix = beforeQ + "?[REDACTED]"
		}
	}
	return prefix + authority + suffix
}

// SanitizeTextURLs 扫描任意错误或日志文本中内嵌的 HTTP/HTTPS/SOCKS/FTP URL 并执行凭据与敏感参数清洗。
func SanitizeTextURLs(text string) string {
	if !strings.Contains(text, "://") {
		return text
	}
	return embeddedURLRe.ReplaceAllStringFunc(text, func(match string) string {
		trimmed := strings.TrimRight(match, ".,;:)]}")
		tail := match[len(trimmed):]
		return SanitizeURL(trimmed) + tail
	})
}

// NewScrubber 按密钥长度降序构造可复用的文本脱敏替换器，防止短前缀密钥截断长密钥。
// 跳过短于 6 个字符的弱口令片段，防止把正常英文单词误替换为掩码（URL 内嵌短密码仍由 SanitizeURL 按结构剥离）。
func NewScrubber(secrets []string) *strings.Replacer {
	if len(secrets) == 0 {
		return nil
	}
	cleaned := make([]string, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if len([]rune(s)) < 6 || strings.HasPrefix(strings.ToLower(s), "env:") {
			continue
		}
		if _, exists := seen[s]; !exists {
			seen[s] = struct{}{}
			cleaned = append(cleaned, s)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	slices.SortFunc(cleaned, func(a, b string) int {
		return len(b) - len(a)
	})
	pairs := make([]string, 0, len(cleaned)*2)
	for _, s := range cleaned {
		pairs = append(pairs, s, MaskKey(s))
	}
	return strings.NewReplacer(pairs...)
}

// Scrub 扫描任意文本，遮蔽已知明文密钥、Authorization/Proxy-Authorization 认证头与内嵌敏感 URL 凭据及查询参数。
func Scrub(text string, secrets []string) string {
	return ScrubWith(text, NewScrubber(secrets))
}

// ScrubWith 使用预构建的密钥替换器遮蔽文本中的已知密钥、Authorization/Proxy-Authorization 认证头与敏感 URL 凭据及参数。
func ScrubWith(text string, rep *strings.Replacer) string {
	if text == "" {
		return ""
	}
	out := text
	if rep != nil {
		out = rep.Replace(out)
	}
	if strings.Contains(out, "://") {
		out = SanitizeTextURLs(out)
	}
	if strings.Contains(strings.ToLower(out), "authorization") {
		out = bearerTokenRe.ReplaceAllStringFunc(out, func(m string) string {
			sub := bearerTokenRe.FindStringSubmatch(m)
			if len(sub) != 3 {
				return m
			}
			return sub[1] + MaskKey(sub[2])
		})
		out = basicAuthRe.ReplaceAllString(out, "${1}[REDACTED]")
	}
	return out
}
