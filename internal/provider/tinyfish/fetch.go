package tinyfish

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
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Fetch 调用 POST https://api.fetch.tinyfish.ai（服务端自动执行 JS 渲染提取 Markdown，支持 ttl 与 per_url_timeout_ms 透传）。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	u, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLength)
	if err != nil {
		return nil, err
	}

	ttlPtr, err := resolveFetchTTL(req.Options)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.fetchGate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.fetchURL,
		Timeout:      p.fetchTimeout,
		AuthHeader:   spec.HeaderAPIKey,
		Body: fetchWireReq{
			URLs:            []string{u.String()},
			Format:          spec.TextFormatMarkdown,
			TTL:             ttlPtr,
			PerURLTimeoutMS: computePerURLTimeoutMS(ctx, p.fetchTimeout),
		},
		ClassifyErr: classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp fetchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode TinyFish fetch JSON", err).WithProvider(ProviderName)
	}

	// 外层 HTTP 200 时必须同时校验 results 与 errors[]
	if len(wireResp.Results) > 0 {
		first := wireResp.Results[0]
		content := textnorm.CleanPlainText(first.Text)
		if content != "" {
			var title string
			if first.Title != nil {
				title = textnorm.CleanInline(*first.Title)
			}
			var pub *time.Time
			if first.PublishedDate != nil {
				pub = textnorm.ParseFlexibleTime(*first.PublishedDate, time.UTC)
			}
			finalURL := core.ValidatedFinalURL(first.FinalURL, u.String(), ProviderName, maxFetchURLLength)
			return &core.FetchOutcome{
				Page: &core.Page{
					URL:        u.String(),
					FinalURL:   finalURL,
					Title:      title,
					Published:  pub,
					Content:    content,
					TotalChars: textnorm.RuneCount(content),
					Provider:   ProviderName,
					SourceType: core.SourceGeneric,
				},
				Cost:      core.CloneCost(p.fetchCost),
				RequestID: resp.RequestID,
			}, nil
		}
	}

	if len(wireResp.Errors) > 0 {
		return nil, classifyFetchItemError(wireResp.Errors[0])
	}
	return nil, core.NewError(core.CodeContentUnavailable, "TinyFish fetch returned empty content").WithProvider(ProviderName)
}

func resolveFetchTTL(opts map[string]string) (*int, error) {
	if len(opts) == 0 {
		return nil, nil
	}
	raw := strings.TrimSpace(textnorm.FirstNonEmpty(opts[spec.OptTinyFishTTL], opts[spec.OptTinyFishTTLAlias]))
	if raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 0 {
			return nil, core.NewError(
				core.CodeInvalidArgument,
				fmt.Sprintf("invalid tinyfish.ttl %q: expected non-negative integer seconds", raw),
			).WithProvider(ProviderName)
		}
		return &v, nil
	}
	if spec.IsFreshFetchOption(opts) {
		return new(0), nil
	}
	return nil, nil
}

func computePerURLTimeoutMS(ctx context.Context, fetchTimeout time.Duration) int {
	budget := httpx.RemainingBudget(ctx, fetchTimeout, 500*time.Millisecond)
	if budget <= 0 {
		return 0
	}
	return min(max(int(budget.Milliseconds()), 1), 110000)
}
