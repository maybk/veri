package brave

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

// Search 根据 Detail 与配置自动选择 /res/v1/llm/context 或 /res/v1/web/search。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	baseQuery := strings.TrimSpace(req.Query)
	if baseQuery == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}
	if exceedsBraveQueryLimits(baseQuery) {
		return nil, core.NewError(core.CodeInvalidArgument, "Brave query exceeds 400 characters or 50 words").
			WithProvider(ProviderName).
			WithHint("Shorten the query to <= 400 characters and <= 50 words.")
	}

	mode := strings.ToLower(textnorm.FirstNonEmpty(req.Options[spec.OptBraveMode], req.Options[spec.OptBraveModeAlias], p.mode))
	switch mode {
	case spec.BraveModeAuto, spec.BraveModeContext, spec.BraveModeWeb:
	default:
		return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid brave.mode %q: must be auto, context, or web", mode)).
			WithProvider(ProviderName)
	}

	var localWarnings []string
	var searchLang string
	if len(req.Filters.Lang) > 0 {
		searchLang = normalizeBraveLang(req.Filters.Lang[0])
		if len(req.Filters.Lang) > 1 {
			localWarnings = append(localWarnings, "brave: only first language filter applied")
		}
	}

	useWeb := mode == spec.BraveModeWeb || (mode == spec.BraveModeAuto && req.Detail == core.DetailSnippet)
	if useWeb {
		return p.searchWeb(ctx, req, baseQuery, searchLang, localWarnings)
	}
	return p.searchContext(ctx, req, baseQuery, searchLang, localWarnings)
}

func (p *Provider) searchContext(ctx context.Context, req *core.SearchRequest, baseQuery, searchLang string, localWarnings []string) (*core.SearchOutcome, error) {
	q, goggles := buildBraveQueryOrGoggles(baseQuery, req.Filters.Sites, req.Filters.ExcludeSites)
	if extraGoggle := strings.TrimSpace(textnorm.FirstNonEmpty(req.Options[spec.OptBraveGoggles], req.Options[spec.OptBraveGogglesAlias])); extraGoggle != "" {
		goggles = append(goggles, extraGoggle)
	}
	threshold := strings.ToLower(strings.TrimSpace(textnorm.FirstNonEmpty(req.Options[spec.OptBraveThreshold], req.Options[spec.OptBraveThresholdAlias])))
	switch threshold {
	case "", "strict", "balanced", "lenient", "disabled":
	default:
		return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid brave.threshold %q: must be strict, balanced, lenient, or disabled", threshold)).
			WithProvider(ProviderName)
	}
	if threshold == "" && req.StrictFilters {
		threshold = "strict"
	}

	count := spec.ClampSearchCount(req.Count, spec.MaxSearchCount)

	tokensPerURL := defaultContextTokensPerURL
	if req.Detail == core.DetailContent {
		tokensPerURL = contentContextTokensPerURL
	}

	wireReq := contextWireReq{
		Q:                           q,
		Count:                       count,
		MaximumNumberOfURLs:         count,
		MaximumNumberOfTokensPerURL: tokensPerURL,
		ContextThresholdMode:        threshold,
		SearchLang:                  searchLang,
		Freshness:                   formatBraveFreshness(req.Filters.Freshness, req.Filters.Freshness.ReferenceTime(time.Now())),
		EnableSourceMetadata:        true,
		Operators:                   true,
		OperatorsFallback:           !req.StrictFilters,
		Goggles:                     goggles,
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/res/v1/llm/context",
		Timeout:      p.timeout,
		AuthHeader:   spec.HeaderBraveToken,
		Body:         wireReq,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp contextWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Brave llm/context JSON", err).WithProvider(ProviderName)
	}

	mapped := make([]core.Result, 0, len(wireResp.Grounding.Generic))
	for _, g := range wireResp.Grounding.Generic {
		joined := strings.Join(g.Snippets, "\n\n")
		srcMeta, hasMeta := wireResp.Sources[g.URL]
		snippet := ""
		if hasMeta && strings.TrimSpace(srcMeta.Description) != "" {
			snippet = srcMeta.Description
		} else if len(g.Snippets) > 0 {
			snippet = textnorm.TruncateRunes(textnorm.CleanInline(g.Snippets[0]), spec.DefaultSnippetMaxRunes)
		}

		var pub *time.Time
		if hasMeta && len(srcMeta.Age) >= 4 {
			pub = textnorm.ParseFlexibleTime(srcMeta.Age[3], time.UTC)
		}

		title := g.Title
		if title == "" && hasMeta {
			title = srcMeta.Title
		}
		var content string
		if req.Detail == core.DetailContent {
			content = joined
		}
		site := srcMeta.SiteName
		if site == "" {
			site = srcMeta.Hostname
		}
		mapped = append(mapped, core.Result{
			Title:     title,
			URL:       g.URL,
			Site:      site,
			Icon:      strings.TrimSpace(srcMeta.Favicon),
			Published: pub,
			Snippet:   snippet,
			Summary:   joined,
			Content:   content,
		})
	}

	cleaned, sanWarnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      core.CloneCost(p.searchCost),
		RequestID: resp.RequestID,
		Warnings:  append(localWarnings, sanWarnings...),
	}, nil
}

func (p *Provider) searchWeb(ctx context.Context, req *core.SearchRequest, baseQuery, searchLang string, localWarnings []string) (*core.SearchOutcome, error) {
	q, goggles := buildBraveQueryOrGoggles(baseQuery, req.Filters.Sites, req.Filters.ExcludeSites)
	if extraGoggle := strings.TrimSpace(textnorm.FirstNonEmpty(req.Options[spec.OptBraveGoggles], req.Options[spec.OptBraveGogglesAlias])); extraGoggle != "" {
		goggles = append(goggles, extraGoggle)
	}
	count := spec.ClampSearchCount(req.Count, maxWebSearchCount)

	params := url.Values{}
	params.Set("q", q)
	params.Set("count", strconv.Itoa(count))
	params.Set("text_decorations", "false")
	params.Set("result_filter", "web")
	params.Set("extra_snippets", "true")
	if searchLang != "" {
		params.Set("search_lang", searchLang)
	}
	if fresh := formatBraveFreshness(req.Filters.Freshness, req.Filters.Freshness.ReferenceTime(time.Now())); fresh != "" {
		params.Set("freshness", fresh)
	}
	for _, g := range goggles {
		params.Add("goggles", g)
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodGet,
		URL:          p.baseURL + "/res/v1/web/search?" + params.Encode(),
		Timeout:      p.timeout,
		AuthHeader:   spec.HeaderBraveToken,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp webSearchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Brave web/search JSON", err).WithProvider(ProviderName)
	}

	now := time.Now()
	mapped := make([]core.Result, 0, len(wireResp.Web.Results))
	for _, item := range wireResp.Web.Results {
		summaryParts := []string{item.Description}
		summaryParts = append(summaryParts, item.ExtraSnippets...)
		summary := strings.Join(summaryParts, "\n")

		var site, icon string
		if item.Profile != nil {
			site = item.Profile.Name
		}
		if item.MetaURL != nil {
			icon = strings.TrimSpace(item.MetaURL.Favicon)
		}
		pub := textnorm.ParseFlexibleTimeAt(item.PageAge, time.UTC, now)
		if pub == nil {
			pub = textnorm.ParseFlexibleTimeAt(item.Age, time.UTC, now)
		}
		var extra map[string]any
		if req.IncludeExtra {
			ex := make(map[string]any, 2)
			if lang := textnorm.CleanInline(item.Language); lang != "" {
				ex["language"] = lang
			}
			if sub := textnorm.CleanInline(item.Subtype); sub != "" {
				ex["subtype"] = sub
			}
			if len(ex) > 0 {
				extra = ex
			}
		}
		mapped = append(mapped, core.Result{
			Title:     item.Title,
			URL:       item.URL,
			Site:      site,
			Icon:      icon,
			Published: pub,
			Snippet:   item.Description,
			Summary:   summary,
			Extra:     extra,
		})
	}

	if altered := strings.TrimSpace(wireResp.Query.Altered); altered != "" && !strings.EqualFold(altered, q) {
		localWarnings = append(localWarnings, fmt.Sprintf("brave: query spell-corrected to %q", altered))
	}
	cleaned, sanWarnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      core.CloneCost(p.searchCost),
		RequestID: resp.RequestID,
		Warnings:  append(localWarnings, sanWarnings...),
	}, nil
}
