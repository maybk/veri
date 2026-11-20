package deepseek

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

// Search 调用 POST /v1/tools/search 执行检索并映射归一化结果。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	composedQuery := buildSearchQuery(req.Query, req.Filters)
	if strings.TrimSpace(composedQuery) == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}
	if n := textnorm.RuneCount(composedQuery); n > maxQueryRunes {
		return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("composed DeepSeek query length %d exceeds 128 characters", n)).
			WithProvider(ProviderName).
			WithHint("Shorten the query or reduce --site/--exclude-site filters.")
	}

	count := spec.ClampSearchCount(req.Count, spec.MaxSearchCount)

	var oriQ string
	if q := strings.TrimSpace(req.Question); q != "" {
		oriQ = textnorm.TruncateRunes(q, maxQueryRunes)
	}

	wireReq := searchWireReq{
		Query:       composedQuery,
		Count:       count,
		OriQuestion: oriQ,
		NeedContent: req.Detail == core.DetailContent,
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.searchGate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/v1/tools/search",
		Timeout:      p.timeout,
		AuthHeader:   spec.HeaderAuthorization,
		AuthPrefix:   spec.AuthBearerPrefix,
		Body:         wireReq,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp searchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode DeepSeek search JSON", err).WithProvider(ProviderName)
	}

	mapped := make([]core.Result, 0, len(wireResp.Results))
	for _, item := range wireResp.Results {
		var site, icon string
		if item.SiteName != nil {
			site = *item.SiteName
		}
		if item.SiteIcon != nil {
			icon = strings.TrimSpace(*item.SiteIcon)
		}
		var pub *time.Time
		if item.Date != nil {
			pub = textnorm.ParseFlexibleTime(*item.Date, time.UTC)
		}
		var rel, auth *float64
		if item.RerankScore != nil {
			rel = textnorm.ClampUnitFloat(*item.RerankScore / maxRerankScore)
		}
		if item.Authority != nil && *item.Authority >= 0 {
			auth = textnorm.ClampUnitFloat(float64(*item.Authority) / maxAuthorityScore)
		}
		mapped = append(mapped, core.Result{
			Title:     item.Title,
			URL:       item.URL,
			Site:      site,
			Icon:      icon,
			Published: pub,
			Snippet:   item.Snippet,
			Summary:   item.Summary,
			Content:   item.Content,
			Relevance: rel,
			Authority: auth,
		})
	}

	cleaned, warnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	var cost *core.Cost
	if len(wireResp.Results) > 0 {
		cost = core.CloneCost(p.searchCost)
	}

	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      cost,
		RequestID: resp.RequestID,
		Warnings:  warnings,
	}, nil
}

func buildSearchQuery(base string, f core.Filters) string {
	var parts []string
	if q := strings.TrimSpace(base); q != "" {
		parts = append(parts, q)
	}
	if len(f.Sites) == 1 {
		parts = append(parts, "site:"+strings.TrimSpace(f.Sites[0]))
	} else if len(f.Sites) > 1 {
		clauses := make([]string, 0, len(f.Sites))
		for _, s := range f.Sites {
			if st := strings.TrimSpace(s); st != "" {
				clauses = append(clauses, "site:"+st)
			}
		}
		if len(clauses) > 0 {
			parts = append(parts, strings.Join(clauses, " OR "))
		}
	}
	for _, ex := range f.ExcludeSites {
		if st := strings.TrimSpace(ex); st != "" {
			parts = append(parts, "-site:"+st)
		}
	}
	if len(f.Lang) > 0 {
		var langs []string
		for _, l := range f.Lang {
			if lt := strings.TrimSpace(l); lt != "" {
				langs = append(langs, lt)
			}
		}
		if len(langs) > 0 {
			parts = append(parts, "lang:"+strings.Join(langs, ","))
		}
	}
	if f.Freshness != nil {
		if !f.Freshness.After.IsZero() {
			parts = append(parts, "after:"+f.Freshness.After.Format("2006-01-02"))
		}
		if !f.Freshness.Before.IsZero() {
			parts = append(parts, "before:"+f.Freshness.Before.Format("2006-01-02"))
		}
	}
	return strings.Join(parts, " ")
}
