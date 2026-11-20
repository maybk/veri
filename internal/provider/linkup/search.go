package linkup

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

var minLinkupDate = time.Date(1970, 1, 2, 0, 0, 0, 0, time.UTC)

// Search 调用 POST /v1/search 执行结构化检索（固定 outputType=searchResults，默认 depth=fast 防触发 10 倍价 deep）。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	if req == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "search request must not be nil").WithProvider(ProviderName)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}

	depth := p.depth
	if rawDepth := textnorm.FirstNonEmpty(req.Options[spec.OptLinkupDepth], req.Options[spec.OptLinkupDepthAlias]); rawDepth != "" {
		var err error
		depth, err = normalizeDepth(rawDepth)
		if err != nil {
			return nil, err
		}
	}

	excludeHosts, _ := urlnorm.SplitHostAndPathFilters(req.Filters.ExcludeSites)
	wireReq := searchWireReq{
		Q:              query,
		Depth:          depth,
		OutputType:     outputTypeSearchResults,
		MaxResults:     spec.ClampSearchCount(req.Count, maxSearchCount),
		IncludeDomains: urlnorm.ExtractFilterHosts(req.Filters.Sites),
		ExcludeDomains: excludeHosts,
		IncludeImages:  false,
	}
	if req.Filters.Freshness != nil {
		fromDate, toDate := clampDateRange(req.Filters.Freshness)
		wireReq.FromDate = fromDate
		wireReq.ToDate = toDate
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/v1/search",
		Timeout:      p.timeout,
		AuthHeader:   spec.HeaderAuthorization,
		AuthPrefix:   spec.AuthBearerPrefix,
		Body:         wireReq,
		ClassifyErr:  classifySearchError,
	})
	if err != nil {
		// Linkup 搜索无结果会返回 HTTP 400 SEARCH_QUERY_NO_RESULT 且不计费，归一化为空结果列表无缝配合 fallback_on_empty。
		if isNoResultError(err) {
			return &core.SearchOutcome{Results: []core.Result{}}, nil
		}
		return nil, err
	}

	var wireResp searchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Linkup search JSON", err).WithProvider(ProviderName)
	}

	mapped := make([]core.Result, 0, len(wireResp.Results))
	for _, item := range wireResp.Results {
		if item.Type != "" && !strings.EqualFold(strings.TrimSpace(item.Type), "text") {
			continue
		}
		var content string
		if req.Detail == core.DetailContent {
			content = item.Content
		}
		mapped = append(mapped, core.Result{
			Title:     item.Name,
			URL:       item.URL,
			Icon:      item.Favicon,
			Published: textnorm.ParseFlexibleTime(item.PublishedAt, time.UTC),
			Summary:   item.Content,
			Content:   content,
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

func clampDateRange(f *core.Freshness) (fromDate, toDate string) {
	var after, before time.Time
	if !f.After.IsZero() {
		after = f.After.UTC()
		if after.Before(minLinkupDate) {
			after = minLinkupDate
		}
	}
	if !f.Before.IsZero() {
		before = f.Before.UTC()
		if before.Before(minLinkupDate) {
			before = minLinkupDate
		}
	}
	if !after.IsZero() && !before.IsZero() && after.After(before) {
		after = before
	}
	if !after.IsZero() {
		fromDate = after.Format("2006-01-02")
	}
	if !before.IsZero() {
		toDate = before.Format("2006-01-02")
	}
	return fromDate, toDate
}
