package tavily

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

// Search 调用 POST /search 并映射归一化结果。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}
	count := spec.ClampSearchCount(req.Count, maxSearchCount)

	depth := strings.ToLower(textnorm.FirstNonEmpty(req.Options[spec.OptTavilySearchDepth], req.Options[spec.OptTavilyDepthAlias], p.searchDepth))
	switch depth {
	case spec.TavilyDepthUltraFast, spec.TavilyDepthFast, spec.TavilyDepthBasic, spec.TavilyDepthAdvanced:
	default:
		return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid tavily.search_depth %q", depth)).WithProvider(ProviderName)
	}

	var localWarnings []string
	wireReq := searchWireReq{
		Query:                query,
		SearchDepth:          depth,
		MaxResults:           count,
		IncludeAnswer:        false,
		IncludePublishedDate: true,
		IncludeFavicon:       req.IncludeExtra,
		IncludeUsage:         true,
	}
	if topic := strings.ToLower(strings.TrimSpace(textnorm.FirstNonEmpty(req.Options[spec.OptTavilyTopic], req.Options[spec.OptTavilyTopicAlias]))); topic != "" {
		switch topic {
		case "general", "news", "finance":
			wireReq.Topic = topic
		default:
			return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid tavily.topic %q (valid: general, news, finance)", topic)).WithProvider(ProviderName)
		}
	}
	if len(req.Filters.Sites) > 0 {
		wireReq.IncludeDomains = req.Filters.Sites
	}
	if len(req.Filters.ExcludeSites) > 0 {
		wireReq.ExcludeDomains = req.Filters.ExcludeSites
	}
	if len(req.Filters.Lang) > 0 {
		if lang := normalizeTavilyLang(req.Filters.Lang[0]); lang != "" {
			wireReq.Language = lang
			if req.StrictFilters {
				wireReq.FilterByLanguage = true
			}
		}
		if len(req.Filters.Lang) > 1 {
			localWarnings = append(localWarnings, "tavily: only first language filter applied")
		}
	}
	if req.Detail == core.DetailContent {
		wireReq.IncludeRawContent = spec.TextFormatMarkdown
	}
	applyFreshness(&wireReq, req.Filters.Freshness)

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + "/search",
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
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Tavily search JSON", err).WithProvider(ProviderName)
	}

	mapped := make([]core.Result, 0, len(wireResp.Results))
	for _, item := range wireResp.Results {
		var rel *float64
		if item.Score != nil {
			rel = textnorm.ClampUnitFloat(*item.Score)
		}
		mapped = append(mapped, core.Result{
			Title:     item.Title,
			URL:       item.URL,
			Icon:      strings.TrimSpace(item.Favicon),
			Published: textnorm.ParseFlexibleTime(item.PublishedDate, time.UTC),
			Snippet:   textnorm.TruncateRunes(textnorm.CleanInline(item.Content), spec.DefaultSnippetMaxRunes),
			Summary:   item.Content,
			Content:   item.RawContent,
			Relevance: rel,
		})
	}

	cleaned, sanWarnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)

	var fallbackMicros int64
	if p.creditCost != nil {
		credits := int64(1)
		if depth == spec.TavilyDepthAdvanced {
			credits = 2
		}
		fallbackMicros = p.creditCost.Micros * credits
	}
	cost := p.costFromUsage(wireResp.Usage, fallbackMicros)
	reqID := textnorm.FirstNonEmpty(wireResp.RequestID, resp.RequestID)

	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      cost,
		RequestID: reqID,
		Warnings:  append(localWarnings, sanWarnings...),
	}, nil
}

func normalizeTavilyLang(raw string) string {
	cleaned := stripLocaleEncoding(raw)
	if cleaned == "" {
		return ""
	}
	if cleaned == "pt-br" {
		return "pt-br"
	}
	if cleaned == "zh" || strings.HasPrefix(cleaned, "zh-") {
		for _, sub := range strings.Split(cleaned, "-")[1:] {
			if sub == "hant" || sub == "tw" || sub == "hk" || sub == "mo" {
				return "zh-tw"
			}
		}
		return "zh-cn"
	}
	base, _, _ := strings.Cut(cleaned, "-")
	return base
}

func stripLocaleEncoding(raw string) string {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	if idx := strings.IndexAny(cleaned, ".@;"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	return strings.ReplaceAll(cleaned, "_", "-")
}

func applyFreshness(wireReq *searchWireReq, f *core.Freshness) {
	if f == nil {
		return
	}
	if f.IsRelative() && f.N == 1 {
		wireReq.TimeRange = string(f.Unit)
		return
	}
	if !f.After.IsZero() {
		wireReq.StartDate = f.After.Format("2006-01-02")
	}
	if !f.Before.IsZero() {
		wireReq.EndDate = f.Before.Format("2006-01-02")
	}
}
