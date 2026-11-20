package local

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	readability "codeberg.org/readeck/go-readability/v2"
	"golang.org/x/net/html"

	"veri/internal/core"
	"veri/internal/htmlmd"
	"veri/internal/mdscan"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

const maxHTMLNodesToParse = htmlmd.DefaultMaxNodes

type htmlDocMeta struct {
	title        string
	noscript     string
	baseURL      *url.URL
	tooManyNodes bool
}

func (p *Provider) extractHTML(ctx context.Context, origURL string, pageURL *url.URL, finalURLStr, rawContentType string, body []byte) (outcome *core.FetchOutcome, retErr error) {
	defer func() {
		if r := recover(); r != nil {
			outcome = nil
			retErr = core.NewError(core.CodeContentUnavailable, fmt.Sprintf("HTML parser recovered from panic: %v", r)).WithProvider(ProviderName)
		}
	}()

	if err := ctx.Err(); err != nil {
		return nil, core.WrapError(core.CodeUpstreamTimeout, "HTML extraction canceled", err).WithProvider(ProviderName)
	}
	utf8Raw := decodeToUTF8(body, rawContentType, expandedUTF8Limit(p.maxBytes))

	meta := htmlDocMeta{baseURL: pageURL}
	doc, parseErr := html.Parse(bytes.NewReader(utf8Raw))
	if parseErr == nil {
		meta = inspectHTMLDocument(doc, pageURL, maxHTMLNodesToParse)
	}
	if err := ctx.Err(); err != nil {
		return nil, core.WrapError(core.CodeUpstreamTimeout, "HTML extraction canceled", err).WithProvider(ProviderName)
	}

	var mdText string
	var title, author, site string
	var pub *time.Time
	var fallbackTime *time.Time
	if parseErr == nil {
		fallbackTime = extractFallbackTimeFromDOM(doc)
	}
	effectiveBase := meta.baseURL
	renderOpts := htmlmd.Options{
		BaseURL:     effectiveBase,
		CleanURL:    urlnorm.StripTrackingParams,
		SkipPrepare: true,
	}

	var fallbackRoot *html.Node
	if !meta.tooManyNodes {
		if parseErr == nil {
			// 先执行现代 DOM 预处理，并克隆主语义容器快照（防止 Readability 原地改写树后无法恢复代码块/表格页）
			htmlmd.Prepare(doc)
			if cand := htmlmd.FindMainContentCandidate(doc); cand != nil {
				fallbackRoot = htmlmd.CloneSubtree(cand, maxHTMLNodesToParse)
			}
		}

		ps := readability.NewParser()
		ps.MaxElemsToParse = maxHTMLNodesToParse
		if p.minContentRunes > 0 {
			ps.CharThresholds = max(p.minContentRunes, 80)
		}
		var article readability.Article
		var readErr error
		if parseErr == nil {
			article, readErr = ps.ParseAndMutate(doc, effectiveBase)
		} else {
			article, readErr = ps.Parse(bytes.NewReader(utf8Raw), effectiveBase)
		}
		if readErr == nil && article.Node != nil {
			title = textnorm.CleanInline(article.Title())
			author = textnorm.CleanInline(article.Byline())
			site = textnorm.CleanInline(article.SiteName())
			if t, tErr := article.PublishedTime(); tErr == nil && !t.IsZero() {
				utc := t.UTC()
				pub = &utc
			}
			mdText = textnorm.CleanPlainText(htmlmd.RenderNode(article.Node, renderOpts))
		}
	}

	if pub == nil && fallbackTime != nil {
		pub = fallbackTime
	}

	if err := ctx.Err(); err != nil {
		return nil, core.WrapError(core.CodeUpstreamTimeout, "HTML extraction canceled", err).WithProvider(ProviderName)
	}

	// 当 Readability 提取为空，或将纯表格/多代码块 API 文档误裁至低于阈值一半时，回退至已预处理的语义容器快照
	if !meta.tooManyNodes && (mdText == "" || (fallbackRoot != nil && textnorm.RuneCount(mdText) < max(p.minContentRunes/2, 80))) {
		var candMD string
		if fallbackRoot != nil {
			candMD = textnorm.CleanPlainText(htmlmd.RenderNode(fallbackRoot, renderOpts))
		} else if convStr, convErr := htmlmd.ConvertBytes(utf8Raw, htmlmd.Options{BaseURL: effectiveBase, CleanURL: urlnorm.StripTrackingParams}); convErr == nil {
			candMD = textnorm.CleanPlainText(convStr)
		}
		if textnorm.RuneCount(candMD) > textnorm.RuneCount(mdText) {
			mdText = candMD
		}
	}

	if mdText == "" && strings.TrimSpace(meta.noscript) != "" {
		mdText = textnorm.CleanText(meta.noscript)
	}
	if title == "" {
		title = textnorm.CleanInline(meta.title)
	}
	if title == "" {
		title = textnorm.CleanInline(mdscan.FirstH1(mdText))
	}

	if mdText == "" {
		if meta.tooManyNodes {
			return nil, core.NewError(core.CodeContentUnavailable, fmt.Sprintf("HTML DOM exceeds %d node safety limit", maxHTMLNodesToParse)).WithProvider(ProviderName)
		}
		return nil, core.NewError(core.CodeContentUnavailable, "extracted HTML content is empty").WithProvider(ProviderName)
	}

	qr := textnorm.EvaluatePageQuality(title, mdText, p.minContentRunes)
	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        origURL,
			FinalURL:   finalURLStr,
			Title:      title,
			Author:     author,
			Site:       site,
			Published:  pub,
			Content:    mdText,
			TotalChars: textnorm.RuneCount(mdText),
			Provider:   ProviderName,
			SourceType: core.SourceGeneric,
		},
		LowQuality: qr.LowQuality,
		QualityMsg: qr.Reason,
	}, nil
}

// inspectHTMLDocument 单遍显式栈遍历 DOM，同时完成节点上限守卫与 title / base / noscript 提取（防止超深嵌套栈溢出与重复扫描）。
func inspectHTMLDocument(root *html.Node, pageURL *url.URL, limit int) htmlDocMeta {
	out := htmlDocMeta{baseURL: pageURL}
	if root == nil {
		return out
	}
	var rawBaseHref string
	count := 0
	stack := []*html.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		count++
		if count > limit {
			out.tooManyNodes = true
			return out
		}
		if n.Type == html.ElementNode {
			switch {
			case out.title == "" && strings.EqualFold(n.Data, "title"):
				out.title = collectSubtreeText(n)
			case rawBaseHref == "" && strings.EqualFold(n.Data, "base"):
				for _, a := range n.Attr {
					if strings.EqualFold(a.Key, "href") {
						if v := strings.TrimSpace(a.Val); v != "" {
							rawBaseHref = v
							break
						}
					}
				}
			case out.noscript == "" && strings.EqualFold(n.Data, "noscript"):
				out.noscript = collectSubtreeText(n)
			}
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	if rawBaseHref != "" && pageURL != nil {
		if ref, err := url.Parse(rawBaseHref); err == nil {
			if resolved := pageURL.ResolveReference(ref); resolved.Scheme == "http" || resolved.Scheme == "https" {
				out.baseURL = resolved
			}
		}
	}
	return out
}

func collectSubtreeText(root *html.Node) string {
	var b strings.Builder
	stack := []*html.Node{root}
	for len(stack) > 0 {
		n := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		for c := n.LastChild; c != nil; c = c.PrevSibling {
			stack = append(stack, c)
		}
	}
	return b.String()
}

func looksLikeHTML(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return false
	}
	prefix := strings.ToLower(string(trimmed[:min(len(trimmed), 64)]))
	return strings.HasPrefix(prefix, "<!doctype html") || strings.HasPrefix(prefix, "<html")
}
