package parallel

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/spec"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

// Search 调用 POST /v1/search 执行检索（显式指定 mode 防默认触发 5 倍价 advanced，遵循 objective 与 search_queries 最佳实践）。
func (p *Provider) Search(ctx context.Context, req *core.SearchRequest) (*core.SearchOutcome, error) {
	if req == nil {
		return nil, core.NewError(core.CodeInvalidArgument, "search request must not be nil").WithProvider(ProviderName)
	}
	query := strings.TrimSpace(req.Query)
	if query == "" {
		return nil, core.NewError(core.CodeInvalidArgument, "query must not be empty").WithProvider(ProviderName)
	}

	mode := p.mode
	if rawMode := textnorm.FirstNonEmpty(req.Options[spec.OptParallelMode], req.Options[spec.OptParallelModeAlias]); rawMode != "" {
		var err error
		mode, err = normalizeMode(rawMode)
		if err != nil {
			return nil, err
		}
	}
	location, err := normalizeLocation(textnorm.FirstNonEmpty(req.Options[spec.OptParallelLocation], req.Options[spec.OptParallelLocationAlias]))
	if err != nil {
		return nil, err
	}

	policy, err := buildSourcePolicy(req.Filters, mode == spec.ParallelModeTurbo)
	if err != nil {
		return nil, err
	}

	wireReq := searchWireReq{
		SearchQueries: []string{query},
		Mode:          mode,
		AdvancedSettings: &advancedSearchSettings{
			MaxResults:   spec.ClampSearchCount(req.Count, maxSearchCount),
			SourcePolicy: policy,
			Location:     location,
		},
	}
	// 官方明确警告切勿将与唯一 search_queries 相同的文本原样塞入 objective。
	if q := strings.TrimSpace(req.Question); q != "" && !strings.EqualFold(q, query) {
		wireReq.Objective = q
	}

	resp, err := p.client.DoJSON(ctx, p.pool, p.gate, httpx.RequestSpec{
		ProviderName: ProviderName,
		Method:       http.MethodPost,
		URL:          p.baseURL + searchEndpointPath,
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
		return nil, core.WrapError(core.CodeUpstreamBadResponse, "failed to decode Parallel search JSON", err).WithProvider(ProviderName)
	}

	mapped := make([]core.Result, 0, len(wireResp.Results))
	for _, item := range wireResp.Results {
		joined := joinExcerpts(item.Excerpts)
		var content string
		if req.Detail == core.DetailContent {
			content = joined
		}
		mapped = append(mapped, core.Result{
			Title:     item.Title,
			URL:       item.URL,
			Published: textnorm.ParseFlexibleTime(item.PublishDate, time.UTC),
			Summary:   joined,
			Content:   content,
		})
	}

	cleaned, warnings := core.SanitizeResults(ProviderName, mapped, req.Detail, req.IncludeExtra, urlnorm.Key)
	warnings = appendWireWarnings(warnings, wireResp.Warnings)

	var cost *core.Cost
	if len(wireResp.Results) > 0 {
		cost = core.CloneCost(p.searchCost)
	}
	return &core.SearchOutcome{
		Results:   cleaned,
		Cost:      cost,
		RequestID: textnorm.FirstNonEmpty(wireResp.SearchID, resp.RequestID),
		Warnings:  warnings,
	}, nil
}

func buildSourcePolicy(filters core.Filters, hostOnly bool) (*sourcePolicy, error) {
	// 先执行域名/路径规范化（剥离协议、端口、查询串与前导 www.），再按需执行 turbo 纯主机名收窄，
	// 避免直接传入带 "https://" 前缀的过滤项时被按 "/" 误切为 "https:"。
	inc := normalizeSourceDomains(filters.Sites)
	exc := normalizeSourceDomains(filters.ExcludeSites)
	if hostOnly {
		inc = urlnorm.ExtractFilterHosts(inc)
		exc, _ = urlnorm.SplitHostAndPathFilters(exc)
	}
	if len(inc)+len(exc) > maxTotalSourceDomains {
		return nil, core.NewError(
			core.CodeInvalidArgument,
			fmt.Sprintf("source_policy exceeds combined limit of %d domains (include=%d, exclude=%d)", maxTotalSourceDomains, len(inc), len(exc)),
		).WithProvider(ProviderName)
	}
	for _, entry := range inc {
		if slices.Contains(exc, entry) {
			return nil, core.NewError(
				core.CodeInvalidArgument,
				fmt.Sprintf("source_policy conflict: %q appears in both include_domains and exclude_domains", entry),
			).WithProvider(ProviderName)
		}
	}

	var afterDate string
	if filters.Freshness != nil && !filters.Freshness.After.IsZero() {
		afterDate = filters.Freshness.After.UTC().Format("2006-01-02")
	}
	if len(inc) == 0 && len(exc) == 0 && afterDate == "" {
		return nil, nil
	}
	return &sourcePolicy{
		IncludeDomains: inc,
		ExcludeDomains: exc,
		AfterDate:      afterDate,
	}, nil
}

// normalizeSourceDomains 规整 Parallel source_policy 域名与路径条目：
// 剥离协议、端口、查询串、锚点、前导 www. 与主机尾点，host 小写化并严格保留 path 大小写敏感性。
func normalizeSourceDomains(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, raw := range items {
		p := strings.TrimSpace(raw)
		if idx := strings.Index(p, "://"); idx >= 0 {
			scheme := strings.ToLower(p[:idx])
			if scheme == "http" || scheme == "https" {
				p = p[idx+3:]
			}
		}
		if cut := strings.IndexAny(p, "?#"); cut >= 0 {
			p = p[:cut]
		}
		// 剥离不支持的 "*." 通配前缀：单段 TLD（如 "*.gov"）保留前导 "."，普通域名（如 "*.example.com"）转为裸域。
		if trimmed, hadWildcard := strings.CutPrefix(p, "*."); hadWildcard {
			if trimmed != "" && !strings.Contains(trimmed, ".") && !strings.Contains(trimmed, "/") {
				p = "." + trimmed
			} else {
				p = trimmed
			}
		}
		p = strings.TrimRight(p, "/")
		if p == "" {
			continue
		}
		hostPart, pathPart, hasPath := strings.Cut(p, "/")
		hostPart = strings.ToLower(strings.TrimSpace(hostPart))
		if h, _, err := net.SplitHostPort(hostPart); err == nil {
			hostPart = h
		}
		hostPart = strings.TrimPrefix(hostPart, "www.")
		hostPart = strings.TrimSuffix(hostPart, ".")
		if hostPart == "" {
			continue
		}
		entry := hostPart
		if hasPath {
			if cleanedPath := strings.Trim(pathPart, "/"); cleanedPath != "" {
				entry = hostPart + "/" + cleanedPath
			}
		}
		if !slices.Contains(out, entry) {
			out = append(out, entry)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func joinExcerpts(excerpts []string) string {
	if len(excerpts) == 0 {
		return ""
	}
	parts := make([]string, 0, len(excerpts))
	for _, raw := range excerpts {
		if cleaned := textnorm.CleanPlainText(raw); cleaned != "" {
			parts = append(parts, cleaned)
		}
	}
	return strings.Join(parts, "\n\n")
}

func appendWireWarnings(dst []string, wireWarnings []wireWarning) []string {
	for _, w := range wireWarnings {
		msg := textnorm.CleanInline(w.Message)
		if msg == "" {
			continue
		}
		dst = append(dst, "parallel: "+msg)
	}
	return dst
}
