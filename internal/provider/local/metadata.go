package local

import (
	"strings"
	"time"

	"golang.org/x/net/html"

	"veri/internal/mdscan"
	"veri/internal/textnorm"
)

type markdownDocMeta struct {
	title     string
	author    string
	site      string
	published *time.Time
}

// parseMarkdownDocument 解析并剥离可选的 YAML Frontmatter，若未声明 title 则回退提取首个一级标题（保留正文标题本身）。
func parseMarkdownDocument(raw string) (markdownDocMeta, string) {
	var meta markdownDocMeta
	body := raw

	trimmedStart := strings.TrimLeft(raw, "\ufeff \t\r\n")
	if strings.HasPrefix(trimmedStart, "---\n") {
		rest := trimmedStart[4:]
		limit := min(len(rest), 4096)
		window := rest[:limit]
		endIdx := strings.Index(window, "\n---\n")
		delimLen := 5
		if endIdx < 0 {
			endIdx = strings.Index(window, "\n...\n")
		}
		if endIdx >= 0 {
			fmBlock := window[:endIdx]
			body = strings.TrimSpace(rest[endIdx+delimLen:])
			parseYAMLFrontmatterBlock(fmBlock, &meta)
		}
	}

	if meta.title == "" {
		meta.title = textnorm.CleanInline(mdscan.FirstH1(body))
	}
	return meta, body
}

func parseYAMLFrontmatterBlock(block string, meta *markdownDocMeta) {
	for line := range strings.SplitSeq(block, "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(k))
		val := unquoteYAMLValue(v)
		if val == "" {
			continue
		}
		switch key {
		case "title", "headline":
			if meta.title == "" {
				meta.title = textnorm.CleanInline(val)
			}
		case "author", "byline":
			if meta.author == "" {
				meta.author = textnorm.CleanInline(val)
			}
		case "site", "site_name", "sitename":
			if meta.site == "" {
				meta.site = textnorm.CleanInline(val)
			}
		case "date", "published", "published_at", "pubdate", "created":
			if meta.published == nil {
				meta.published = textnorm.ParseFlexibleTime(val, time.UTC)
			}
		}
	}
}

func unquoteYAMLValue(raw string) string {
	s := strings.TrimSpace(raw)
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return strings.TrimSpace(s[1 : len(s)-1])
		}
	}
	return s
}

// extractFallbackTimeFromDOM 在 Readability 未能解析发布日期时，从 meta 标签或非外链包裹的 <time datetime> 回退提取发布时间。
func extractFallbackTimeFromDOM(root *html.Node) *time.Time {
	if root == nil {
		return nil
	}
	var firstTimeCandidate string
	visited := 0
	stack := []*html.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		visited++
		if visited > maxHTMLNodesToParse {
			break
		}
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "meta":
				key := strings.ToLower(firstDOMAttr(n, "property", "name", "itemprop"))
				switch key {
				case "article:published_time", "og:published_time", "datepublished", "publishdate", "pubdate", "dc.date", "dcterms.created":
					if pub := textnorm.ParseFlexibleTime(firstDOMAttr(n, "content"), time.UTC); pub != nil {
						return pub
					}
				}
			case "time":
				if firstTimeCandidate == "" && !isInsideCardLink(n) {
					if dt := firstDOMAttr(n, "datetime"); dt != "" {
						firstTimeCandidate = dt
					}
				}
			}
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	if firstTimeCandidate != "" {
		return textnorm.ParseFlexibleTime(firstTimeCandidate, time.UTC)
	}
	return nil
}

// isInsideCardLink 排除嵌套在指向其他页面的 <a href="..."> 推荐卡片内的 <time>，防止相关文章日期污染当前页发布时间。
func isInsideCardLink(n *html.Node) bool {
	for cur := n.Parent; cur != nil; cur = cur.Parent {
		if cur.Type == html.ElementNode && strings.EqualFold(cur.Data, "a") {
			href := strings.TrimSpace(firstDOMAttr(cur, "href"))
			if href != "" && !strings.HasPrefix(href, "#") {
				return true
			}
		}
	}
	return false
}

func firstDOMAttr(n *html.Node, keys ...string) string {
	for _, want := range keys {
		for _, a := range n.Attr {
			if strings.EqualFold(a.Key, want) {
				if v := strings.TrimSpace(a.Val); v != "" {
					return v
				}
			}
		}
	}
	return ""
}
