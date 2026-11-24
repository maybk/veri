package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"veri/internal/core"
	"veri/internal/spec"
)

// FetchBatch 有界并发保序抓取一组 URL（CLI 模式）；当仅含 1 个 URL 时委托单页 Fetch 以保持 Page 契约一致。
func (a *App) FetchBatch(ctx context.Context, urls []string, baseReq *core.FetchRequest) (*core.Envelope, error) {
	return a.runBatchFetch(ctx, urls, baseReq, spec.MaxBatchFetchURLs, false)
}

// FetchBatchMCP 有界并发保序抓取一组 URL（MCP 模式，复用进程内缓存与并发合并）；单 URL 委托 FetchMCP。
func (a *App) FetchBatchMCP(ctx context.Context, urls []string, baseReq *core.FetchRequest) (*core.Envelope, error) {
	return a.runBatchFetch(ctx, urls, baseReq, spec.MaxMCPBatchFetchURLs, true)
}

func (a *App) runBatchFetch(ctx context.Context, rawURLs []string, baseReq *core.FetchRequest, maxURLs int, useMCP bool) (*core.Envelope, error) {
	start := time.Now()
	cleaned := cleanBatchURLs(rawURLs)
	if len(cleaned) == 0 {
		se := core.NewError(core.CodeInvalidArgument, "at least one target URL is required").
			WithI18n("err.missing_fetch_url", "hint.missing_fetch_url")
		return &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     false,
			Kind:   spec.KindFetch,
			Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
			Error:  se.ToErrorInfo(),
		}, se
	}
	if maxURLs > 0 && len(cleaned) > maxURLs {
		se := core.NewError(core.CodeInvalidArgument, fmt.Sprintf("batch fetch received %d URLs, exceeding maximum %d", len(cleaned), maxURLs)).
			WithHint(fmt.Sprintf("Pass at most %d URLs per call.", maxURLs)).
			WithMsgKey("err.batch_max_urls").
			WithParam("count", fmt.Sprintf("%d", len(cleaned))).
			WithParam("max", fmt.Sprintf("%d", maxURLs))
		return &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     false,
			Kind:   spec.KindFetch,
			Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
			Error:  se.ToErrorInfo(),
		}, se
	}

	var tpl core.FetchRequest
	if baseReq != nil {
		tpl = *baseReq
	}
	if len(cleaned) == 1 {
		tpl.URL = cleaned[0]
		if useMCP {
			return a.FetchMCP(ctx, &tpl)
		}
		return a.Fetch(ctx, &tpl)
	}

	// 批量模式下将总字符预算均摊到单页（保留合理保底下限），防止多页叠加撑爆调用方上下文。
	perPageMax := tpl.MaxChars
	if useMCP && perPageMax <= 0 {
		perPageMax = a.loaded.Config.MCP.FetchMaxChars
	}
	if perPageMax > 0 {
		perPageMax = max(perPageMax/len(cleaned), spec.MinMCPFetchMaxChars)
	}

	subEnvs := make([]*core.Envelope, len(cleaned))
	subErrs := make([]error, len(cleaned))

	var g errgroup.Group
	g.SetLimit(spec.DefaultBatchFetchConcurrency)
	for idx, u := range cleaned {
		g.Go(func() error {
			defer func() {
				if rec := recover(); rec != nil {
					subEnvs[idx] = nil
					subErrs[idx] = a.ScrubCoreError(core.NewError(core.CodeInternal, fmt.Sprintf("batch fetch branch recovered from panic: %v", rec)))
				}
			}()
			itemReq := tpl
			itemReq.URL = u
			itemReq.MaxChars = perPageMax
			if useMCP {
				subEnvs[idx], subErrs[idx] = a.FetchMCP(ctx, &itemReq)
			} else {
				subEnvs[idx], subErrs[idx] = a.Fetch(ctx, &itemReq)
			}
			return nil
		})
	}
	_ = g.Wait()

	pages := make([]core.Page, len(cleaned))
	var (
		reports      []core.ProviderReport
		costs        []*core.Cost
		notes        []string
		anyOK        bool
		partial      bool
		fallbackUsed bool
		truncated    bool
		allCached    = true
		firstErr     *core.Error
		strategy     = string(tpl.Strategy)
	)
	if strategy == "" {
		strategy = a.loaded.Config.Fetch.Strategy
	}

	for i, u := range cleaned {
		sub := subEnvs[i]
		sErr := subErrs[i]
		if sub != nil {
			reports = append(reports, sub.Meta.Providers...)
			for j := range sub.Meta.Cost {
				c := sub.Meta.Cost[j]
				costs = append(costs, &c)
			}
			notes = append(notes, sub.Meta.Notes...)
			if sub.Meta.FallbackUsed {
				fallbackUsed = true
			}
			if sub.Meta.Truncated {
				truncated = true
			}
			if !sub.Meta.Cached {
				allCached = false
			}
		} else {
			allCached = false
		}

		if sErr == nil && sub != nil && sub.OK && sub.Page != nil {
			anyOK = true
			pages[i] = *sub.Page.Clone()
			continue
		}

		partial = true
		se := a.ScrubCoreError(core.AsError(sErr))
		if sErr == nil && sub != nil && sub.Error != nil {
			se = a.ScrubCoreError(&core.Error{
				Code:      sub.Error.Code,
				Provider:  sub.Error.Provider,
				Message:   sub.Error.Message,
				Hint:      sub.Error.Hint,
				Retryable: sub.Error.Retryable,
				Status:    sub.Error.Status,
			})
		}
		if firstErr == nil {
			firstErr = se
		}
		pages[i] = core.Page{
			URL:   a.ScrubText(u),
			Error: se.ToErrorInfo(),
		}
	}

	if reports == nil {
		reports = []core.ProviderReport{}
	}
	batchCached := anyOK && allCached
	durationMS := time.Since(start).Milliseconds()
	if batchCached {
		durationMS = 0
	}
	env := &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     anyOK,
		Kind:   spec.KindFetch,
		Pages:  pages,
		Meta: core.Meta{
			Strategy:     strategy,
			Providers:    reports,
			FallbackUsed: fallbackUsed,
			Partial:      anyOK && partial,
			Truncated:    truncated,
			Notes:        dedupStrings(notes),
			Cost:         core.SumCosts(costs),
			DurationMS:   durationMS,
			Cached:       batchCached,
		},
	}
	if !anyOK {
		if firstErr == nil {
			firstErr = core.NewError(core.CodeContentUnavailable, "all URLs in batch fetch failed")
		}
		env.Error = firstErr.ToErrorInfo()
		return env, firstErr
	}
	return env, nil
}

func cleanBatchURLs(raw []string) []string {
	seen := make(map[string]struct{}, len(raw))
	var out []string
	for _, item := range raw {
		u := strings.TrimSpace(item)
		if u == "" {
			continue
		}
		if _, dup := seen[u]; !dup {
			seen[u] = struct{}{}
			out = append(out, u)
		}
	}
	return out
}

func dedupStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	var out []string
	for _, s := range items {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; !dup {
			seen[s] = struct{}{}
			out = append(out, s)
		}
	}
	return out
}
