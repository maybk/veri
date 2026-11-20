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
	req.Filters = core.Filters{
		Sites:        urlnorm.CleanDomainFilters(req.Filters.Sites),
		ExcludeSites: urlnorm.CleanDomainFilters(req.Filters.ExcludeSites),
		Lang:         urlnorm.CleanLangFilters(req.Filters.Lang),
		Freshness:    req.Filters.Freshness,
	}
	return req, nil
}

// selectSearchChain 选择生效检索链与最终路由策略（含可选纯英文 providers_en 分池）。
func (r *Router) selectSearchChain(req *core.SearchRequest) ([]core.Searcher, core.Strategy, error) {
	strategy := req.Strategy
	if len(req.Providers) == 1 {
		strategy = core.StrategySingle
	}

	var candidateNames []string
	explicit := len(req.Providers) > 0
	if explicit {
		candidateNames = req.Providers
	} else if strategy == core.StrategyFusion && len(r.opts.FusionProviders) > 0 {
		candidateNames = r.opts.FusionProviders
	} else if len(r.opts.SearchProvidersEN) > 0 && isEnglishEligibleQuery(req.Query, req.Filters.Lang) {
		candidateNames = r.opts.SearchProvidersEN
	} else {
		candidateNames = r.opts.SearchProviders
	}

	var chain []core.Searcher
	seen := make(map[string]struct{})
	for _, rawName := range candidateNames {
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
		if explicit && len(req.Providers) == 1 {
			return nil, strategy, core.NewError(core.CodeNoProvider, fmt.Sprintf("provider %q is not enabled or has no configured API key", name)).
				WithHint("Verify provider name or save its API key via `veri key set <provider>`.")
		}
	}

	if len(chain) == 0 {
		if explicit {
			return nil, strategy, core.NewError(core.CodeNoProvider, fmt.Sprintf("requested search provider(s) %s are not enabled or have no configured API key", strings.Join(req.Providers, ", "))).
				WithHint("Verify provider names or save an API key via `veri key set <provider>`.")
		}
		return nil, strategy, core.NewError(core.CodeNoProvider, "no search provider is enabled").
			WithHint("Configure a search provider via `veri key set <provider>` (see `veri providers`), or set its API key environment variable.")
	}

	if strategy == core.StrategyFusion && !explicit && len(r.opts.FusionProviders) == 0 && len(chain) > spec.DefaultFusionAutoLimit {
		chain = chain[:spec.DefaultFusionAutoLimit]
	}
	if !explicit && strategy == core.StrategyAuto {
		chain = orderByHealth(r.health, chain)
	}
	return chain, strategy, nil
}

func (r *Router) searchChain(ctx context.Context, req *core.SearchRequest, chain []core.Searcher, strategy core.Strategy, start time.Time) (*core.Envelope, error) {
	var reports []core.ProviderReport
	var costs []*core.Cost
	var notes []string
	var lastErr *core.Error
	nativeEmptyCount := 0

	for idx, s := range chain {
		plan := matchCapabilities(resolveCapabilities(s, req), req, req.StrictFilters)
		if plan.unsupportedReason != "" {
			if strategy == core.StrategySingle {
				se := core.NewError(core.CodeInvalidArgument, fmt.Sprintf("provider %s does not natively support %q", s.Name(), plan.unsupportedReason)).
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
		notes = append(notes, plan.warnings...)
		notes = append(notes, out.Warnings...)
		notes = append(notes, emuNotes...)

		repStatus := core.ProviderStatusOK
		if len(results) == 0 {
			repStatus = core.ProviderStatusEmpty
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
			nativeEmptyCount++
			if nativeEmptyCount >= searchEmptyQuorum && idx < len(chain)-1 && strategy != core.StrategySingle && r.opts.FallbackOnEmpty {
				emptyQuorumReached = true
				notes = append(notes, fmt.Sprintf("stopped empty fallback after %d native-filter providers returned 0 results", nativeEmptyCount))
			}
		}

		if len(results) > 0 || emptyQuorumReached || strategy == core.StrategySingle || !r.opts.FallbackOnEmpty || idx == len(chain)-1 {
			finalResults, truncated, budgetNotes := applyFieldCapsAndBudget(results, r.opts.SummaryMaxRunes, r.opts.ContentMaxRunes, req.MaxChars)
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

	if lastErr == nil {
		lastErr = core.NewError(core.CodeNoProvider, "no provider matched requested filter capabilities").
			WithHint("Disable strict filter mode or relax filter conditions.").
			WithHintKey("hint.strict_filter_empty")
	} else {
		lastErr = summarizeChainFailure("all search providers failed", reports, lastErr)
	}
	return buildSearchErrorEnvelope(req.Query, string(strategy), reports, len(reports) > 1, costs, start, lastErr), lastErr
}

func isEnglishEligibleQuery(query string, langs []string) bool {
	for _, l := range langs {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(l)), "zh") {
			return false
		}
	}
	for _, r := range query {
		if unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) || unicode.Is(unicode.Hangul, r) {
			return false
		}
	}
	return true
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
