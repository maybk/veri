package keenable

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Fetch 调用 GET /v1/fetch 提取目标网页干净 Markdown；固定拉取完整文档上限供路由层执行章节与分页裁剪。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	if req == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "fetch request must not be nil").WithProvider(ProviderName)
	}
	u, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLength)
	if err != nil {
		return nil, err
	}

	params := url.Values{}
	params.Set("url", u.String())
	params.Set("max_chars", strconv.Itoa(maxFetchChars))
	if spec.IsFreshFetchOption(req.Options) {
		params.Set("live", "true")
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodGet,
		URL:          p.baseURL + "/v1/fetch?" + params.Encode(),
		Timeout:      p.fetchTimeout,
		AuthHeader:   spec.HeaderAPIKey,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp fetchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Keenable fetch JSON", err).WithProvider(ProviderName)
	}

	content := textnorm.CleanPlainText(wireResp.Content)
	if content == "" {
		return nil, core.NewError(core.CodeContentUnavailable, "Keenable fetch returned empty content").WithProvider(ProviderName)
	}

	finalURL := core.ValidatedFinalURL(wireResp.URL, u.String(), ProviderName, maxFetchURLLength)

	charCount := textnorm.RuneCount(content)
	var warnings []string
	if wireResp.Truncated {
		if wireResp.TotalChars > charCount {
			warnings = append(warnings, fmt.Sprintf("keenable: upstream truncated content at %d chars (full page: %d chars)", charCount, wireResp.TotalChars))
		} else {
			warnings = append(warnings, fmt.Sprintf("keenable: upstream truncated content at %d chars", charCount))
		}
	}

	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        u.String(),
			FinalURL:   finalURL,
			Title:      textnorm.CleanInline(wireResp.Title),
			Author:     textnorm.CleanInline(wireResp.Author),
			Published:  parseFlexiblePublished(wireResp.PublishedAt),
			Content:    content,
			TotalChars: charCount,
			Provider:   ProviderName,
			SourceType: core.SourceGeneric,
		},
		Cost:      p.costFromUsage(wireResp.Usage),
		RequestID: resp.RequestID,
		Warnings:  warnings,
	}, nil
}
