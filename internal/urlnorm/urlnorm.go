// Package urlnorm 提供 URL 规范化去重键计算、过滤列表归一化与域名匹配工具。
//
// 依赖边界：仅依赖 Go 标准库。Key 仅用于去重索引，绝不改写对外展示的原始 URL。
package urlnorm

import (
	"net"
	"net/url"
	"slices"
	"strings"
)

var trackingParams = map[string]struct{}{
	"spm":          {},
	"spm_id_from":  {},
	"fbclid":       {},
	"gclid":        {},
	"gclsrc":       {},
	"gbraid":       {},
	"wbraid":       {},
	"dclid":        {},
	"msclkid":      {},
	"yclid":        {},
	"ttclid":       {},
	"twclid":       {},
	"igshid":       {},
	"mc_cid":       {},
	"mc_eid":       {},
	"_gl":          {},
	"_hsenc":       {},
	"_hsmi":        {},
	"mkt_tok":      {},
	"share_source": {},
	"vd_source":    {},
}

func isTrackingParam(key string) bool {
	lk := strings.ToLower(strings.TrimSpace(key))
	if strings.HasPrefix(lk, "utm_") {
		return true
	}
	_, drop := trackingParams[lk]
	return drop
}

// Key 计算 URL 的规范化去重键。
func Key(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return trimmed
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return trimmed
	}

	host := strings.ToLower(u.Hostname())
	host = strings.TrimPrefix(host, "www.")
	port := u.Port()
	if (scheme == "http" && port == "80") || (scheme == "https" && port == "443") {
		port = ""
	}
	authority := host
	if port != "" {
		authority = net.JoinHostPort(host, port)
	}

	escapedPath := u.EscapedPath()
	if escapedPath == "" {
		escapedPath = "/"
	} else if len(escapedPath) > 1 {
		escapedPath = strings.TrimRight(escapedPath, "/")
		if escapedPath == "" {
			escapedPath = "/"
		}
	}

	var encodedQuery string
	if u.RawQuery != "" {
		if q, qErr := url.ParseQuery(u.RawQuery); qErr == nil {
			for k := range q {
				if isTrackingParam(k) {
					q.Del(k)
				}
			}
			encodedQuery = q.Encode()
		} else {
			// 非标转义或分号分隔查询串在 ParseQuery 报错时保留原文，防止静默丢参导致异页误去重
			encodedQuery = u.RawQuery
		}
	}

	var b strings.Builder
	b.Grow(len(authority) + len(escapedPath) + len(encodedQuery) + 10)
	b.WriteString("https://")
	b.WriteString(authority)
	b.WriteString(escapedPath)
	if encodedQuery != "" {
		b.WriteByte('?')
		b.WriteString(encodedQuery)
	}
	return b.String()
}

// StripTrackingParams 仅剥离 http/https URL 中的已知营销追踪查询参数，保留原始协议、主机、路径与锚点。
// 若 URL 未包含追踪参数或查询串无法标准解析，零改写原样返回。
func StripTrackingParams(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" || !strings.Contains(trimmed, "?") {
		return rawURL
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" || u.RawQuery == "" {
		return rawURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return rawURL
	}
	q, qErr := url.ParseQuery(u.RawQuery)
	if qErr != nil {
		return rawURL
	}
	removed := false
	for k := range q {
		if isTrackingParam(k) {
			q.Del(k)
			removed = true
		}
	}
	if !removed {
		return rawURL
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// CleanDomainFilters 清洗并去重站点过滤项（剥离空白、协议前缀、通配前缀与末尾斜杠）。
func CleanDomainFilters(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		p := strings.TrimSpace(item)
		if idx := strings.Index(p, "://"); idx >= 0 {
			scheme := strings.ToLower(p[:idx])
			if scheme == "http" || scheme == "https" {
				p = p[idx+3:]
			}
		}
		p = strings.TrimPrefix(p, "*.")
		p = strings.TrimRight(p, "/")
		if p == "" {
			continue
		}
		hostPart, pathPart, hasPath := strings.Cut(p, "/")
		hostPart = strings.ToLower(strings.TrimSpace(hostPart))
		if hostPart == "" {
			continue
		}
		normalized := hostPart
		if hasPath && pathPart != "" {
			normalized = hostPart + "/" + pathPart
		}
		key := strings.ToLower(normalized)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, normalized)
	}
	return out
}

// HasPathDomainFilter 判断站点过滤列表中是否存在带路径前缀（host/path）的条目。
func HasPathDomainFilter(items []string) bool {
	for _, s := range items {
		if strings.Contains(s, "/") {
			return true
		}
	}
	return false
}

// ExtractFilterHosts 从站点过滤列表中提取去重后的纯主机名部分（截断路径前缀，仅适用于白名单召回收敛）。
func ExtractFilterHosts(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		host, _, _ := strings.Cut(strings.TrimSpace(raw), "/")
		host = strings.TrimSpace(host)
		if host != "" && !slices.Contains(out, host) {
			out = append(out, host)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SplitHostAndPathFilters 将站点过滤列表拆分为纯主机名项与含路径项（避免将 host/path 截断为裸主机误杀整站排除）。
func SplitHostAndPathFilters(items []string) (hostOnly, pathOnly []string) {
	for _, raw := range items {
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		if strings.Contains(s, "/") {
			if !slices.Contains(pathOnly, s) {
				pathOnly = append(pathOnly, s)
			}
		} else if !slices.Contains(hostOnly, s) {
			hostOnly = append(hostOnly, s)
		}
	}
	return hostOnly, pathOnly
}

// CleanLangFilters 清洗并去重语言代码列表。
func CleanLangFilters(raw []string) []string {
	if len(raw) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(raw))
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		v := strings.ToLower(strings.TrimSpace(item))
		if v == "" {
			continue
		}
		if _, dup := seen[v]; !dup {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	return out
}

// IsSafeHTTPURL 判断是否为不含用户名密码且包含有效主机的 http / https URL。
func IsSafeHTTPURL(rawURL string) (*url.URL, bool) {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil, false
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.User != nil {
		return nil, false
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, false
	}
	if u.Hostname() == "" {
		return nil, false
	}
	return u, true
}

// MatchesAnyDomain 判断目标 URL 是否命中给定的任一域名过滤项（支持子域名及大小写不敏感路径前缀）。
func MatchesAnyDomain(rawURL string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	u, ok := IsSafeHTTPURL(rawURL)
	if !ok {
		return false
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	urlPathLower := strings.ToLower(u.EscapedPath())
	if urlPathLower == "" {
		urlPathLower = "/"
	}

	for _, pat := range patterns {
		if matchSingleDomainPattern(host, port, urlPathLower, pat) {
			return true
		}
	}
	return false
}

func matchSingleDomainPattern(host, port, urlPathLower, rawPattern string) bool {
	p := strings.ToLower(strings.TrimSpace(rawPattern))
	p = strings.TrimPrefix(p, "https://")
	p = strings.TrimPrefix(p, "http://")
	p = strings.TrimPrefix(p, "*.")
	p = strings.TrimRight(p, "/")
	if p == "" {
		return false
	}

	patAuthority, patPath, hasPath := strings.Cut(p, "/")
	if patAuthority == "" {
		return false
	}
	patHost := patAuthority
	patPort := ""
	if h, prt, err := net.SplitHostPort(patAuthority); err == nil {
		patHost = h
		patPort = prt
	}
	if patPort != "" && patPort != port && !isDefaultPortMatch(patPort, port) {
		return false
	}
	if host != patHost && !strings.HasSuffix(host, "."+patHost) {
		return false
	}
	if !hasPath || patPath == "" {
		return true
	}
	wantPrefix := "/" + patPath
	return urlPathLower == wantPrefix || strings.HasPrefix(urlPathLower, wantPrefix+"/")
}

func isDefaultPortMatch(patPort, port string) bool {
	return port == "" && (patPort == "80" || patPort == "443")
}
