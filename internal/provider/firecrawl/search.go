package firecrawl

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

// Search 调用 POST /v2/search 执行网页检索；绝不默认透传 scrapeOptions 且锁定 limit <= 10 防点数暴涨。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	if req == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "search request must not be nil").WithProvider(ProviderName)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}
	if n := textnorm.RuneCount(query); n > maxQueryRunes {
		return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("Firecrawl query length %d exceeds %d characters", n, maxQueryRunes)).WithProvider(ProviderName)
	}

	categories, err := resolveCategories(req.Options)
	if err != nil {
		return nil, err
	}

	excludeHosts, _ := urlnorm.SplitHostAndPathFilters(req.Filters.ExcludeSites)
	wireReq := searchWireReq{
		Query:          query,
		Limit:          spec.ClampSearchCount(req.Count, maxSearchCount),
		Sources:        []string{"web"},
		Categories:     categories,
		IncludeDomains: urlnorm.ExtractFilterHosts(req.Filters.Sites),
		ExcludeDomains: excludeHosts,
		TBS:            buildTBS(req.Filters.Freshness),
	}

	authHeader, authPrefix := p.authSpec()
	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/v2/search",
		Timeout:      p.timeout,
		AuthHeader:   authHeader,
		AuthPrefix:   authPrefix,
		Body:         wireReq,
		InspectBody:  inspectBody,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp searchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Firecrawl search JSON", err).WithProvider(ProviderName)
	}
	if !wireResp.Success {
		return nil, classifyError(resp.StatusCode, resp.Headers, resp.Body)
	}

	var rawItems []searchWireItem
	if wireResp.Data != nil {
		rawItems = append(rawItems, wireResp.Data.Web...)
		rawItems = append(rawItems, wireResp.Data.News...)
	}

	mapped := make([]core.Result, 0, len(rawItems))
	for _, item := range rawItems {
		var extra map[string]any
		if req.IncludeExtra {
			if cat := textnorm.CleanInline(item.Category); cat != "" {
				extra = map[string]any{"category": cat}
			}
		}
		mapped = append(mapped, core.Result{
			Title:   item.Title,
			URL:     item.URL,
			Summary: item.Description,
			Extra:   extra,
		})
	}

	cleaned, warnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	if w := textnorm.CleanInline(wireResp.Warning); w != "" {
		warnings = append(warnings, "firecrawl: "+w)
	}
	credits := int64(searchCredits)
	if wireResp.CreditsUsed > 0 {
		credits = int64(wireResp.CreditsUsed)
	}
	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      p.estimateCreditsCost(credits),
		RequestID: resp.RequestID,
		Warnings:  warnings,
	}, nil
}

func resolveCategories(opts map[string]string) ([]string, error) {
	if len(opts) == 0 {
		return nil, nil
	}
	raw := strings.TrimSpace(textnorm.FirstNonEmpty(opts[spec.OptFirecrawlCategory], opts[spec.OptFirecrawlCategoryAlias]))
	if raw == "" {
		return nil, nil
	}
	var out []string
	for part := range strings.SplitSeq(raw, ",") {
		v := strings.ToLower(strings.TrimSpace(part))
		if v == "" {
			continue
		}
		switch v {
		case "research", "pdf", "developer", "gov":
			if !slices.Contains(out, v) {
				out = append(out, v)
			}
		case "github":
			return nil, core.NewError(core.CodeInvalidArgument, "firecrawl.category \"github\" was removed upstream (use \"developer\" instead)").WithProvider(ProviderName)
		default:
			return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid firecrawl.category %q (valid: research, pdf, developer, gov)", v)).WithProvider(ProviderName)
		}
	}
	if len(out) > 1 {
		for _, solo := range []string{"developer", "gov"} {
			if slices.Contains(out, solo) {
				return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("firecrawl.category %q cannot be combined with other categories", solo)).WithProvider(ProviderName)
			}
		}
	}
	return out, nil
}

func buildTBS(f *core.Freshness) string {
	if f == nil {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(f.Raw)) {
	case "hour", "1h":
		return "qdr:h"
	case "day", "1d":
		return "qdr:d"
	case "week", "7d", "1w":
		return "qdr:w"
	case "month", "1m", "30d":
		return "qdr:m"
	case "year", "1y":
		return "qdr:y"
	}
	var parts []string
	parts = append(parts, "cdr:1")
	hasBound := false
	if !f.After.IsZero() {
		parts = append(parts, "cd_min:"+f.After.UTC().Format("01/02/2006"))
		hasBound = true
	}
	if !f.Before.IsZero() {
		parts = append(parts, "cd_max:"+f.Before.UTC().Format("01/02/2006"))
		hasBound = true
	}
	if !hasBound {
		return ""
	}
	return strings.Join(parts, ",")
}
