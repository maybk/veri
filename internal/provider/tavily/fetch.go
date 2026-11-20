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
			return &core.FetchOutcome{
				Page: &core.Page{
					URL:        u.String(),
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
		if isNotFoundExtractReason(reason) {
			return nil, core.NewError(core.CodeNotFound, fmt.Sprintf("Tavily extract failed: %s", reason)).WithProvider(ProviderName).WithStatus(http.StatusNotFound)
		}
		return nil, core.NewError(core.CodeContentUnavailable, fmt.Sprintf("Tavily extract failed: %s", reason)).WithProvider(ProviderName)
	}
	return nil, core.NewError(core.CodeContentUnavailable, "Tavily extract returned empty content").WithProvider(ProviderName)
}

func isNotFoundExtractReason(reason string) bool {
	lower := strings.ToLower(strings.TrimSpace(reason))
	if lower == "" {
		return false
	}
	for _, guard := range []string{"captcha", "challenge", "cloudflare", "forbidden", "403", "bot"} {
		if strings.Contains(lower, guard) {
			return false
		}
	}
	return strings.Contains(lower, "404") ||
		strings.Contains(lower, "410") ||
		strings.Contains(lower, "not found") ||
		strings.Contains(lower, "does not exist")
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
	budget := fetchTimeout
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem > 0 && (budget <= 0 || rem < budget) {
			budget = rem
		}
	}
	if budget <= 0 {
		return 0
	}
	if budget > 1400*time.Millisecond {
		budget -= 400 * time.Millisecond
	}
	sec := math.Round(budget.Seconds()*10) / 10
	if sec < 1.0 {
		sec = 1.0
	} else if sec > 60.0 {
		sec = 60.0
	}
	return sec
}
