package tavily

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/mdscan"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Fetch 调用 POST /extract，支持按次覆盖或因反爬/JS 盾自动升档 advanced 深度，并同步校验 results 与 failed_results。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	u, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLength)
	if err != nil {
		return nil, err
	}

	effectiveDepth, err := p.resolveExtractDepth(req)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/extract",
		Timeout:      p.fetchTimeout,
		AuthHeader:   spec.HeaderAuthorization,
		AuthPrefix:   spec.AuthBearerPrefix,
		Body: extractWireReq{
			URLs:                 []string{u.String()},
			ExtractDepth:         effectiveDepth,
			Format:               spec.TextFormatMarkdown,
			IncludeFavicon:       true,
			IncludePublishedDate: true,
			IncludeUsage:         true,
			Timeout:              computeExtractTimeoutSec(ctx, p.fetchTimeout),
		},
		ClassifyErr: classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp extractWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Tavily extract JSON", err).WithProvider(ProviderName)
	}

	if len(wireResp.Results) > 0 {
		first := wireResp.Results[0]
		content := textnorm.CleanPlainText(first.RawContent)
		if content != "" {
			title := textnorm.CleanInline(first.Title)
			if title == "" {
				title = textnorm.CleanInline(mdscan.FirstH1(content))
			}
			var fallbackMicros int64
			if p.creditCost != nil {
				if effectiveDepth == spec.TavilyDepthAdvanced {
					fallbackMicros = (p.creditCost.Micros*2 + 2) / 5
				} else {
					fallbackMicros = (p.creditCost.Micros + 2) / 5
				}
			}
			cost := p.costFromUsage(wireResp.Usage, fallbackMicros)
			reqID := textnorm.FirstNonEmpty(wireResp.RequestID, resp.RequestID)
			finalURL := core.ValidatedFinalURL(first.URL, u.String(), ProviderName, maxFetchURLLength)
			return &core.FetchOutcome{
				Page: &core.Page{
					URL:        u.String(),
					FinalURL:   finalURL,
					Title:      title,
					Published:  textnorm.ParseFlexibleTime(first.PublishedDate, time.UTC),
					Content:    content,
					TotalChars: textnorm.RuneCount(content),
					Provider:   ProviderName,
					SourceType: core.SourceGeneric,
				},
				Cost:      cost,
				RequestID: reqID,
			}, nil
		}
	}

	if len(wireResp.FailedResults) > 0 {
		reason := textnorm.CleanInline(wireResp.FailedResults[0].Error)
		if httpx.LooksLikeNotFoundMessage("", reason) {
			return nil, core.NewError(core.CodeNotFound, fmt.Sprintf("Tavily extract failed: %s", reason)).WithProvider(ProviderName).WithStatus(http.StatusNotFound)
		}
		se := core.NewError(core.CodeContentUnavailable, fmt.Sprintf("Tavily extract failed: %s", reason)).WithProvider(ProviderName)
		if httpx.LooksLikeChallengeMessage(reason) {
			se = se.WithHintKey(core.HintKeyChallengeDetected)
		}
		return nil, se
	}
	return nil, core.NewError(core.CodeContentUnavailable, "Tavily extract returned empty content").WithProvider(ProviderName)
}

func (p *Provider) resolveExtractDepth(req *core.FetchRequest) (string, error) {
	if req != nil && len(req.Options) > 0 {
		raw := strings.ToLower(strings.TrimSpace(textnorm.FirstNonEmpty(
			req.Options[spec.OptTavilyExtractDepth],
			req.Options[spec.OptTavilyExtractAlias],
		)))
		if raw != "" {
			switch raw {
			case spec.TavilyDepthBasic, spec.TavilyDepthAdvanced:
				return raw, nil
			default:
				return "", core.NewError(
					core.CodeInvalidArgument,
					fmt.Sprintf("unsupported tavily extract_depth %q (valid: basic, advanced)", raw),
				).WithProvider(ProviderName)
			}
		}
	}
	if req != nil && req.DynamicRenderHint {
		return spec.TavilyDepthAdvanced, nil
	}
	return p.extractDepth, nil
}

func computeExtractTimeoutSec(ctx context.Context, fetchTimeout time.Duration) float64 {
	budget := httpx.RemainingBudget(ctx, fetchTimeout, 400*time.Millisecond)
	if budget <= 0 {
		return 0
	}
	sec := math.Round(budget.Seconds()*10) / 10
	return min(max(sec, 1.0), 60.0)
}
