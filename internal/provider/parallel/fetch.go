package parallel

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/mdscan"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Fetch 调用 POST /v1/extract 提取目标 URL 的完整 Markdown 正文（支持 JS 渲染与 PDF）。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	if req == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "fetch request must not be nil").WithProvider(ProviderName)
	}
	u, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLength)
	if err != nil {
		return nil, err
	}

	adv := &advancedExtractSettings{
		FullContent: true,
	}
	fp := buildExtractFetchPolicy(ctx, p.fetchTimeout, spec.IsFreshFetchOption(req.Options))
	if fp != nil {
		adv.FetchPolicy = fp
	}

	wireReq := extractWireReq{
		URLs:             []string{u.String()},
		AdvancedSettings: adv,
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + extractEndpointPath,
		Timeout:      p.fetchTimeout,
		AuthHeader:   spec.HeaderAPIKey,
		Body:         wireReq,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp extractWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Parallel extract JSON", err).WithProvider(ProviderName)
	}
	if len(wireResp.Results) == 0 {
		if len(wireResp.Errors) > 0 {
			return nil, classifyExtractItemError(wireResp.Errors[0])
		}
		return nil, core.NewError(core.CodeContentUnavailable, "Parallel extract returned no content").WithProvider(ProviderName)
	}

	item := wireResp.Results[0]
	fullContent := textnorm.CleanPlainText(item.FullContent)
	hasFullContent := fullContent != ""
	content := fullContent
	if content == "" {
		content = joinExcerpts(item.Excerpts)
	}
	if content == "" {
		if len(wireResp.Errors) > 0 {
			return nil, classifyExtractItemError(wireResp.Errors[0])
		}
		return nil, core.NewError(core.CodeContentUnavailable, "Parallel extract returned empty content").WithProvider(ProviderName)
	}

	title := textnorm.CleanInline(item.Title)
	if title == "" {
		title = textnorm.CleanInline(mdscan.FirstH1(content))
	}
	finalURL := core.ValidatedFinalURL(item.URL, u.String(), ProviderName, maxFetchURLLength)

	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        u.String(),
			FinalURL:   finalURL,
			Title:      title,
			Published:  textnorm.ParseFlexibleTime(item.PublishDate, time.UTC),
			Content:    content,
			TotalChars: textnorm.RuneCount(content),
			Provider:   ProviderName,
			SourceType: core.SourceGeneric,
		},
		Cost:      core.CloneCost(p.fetchCost),
		RequestID: textnorm.FirstNonEmpty(wireResp.ExtractID, resp.RequestID),
		Warnings:  filterExtractWarnings(wireResp.Warnings, hasFullContent),
	}, nil
}

// buildExtractFetchPolicy 仅在显式请求 --fresh 时构造 fetch_policy；
// 常规抓取省略该字段以保留服务端默认智能缓存策略，避免只传 timeout_seconds 时触发 max_age_seconds=0 的 600s 强制覆写警告。
func buildExtractFetchPolicy(ctx context.Context, fetchTimeout time.Duration, fresh bool) *fetchPolicy {
	if !fresh {
		return nil
	}
	return &fetchPolicy{
		MaxAgeSeconds:        minFetchPolicyMaxAgeSeconds,
		TimeoutSeconds:       computeExtractTimeoutSeconds(ctx, fetchTimeout),
		DisableCacheFallback: true,
	}
}

func filterExtractWarnings(wireWarnings []wireWarning, hasFullContent bool) []string {
	var out []string
	for _, w := range wireWarnings {
		msg := textnorm.CleanInline(w.Message)
		if msg == "" {
			continue
		}
		// 全文提取模式不传 objective/search_queries 属正常用法，跳过上游针对 excerpts 相关性给出的提示以免刷屏。
		if hasFullContent && strings.Contains(msg, "Neither objective nor search_queries were provided") {
			continue
		}
		out = append(out, "parallel: "+msg)
	}
	return out
}

func computeExtractTimeoutSeconds(ctx context.Context, fetchTimeout time.Duration) int {
	budget := httpx.RemainingBudget(ctx, fetchTimeout, time.Second)
	if budget <= 0 {
		return 0
	}
	sec := int(budget / time.Second)
	return min(max(sec, minFetchPolicyTimeoutSeconds), maxFetchPolicyTimeoutSeconds)
}
