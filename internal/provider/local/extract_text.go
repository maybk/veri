package local

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"io"
	"math"
	"net/url"
	"strings"

	"golang.org/x/net/html/charset"

	"veri/internal/core"
	"veri/internal/mdscan"
	"veri/internal/textnorm"
)

func (p *Provider) extractMarkdown(reqCtx context.Context, targetURL *url.URL, payload *rawFetchPayload) (*core.FetchOutcome, error) {
	utf8Raw := decodeToUTF8(payload.body, payload.rawCT, expandedUTF8Limit(p.maxBytes))
	cleaned := textnorm.CleanPlainText(string(utf8Raw))
	if cleaned == "" {
		return nil, core.NewError(core.CodeContentUnavailable, "page returned empty markdown body").WithProvider(ProviderName)
	}
	return p.buildMarkdownOutcome(reqCtx, targetURL, payload, cleaned)
}

func (p *Provider) extractPlainText(reqCtx context.Context, targetURL *url.URL, payload *rawFetchPayload) (*core.FetchOutcome, error) {
	utf8Raw := decodeToUTF8(payload.body, payload.rawCT, expandedUTF8Limit(p.maxBytes))
	text := textnorm.CleanPlainText(string(utf8Raw))
	if text == "" {
		return nil, core.NewError(core.CodeContentUnavailable, "page returned empty text body").WithProvider(ProviderName)
	}
	// 部分站点（如 react.dev、llms.txt）响应 Accept: text/markdown 时仍标注 text/plain；保守嗅探 Markdown 并解析 Frontmatter/H1 标题
	if looksLikeMarkdownDocument(text) {
		return p.buildMarkdownOutcome(reqCtx, targetURL, payload, text)
	}
	qr := textnorm.EvaluatePageQuality("", text, p.minContentRunes)
	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        targetURL.String(),
			FinalURL:   payload.finalURLSt,
			Content:    text,
			TotalChars: textnorm.RuneCount(text),
			Provider:   ProviderName,
			SourceType: core.SourceGeneric,
		},
		LowQuality: qr.LowQuality,
		QualityMsg: qr.Reason,
	}, nil
}

func (p *Provider) buildMarkdownOutcome(reqCtx context.Context, targetURL *url.URL, payload *rawFetchPayload, cleaned string) (*core.FetchOutcome, error) {
	mdMeta, bodyText := parseMarkdownDocument(cleaned)
	if bodyText == "" {
		bodyText = cleaned
	}
	qr := textnorm.EvaluatePageQuality(mdMeta.title, bodyText, p.minContentRunes)
	mdOut := &core.FetchOutcome{
		Page: &core.Page{
			URL:        targetURL.String(),
			FinalURL:   payload.finalURLSt,
			Title:      mdMeta.title,
			Author:     mdMeta.author,
			Site:       mdMeta.site,
			Published:  mdMeta.published,
			Content:    bodyText,
			TotalChars: textnorm.RuneCount(bodyText),
			Provider:   ProviderName,
			SourceType: core.SourceGeneric,
		},
		LowQuality: qr.LowQuality,
		QualityMsg: qr.Reason,
	}
	// 若站点的 Markdown 协商端点返回残缺壳子，原地降级请求一次同源 HTML 提取以救回完整正文
	if qr.LowQuality && reqCtx.Err() == nil {
		if htmlPayload, hErr := p.fetchPayload(reqCtx, targetURL, acceptHTMLOnly); hErr == nil &&
			(htmlPayload.mediaType == "text/html" || htmlPayload.mediaType == "application/xhtml+xml") {
			if htmlOut, exErr := p.extractHTML(reqCtx, targetURL.String(), htmlPayload.finalURL, htmlPayload.finalURLSt, htmlPayload.rawCT, htmlPayload.body); exErr == nil && htmlOut != nil && htmlOut.Page != nil {
				if !htmlOut.LowQuality || htmlOut.Page.TotalChars > mdOut.Page.TotalChars {
					return htmlOut, nil
				}
			}
		}
	}
	return mdOut, nil
}

func (p *Provider) extractJSON(targetURL *url.URL, payload *rawFetchPayload) (*core.FetchOutcome, error) {
	var formatted string
	if indented, fmtErr := jsontext.AppendFormat(nil, payload.body, jsontext.WithIndent("  ")); fmtErr == nil {
		formatted = "```json\n" + textnorm.CleanPlainText(string(indented)) + "\n```"
	} else {
		formatted = "```json\n" + textnorm.CleanPlainText(string(payload.body)) + "\n```"
	}
	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        targetURL.String(),
			FinalURL:   payload.finalURLSt,
			Content:    formatted,
			TotalChars: textnorm.RuneCount(formatted),
			Provider:   ProviderName,
			SourceType: core.SourceGeneric,
		},
	}, nil
}

// looksLikeMarkdownDocument 保守识别被标注为 text/plain 的 Markdown 文档：
// 仅在文档开头包含闭合的 YAML Frontmatter 或首个非空行为 ATX 标题时返回 true，避免误伤普通源码与日志文本。
func looksLikeMarkdownDocument(cleaned string) bool {
	trimmedStart := strings.TrimLeft(cleaned, "\ufeff \t\r\n")
	if trimmedStart == "" {
		return false
	}
	if strings.HasPrefix(trimmedStart, "---\n") {
		rest := trimmedStart[4:]
		window := rest[:min(len(rest), 4096)]
		if strings.Contains(window, "\n---\n") || strings.Contains(window, "\n...\n") {
			return true
		}
	}
	firstLine, _, _ := strings.Cut(trimmedStart, "\n")
	_, _, isHeading := mdscan.ParseATXHeading(firstLine, mdscan.MaxHeadingLevel)
	return isHeading
}

func expandedUTF8Limit(maxBytes int64) int64 {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	if maxBytes > math.MaxInt64/4 {
		return math.MaxInt64
	}
	return maxBytes * 4
}

func decodeToUTF8(body []byte, rawContentType string, maxOutBytes int64) []byte {
	r, err := charset.NewReader(bytes.NewReader(body), rawContentType)
	if err != nil {
		return body
	}
	decoded, readErr := io.ReadAll(io.LimitReader(r, maxOutBytes))
	if readErr != nil || len(decoded) == 0 {
		return body
	}
	return decoded
}
