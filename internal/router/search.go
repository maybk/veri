package router

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"veri/internal/core"
	"veri/internal/spec"
	"veri/internal/urlnorm"
)

// Search 执行检索路由（single / auto / fusion），始终返回包含完整执行轨迹 Meta 的 Envelope。
func (r *Router) Search(ctx context.Context, rawReq *core.SearchRequest) (*core.Envelope, error) {
	start := time.Now()
	req, err := r.normalizeSearchRequest(rawReq)
	if err != nil {
		se := core.AsError(err)
		return buildSearchErrorEnvelope(rawQuery(rawReq), string(r.opts.DefaultStrategy), nil, false, nil, start, se), se
	}

	chain, strategy, chainErr := r.selectSearchChain(&req)
	if chainErr != nil {
		se := core.AsError(chainErr)
		return buildSearchErrorEnvelope(req.Query, string(strategy), nil, false, nil, start, se), se
	}
	if r.logger != nil {
		r.logger.Debug("router.search",
			"strategy", string(strategy),
			"providers", providerNames(chain),
		)
	}

	timeout := r.opts.SearchTimeout
	if req.Timeout > 0 {
		timeout = req.Timeout
	}
	overallCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	switch strategy {
	case core.StrategyFusion:
		return r.searchFusion(overallCtx, &req, chain, start)
	case core.StrategySingle:
		return r.searchChain(overallCtx, &req, chain, core.StrategySingle, start)
	default:
		return r.searchChain(overallCtx, &req, chain, core.StrategyAuto, start)
	}
}

// ValidateSearchRequest 校验并归一化检索请求（不执行上游调用，供应用层查缓存前复核参数合法性）。
func (r *Router) ValidateSearchRequest(rawReq *core.SearchRequest) (core.SearchRequest, error) {
	return r.normalizeSearchRequest(rawReq)
}

func (r *Router) normalizeSearchRequest(rawReq *core.SearchRequest) (core.SearchRequest, error) {
	if rawReq == nil {
		return core.SearchRequest{}, core.NewError(core.CodeInvalidArgument, "search request must not be nil")
	}
	req := *rawReq
	req.Query = strings.TrimSpace(req.Query)
	if req.Query == "" {
		return core.SearchRequest{}, core.NewError(core.CodeInvalidArgument, "query must not be empty").
			WithMsgKey("err.empty_query")
	}
	if req.Count == 0 {
		req.Count = r.opts.DefaultCount
	}
	if req.Count < spec.MinSearchCount || req.Count > spec.MaxSearchCount {
		return core.SearchRequest{}, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("count %d out of valid range [%d, %d]", req.Count, spec.MinSearchCount, spec.MaxSearchCount)).
			WithMsgKey("err.invalid_count").
			WithParam("count", fmt.Sprintf("%d", req.Count)).
			WithParam("min", fmt.Sprintf("%d", spec.MinSearchCount)).
			WithParam("max", fmt.Sprintf("%d", spec.MaxSearchCount))
	}
	detail, err := core.ParseDetail(string(req.Detail))
	if err != nil {
		return core.SearchRequest{}, err
	}
	if req.Detail == "" {
		detail = r.opts.DefaultDetail
	}
	req.Detail = detail

	strategy, err := core.ParseStrategy(string(req.Strategy))
	if err != nil {
		return core.SearchRequest{}, err
	}
	if req.Strategy == "" {
		strategy = r.opts.DefaultStrategy
	}
	req.Strategy = strategy
	if r.opts.StrictFilters {
		req.StrictFilters = true
	}
	if req.MaxChars < 0 {
		return core.SearchRequest{}, core.NewError(core.CodeInvalidArgument, "max_chars must be >= 0").
			WithMsgKey("err.invalid_max_chars")
	}
	if req.MaxChars == 0 {
		req.MaxChars = r.opts.SearchMaxChars
	}
	req.Providers = cleanProviderKeys(req.Providers)
	req.Options = spec.NormalizeOptions(req.Options)
	req.Filters = core.Filters{
		Sites:        urlnorm.CleanDomainFilters(req.Filters.Sites),
		ExcludeSites: urlnorm.CleanDomainFilters(req.Filters.ExcludeSites),
		Lang:         urlnorm.CleanLangFilters(req.Filters.Lang),
		Freshness:    req.Filters.Freshness,
	}
	return req, nil
}

// selectSearchChain 选择生效检索链与最终路由策略（含非中文 providers_global 与 fusion_providers_global 分池）。
func (r *Router) selectSearchChain(req *core.SearchRequest) ([]core.Searcher, core.Strategy, error) {
	strategy := req.Strategy
	if len(req.Providers) == 1 {
		strategy = core.StrategySingle
	}

	explicit := len(req.Providers) > 0
	var chain []core.Searcher
	var err error
	if explicit {
		chain, err = r.resolveSearchers(req.Providers, len(req.Providers) == 1)
		if err != nil {
			return nil, strategy, err
		}
	} else if strategy == core.StrategyFusion {
		chain = r.resolveFusionChain(isNonChineseQuery(req.Query, req.Filters.Lang), req)
	} else if len(r.opts.SearchProvidersGlobal) > 0 && isNonChineseQuery(req.Query, req.Filters.Lang) {
		chain, _ = r.resolveSearchers(r.opts.SearchProvidersGlobal, false)
		if len(chain) == 0 {
			chain, _ = r.resolveSearchers(r.opts.SearchProviders, false)
		}
	} else {
		chain, _ = r.resolveSearchers(r.opts.SearchProviders, false)
	}

	if len(chain) == 0 {
		if explicit {
			return nil, strategy, core.NewError(core.CodeNoProvider, fmt.Sprintf("requested search provider(s) %s are not enabled or have no configured API key", strings.Join(req.Providers, ", "))).
				WithHint("Verify provider names or save an API key via `veri key set <provider>`.")
		}
		return nil, strategy, core.NewError(core.CodeNoProvider, "no search provider is enabled").
			WithHint("Configure a search provider via `veri key set <provider>` (see `veri providers`), or set its API key environment variable.")
	}

	if !explicit && strategy == core.StrategyAuto {
		chain = orderAutoSearchers(chain, r.health, req)
	}
	return chain, strategy, nil
}

func (r *Router) resolveSearchers(names []string, strictSingle bool) ([]core.Searcher, error) {
	var chain []core.Searcher
	seen := make(map[string]struct{}, len(names))
	for _, rawName := range names {
		name := canonicalProviderKey(rawName)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		if s, ok := r.searchers[name]; ok {
			chain = append(chain, s)
			continue
		}
		if strictSingle {
			return nil, core.NewError(core.CodeNoProvider, fmt.Sprintf("provider %q is not enabled or has no configured API key", name)).
				WithHint("Verify provider name or save its API key via `veri key set <provider>`.")
		}
	}
	return chain, nil
}

func (r *Router) searchChain(ctx context.Context, req *core.SearchRequest, chain []core.Searcher, strategy core.Strategy, start time.Time) (*core.Envelope, error) {
	var reports []core.ProviderReport
	var costs []*core.Cost
	var notes []string
	var lastErr *core.Error
	var nativeEmptyGroups map[string]struct{}
	hadEmptySuccess := false
	hadFallbackErr := false

	for idx, s := range chain {
		plan := matchCapabilities(resolveCapabilities(s, req), req, req.StrictFilters)
		if plan.unsupportedReason != "" {
			if strategy == core.StrategySingle {
				msg := fmt.Sprintf("provider %s does not natively support %q", s.Name(), plan.unsupportedReason)
				if plan.unsupportedReason == reasonSiteScope {
					msg = fmt.Sprintf("provider %s cannot match the requested site filter scope", s.Name())
				}
				se := core.NewError(core.CodeInvalidArgument, msg).
					WithProvider(s.Name())
				reports = append(reports, core.ProviderReport{
					Name:   s.Name(),
					Status: core.ProviderStatusSkipped,
					Error:  se.ToErrorInfo(),
				})
				return buildSearchErrorEnvelope(req.Query, string(strategy), reports, idx > 0, costs, start, se), se
			}
			notes = append(notes, fmt.Sprintf("capability_mismatch: skipped %s (%s)", s.Name(), plan.unsupportedReason))
			reports = append(reports, core.ProviderReport{
				Name:   s.Name(),
				Status: core.ProviderStatusSkipped,
			})
			continue
		}

		callStart := time.Now()
		out, err := s.Search(ctx, &plan.adapted)
		latency := time.Since(callStart).Milliseconds()

		if err != nil {
			se := core.AsError(err)
			if se.Provider == "" {
				se = se.WithProvider(s.Name())
			}
			r.health.recordError(s.Name(), se)
			lastErr = se
			hadFallbackErr = true
			status := core.ProviderStatusError
			if se.Code == core.CodeUpstreamTimeout {
				status = core.ProviderStatusTimeout
			}
			reports = append(reports, core.ProviderReport{
				Name:      s.Name(),
				Status:    status,
				LatencyMS: latency,
				Degraded:  plan.degraded,
				Error:     se.ToErrorInfo(),
			})
			if strategy == core.StrategySingle || !core.ShouldFallback(se) || ctx.Err() != nil {
				return buildSearchErrorEnvelope(req.Query, string(strategy), reports, idx > 0, costs, start, se), se
			}
			continue
		}

		r.health.recordSuccess(s.Name())

		if out.Cost != nil {
			c := *out.Cost
			costs = append(costs, &c)
		}
		results, emuNotes := applyEmulation(plan, out.Results)
		if strategy == core.StrategyAuto && len(results) > 1 {
			var deduped int
			results, deduped = dedupResultsByContent(results)
			if deduped > 0 {
				notes = append(notes, formatContentDedupNote(deduped))
			}
		}
		notes = append(notes, plan.warnings...)
		notes = append(notes, out.Warnings...)
		notes = append(notes, emuNotes...)

		repStatus := core.ProviderStatusOK
		if len(results) == 0 {
			repStatus = core.ProviderStatusEmpty
			hadEmptySuccess = true
		}
		reports = append(reports, core.ProviderReport{
			Name:      s.Name(),
			Status:    repStatus,
			Results:   len(results),
			LatencyMS: latency,
			Cost:      out.Cost,
			Degraded:  plan.degraded,
			Warnings:  slices.Concat(plan.warnings, out.Warnings),
			RequestID: out.RequestID,
		})

		emptyQuorumReached := false
		if len(results) == 0 && len(plan.degraded) == 0 {
			if nativeEmptyGroups == nil {
				nativeEmptyGroups = make(map[string]struct{}, searchEmptyQuorum)
			}
			nativeEmptyGroups[providerGroup(s.Name())] = struct{}{}
			if len(nativeEmptyGroups) >= searchEmptyQuorum && idx < len(chain)-1 && strategy != core.StrategySingle && r.opts.FallbackOnEmpty {
				emptyQuorumReached = true
				notes = append(notes, fmt.Sprintf("stopped empty fallback after %d native-filter providers returned 0 results", len(nativeEmptyGroups)))
			}
		}

		if len(results) > 0 || emptyQuorumReached || strategy == core.StrategySingle || !r.opts.FallbackOnEmpty || idx == len(chain)-1 {
			finalResults, truncated, budgetNotes := applyFieldCapsAndBudget(results, r.opts.SummaryMaxChars, r.opts.ContentMaxChars, req.MaxChars)
			if finalResults == nil {
				finalResults = []core.Result{}
			}
			notes = append(notes, budgetNotes...)
			return &core.Envelope{
				Schema:  spec.SchemaVersion,
				OK:      true,
				Kind:    spec.KindSearch,
				Query:   req.Query,
				Results: finalResults,
				Meta: core.Meta{
					Strategy:     string(strategy),
					Providers:    reports,
					FallbackUsed: len(reports) > 1,
					Truncated:    truncated,
					Notes:        notes,
					Cost:         core.SumCosts(costs),
					DurationMS:   time.Since(start).Milliseconds(),
				},
			}, nil
		}
	}

	if hadEmptySuccess {
		return &core.Envelope{
			Schema:  spec.SchemaVersion,
			OK:      true,
			Kind:    spec.KindSearch,
			Query:   req.Query,
			Results: []core.Result{},
			Meta: core.Meta{
				Strategy:     string(strategy),
				Providers:    reports,
				FallbackUsed: len(reports) > 1,
				Partial:      hadFallbackErr,
				Notes:        notes,
				Cost:         core.SumCosts(costs),
				DurationMS:   time.Since(start).Milliseconds(),
			},
		}, nil
	}

	if lastErr == nil {
		lastErr = noMatchedProviderError(req.StrictFilters)
	} else {
		lastErr = summarizeChainFailure("all search providers failed", reports, lastErr)
	}
	return buildSearchErrorEnvelope(req.Query, string(strategy), reports, len(reports) > 1, costs, start, lastErr), lastErr
}

// isNonChineseQuery 判断当前查询是否应走非中文检索链（search.providers_global）。
// 显式指定语言过滤时以 lang 为准（含中文语言码走中文主链，否则走非中文链）；
// 未指定时按脚本特征判断：含日文假名或韩文谚文判为非中文，含汉字判为中文，其余（英文及其他语种）判为非中文。
func isNonChineseQuery(query string, langs []string) bool {
	hasExplicitLang := false
	for _, raw := range langs {
		l := strings.ToLower(strings.TrimSpace(raw))
		if l == "" {
			continue
		}
		hasExplicitLang = true
		if l == "zh" || l == "zho" || l == "chinese" || strings.HasPrefix(l, "zh-") || strings.HasPrefix(l, "zh_") {
			return false
		}
	}
	if hasExplicitLang {
		return true
	}
	hasHan := false
	for _, r := range query {
		switch {
		case unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r):
			return true
		case unicode.Is(unicode.Han, r):
			hasHan = true
		}
	}
	return !hasHan
}

func rawQuery(req *core.SearchRequest) string {
	if req == nil {
		return ""
	}
	return req.Query
}

func buildSearchErrorEnvelope(query, strategy string, reports []core.ProviderReport, fallback bool, costs []*core.Cost, start time.Time, se *core.Error) *core.Envelope {
	if reports == nil {
		reports = []core.ProviderReport{}
	}
	return &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     false,
		Kind:   spec.KindSearch,
		Query:  query,
		Meta: core.Meta{
			Strategy:     strategy,
			Providers:    reports,
			FallbackUsed: fallback,
			Cost:         core.SumCosts(costs),
			DurationMS:   time.Since(start).Milliseconds(),
		},
		Error: se.ToErrorInfo(),
	}
}
