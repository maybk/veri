package deepseek

import (
	"context"
	"encoding/json/v2"
	"net/http"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Fetch 调用 POST /v1/tools/fetch 提取单页正文 Markdown。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	u, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLength)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/v1/tools/fetch",
		Timeout:      p.fetchTimeout,
		AuthHeader:   spec.HeaderAuthorization,
		AuthPrefix:   spec.AuthBearerPrefix,
		Body:         fetchWireReq{URL: u.String()},
		ClassifyErr:  classifyFetchError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp fetchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode DeepSeek fetch JSON", err).WithProvider(ProviderName)
	}

	content := textnorm.CleanPlainText(wireResp.Content)
	if content == "" {
		return nil, core.NewError(core.CodeContentUnavailable, "DeepSeek fetch returned empty content").WithProvider(ProviderName)
	}

	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        u.String(),
			Title:      textnorm.CleanInline(wireResp.Title),
			Content:    content,
			TotalChars: textnorm.RuneCount(content),
			Provider:   ProviderName,
			SourceType: core.SourceGeneric,
		},
		Cost:      core.CloneCost(p.fetchCost),
		RequestID: resp.RequestID,
	}, nil
}
