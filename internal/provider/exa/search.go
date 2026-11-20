package exa

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

// Search 调用 POST /search 并以响应中的 costDollars.total 精确记账（Estimated=false）。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}

	searchType := strings.ToLower(textnorm.FirstNonEmpty(req.Options[spec.OptExaSearchType], req.Options[spec.OptExaTypeAlias], p.searchType))
	switch searchType {
	case spec.ExaTypeLite, spec.ExaTypeAuto, spec.ExaTypeFast, spec.ExaTypeInstant, spec.ExaTypeDeepLite, spec.ExaTypeDeep, spec.ExaTypeDeepReasoning:
	default:
		return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid exa.search_type %q: must be lite, auto, fast, instant, deep-lite, deep, or deep-reasoning", searchType)).
			WithProvider(ProviderName)
	}

	cat := normalizeExaCategory(textnorm.FirstNonEmpty(req.Options[spec.OptExaCategory], req.Options[spec.OptExaCategoryAlias]))
	if strippedQ, inlineCat := extractInlineExaCategory(query); inlineCat != "" {
		query = strippedQ
		if cat == "" {
			cat = inlineCat
		}
	}

	maxAllowed := defaultMaxCount
	if p.allowOver10 {
		maxAllowed = extendedMaxCount
	}
	count := spec.ClampSearchCount(req.Count, maxAllowed)

	var highlightsSpec any = true
	if qIntent := strings.TrimSpace(req.Question); qIntent != "" {
		highlightsSpec = wireHighlightsSpec{Query: qIntent}
	}
	var textSpec any
	if req.Detail == core.DetailContent {
		textSpec = wireTextSpec{MaxCharacters: defaultSearchTextCap}
	}

	wireReq := searchWireReq{
		Query:      query,
		Type:       searchType,
		NumResults: count,
		Contents: searchContentsSpec{
			Highlights: highlightsSpec,
			Text:       textSpec,
		},
	}
	var localWarnings []string
	if cat != "" {
		wireReq.Category = cat
	}
	if len(req.Filters.Sites) > 0 {
		wireReq.IncludeDomains = req.Filters.Sites
	}
	if len(req.Filters.ExcludeSites) > 0 {
		if cat == categoryPeople {
			if req.StrictFilters {
				return nil, core.NewError(core.CodeInvalidArgument, `Exa category "people" does not support exclude_sites`).WithProvider(ProviderName)
			}
			localWarnings = append(localWarnings, `exa: exclude_sites omitted for category "people"`)
		} else {
			wireReq.ExcludeDomains = req.Filters.ExcludeSites
		}
	}
	if f := req.Filters.Freshness; f != nil && (!f.After.IsZero() || !f.Before.IsZero()) {
		if cat == categoryCompany || cat == categoryPeople {
			if req.StrictFilters {
				return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("Exa category %q does not support freshness date filters", cat)).WithProvider(ProviderName)
			}
			localWarnings = append(localWarnings, fmt.Sprintf("exa: freshness filter omitted for category %q", cat))
		} else {
			if !f.After.IsZero() {
				wireReq.StartPublishedDate = f.After.UTC().Format(time.RFC3339)
			}
			if !f.Before.IsZero() {
				wireReq.EndPublishedDate = f.EndOfBeforeDay().Format(time.RFC3339)
			}
		}
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/search",
		Timeout:      p.timeout,
		AuthHeader:   spec.HeaderExaAPIKey,
		Body:         wireReq,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp searchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Exa search JSON", err).WithProvider(ProviderName)
	}

	mapped := make([]core.Result, 0, len(wireResp.Results))
	for _, item := range wireResp.Results {
		summary := strings.Join(item.Highlights, "\n\n")
		snippet := ""
		if len(item.Highlights) > 0 {
			snippet = textnorm.TruncateRunes(textnorm.CleanInline(item.Highlights[0]), spec.DefaultSnippetMaxRunes)
		}
		var rel *float64
		if len(item.HighlightScores) > 0 {
			rel = textnorm.ClampUnitFloat(item.HighlightScores[0])
		}
		var authors []string
		if a := strings.TrimSpace(item.Author); a != "" {
			authors = []string{a}
		}
		mapped = append(mapped, core.Result{
			Title:     item.Title,
			URL:       item.URL,
			Authors:   authors,
			Icon:      strings.TrimSpace(item.Favicon),
			Published: textnorm.ParseFlexibleTime(item.PublishedDate, time.UTC),
			Snippet:   snippet,
			Summary:   summary,
			Content:   item.Text,
			Relevance: rel,
		})
	}

	cleaned, sanWarnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	warnings := append(localWarnings, sanWarnings...)
	for _, n := range wireResp.Notices {
		if msg := strings.TrimSpace(n.Message); msg != "" {
			warnings = append(warnings, msg)
		}
	}
	for _, w := range wireResp.Warnings {
		if msg := strings.TrimSpace(w.Message); msg != "" {
			warnings = append(warnings, msg)
		}
	}

	var cost *core.Cost
	if wireResp.CostDollars != nil {
		cost = &core.Cost{
			Currency:  core.CurrencyUSD,
			Micros:    core.AmountToMicros(wireResp.CostDollars.Total),
			Estimated: false,
		}
	}
	reqID := textnorm.FirstNonEmpty(wireResp.RequestID, resp.RequestID)

	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      cost,
		RequestID: reqID,
		Warnings:  warnings,
	}, nil
}
