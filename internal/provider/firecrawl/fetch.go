package firecrawl

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/mdscan"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Fetch 调用 POST /v2/scrape 提取目标页 Markdown 正文。
// 强制固定 formats=["markdown"]、控制 PDF 最大页数上限，并在 HTTP 200 下执行 data.metadata.statusCode 双层状态检查。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	if req == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "fetch request must not be nil").WithProvider(ProviderName)
	}
	u, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLength)
	if err != nil {
		return nil, err
	}

	proxyMode, err := p.resolveProxy(req)
	if err != nil {
		return nil, err
	}
	maxPages, err := p.resolveMaxPDFPages(req)
	if err != nil {
		return nil, err
	}

	wireReq := scrapeWireReq{
		URL:             u.String(),
		Formats:         []string{spec.TextFormatMarkdown},
		OnlyMainContent: true,
		Proxy:           proxyMode,
		Parsers: []any{
			pdfParserSpec{Type: "pdf", Mode: "auto", MaxPages: maxPages},
		},
		Timeout: computeScrapeTimeoutMS(ctx, p.fetchTimeout),
	}
	if spec.IsFreshFetchOption(req.Options) {
		wireReq.MaxAge = new(0)
	}

	authHeader, authPrefix := p.authSpec()
	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/v2/scrape",
		Timeout:      p.fetchTimeout,
		AuthHeader:   authHeader,
		AuthPrefix:   authPrefix,
		Body:         wireReq,
		InspectBody:  inspectBody,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp scrapeWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Firecrawl scrape JSON", err).WithProvider(ProviderName)
	}
	if !wireResp.Success || wireResp.Data == nil {
		return nil, classifyError(resp.StatusCode, resp.Headers, resp.Body)
	}

	// 双层状态检查：即使 Firecrawl 外层返回 HTTP 200 + success=true，也必须核验目标站 metadata.statusCode。
	if meta := wireResp.Data.Metadata; meta != nil && meta.StatusCode != 0 {
		if innerErr := checkInnerStatus(meta.StatusCode, meta.Error); innerErr != nil {
			return nil, innerErr
		}
	}

	content := textnorm.CleanPlainText(wireResp.Data.Markdown)
	if content == "" {
		return nil, core.NewError(core.CodeContentUnavailable, "Firecrawl scrape returned empty markdown").WithProvider(ProviderName)
	}

	var title, finalURL string
	var warnings []string
	credits := int64(1)
	if meta := wireResp.Data.Metadata; meta != nil {
		title = firstFlexString(meta.Title)
		finalURL = core.ValidatedFinalURL(meta.URL, u.String(), ProviderName, maxFetchURLLength)
		if meta.NumPages > 0 {
			credits = int64(meta.NumPages)
			if meta.TotalPages > meta.NumPages {
				warnings = append(warnings, fmt.Sprintf("firecrawl: PDF truncated at %d of %d pages (firecrawl.max_pages)", meta.NumPages, meta.TotalPages))
			}
		}
	}
	if title == "" {
		title = textnorm.CleanInline(mdscan.FirstH1(content))
	}

	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        u.String(),
			FinalURL:   finalURL,
			Title:      title,
			Content:    content,
			TotalChars: textnorm.RuneCount(content),
			Provider:   ProviderName,
			SourceType: core.SourceGeneric,
		},
		Cost:      p.estimateCreditsCost(credits),
		RequestID: resp.RequestID,
		Warnings:  warnings,
	}, nil
}

func checkInnerStatus(statusCode int, metaErr string) *core.Error {
	if (statusCode >= 200 && statusCode < 300) || statusCode == http.StatusNotModified {
		return nil
	}
	detail := textnorm.CleanInline(metaErr)
	if detail == "" {
		detail = fmt.Sprintf("target page returned HTTP %d", statusCode)
	}
	switch statusCode {
	case http.StatusNotFound, http.StatusGone:
		return core.NewError(core.CodeNotFound, detail).WithProvider(ProviderName).WithStatus(statusCode)
	case http.StatusForbidden, http.StatusTooManyRequests:
		return core.NewError(core.CodeContentUnavailable, detail).
			WithProvider(ProviderName).
			WithStatus(statusCode).
			WithHintKey(core.HintKeyChallengeDetected)
	default:
		// 内层 statusCode 反映的是目标站自身响应（含 5xx），映射为 content_unavailable 以触发链式回退而不误熔断 Firecrawl 服务本身。
		return core.NewError(core.CodeContentUnavailable, detail).WithProvider(ProviderName).WithStatus(statusCode)
	}
}

func (p *Provider) resolveProxy(req *core.FetchRequest) (string, error) {
	if len(req.Options) > 0 {
		if raw := textnorm.FirstNonEmpty(req.Options[spec.OptFirecrawlProxy], req.Options[spec.OptFirecrawlProxyAlias]); raw != "" {
			return normalizeProxy(raw)
		}
	}
	if req.DynamicRenderHint && p.proxy == spec.FirecrawlProxyBasic {
		return spec.FirecrawlProxyAuto, nil
	}
	return p.proxy, nil
}

func (p *Provider) resolveMaxPDFPages(req *core.FetchRequest) (int, error) {
	if len(req.Options) > 0 {
		if raw := strings.TrimSpace(textnorm.FirstNonEmpty(req.Options[spec.OptFirecrawlMaxPages], req.Options[spec.OptFirecrawlMaxPagesAlias])); raw != "" {
			v, err := strconv.Atoi(raw)
			if err != nil || v < 1 || v > spec.MaxFirecrawlPDFPages {
				return 0, core.NewError(
					core.CodeInvalidArgument,
					fmt.Sprintf("invalid firecrawl.max_pages %q (expected integer in [1, %d])", raw, spec.MaxFirecrawlPDFPages),
				).WithProvider(ProviderName)
			}
			return v, nil
		}
	}
	return p.maxPDFPages, nil
}

func computeScrapeTimeoutMS(ctx context.Context, fetchTimeout time.Duration) int {
	budget := httpx.RemainingBudget(ctx, fetchTimeout, time.Second)
	if budget <= 0 {
		return 0
	}
	return min(max(int(budget.Milliseconds()), 1000), 120000)
}
