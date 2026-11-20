package router

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
	"veri/internal/urlnorm"
)

type fetchExecPolicy struct {
	strategyLabel     string
	singleLocked      bool
	explicitProviders bool
	localPinned       bool
	initialNotes      []string
}

// Fetch 执行单页正文提取路由（api_first / smart_local / local_only / api_only），
// 支持内网钉死防泄密、全链路挑战盾拦截、独立否定 404 法定数、智能云端选路、优雅保底与段落边界分页续读。
func (r *Router) Fetch(ctx context.Context, rawReq *core.FetchRequest) (*core.Envelope, error) {
	start := time.Now()
	req, parsedURL, err := r.normalizeFetchRequest(rawReq)
	if err != nil {
		se := core.AsError(err)
		return buildFetchErrorEnvelope(rawFetchURL(rawReq), string(r.opts.FetchStrategy), nil, false, nil, start, se), se
	}

	timeout := r.opts.FetchTimeout
	if req.Timeout > 0 {
		timeout = req.Timeout
	}
	overallCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// 本地/回环/私网/内网后缀目标强制钉死本地通道，严禁向公网云端提取源泄露内部 URL。
	if urlnorm.IsLocalOrPrivateTarget(parsedURL) {
		return r.fetchLocalPinned(overallCtx, &req, start)
	}

	if len(req.Providers) == 1 {
		name := canonicalProviderKey(req.Providers[0])
		f, ok := r.fetchers[name]
		if !ok {
			se := core.NewError(core.CodeNoProvider, fmt.Sprintf("fetcher %q is not enabled or has no configured API key", name)).
				WithHint("Verify extractor name or save its API key via `veri key set <provider>`.")
			return buildFetchErrorEnvelope(req.URL, string(core.StrategySingle), nil, false, nil, start, se), se
		}
		return r.runFetchSequence(overallCtx, &req, []core.Fetcher{f}, fetchExecPolicy{
			strategyLabel:     string(core.StrategySingle),
			singleLocked:      true,
			explicitProviders: true,
		}, start)
	}

	sequence, initNotes := r.buildFetchSequence(parsedURL, req.Strategy, req.Providers)
	if len(sequence) == 0 {
		msg := fmt.Sprintf("no fetcher available for strategy %q", req.Strategy)
		if len(req.Providers) > 0 {
			msg = fmt.Sprintf("requested fetcher(s) %s are not enabled or have no configured API key", strings.Join(req.Providers, ", "))
		}
		se := core.NewError(core.CodeNoProvider, msg).
			WithHint("Run `veri key set <provider>` for a cloud extractor, or set `providers.local.enabled = true` in config.toml.")
		return buildFetchErrorEnvelope(req.URL, string(req.Strategy), nil, false, nil, start, se), se
	}

	return r.runFetchSequence(overallCtx, &req, sequence, fetchExecPolicy{
		strategyLabel:     string(req.Strategy),
		explicitProviders: len(req.Providers) > 0,
		initialNotes:      initNotes,
	}, start)
}

func (r *Router) fetchLocalPinned(ctx context.Context, req *core.FetchRequest, start time.Time) (*core.Envelope, error) {
	strategyLabel := string(req.Strategy)
	if len(req.Providers) == 1 {
		strategyLabel = string(core.StrategySingle)
	}
	cloudOnly := (len(req.Providers) > 0 && !slices.Contains(req.Providers, spec.ProviderLocal)) ||
		(len(req.Providers) == 0 && req.Strategy == core.FetchAPIOnly)
	if cloudOnly {
		se := core.NewError(core.CodeBlockedByPolicy, "refusing to send local or private URL to external cloud extractor").
			WithMsgKey("err.private_url_cloud_blocked").
			WithHint("Use local extraction (api_first, smart_local, or -p local) and set network.allow_private = true in config.toml if needed.").
			WithHintKey("hint.private_url_cloud_blocked")
		return buildFetchErrorEnvelope(req.URL, strategyLabel, nil, false, nil, start, se), se
	}
	if r.localFetch == nil {
		se := core.NewError(core.CodeNoProvider, "local extractor is disabled and cannot read private target URL").
			WithHint("Set `providers.local.enabled = true` and `network.allow_private = true` in config.toml if needed.").
			WithI18n("err.local_disabled_private_url", "hint.local_disabled_private_url")
		return buildFetchErrorEnvelope(req.URL, strategyLabel, nil, false, nil, start, se), se
	}
	return r.runFetchSequence(ctx, req, []core.Fetcher{r.localFetch}, fetchExecPolicy{
		strategyLabel:     strategyLabel,
		singleLocked:      len(req.Providers) == 1,
		explicitProviders: len(req.Providers) > 0,
		localPinned:       true,
	}, start)
}

func (r *Router) normalizeFetchRequest(rawReq *core.FetchRequest) (core.FetchRequest, *url.URL, error) {
	if rawReq == nil {
		return core.FetchRequest{}, nil, core.NewError(core.CodeInvalidArgument, "fetch request must not be nil")
	}
	req := *rawReq
	req.URL = strings.TrimSpace(req.URL)
	u, ok := urlnorm.IsSafeHTTPURL(req.URL)
	if !ok {
		return core.FetchRequest{}, nil, core.NewError(core.CodeInvalidArgument, "URL must be a valid http or https address without credentials").
			WithMsgKey("err.invalid_url")
	}
	if req.Offset < 0 {
		return core.FetchRequest{}, nil, core.NewError(core.CodeInvalidArgument, "offset must be >= 0").
			WithMsgKey("err.invalid_offset")
	}
	if req.MaxChars < 0 {
		return core.FetchRequest{}, nil, core.NewError(core.CodeInvalidArgument, "max_chars must be >= 0").
			WithMsgKey("err.invalid_max_chars")
	}

	req.Providers = cleanProviderKeys(req.Providers)
	rawStrategy := strings.TrimSpace(string(req.Strategy))
	if len(req.Providers) > 0 && rawStrategy != "" {
		return core.FetchRequest{}, nil, core.NewError(core.CodeInvalidArgument, "cannot specify both explicit providers and fetch strategy").
			WithHint("Omit `strategy` when selecting explicit providers, or omit `providers` to let strategy route automatically.").
			WithI18n("err.fetch_providers_strategy_conflict", "hint.fetch_providers_strategy_conflict")
	}

	strategy, err := core.ParseFetchStrategy(rawStrategy)
	if err != nil {
		return core.FetchRequest{}, nil, err
	}
	if rawStrategy == "" {
		strategy = r.opts.FetchStrategy
	}
	req.Strategy = strategy
	if req.MaxChars == 0 {
		req.MaxChars = r.opts.FetchMaxChars
	}
	return req, u, nil
}

func buildFetchSuccessEnvelope(page *core.Page, req *core.FetchRequest, strategy string, reports []core.ProviderReport, fallback bool, notes []string, costs []*core.Cost, start time.Time) *core.Envelope {
	paged, viewNotes := ApplyPageView(page, req)
	notes = append(notes, viewNotes...)
	return &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     true,
		Kind:   spec.KindFetch,
		Page:   paged,
		Meta: core.Meta{
			Strategy:     strategy,
			Providers:    reports,
			FallbackUsed: fallback,
			Truncated:    paged.NextOffset != nil,
			Notes:        notes,
			Cost:         core.SumCosts(costs),
			DurationMS:   time.Since(start).Milliseconds(),
		},
	}
}

func rawFetchURL(req *core.FetchRequest) string {
	if req == nil {
		return ""
	}
	return req.URL
}

func buildFetchErrorEnvelope(targetURL, strategy string, reports []core.ProviderReport, fallback bool, costs []*core.Cost, start time.Time, se *core.Error) *core.Envelope {
	if reports == nil {
		reports = []core.ProviderReport{}
	}
	return &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     false,
		Kind:   spec.KindFetch,
		Query:  targetURL,
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
