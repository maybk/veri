package local

import (
	"context"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"

	"veri/internal/core"
	"veri/internal/httpx"
)

const (
	acceptPreferMarkdown = "text/markdown, text/html;q=0.9, application/xhtml+xml;q=0.9, text/plain;q=0.8, application/json;q=0.7, */*;q=0.1"
	acceptHTMLOnly       = "text/html, application/xhtml+xml;q=0.9, */*;q=0.1"
	errorPreviewBytes    = 8192
)

type rawFetchPayload struct {
	rawCT      string
	mediaType  string
	body       []byte
	finalURL   *url.URL
	finalURLSt string
}

// Fetch 经 SSRF 安全客户端拉取目标 URL，按 Content-Type 提取 Markdown 并执行质量闸门检查。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	targetURL, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLength)
	if err != nil {
		return nil, err
	}

	release, gateErr := p.gate.Acquire(ctx)
	if gateErr != nil {
		return nil, core.AsError(gateErr).WithProvider(ProviderName)
	}
	defer release()

	if p.respectRobots && p.isDisallowedByRobots(ctx, targetURL) {
		return nil, core.NewError(core.CodeBlockedByPolicy, "blocked by target robots.txt policy").WithProvider(ProviderName)
	}

	reqCtx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	payload, err := p.fetchPayload(reqCtx, targetURL, acceptPreferMarkdown)
	if err != nil {
		return nil, err
	}

	switch {
	case payload.mediaType == "text/html" || payload.mediaType == "application/xhtml+xml" || (payload.mediaType == "" && looksLikeHTML(payload.body)):
		return p.extractHTML(reqCtx, targetURL.String(), payload.finalURL, payload.finalURLSt, payload.rawCT, payload.body)
	case payload.mediaType == "text/markdown" || payload.mediaType == "text/x-markdown":
		return p.extractMarkdown(reqCtx, targetURL, payload)
	case payload.mediaType == "text/plain":
		return p.extractPlainText(reqCtx, targetURL, payload)
	case payload.mediaType == "application/json" || strings.HasSuffix(payload.mediaType, "+json"):
		return p.extractJSON(targetURL, payload)
	default:
		return nil, unsupportedMediaTypeError(payload.mediaType)
	}
}

func (p *Provider) fetchPayload(reqCtx context.Context, targetURL *url.URL, accept string) (*rawFetchPayload, error) {
	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodGet, targetURL.String(), nil)
	if err != nil {
		return nil, core.WrapError(core.CodeInvalidArgument, "failed to create HTTP GET request", err).WithProvider(ProviderName)
	}
	httpReq.Header.Set("User-Agent", p.userAgent)
	httpReq.Header.Set("Accept", accept)
	httpReq.Header.Set("Accept-Language", "en-US,en;q=0.9,zh-CN;q=0.8,zh;q=0.7")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return nil, core.AsError(err).WithProvider(ProviderName)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, errorPreviewBytes))
		return nil, classifyNonOKResponse(resp.StatusCode, resp.Header, preview)
	}

	rawCT := resp.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(rawCT)
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if mediaType != "" && !isSupportedLocalMediaType(mediaType) {
		return nil, unsupportedMediaTypeError(mediaType)
	}

	body, readErr := httpx.ReadBodyLimited(resp, p.maxBytes)
	if readErr != nil {
		return nil, core.AsError(readErr).WithProvider(ProviderName)
	}

	finalURL := targetURL
	if resp.Request != nil && resp.Request.URL != nil {
		finalURL = resp.Request.URL
	}
	finalURLStr := ""
	if finalURL.String() != targetURL.String() {
		finalURLStr = finalURL.String()
	}
	return &rawFetchPayload{
		rawCT:      rawCT,
		mediaType:  mediaType,
		body:       body,
		finalURL:   finalURL,
		finalURLSt: finalURLStr,
	}, nil
}

func isSupportedLocalMediaType(mediaType string) bool {
	switch mediaType {
	case "text/html", "application/xhtml+xml", "text/plain", "text/markdown", "text/x-markdown", "application/json":
		return true
	default:
		return strings.HasSuffix(mediaType, "+json")
	}
}

func unsupportedMediaTypeError(mediaType string) error {
	return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("unsupported media type %q", mediaType)).
		WithProvider(ProviderName).
		WithHint("Local extractor supports HTML, Markdown, plain text, and JSON.")
}
