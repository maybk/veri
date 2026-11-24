package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"veri/clikit/i18n"
	"veri/clikit/redact"
	"veri/internal/core"
	"veri/internal/render"
	"veri/internal/spec"
)

type searchFlags struct {
	count        int
	providers    []string
	fusion       bool
	sites        []string
	excludeSites []string
	freshness    string
	langs        []string
	detail       string
	question     string
	opts         []string
	jsonOut      bool
	format       string
	maxChars     int
	timeout      time.Duration
	extra        bool
	strict       bool
	noCache      bool
}

func bindSearchFlags(fs *pflag.FlagSet, sf *searchFlags, cat i18n.Catalog) {
	fs.IntVarP(&sf.count, "count", "n", 0, cat.T("search.flag.count"))
	fs.StringVarP(&sf.detail, "detail", "d", "", cat.T("search.flag.detail"))
	fs.StringSliceVarP(&sf.providers, "provider", "p", nil, cat.T("search.flag.provider"))
	fs.BoolVarP(&sf.fusion, "fusion", "F", false, cat.T("search.flag.fusion"))
	fs.StringArrayVarP(&sf.sites, "site", "s", nil, cat.T("search.flag.site"))
	fs.StringArrayVarP(&sf.excludeSites, "exclude-site", "x", nil, cat.T("search.flag.exclude_site"))
	fs.StringVarP(&sf.freshness, "freshness", "t", "", cat.T("search.flag.freshness"))
	fs.StringSliceVar(&sf.langs, "lang", nil, cat.T("search.flag.lang"))
	fs.BoolVar(&sf.strict, "strict", false, cat.T("search.flag.strict"))
	fs.StringVar(&sf.question, "question", "", cat.T("search.flag.question"))
	fs.StringVar(&sf.format, "format", spec.FormatMarkdown, cat.T("search.flag.format"))
	fs.BoolVar(&sf.jsonOut, "json", false, cat.T("search.flag.json"))
	fs.IntVar(&sf.maxChars, "max-chars", 0, cat.T("search.flag.max_chars"))
	fs.BoolVar(&sf.extra, "extra", false, cat.T("search.flag.extra"))
	fs.BoolVar(&sf.noCache, "no-cache", false, cat.T("search.flag.no_cache"))
	fs.StringArrayVarP(&sf.opts, "opt", "o", nil, cat.T("search.flag.opt"))
	fs.DurationVar(&sf.timeout, "timeout", 0, cat.T("search.flag.timeout"))

	setFlagDisplayDefault(fs, "count", strconv.Itoa(spec.DefaultSearchCount))
	setFlagDisplayDefault(fs, "detail", string(core.DetailSummary))
	setFlagDisplayDefault(fs, "timeout", spec.DefaultSearchTimeout.String())
}

func newSearchCmd(rt *runtimeEnv) *cobra.Command {
	var sf searchFlags
	cmd := &cobra.Command{
		Use:     "search <query...>",
		GroupID: groupQuery,
		Short:   rt.cat.T("search.short"),
		Example: rt.cat.T("search.example"),
		Args:    requireMinArgs(1, "err.missing_search_query", "hint.missing_search_query"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSearchCommand(cmd.Context(), cmd.Flags(), rt, sf, args)
		},
	}
	bindSearchFlags(cmd.Flags(), &sf, rt.cat)
	return cmd
}

func resolveCLIFormat(fs *pflag.FlagSet, formatVal string, jsonOut bool) (string, error) {
	outFormat := strings.ToLower(strings.TrimSpace(formatVal))
	if jsonOut {
		if fs != nil && fs.Changed("format") && outFormat != "" && outFormat != spec.FormatJSON {
			return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("conflicting output flags: `--json` and `--format %s`", outFormat)).
				WithMsgKey("err.flag_conflict").
				WithParam("flags", fmt.Sprintf("--json, --format %s", outFormat))
		}
		outFormat = spec.FormatJSON
	}
	return outFormat, nil
}

func runSearchCommand(ctx context.Context, fs *pflag.FlagSet, rt *runtimeEnv, sf searchFlags, args []string) error {
	queryStr := strings.Join(args, " ")
	outFormat, fmtConflictErr := resolveCLIFormat(fs, sf.format, sf.jsonOut)
	if fmtConflictErr != nil {
		return handleCommandError(rt, false, spec.KindSearch, queryStr, nil, fmtConflictErr)
	}
	if err := render.ValidateSearchFormat(outFormat); err != nil {
		return handleCommandError(rt, false, spec.KindSearch, queryStr, nil, err)
	}
	if fs != nil && fs.Changed("count") && sf.count < spec.MinSearchCount {
		se := core.NewError(core.CodeInvalidArgument, fmt.Sprintf("count %d out of valid range [%d, %d]", sf.count, spec.MinSearchCount, spec.MaxSearchCount)).
			WithMsgKey("err.invalid_count").
			WithParam("count", strconv.Itoa(sf.count)).
			WithParam("min", strconv.Itoa(spec.MinSearchCount)).
			WithParam("max", strconv.Itoa(spec.MaxSearchCount))
		return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindSearch, queryStr, nil, se)
	}
	optMap, err := parseKeyValueOptions(sf.opts)
	if err != nil {
		return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindSearch, queryStr, nil, err)
	}

	fresh, err := core.ParseFreshness(sf.freshness, time.Now())
	if err != nil {
		return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindSearch, queryStr, nil, err)
	}
	providers, err := validateProviderFlags(sf.providers, fs != nil && fs.Changed("provider"), spec.RoleSearch)
	if err != nil {
		return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindSearch, queryStr, nil, err)
	}

	application, err := rt.loadApp()
	if err != nil {
		return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindSearch, queryStr, nil, err)
	}

	var strategy core.Strategy
	if fs != nil && fs.Changed("fusion") {
		if sf.fusion {
			strategy = core.StrategyFusion
		} else {
			strategy = core.StrategyAuto
		}
	}

	req := &core.SearchRequest{
		Query:         queryStr,
		Question:      sf.question,
		Count:         sf.count,
		Detail:        core.Detail(sf.detail),
		Providers:     providers,
		Strategy:      strategy,
		StrictFilters: sf.strict,
		IncludeExtra:  sf.extra,
		MaxChars:      sf.maxChars,
		Timeout:       sf.timeout,
		NoCache:       sf.noCache,
		Options:       optMap,
		Filters: core.Filters{
			Sites:        cleanList(sf.sites),
			ExcludeSites: cleanList(sf.excludeSites),
			Lang:         cleanList(sf.langs),
			Freshness:    fresh,
		},
	}

	env, searchErr := application.Search(ctx, req)
	if searchErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindSearch, req.Query, env, searchErr)
	}

	if !rt.flags.quiet {
		if outFormat != spec.FormatJSON && env.Meta.Cached {
			_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("note.search_cached"))
		}
		emitStderrNotes(rt, env)
	}

	pal := render.DetectPaletteWithEnv(rt.stdout, rt.flags.noColor, rt.getenv)
	rendered, fmtErr := render.FormatSearch(env, outFormat, pal)
	if fmtErr != nil {
		return fmtErr
	}
	_, _ = fmt.Fprint(rt.stdout, rendered)
	if outFormat == spec.FormatMarkdown && len(env.Results) == 0 && !rt.flags.quiet {
		_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("note.empty"))
	}
	return nil
}

func parseKeyValueOptions(items []string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(items))
	for _, raw := range items {
		k, v, ok := strings.Cut(raw, "=")
		k = strings.TrimSpace(k)
		if !ok || k == "" {
			return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid --opt %q: expected key=value", raw)).
				WithMsgKey("err.invalid_opt").
				WithParam("opt", raw)
		}
		out[k] = strings.TrimSpace(v)
	}
	return out, nil
}

func cleanList(items []string) []string {
	var out []string
	for _, item := range items {
		for p := range strings.SplitSeq(item, ",") {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// validateProviderFlags 校验 `-p/--provider` 指定的名称均为已登记内置 Provider 且支持目标角色，并规整别名、大小写与去重。
func validateProviderFlags(items []string, flagChanged bool, wantRole spec.Role) ([]string, error) {
	cleaned := cleanList(items)
	if len(cleaned) == 0 {
		if !flagChanged {
			return nil, nil
		}
		return nil, unsupportedProviderError("", wantRole)
	}
	normalized, bad, ok := spec.NormalizeProvidersForRole(cleaned, wantRole)
	if !ok {
		return nil, unsupportedProviderError(bad, wantRole)
	}
	return normalized, nil
}

func unsupportedProviderError(raw string, wantRole spec.Role) error {
	valid := strings.Join(spec.ProvidersWithRole(wantRole), ", ")
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported provider %q (valid: %s)", raw, valid)).
		WithMsgKey("err.unsupported_provider").
		WithParam("provider", raw).
		WithParam("valid", valid)
}

func emitStderrNotes(rt *runtimeEnv, env *core.Envelope) {
	if env == nil {
		return
	}
	if env.Meta.FallbackUsed {
		var steps []string
		for _, p := range env.Meta.Providers {
			steps = append(steps, fmt.Sprintf("%s:%s", p.Name, p.Status))
		}
		_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("note.fallback", "chain", strings.Join(steps, " -> ")))
	}
	for _, n := range env.Meta.Notes {
		_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("note.item", "text", n))
	}
}

func handleCommandError(rt *runtimeEnv, asJSON bool, kind, query string, env *core.Envelope, err error) error {
	se := rt.scrubCoreError(core.AsError(err))
	if asJSON {
		if env == nil {
			safeQuery := rt.scrubText(query)
			if kind == spec.KindFetch {
				safeQuery = rt.scrubText(redact.SanitizeURL(query))
			}
			env = newErrorEnvelope(kind, safeQuery, se)
		}
		if jsonText, jErr := render.MarshalEnvelopeJSON(env); jErr == nil {
			_, _ = fmt.Fprint(rt.stdout, jsonText)
			rt.jsonError = true
		}
	}
	return se
}
