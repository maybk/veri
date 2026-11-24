package router

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"veri/internal/core"
	"veri/internal/spec"
)

type branchResult struct {
	chainIdx int
	name     string
	report   core.ProviderReport
	results  []core.Result
	notes    []string
	err      *core.Error
}

// searchFusion 并发调用融合池中的所有 Searcher（单个失败不取消其余请求），支持免加价最小召回深度与慢节点宽限截断，并执行 RRF 融合去重。
func (r *Router) searchFusion(ctx context.Context, req *core.SearchRequest, chain []core.Searcher, start time.Time) (*core.Envelope, error) {
	branches := make([]branchResult, len(chain))
	fusionCtx, cancelFusion := context.WithCancel(ctx)
	defer cancelFusion()

	groupSet := make(map[string]struct{}, len(chain))
	for _, s := range chain {
		groupSet[providerGroup(s.Name())] = struct{}{}
	}
	totalGroups := len(groupSet)

	var (
		g              errgroup.Group
		graceMu        sync.Mutex
		graceTimer     *time.Timer
		nonEmptyGroups = make(map[string]struct{}, totalGroups)
		quorum         = max(2, totalGroups-1)
	)

	branchBaseReq := *req
	if branchBaseReq.Count < defaultFusionFetchFloor {
		branchBaseReq.Count = defaultFusionFetchFloor
	}

	for idx, s := range chain {
		g.Go(func() error {
			defer func() {
				if rec := recover(); rec != nil {
					se := core.NewError(core.CodeInternal, fmt.Sprintf("search branch recovered from panic: %v", rec)).WithProvider(s.Name())
					branches[idx] = branchResult{
						chainIdx: idx,
						name:     s.Name(),
						err:      se,
						report: core.ProviderReport{
							Name:   s.Name(),
							Status: core.ProviderStatusError,
							Error:  se.ToErrorInfo(),
						},
					}
				}
			}()

			plan := matchCapabilities(resolveCapabilities(s, &branchBaseReq), &branchBaseReq, req.StrictFilters)
			if plan.unsupportedReason != "" {
				branches[idx] = branchResult{
					chainIdx: idx,
					name:     s.Name(),
					notes:    []string{fmt.Sprintf("capability_mismatch: skipped %s (%s)", s.Name(), plan.unsupportedReason)},
					report: core.ProviderReport{
						Name:   s.Name(),
						Status: core.ProviderStatusSkipped,
					},
				}
				return nil
			}

			callStart := time.Now()
			out, err := s.Search(fusionCtx, &plan.adapted)
			latency := time.Since(callStart).Milliseconds()

			if err != nil {
				se := core.AsError(err)
				if se.Provider == "" {
					se = se.WithProvider(s.Name())
				}
				stragglerCanceled := fusionCtx.Err() != nil && ctx.Err() == nil && (se.Code == core.CodeUpstreamTimeout || errors.Is(err, context.Canceled))
				if !stragglerCanceled {
					r.health.recordError(s.Name(), se)
				}
				status := core.ProviderStatusError
				if se.Code == core.CodeUpstreamTimeout || errors.Is(fusionCtx.Err(), context.DeadlineExceeded) || stragglerCanceled {
					status = core.ProviderStatusTimeout
				}
				branches[idx] = branchResult{
					chainIdx: idx,
					name:     s.Name(),
					err:      se,
					report: core.ProviderReport{
						Name:      s.Name(),
						Status:    status,
						LatencyMS: latency,
						Degraded:  plan.degraded,
						Error:     se.ToErrorInfo(),
					},
				}
				return nil
			}

			r.health.recordSuccess(s.Name())
			filtered, emuNotes := applyEmulation(plan, out.Results)
			if len(filtered) > 0 && totalGroups >= 2 {
				group := providerGroup(s.Name())
				graceMu.Lock()
				nonEmptyGroups[group] = struct{}{}
				if len(nonEmptyGroups) >= quorum && graceTimer == nil {
					grace := r.stragglerGrace
					if grace <= 0 {
						grace = defaultFusionStragglerGrace
					}
					graceTimer = time.AfterFunc(grace, cancelFusion)
				}
				graceMu.Unlock()
			}
			repStatus := core.ProviderStatusOK
			if len(filtered) == 0 {
				repStatus = core.ProviderStatusEmpty
			}
			var branchNotes []string
			branchNotes = append(branchNotes, plan.warnings...)
			branchNotes = append(branchNotes, out.Warnings...)
			branchNotes = append(branchNotes, emuNotes...)

			branches[idx] = branchResult{
				chainIdx: idx,
				name:     s.Name(),
				results:  filtered,
				notes:    branchNotes,
				report: core.ProviderReport{
					Name:      s.Name(),
					Status:    repStatus,
					Results:   len(filtered),
					LatencyMS: latency,
					Cost:      out.Cost,
					Degraded:  plan.degraded,
					Warnings:  slices.Concat(plan.warnings, out.Warnings),
					RequestID: out.RequestID,
				},
			}
			return nil
		})
	}
	_ = g.Wait()
	graceMu.Lock()
	if graceTimer != nil {
		graceTimer.Stop()
	}
	graceMu.Unlock()

	reports := make([]core.ProviderReport, 0, len(branches))
	var costs []*core.Cost
	var allNotes []string
	succeeded := 0
	skipped := 0
	partial := false
	var lastErr *core.Error

	for _, br := range branches {
		reports = append(reports, br.report)
		if br.report.Cost != nil {
			c := *br.report.Cost
			costs = append(costs, &c)
		}
		allNotes = append(allNotes, br.notes...)
		switch br.report.Status {
		case core.ProviderStatusOK, core.ProviderStatusEmpty:
			succeeded++
		case core.ProviderStatusSkipped:
			skipped++
		case core.ProviderStatusTimeout, core.ProviderStatusError:
			partial = true
			if br.err != nil {
				lastErr = br.err
			}
		}
	}

	if succeeded == 0 {
		if lastErr == nil {
			if skipped > 0 && skipped == len(branches) {
				lastErr = noMatchedProviderError(req.StrictFilters)
			} else {
				lastErr = core.NewError(core.CodeNoProvider, "no fusion provider succeeded")
			}
		} else {
			lastErr = summarizeChainFailure("all fusion providers failed", reports, lastErr)
		}
		return buildSearchErrorEnvelope(req.Query, string(core.StrategyFusion), reports, false, costs, start, lastErr), lastErr
	}

	fused, deduped := fuseRRF(branches, r.opts.FusionK, r.weightFor, req.Count)
	if deduped > 0 {
		allNotes = append(allNotes, formatContentDedupNote(deduped))
	}
	finalResults, truncated, budgetNotes := applyFieldCapsAndBudget(fused, r.opts.SummaryMaxChars, r.opts.ContentMaxChars, req.MaxChars)
	if finalResults == nil {
		finalResults = []core.Result{}
	}
	allNotes = append(allNotes, budgetNotes...)

	return &core.Envelope{
		Schema:  spec.SchemaVersion,
		OK:      true,
		Kind:    spec.KindSearch,
		Query:   req.Query,
		Results: finalResults,
		Meta: core.Meta{
			Strategy:   string(core.StrategyFusion),
			Providers:  reports,
			Partial:    partial,
			Truncated:  truncated,
			Notes:      allNotes,
			Cost:       core.SumCosts(costs),
			DurationMS: time.Since(start).Milliseconds(),
		},
	}, nil
}
