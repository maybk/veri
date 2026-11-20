package linkup

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/mdscan"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// Fetch 调用 POST /v1/fetch 提取 HTML 或 PDF 为干净 Markdown；遇反爬/JS 盾自动按需升档 renderJs。
func (p *Provider) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	if req == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "fetch request must not be nil").WithProvider(ProviderName)
	}
	u, err := core.ValidateTargetURL(req.URL, ProviderName, maxFetchURLLength)
	if err != nil {
		return nil, err
	}

	mode, err := p.resolveFetchMode(req)
	if err != nil {
		return nil, err
	}
	renderJS, err := p.resolveRenderJS(req)
	if err != nil {
		return nil, err
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/v1/fetch",
		Timeout:      p.fetchTimeout,
		AuthHeader:   spec.HeaderAuthorization,
		AuthPrefix:   spec.AuthBearerPrefix,
		Body: fetchWireReq{
			URL:               u.String(),
			Mode:              mode,
			RenderJS:          renderJS,
			IncludeRawContent: false,
			ExtractImages:     false,
		},
		ClassifyErr: classifyFetchError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp fetchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Linkup fetch JSON", err).WithProvider(ProviderName)
	}

	content := textnorm.CleanPlainText(wireResp.Markdown)
	if content == "" {
		return nil, core.NewError(core.CodeContentUnavailable, "Linkup fetch returned empty markdown").WithProvider(ProviderName)
	}

	title := textnorm.CleanInline(mdscan.FirstH1(content))
	return &core.FetchOutcome{
		Page: &core.Page{
			URL:        u.String(),
			Title:      title,
			Content:    content,
			TotalChars: textnorm.RuneCount(content),
			Provider:   ProviderName,
			SourceType: core.SourceGeneric,
		},
		Cost:      core.CloneCost(p.fetchCost),
		RequestID: resp.RequestID,
	}, nil
}

func (p *Provider) resolveFetchMode(req *core.FetchRequest) (string, error) {
	if len(req.Options) > 0 {
		if raw := textnorm.FirstNonEmpty(req.Options[spec.OptLinkupFetchMode], req.Options[spec.OptLinkupFetchModeAlias]); raw != "" {
			return normalizeFetchMode(raw)
		}
	}
	return p.fetchMode, nil
}

func (p *Provider) resolveRenderJS(req *core.FetchRequest) (bool, error) {
	if len(req.Options) > 0 {
		raw := textnorm.FirstNonEmpty(
			req.Options[spec.OptLinkupRenderJS],
			req.Options[spec.OptLinkupRenderJSAlias],
		)
		if raw != "" {
			v, ok := spec.ParseBoolOption(raw)
			if !ok {
				return false, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid linkup.render_js %q (valid: true, false)", raw)).WithProvider(ProviderName)
			}
			return v, nil
		}
	}
	if req.DynamicRenderHint {
		return true, nil
	}
	return p.renderJS, nil
}
