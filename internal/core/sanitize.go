package core

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"veri/internal/spec"
	"veri/internal/textnorm"
)

// ValidateTargetURL 校验抓取请求的目标 URL（仅允许 http/https、含有效主机、无用户名密码、不超过长度上限）。
func ValidateTargetURL(rawURL, providerName string, maxLen int) (*url.URL, error) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, NewError(CodeInvalidArgument, "URL must not be empty").WithProvider(providerName)
	}
	if maxLen > 0 && len(trimmed) > maxLen {
		return nil, NewError(CodeInvalidArgument, fmt.Sprintf("URL length %d exceeds maximum %d", len(trimmed), maxLen)).
			WithProvider(providerName)
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.User != nil || u.Hostname() == "" {
		return nil, NewError(CodeInvalidArgument, "URL must use http or https with a valid host and without credentials").
			WithProvider(providerName)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, NewError(CodeInvalidArgument, fmt.Sprintf("unsupported URL scheme %q: only http and https are allowed", u.Scheme)).
			WithProvider(providerName)
	}
	return u, nil
}

// ValidatedFinalURL 校验上游返回的重定向终态 URL；为空、与原请求相同或不满足安全 http/https 边界时返回空字符串。
func ValidatedFinalURL(rawFinal, requestURL, providerName string, maxLen int) string {
	trimmed := strings.TrimSpace(rawFinal)
	if trimmed == "" || trimmed == requestURL {
		return ""
	}
	if _, err := ValidateTargetURL(trimmed, providerName, maxLen); err != nil {
		return ""
	}
	return trimmed
}

// FallbackTitleFromURL 从 URL 路径末段或主机名推导可读兜底标题，供裸源码、纯文本及无标题网页统一使用。
func FallbackTitleFromURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return ""
	}
	rawPath := strings.Trim(u.EscapedPath(), "/")
	if rawPath != "" {
		for _, seg := range slices.Backward(strings.Split(rawPath, "/")) {
			if seg == "" {
				continue
			}
			decoded, uErr := url.PathUnescape(seg)
			if uErr != nil {
				decoded = seg
			}
			if cleaned := textnorm.CleanInline(decoded); cleaned != "" {
				return cleaned
			}
		}
	}
	return textnorm.CleanInline(u.Hostname())
}

// AlignResultDetail 按请求的 Detail 粒度执行字段回退补齐与多余字段裁剪。
func AlignResultDetail(r *Result, detail Detail, keepExtra bool) {
	r.Title = textnorm.CleanResultTitle(r.Title)
	if r.Title == "" && r.URL != "" {
		r.Title = FallbackTitleFromURL(r.URL)
	}
	r.Site = textnorm.CleanInline(r.Site)
	r.Authors = cleanAuthors(r.Authors)
	r.Snippet = textnorm.CleanResultSnippet(r.Snippet)
	r.Summary = textnorm.CleanResultSummary(r.Summary)
	r.Content = textnorm.CleanPlainText(r.Content)
	if r.Published != nil {
		r.Published = new(r.Published.UTC())
	}

	switch detail {
	case DetailSnippet:
		if r.Snippet == "" && r.Summary != "" {
			r.Snippet = textnorm.TruncateRunes(textnorm.CleanInline(r.Summary), spec.DefaultSnippetMaxRunes)
		}
		r.Summary = ""
		r.Content = ""
	case DetailContent:
		if r.Summary == "" && r.Snippet != "" {
			r.Summary = r.Snippet
		}
		if r.Snippet == "" && r.Summary != "" {
			r.Snippet = textnorm.TruncateRunes(textnorm.CleanInline(r.Summary), spec.DefaultSnippetMaxRunes)
		}
	default:
		if r.Summary == "" && r.Snippet != "" {
			r.Summary = r.Snippet
		}
		if r.Snippet == "" && r.Summary != "" {
			r.Snippet = textnorm.TruncateRunes(textnorm.CleanInline(r.Summary), spec.DefaultSnippetMaxRunes)
		}
		r.Content = ""
	}

	if !keepExtra {
		r.Icon = ""
		r.Extra = nil
	} else {
		icon := textnorm.CleanInline(r.Icon)
		if icon != "" {
			if _, err := ValidateTargetURL(icon, "", 0); err != nil {
				icon = ""
			}
		}
		r.Icon = icon
		if len(r.Extra) == 0 {
			r.Extra = nil
		}
	}
}

func cleanAuthors(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, a := range raw {
		c := textnorm.CleanInline(a)
		if c == "" {
			continue
		}
		k := strings.ToLower(c)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SanitizeResults 统一执行检索结果的 http/https 安全过滤、同源去重、隐形字符清洗与 Detail 对齐。
func SanitizeResults(providerName string, items []Result, detail Detail, keepExtra bool, keyFn func(string) string) ([]Result, []string) {
	if len(items) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(items))
	out := make([]Result, 0, len(items))
	var warnings []string

	for i, item := range items {
		if _, err := ValidateTargetURL(item.URL, providerName, 0); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: dropped result #%d with invalid or unsafe URL", providerName, i+1))
			continue
		}
		k := item.URL
		if keyFn != nil {
			k = keyFn(item.URL)
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}

		r := item
		AlignResultDetail(&r, detail, keepExtra)
		r.Providers = []string{providerName}
		r.Rank = len(out) + 1
		out = append(out, r)
	}
	return out, warnings
}
