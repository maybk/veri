package router

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

func (r *Router) runFetchSequence(ctx context.Context, req *core.FetchRequest, seq []core.Fetcher, policy fetchExecPolicy, start time.Time) (*core.Envelope, error) {
	var reports []core.ProviderReport
	var costs []*core.Cost
	notes := slices.Clone(policy.initialNotes)
	var lastErr *core.Error
	var lowQualityFallback *core.Page
	var lowQualityReason string
	var lastWallProvider string
	var lastWallReason string
	notFoundCount := 0
	localTried := false

	workReq := *req
	for idx := range seq {
		f := seq[idx]
		if f.Name() == spec.ProviderLocal {
			localTried = true
		}

		callCtx := ctx
		var cancelProbe context.CancelFunc
		if f.Name() == spec.ProviderLocal && !policy.localPinned && !policy.explicitProviders && idx < len(seq)-1 {
			callCtx, cancelProbe = context.WithTimeout(ctx, spec.DefaultSmartLocalProbeTimeout)
		}

		callStart := time.Now()
		out, err := f.Fetch(callCtx, &workReq)
		if cancelProbe != nil {
			cancelProbe()
		}
		latency := time.Since(callStart).Milliseconds()

		if err != nil {
			se := core.AsError(err)
			if se.Provider == "" {
				se = se.WithProvider(f.Name())
			}
			if f.Name() != spec.ProviderLocal {
				r.health.recordError(f.Name(), se)
			}
			lastErr = se
			status := core.ProviderStatusError
			if se.Code == core.CodeUpstreamTimeout {
				status = core.ProviderStatusTimeout
			}
			reports = append(reports, core.ProviderReport{
				Name:      f.Name(),
				Status:    status,
				LatencyMS: latency,
				Error:     se.ToErrorInfo(),
			})

			// Split-DNS 解析至非公网 IP 被本地 SSRF 守卫拦截时立即终止，严禁向公网云端源转发该内部域名。
			if se.HintKey == core.HintKeySSRFPrivateBlocked {
				return buildFetchErrorEnvelope(req.URL, policy.strategyLabel, reports, idx > 0, costs, start, se), se
			}

			// 独立否定 404/410 法定数（不计入专用 API 源如 github）：
			// local 第一方直接观测到干净 404 立即终止；云端首次 404 时优先将免费 local 提前做第二次独立确认，达到法定数即终止。
			if !policy.explicitProviders && se.Code == core.CodeNotFound && se.HintKey != core.HintKeyChallengeDetected && !isSpecialistFetcher(f) {
				if f.Name() == spec.ProviderLocal {
					finalErr := se
					if len(reports) > 1 {
						finalErr = summarizeChainFailure("target page not found across independent extractors", reports, se)
					}
					return buildFetchErrorEnvelope(req.URL, policy.strategyLabel, reports, idx > 0, costs, start, finalErr), finalErr
				}
				notFoundCount++
				if notFoundCount >= fetchNotFoundQuorum {
					finalErr := summarizeChainFailure("target page not found across independent extractors", reports, se)
					return buildFetchErrorEnvelope(req.URL, policy.strategyLabel, reports, idx > 0, costs, start, finalErr), finalErr
				}
				if !localTried && idx < len(seq)-1 {
					promoteFetcherByName(seq[idx+1:], spec.ProviderLocal)
				}
			}

			if se.HintKey == core.HintKeyChallengeDetected {
				lastWallProvider = f.Name()
				lastWallReason = textnorm.QualityReasonBotOrJSWall
				workReq.DynamicRenderHint = true
				if !policy.explicitProviders && idx < len(seq)-1 {
					reorderFetchersWhereInPlace(seq[idx+1:], isDynamicRenderFetcher)
					notes = append(notes, fmt.Sprintf("%s hit anti-bot challenge (HTTP %d), escalating to dynamic API extractor", f.Name(), se.Status))
				}
			} else if !policy.explicitProviders && f.Name() == spec.ProviderLocal && strings.Contains(se.Message, `"application/pdf"`) && idx < len(seq)-1 {
				reorderFetchersWhereInPlace(seq[idx+1:], isPDFFetcher)
			}
			if ctx.Err() != nil {
				break
			}
			continue
		}

		if f.Name() != spec.ProviderLocal {
			r.health.recordSuccess(f.Name())
		}

		if out.Cost != nil {
			c := *out.Cost
			costs = append(costs, &c)
		}
		notes = append(notes, out.Warnings...)

		// 对云端提取结果统一执行挑战盾与软 404 检测，防止前序云端源返回 HTTP 200 盾页截断后续渲染源。
		if f.Name() != spec.ProviderLocal && !out.LowQuality && out.Page != nil {
			if wall := textnorm.DetectChallengeWall(out.Page.Title, out.Page.Content); wall.LowQuality {
				out.LowQuality = true
				out.QualityMsg = wall.Reason
			}
		}

		if !policy.localPinned && !policy.singleLocked && out.LowQuality {
			reports = append(reports, core.ProviderReport{
				Name:      f.Name(),
				Status:    core.ProviderStatusLowQuality,
				Results:   1,
				LatencyMS: latency,
				Cost:      out.Cost,
				Warnings:  []string{"quality_gate:" + out.QualityMsg},
				RequestID: out.RequestID,
			})

			if isWallQualityReason(out.QualityMsg) {
				lastWallProvider = f.Name()
				lastWallReason = out.QualityMsg
				workReq.DynamicRenderHint = true
				if !policy.explicitProviders && idx < len(seq)-1 {
					reorderFetchersWhereInPlace(seq[idx+1:], isDynamicRenderFetcher)
				}
			} else if out.Page != nil && out.Page.Content != "" && lowQualityFallback == nil {
				lowQualityFallback = out.Page.Clone()
				lowQualityReason = out.QualityMsg
			}

			if idx < len(seq)-1 {
				if f.Name() == spec.ProviderLocal {
					notes = append(notes, fmt.Sprintf("local extraction marked low_quality (%s), escalating to API extractor", out.QualityMsg))
				} else {
					notes = append(notes, fmt.Sprintf("%s returned challenge/shell page (%s), falling back to next extractor", f.Name(), out.QualityMsg))
				}
				continue
			}

			if lowQualityFallback != nil {
				var note string
				switch {
				case idx > 0:
					note = fmt.Sprintf("all API extractors failed; returned best-effort local content (%s)", lowQualityReason)
				case policy.strategyLabel == string(core.FetchLocalOnly):
					note = fmt.Sprintf("returned best-effort local content (%s) under local_only strategy", lowQualityReason)
				default:
					note = fmt.Sprintf("returned best-effort local content (%s); configure an API extractor for dynamic rendering", lowQualityReason)
				}
				notes = append(notes, note)
				return buildFetchSuccessEnvelope(lowQualityFallback, &workReq, policy.strategyLabel, reports, idx > 0, notes, costs, start), nil
			}
			se := buildWallExhaustedError(lastWallProvider, lastWallReason, len(seq) > 1, policy.strategyLabel)
			return buildFetchErrorEnvelope(req.URL, policy.strategyLabel, reports, idx > 0, costs, start, se), se
		}

		var repWarnings []string
		if out.LowQuality && out.QualityMsg != "" {
			repWarnings = append(repWarnings, "quality_gate:"+out.QualityMsg)
		}
		repWarnings = append(repWarnings, out.Warnings...)

		reports = append(reports, core.ProviderReport{
			Name:      f.Name(),
			Status:    core.ProviderStatusOK,
			Results:   1,
			LatencyMS: latency,
			Cost:      out.Cost,
			Warnings:  repWarnings,
			RequestID: out.RequestID,
		})
		return buildFetchSuccessEnvelope(out.Page, &workReq, policy.strategyLabel, reports, idx > 0, notes, costs, start), nil
	}

	if lowQualityFallback != nil {
		notes = append(notes, fmt.Sprintf("all API extractors failed; returned best-effort local content (%s)", lowQualityReason))
		return buildFetchSuccessEnvelope(lowQualityFallback, &workReq, policy.strategyLabel, reports, true, notes, costs, start), nil
	}

	if lastErr == nil && lastWallProvider != "" {
		lastErr = buildWallExhaustedError(lastWallProvider, lastWallReason, len(seq) > 1, policy.strategyLabel)
	} else if lastErr == nil {
		lastErr = core.NewError(core.CodeContentUnavailable, "failed to extract page content")
	} else {
		lastErr = summarizeChainFailure("all extractors failed to read target URL", reports, lastErr)
	}
	return buildFetchErrorEnvelope(req.URL, policy.strategyLabel, reports, len(reports) > 1, costs, start, lastErr), lastErr
}

func isWallQualityReason(reason string) bool {
	return reason == textnorm.QualityReasonBotOrJSWall || reason == textnorm.QualityReasonSoft404
}

func buildWallExhaustedError(providerName, reason string, hadAPIChain bool, strategyLabel string) *core.Error {
	if reason == textnorm.QualityReasonSoft404 {
		return core.NewError(core.CodeNotFound, "target page returned soft-404 not found content").
			WithProvider(providerName)
	}
	if hadAPIChain {
		return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("page blocked by JavaScript or anti-bot challenge (%s) across all enabled extractors", reason)).
			WithProvider(providerName)
	}
	if strategyLabel == string(core.FetchLocalOnly) {
		return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("page blocked by JavaScript or anti-bot challenge (%s) under local_only strategy", reason)).
			WithProvider(providerName).
			WithHint("Retry with default `api_first` strategy or specify a cloud extractor with `-p`.").
			WithHintKey("hint.local_only_js_wall")
	}
	return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("page blocked by JavaScript or anti-bot challenge (%s) and no API extractor is enabled", reason)).
		WithProvider(providerName).
		WithHint("Configure a cloud API extractor via `veri key set <provider>` and retry.").
		WithHintKey("hint.js_wall_need_api")
}
