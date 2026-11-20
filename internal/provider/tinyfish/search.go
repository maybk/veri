package tinyfish

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

// Search 调用 GET https://api.search.tinyfish.ai 并映射归一化结果。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}
	if n := textnorm.RuneCount(query); n > maxQueryRunes {
		return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("TinyFish query length %d exceeds 2000 characters", n)).
			WithProvider(ProviderName)
	}

	params := url.Values{}
	params.Set("query", query)
	if q := strings.TrimSpace(req.Question); q != "" {
		params.Set("purpose", textnorm.TruncateRunes(q, maxQueryRunes))
	}

	var localWarnings []string
	if len(req.Filters.Lang) > 0 {
		if lang := normalizeTinyFishLang(req.Filters.Lang[0]); lang != "" {
			params.Set("language", lang)
		}
		if len(req.Filters.Lang) > 1 {
			localWarnings = append(localWarnings, "tinyfish: only first language filter applied")
		}
	} else if inferred := inferQueryLanguage(query); inferred != "" {
		params.Set("language", inferred)
	}
	if len(req.Filters.Sites) > 0 {
		params.Set("include_domains", strings.Join(req.Filters.Sites, ","))
	}
	if len(req.Filters.ExcludeSites) > 0 {
		params.Set("exclude_domains", strings.Join(req.Filters.ExcludeSites, ","))
	}

	domainType := strings.ToLower(strings.TrimSpace(textnorm.FirstNonEmpty(req.Options[spec.OptTinyFishDomainType], req.Options[spec.OptTinyFishDomainAlias])))
	switch domainType {
	case "", domainWeb, domainNews, domainResearchPaper:
	default:
		return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid tinyfish.domain_type %q: must be web, news, or research_paper", domainType)).
			WithProvider(ProviderName)
	}
	if domainType != "" {
		params.Set("domain_type", domainType)
	}
	isPaper := domainType == domainResearchPaper
	if isPaper && req.Filters.Freshness != nil && !isFullYearFreshness(req.Filters.Freshness) {
		if req.StrictFilters {
			return nil, core.NewError(core.CodeInvalidArgument, `TinyFish domain_type "research_paper" only supports full-year freshness ranges`).
				WithProvider(ProviderName)
		}
		localWarnings = append(localWarnings, `tinyfish: freshness approximated to publication year range for domain_type "research_paper"`)
	}
	applyFreshnessParams(params, req.Filters.Freshness, isPaper)

	resp, err := p.client.DoJSON(ctx, p.pool, p.searchGate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodGet,
		URL:          p.searchURL + "?" + params.Encode(),
		Timeout:      p.timeout,
		AuthHeader:   spec.HeaderAPIKey,
		ClassifyErr:  classifyError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp searchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode TinyFish search JSON", err).WithProvider(ProviderName)
	}

	now := time.Now()
	mapped := make([]core.Result, 0, len(wireResp.Results))
	for _, item := range wireResp.Results {
		var pub *time.Time
		if item.Date != nil {
			pub = textnorm.ParseFlexibleTimeAt(*item.Date, time.UTC, now)
		}
		if pub == nil && item.Year != nil && *item.Year >= 1970 && *item.Year <= 9999 {
			t := time.Date(*item.Year, time.January, 1, 0, 0, 0, 0, time.UTC)
			pub = &t
		}
		var extra map[string]any
		if req.IncludeExtra {
			extra = extractPaperExtra(item)
		}
		site := item.SiteName
		if site == "" && item.Publisher != nil {
			site = *item.Publisher
		}
		mapped = append(mapped, core.Result{
			Title:     item.Title,
			URL:       unwrapRedirectURL(item.URL),
			Site:      site,
			Authors:   item.Authors,
			Published: pub,
			Snippet:   item.Snippet,
			Summary:   item.Snippet,
			Extra:     extra,
		})
	}

	cleaned, sanWarnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	if req.Count > 0 && len(cleaned) > req.Count {
		cleaned = cleaned[:req.Count]
	}

	var cost *core.Cost
	if len(wireResp.Results) > 0 {
		cost = core.CloneCost(p.searchCost)
	}

	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      cost,
		RequestID: resp.RequestID,
		Warnings:  append(localWarnings, sanWarnings...),
	}, nil
}

func applyFreshnessParams(params url.Values, f *core.Freshness, isPaper bool) {
	if f == nil {
		return
	}
	if isPaper {
		if !f.After.IsZero() {
			params.Set("pub_year_min", strconv.Itoa(f.After.Year()))
		}
		if !f.Before.IsZero() {
			params.Set("pub_year_max", strconv.Itoa(f.Before.Year()))
		}
		return
	}
	// recency_minutes 与 after_date/before_date 互斥，不得同时传递
	if f.IsRelative() {
		mins := f.ApproximateDays() * 24 * 60
		if mins < 1 {
			mins = 60
		} else if mins > maxRecencyMinutes {
			mins = maxRecencyMinutes
		}
		params.Set("recency_minutes", strconv.Itoa(mins))
		return
	}
	if !f.After.IsZero() {
		params.Set("after_date", f.After.Format("2006-01-02"))
	}
	if !f.Before.IsZero() {
		params.Set("before_date", f.Before.Format("2006-01-02"))
	}
}

func extractPaperExtra(item searchWireItem) map[string]any {
	extra := make(map[string]any)
	if item.Venue != nil && *item.Venue != "" {
		extra["venue"] = *item.Venue
	}
	if item.Year != nil {
		extra["year"] = *item.Year
	}
	if item.CitedByCount != nil {
		extra["cited_by_count"] = *item.CitedByCount
	}
	if item.PDFURL != nil && *item.PDFURL != "" {
		extra["pdf_url"] = *item.PDFURL
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func isFullYearFreshness(f *core.Freshness) bool {
	if f == nil || (f.After.IsZero() && f.Before.IsZero()) {
		return true
	}
	if f.IsRelative() {
		return false
	}
	if !f.After.IsZero() && (f.After.Month() != time.January || f.After.Day() != 1) {
		return false
	}
	if !f.Before.IsZero() && (f.Before.Month() != time.December || f.Before.Day() != 31) {
		return false
	}
	return true
}

// normalizeTinyFishLang 提取 ISO 639-1 基础语言码（如 zh-CN.UTF-8 -> zh），匹配 TinySearch 参数约定。
func normalizeTinyFishLang(raw string) string {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	if idx := strings.IndexAny(cleaned, ".@"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	cleaned = strings.ReplaceAll(cleaned, "_", "-")
	base, _, _ := strings.Cut(cleaned, "-")
	switch base {
	case "chinese":
		return "zh"
	case "english":
		return "en"
	case "japanese":
		return "ja"
	case "korean":
		return "ko"
	}
	if len(base) == 2 && base[0] >= 'a' && base[0] <= 'z' && base[1] >= 'a' && base[1] <= 'z' {
		return base
	}
	return ""
}

// inferQueryLanguage 在未显式传入语言过滤时按脚本特征自动推导语言码，防止中日韩查询默认回退到 US/en。
func inferQueryLanguage(query string) string {
	hasHan := false
	for _, r := range query {
		switch {
		case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			return "ja"
		case unicode.Is(unicode.Hangul, r):
			return "ko"
		case unicode.Is(unicode.Han, r):
			hasHan = true
		}
	}
	if hasHan {
		return "zh"
	}
	return ""
}

// unwrapRedirectURL 还原搜索引擎偶发返回的 /url?q=https://... 或 google.com/url?q=... 跳转包装链接。
func unwrapRedirectURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if strings.HasPrefix(trimmed, "/url?") {
		if q, err := url.ParseQuery(trimmed[len("/url?"):]); err == nil {
			if target := strings.TrimSpace(textnorm.FirstNonEmpty(q.Get("q"), q.Get("url"))); strings.HasPrefix(strings.ToLower(target), "http://") || strings.HasPrefix(strings.ToLower(target), "https://") {
				return target
			}
		}
		return trimmed
	}
	if u, err := url.Parse(trimmed); err == nil && u.Path == "/url" && strings.Contains(strings.ToLower(u.Hostname()), "google.") {
		q := u.Query()
		if target := strings.TrimSpace(textnorm.FirstNonEmpty(q.Get("q"), q.Get("url"))); strings.HasPrefix(strings.ToLower(target), "http://") || strings.HasPrefix(strings.ToLower(target), "https://") {
			return target
		}
	}
	return trimmed
}
