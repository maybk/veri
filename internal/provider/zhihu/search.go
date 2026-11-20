package zhihu

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

var highlightTagReplacer = strings.NewReplacer(
	"<em>", "",
	"</em>", "",
	"<EM>", "",
	"</EM>", "",
)

// Search 调用知乎全网搜索（global_search）或站内搜索（zhihu_search）并映射归一化结果。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	if req == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "search request must not be nil").WithProvider(ProviderName)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}

	scope := p.scope
	if rawScope := textnorm.FirstNonEmpty(req.Options[spec.OptZhihuScope], req.Options[spec.OptZhihuScopeAlias]); rawScope != "" {
		var err error
		scope, err = normalizeScope(rawScope)
		if err != nil {
			return nil, err
		}
	}

	searchDB := p.searchDB
	rawDB := strings.TrimSpace(textnorm.FirstNonEmpty(req.Options[spec.OptZhihuSearchDB], req.Options[spec.OptZhihuSearchDBAlias]))
	if rawDB != "" {
		var err error
		searchDB, err = normalizeSearchDB(rawDB)
		if err != nil {
			return nil, err
		}
	}

	rawSort := strings.TrimSpace(textnorm.FirstNonEmpty(req.Options[spec.OptZhihuSort], req.Options[spec.OptZhihuSortAlias]))
	if rawSort != "" {
		if err := validateSortBy(rawSort); err != nil {
			return nil, err
		}
	}

	params := url.Values{}
	params.Set("Query", query)
	var endpoint string
	var callCost *core.Cost
	var localWarnings []string

	if scope == spec.ZhihuScopeZhihu {
		endpoint = p.baseURL + pathZhihuSearch
		params.Set("Count", strconv.Itoa(spec.ClampSearchCount(req.Count, maxZhihuSearchCount)))
		if rawSort != "" {
			params.Set("SortBy", rawSort)
		} else if sortRange := buildEditTimeSortBy(req.Filters.Freshness); sortRange != "" {
			params.Set("SortBy", sortRange)
		}
		if rawDB != "" {
			localWarnings = append(localWarnings, "zhihu: search_db option is ignored in zhihu scope")
		}
		callCost = core.CloneCost(p.zhihuSearchCost)
	} else {
		endpoint = p.baseURL + pathGlobalSearch
		params.Set("Count", strconv.Itoa(spec.ClampSearchCount(req.Count, maxGlobalSearchCount)))
		params.Set("SearchDB", searchDB)
		if filterExpr := buildGlobalTimeFilter(req.Filters.Freshness); filterExpr != "" {
			params.Set("Filter", filterExpr)
		}
		if rawSort != "" {
			localWarnings = append(localWarnings, "zhihu: sort option is ignored in global scope")
		}
		callCost = core.CloneCost(p.globalSearchCost)
	}

	nowFn := p.nowFn
	if nowFn == nil {
		nowFn = time.Now
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodGet,
		URL:          endpoint + "?" + params.Encode(),
		Timeout:      p.timeout,
		AuthHeader:   spec.HeaderAuthorization,
		AuthPrefix:   spec.AuthBearerPrefix,
		Headers: map[string]string{
			headerRequestTimestamp: strconv.FormatInt(nowFn().Unix(), 10),
			"Content-Type":         "application/json",
		},
		InspectBody: inspectBody,
		ClassifyErr: classifyHTTPError,
	})
	if err != nil {
		return nil, err
	}

	var wireResp searchWireResp
	if err := json.Unmarshal(resp.Body, &wireResp); err != nil {
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Zhihu search JSON", err).WithProvider(ProviderName)
	}

	mapped := make([]core.Result, 0, len(wireResp.Data.Items))
	for _, item := range wireResp.Data.Items {
		cleanText := stripHighlightTags(item.ContentText)
		var content string
		if req.Detail == core.DetailContent {
			content = cleanText
		}
		pub := textnorm.UnixSecondsToTime(item.EditTime)
		if pub == nil {
			pub = textnorm.UnixSecondsToTime(item.CreateTime)
		}
		var authors []string
		if author := strings.TrimSpace(item.AuthorName); author != "" {
			authors = []string{author}
		}
		var rel *float64
		if item.RankingScore != nil && *item.RankingScore >= 0 && *item.RankingScore <= 1 {
			rel = textnorm.ClampUnitFloat(*item.RankingScore)
		}
		var extra map[string]any
		if req.IncludeExtra {
			extra = buildExtra(item)
		}
		mapped = append(mapped, core.Result{
			Title:     stripHighlightTags(item.Title),
			URL:       urlnorm.StripTrackingParams(item.URL),
			Authors:   authors,
			Published: pub,
			Summary:   cleanText,
			Content:   content,
			Relevance: rel,
			Authority: parseAuthorityLevel(item.AuthorityLevel),
			Extra:     extra,
		})
	}

	cleaned, warnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	allWarnings := append(localWarnings, warnings...)
	reqID := textnorm.FirstNonEmpty(wireResp.Data.SearchHashID, resp.RequestID)
	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      callCost,
		RequestID: reqID,
		Warnings:  allWarnings,
	}, nil
}

func stripHighlightTags(raw string) string {
	if !strings.Contains(raw, "<") {
		return raw
	}
	return highlightTagReplacer.Replace(raw)
}

func buildGlobalTimeFilter(f *core.Freshness) string {
	if f == nil {
		return ""
	}
	if !f.After.IsZero() && !f.Before.IsZero() && f.After.UTC().After(f.EndOfBeforeDay().UTC()) {
		return ""
	}
	var clauses []string
	if !f.After.IsZero() {
		clauses = append(clauses, fmt.Sprintf("publish_time>=%d", f.After.UTC().Unix()))
	}
	if !f.Before.IsZero() {
		clauses = append(clauses, fmt.Sprintf("publish_time<=%d", f.EndOfBeforeDay().UTC().Unix()))
	}
	return strings.Join(clauses, " AND ")
}

func buildEditTimeSortBy(f *core.Freshness) string {
	if f == nil || (f.After.IsZero() && f.Before.IsZero()) {
		return ""
	}
	if !f.After.IsZero() && !f.Before.IsZero() && f.After.UTC().After(f.EndOfBeforeDay().UTC()) {
		return ""
	}
	var minStr, maxStr string
	if !f.After.IsZero() && f.After.UTC().Unix() >= 0 {
		minStr = strconv.FormatInt(f.After.UTC().Unix(), 10)
	}
	if !f.Before.IsZero() && f.EndOfBeforeDay().UTC().Unix() >= 0 {
		maxStr = strconv.FormatInt(f.EndOfBeforeDay().UTC().Unix(), 10)
	}
	if minStr == "" && maxStr == "" {
		return ""
	}
	return fmt.Sprintf("EditTime:none:(%s,%s)", minStr, maxStr)
}

func validateSortBy(raw string) error {
	field, rest, hasColon := strings.Cut(raw, ":")
	switch field {
	case "CommentCount", "VoteUpCount", "EditTime", "CreateTime":
	default:
		return core.NewError(
			core.CodeInvalidArgument,
			fmt.Sprintf("invalid zhihu.sort field %q (valid: CommentCount, VoteUpCount, EditTime, CreateTime)", field),
		).WithProvider(ProviderName)
	}
	if !hasColon {
		return nil
	}
	dir, rangeExpr, hasRange := strings.Cut(rest, ":")
	switch strings.ToLower(strings.TrimSpace(dir)) {
	case "asc", "desc", "none":
	default:
		return core.NewError(
			core.CodeInvalidArgument,
			fmt.Sprintf("invalid zhihu.sort direction %q (valid: asc, desc, none)", dir),
		).WithProvider(ProviderName)
	}
	if hasRange {
		if err := validateSortRange(rangeExpr); err != nil {
			return err
		}
	}
	return nil
}

func validateSortRange(expr string) error {
	trimmed := strings.TrimSpace(expr)
	if len(trimmed) < 3 || trimmed[0] != '(' || trimmed[len(trimmed)-1] != ')' {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid zhihu.sort range %q (expected (min,max))", expr)).WithProvider(ProviderName)
	}
	inner := trimmed[1 : len(trimmed)-1]
	loStr, hiStr, ok := strings.Cut(inner, ",")
	if !ok || strings.Contains(hiStr, ",") {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid zhihu.sort range %q (expected (min,max))", expr)).WithProvider(ProviderName)
	}
	loStr = strings.TrimSpace(loStr)
	hiStr = strings.TrimSpace(hiStr)
	if loStr == "" && hiStr == "" {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid zhihu.sort range %q: at least one bound is required", expr)).WithProvider(ProviderName)
	}
	var loVal, hiVal int64
	if loStr != "" {
		v, err := strconv.ParseInt(loStr, 10, 64)
		if err != nil || v < 0 {
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid zhihu.sort range lower bound %q", loStr)).WithProvider(ProviderName)
		}
		loVal = v
	}
	if hiStr != "" {
		v, err := strconv.ParseInt(hiStr, 10, 64)
		if err != nil || v < 0 {
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid zhihu.sort range upper bound %q", hiStr)).WithProvider(ProviderName)
		}
		hiVal = v
	}
	if loStr != "" && hiStr != "" && loVal > hiVal {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid zhihu.sort range %q: min exceeds max", expr)).WithProvider(ProviderName)
	}
	return nil
}

func buildExtra(item searchWireItem) map[string]any {
	extra := make(map[string]any, 4)
	if ct := textnorm.CleanInline(item.ContentType); ct != "" {
		extra["content_type"] = ct
	}
	if item.VoteUpCount > 0 {
		extra["vote_up_count"] = item.VoteUpCount
	}
	if item.CommentCount > 0 {
		extra["comment_count"] = item.CommentCount
	}
	if badge := textnorm.CleanInline(item.AuthorBadgeText); badge != "" {
		extra["author_badge"] = badge
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}
