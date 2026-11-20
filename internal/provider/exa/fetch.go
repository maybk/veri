package exa

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

// Fetch 调用 POST /contents 提取单页正文，支持 maxAgeHours / livecrawlTimeout 控制并对 statuses[].error 执行细分映射。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	u, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLen)
	if err != nil {
		return nil, err
	}

	maxAgePtr, err := resolveMaxAgeHours(req.Options)
	if err != nil {
		return nil, err
	}
	var livecrawlMS int
	if maxAgePtr == nil || *maxAgePtr >= 0 {
		livecrawlMS = computeLivecrawlTimeoutMS(ctx, p.fetchTimeout)
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/contents",
		Timeout:      p.fetchTimeout,
		AuthHeader:   spec.HeaderExaAPIKey,
		Body: contentsWireReq{
			URLs:             []string{u.String()},
			Text:             true,
			MaxAgeHours:      maxAgePtr,
			LivecrawlTimeout: livecrawlMS,
		},
		ClassifyErr: classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp contentsWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Exa contents JSON", err).WithProvider(ProviderName)
	}

	if len(wireResp.Results) > 0 {
		first := wireResp.Results[0]
		content := textnorm.CleanPlainText(first.Text)
		if content != "" {
			var cost *core.Cost
			if wireResp.CostDollars != nil {
				cost = &core.Cost{
					Currency:  core.CurrencyUSD,
					Micros:    core.AmountToMicros(wireResp.CostDollars.Total),
					Estimated: false,
				}
			}
			reqID := textnorm.FirstNonEmpty(wireResp.RequestID, resp.RequestID)
			return &core.FetchOutcome{
				Page: &core.Page{
					URL:        u.String(),
					Title:      textnorm.CleanInline(first.Title),
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

	if len(wireResp.Statuses) > 0 && wireResp.Statuses[0].Error != nil {
		stErr := wireResp.Statuses[0].Error
		return nil, classifyCrawlTagError(strings.TrimSpace(stErr.Tag), stErr.HTTPStatusCode)
	}
	return nil, core.NewError(core.CodeContentUnavailable, "Exa contents returned empty text").WithProvider(ProviderName)
}

func resolveMaxAgeHours(opts map[string]string) (*int, error) {
	if len(opts) == 0 {
		return nil, nil
	}
	raw := strings.TrimSpace(textnorm.FirstNonEmpty(opts[spec.OptExaMaxAgeHours], opts[spec.OptExaMaxAgeAlias]))
	if raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < -1 || v > maxContentsAgeHours {
			return nil, core.NewError(
				core.CodeInvalidArgument,
				fmt.Sprintf("invalid exa.max_age_hours %q: expected integer in [-1, %d] (0 = livecrawl, -1 = cache only)", raw, maxContentsAgeHours),
			).WithProvider(ProviderName)
		}
		return &v, nil
	}
	if spec.IsFreshFetchOption(opts) {
		return new(0), nil
	}
	return nil, nil
}

func computeLivecrawlTimeoutMS(ctx context.Context, fetchTimeout time.Duration) int {
	budget := httpx.RemainingBudget(ctx, fetchTimeout, 500*time.Millisecond)
	if budget < time.Second {
		return 0
	}
	return min(int(budget.Milliseconds()), 25000)
}
