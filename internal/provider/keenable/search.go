package keenable

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

// Search 调用 POST /v1/search 执行语义与关键字检索，默认开启 disable_cards=true 以确保全为真实网页链接。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	if req == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "search request must not be nil").WithProvider(ProviderName)
	}
	baseQuery := strings.TrimSpace(req.Query)
	if baseQuery == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}

	mode := p.mode
	if rawMode := textnorm.FirstNonEmpty(req.Options[spec.OptKeenableMode], req.Options[spec.OptKeenableModeAlias]); rawMode != "" {
		var err error
		mode, err = normalizeMode(rawMode)
		if err != nil {
			return nil, err
		}
	}

	query, singleSite, excludeDomains := buildSearchFilters(baseQuery, req.Filters)
	wireReq := searchWireReq{
		Query:            query,
		Mode:             mode,
		Site:             singleSite,
		ExcludeDomains:   excludeDomains,
		MaxResults:       spec.ClampSearchCount(req.Count, maxSearchCount),
		SnippetMaxLength: snippetLimitForDetail(req.Detail),
		DisableCards:     true,
	}
	if req.Filters.Freshness != nil {
		if !req.Filters.Freshness.After.IsZero() {
			wireReq.PublishedAfter = req.Filters.Freshness.After.UTC().Format("2006-01-02")
		}
		if !req.Filters.Freshness.Before.IsZero() {
			wireReq.PublishedBefore = req.Filters.Freshness.Before.UTC().Format("2006-01-02")
		}
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/v1/search",
		Timeout:      p.timeout,
		AuthHeader:   spec.HeaderAPIKey,
		Body:         wireReq,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp searchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Keenable search JSON", err).WithProvider(ProviderName)
	}

	mapped := make([]core.Result, 0, len(wireResp.Results))
	for _, item := range wireResp.Results {
		var content string
		if req.Detail == core.DetailContent {
			content = item.Snippet
		}
		mapped = append(mapped, core.Result{
			Title:     item.Title,
			URL:       item.URL,
			Published: textnorm.ParseFlexibleTime(item.PublishedAt, time.UTC),
			Summary:   item.Snippet,
			Content:   content,
		})
	}

	cleaned, warnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      p.costFromUsage(wireResp.Usage),
		RequestID: resp.RequestID,
		Warnings:  warnings,
	}, nil
}

// buildSearchFilters 将白名单与黑名单映射到 Keenable 协议边界：
// 纯主机单站点用原生 site 字段，含路径/通配或多站点拼为 query 内 site: / 大写 OR 子句；排除项含路径时回退至 -site: 防止触发 exclude_domains 400。
func buildSearchFilters(baseQuery string, f core.Filters) (query, singleSite string, excludeDomains []string) {
	parts := []string{baseQuery}

	sites := make([]string, 0, len(f.Sites))
	for _, s := range f.Sites {
		if st := strings.TrimSpace(s); st != "" {
			sites = append(sites, st)
		}
	}
	switch {
	case len(sites) == 1 && !strings.ContainsAny(sites[0], "/*"):
		singleSite = sites[0]
	case len(sites) == 1:
		parts = append(parts, "site:"+sites[0])
	case len(sites) > 1:
		clauses := make([]string, 0, len(sites))
		for _, st := range sites {
			clauses = append(clauses, "site:"+st)
		}
		parts = append(parts, "("+strings.Join(clauses, " OR ")+")")
	}

	for _, ex := range f.ExcludeSites {
		st := strings.TrimSpace(ex)
		if st == "" {
			continue
		}
		if strings.ContainsAny(st, "/*") {
			parts = append(parts, "-site:"+st)
			continue
		}
		excludeDomains = append(excludeDomains, st)
	}
	return strings.Join(parts, " "), singleSite, excludeDomains
}

func snippetLimitForDetail(detail core.Detail) int {
	switch detail {
	case core.DetailSnippet:
		return snippetLenSnippet
	case core.DetailContent:
		return snippetLenContent
	default:
		return snippetLenSummary
	}
}
