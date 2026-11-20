package local

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"veri/internal/httpx"
)

type robotsRule struct {
	pattern string
	allow   bool
}

type robotsGroup struct {
	agents []string
	rules  []robotsRule
}

func (p *Provider) isDisallowedByRobots(ctx context.Context, targetURL *url.URL) bool {
	robotsURL := fmt.Sprintf("%s://%s/robots.txt", targetURL.Scheme, targetURL.Host)
	rCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(rCtx, http.MethodGet, robotsURL, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", p.userAgent)
	resp, err := p.client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := httpx.ReadBodyLimited(resp, 128<<10)
	if err != nil || resp.StatusCode != http.StatusOK {
		return false
	}
	return isPathDisallowedByRobots(string(data), p.userAgent, targetPathWithQuery(targetURL))
}

func targetPathWithQuery(u *url.URL) string {
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		return path + "?" + u.RawQuery
	}
	return path
}

// isPathDisallowedByRobots 按 RFC 9309 解析多 User-Agent 组、专属优先与最长规则匹配。
func isPathDisallowedByRobots(robotsTxt, userAgent, target string) bool {
	groups := parseRobotsGroups(robotsTxt)
	if len(groups) == 0 {
		return false
	}
	uaLower := strings.ToLower(userAgent)

	var specificRules []robotsRule
	var wildcardRules []robotsRule
	bestAgentLen := 0

	for _, g := range groups {
		for _, agent := range g.agents {
			a := strings.ToLower(strings.TrimSpace(agent))
			if a == "" {
				continue
			}
			if a == "*" {
				wildcardRules = append(wildcardRules, g.rules...)
				continue
			}
			if idx := strings.IndexByte(a, '/'); idx > 0 {
				a = a[:idx]
			}
			if strings.Contains(uaLower, a) {
				if len(a) > bestAgentLen {
					bestAgentLen = len(a)
					specificRules = slices.Clone(g.rules)
				} else if len(a) == bestAgentLen {
					specificRules = append(specificRules, g.rules...)
				}
			}
		}
	}

	activeRules := wildcardRules
	if bestAgentLen > 0 {
		activeRules = specificRules
	}
	if len(activeRules) == 0 {
		return false
	}

	bestMatchLen := -1
	bestAllow := true
	for _, rule := range activeRules {
		if rule.pattern == "" {
			continue
		}
		if robotsPatternMatches(target, rule.pattern) {
			patLen := len(rule.pattern)
			if patLen > bestMatchLen || (patLen == bestMatchLen && rule.allow) {
				bestMatchLen = patLen
				bestAllow = rule.allow
			}
		}
	}
	return bestMatchLen >= 0 && !bestAllow
}

func parseRobotsGroups(robotsTxt string) []robotsGroup {
	var groups []robotsGroup
	var cur robotsGroup
	seenRuleInCur := false

	for line := range strings.SplitSeq(robotsTxt, "\n") {
		if before, _, ok := strings.Cut(line, "#"); ok {
			line = before
		}
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		switch k {
		case "user-agent":
			if seenRuleInCur {
				groups = append(groups, cur)
				cur = robotsGroup{}
				seenRuleInCur = false
			}
			cur.agents = append(cur.agents, v)
		case "allow":
			if len(cur.agents) > 0 {
				seenRuleInCur = true
				cur.rules = append(cur.rules, robotsRule{pattern: v, allow: true})
			}
		case "disallow":
			if len(cur.agents) > 0 {
				seenRuleInCur = true
				cur.rules = append(cur.rules, robotsRule{pattern: v, allow: false})
			}
		}
	}
	if len(cur.agents) > 0 {
		groups = append(groups, cur)
	}
	return groups
}

func robotsPatternMatches(target, pattern string) bool {
	anchoredEnd := strings.HasSuffix(pattern, "$")
	if anchoredEnd {
		pattern = strings.TrimSuffix(pattern, "$")
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		if anchoredEnd {
			return target == pattern
		}
		return strings.HasPrefix(target, pattern)
	}
	if !strings.HasPrefix(target, parts[0]) {
		return false
	}
	rest := target[len(parts[0]):]
	for i := 1; i < len(parts); i++ {
		seg := parts[i]
		if seg == "" {
			continue
		}
		if i == len(parts)-1 && anchoredEnd {
			return strings.HasSuffix(rest, seg)
		}
		idx := strings.Index(rest, seg)
		if idx < 0 {
			return false
		}
		rest = rest[idx+len(seg):]
	}
	if anchoredEnd && parts[len(parts)-1] == "" {
		return true
	}
	return true
}
