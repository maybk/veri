package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"veri/internal/core"
	"veri/internal/render"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

const maxStdinURLBytes = 1 << 20 // 1 MiB

type fetchFlags struct {
	providers []string
	strategy  string
	section   string
	match     string
	maxChars  int
	offset    int
	fresh     bool
	opts      []string
	jsonOut   bool
	format    string
	timeout   time.Duration
}

func newFetchCmd(rt *runtimeEnv) *cobra.Command {
	var ff fetchFlags
	cmd := &cobra.Command{
		Use:     "fetch <url>... | -",
		GroupID: groupQuery,
		Short:   rt.cat.T("fetch.short"),
		Example: rt.cat.T("fetch.example"),
		Args:    requireMinArgs(1, "err.missing_fetch_url", "hint.missing_fetch_url"),
		RunE: func(cmd *cobra.Command, args []string) error {
			outFormat, fmtConflictErr := resolveCLIFormat(cmd.Flags(), ff.format, ff.jsonOut)
			if fmtConflictErr != nil {
				return handleCommandError(rt, false, spec.KindFetch, strings.TrimSpace(args[0]), nil, fmtConflictErr)
			}
			if err := render.ValidateFetchFormat(outFormat); err != nil {
				return handleCommandError(rt, false, spec.KindFetch, strings.TrimSpace(args[0]), nil, err)
			}

			urls, rErr := resolveFetchCLIURLs(args, rt.stdin)
			primaryLabel := ""
			if len(urls) > 0 {
				primaryLabel = urls[0]
			} else if len(args) > 0 {
				primaryLabel = strings.TrimSpace(args[0])
			}
			if rErr != nil {
				return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindFetch, primaryLabel, nil, rErr)
			}

			optMap, err := parseKeyValueOptions(ff.opts)
			if err != nil {
				return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindFetch, primaryLabel, nil, err)
			}
			providers, err := validateProviderFlags(ff.providers, cmd.Flags().Changed("provider"), spec.RoleFetch)
			if err != nil {
				return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindFetch, primaryLabel, nil, err)
			}
			if ff.fresh {
				if optMap == nil {
					optMap = make(map[string]string, 1)
				}
				optMap[spec.OptFetchFresh] = "true"
			}

			application, err := rt.loadApp()
			if err != nil {
				return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindFetch, primaryLabel, nil, err)
			}

			req := &core.FetchRequest{
				URL:       primaryLabel,
				Section:   strings.TrimSpace(ff.section),
				Match:     strings.TrimSpace(ff.match),
				Providers: providers,
				Strategy:  core.FetchStrategy(strings.TrimSpace(ff.strategy)),
				MaxChars:  ff.maxChars,
				Offset:    ff.offset,
				Timeout:   ff.timeout,
				Options:   optMap,
			}

			var (
				env      *core.Envelope
				fetchErr error
			)
			if len(urls) > 1 {
				env, fetchErr = application.FetchBatch(cmd.Context(), urls, req)
			} else {
				env, fetchErr = application.Fetch(cmd.Context(), req)
			}
			if fetchErr != nil {
				if errors.Is(cmd.Context().Err(), context.Canceled) {
					return cmd.Context().Err()
				}
				return handleCommandError(rt, outFormat == spec.FormatJSON, spec.KindFetch, primaryLabel, env, fetchErr)
			}

			if !rt.flags.quiet {
				if outFormat != spec.FormatJSON && env.Meta.Cached {
					_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("note.fetch_cached"))
				}
				emitStderrNotes(rt, env)
				if outFormat != spec.FormatJSON && env.Page != nil && env.Page.NextOffset != nil {
					shown := strconv.Itoa(textnorm.RuneCount(env.Page.Content))
					total := strconv.Itoa(env.Page.TotalChars)
					cont := formatFetchContinueArgs(req.Section, req.Match, env.Meta.Notes, *env.Page.NextOffset)
					if preview := render.FormatOutlinePreview(env.Page.Outline, 5); preview != "" {
						_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("note.fetch_truncated_outline",
							"shown", shown,
							"total", total,
							"continue", cont,
							"outline", preview,
						))
					} else {
						_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("note.fetch_truncated",
							"shown", shown,
							"total", total,
							"continue", cont,
						))
					}
				}
			}

			rendered, fmtErr := render.FormatFetch(env, outFormat)
			if fmtErr != nil {
				return fmtErr
			}
			_, _ = fmt.Fprint(rt.stdout, rendered)
			return nil
		},
	}

	fs := cmd.Flags()
	fs.StringVar(&ff.section, "section", "", rt.cat.T("fetch.flag.section"))
	fs.StringVar(&ff.match, "match", "", rt.cat.T("fetch.flag.match"))
	fs.IntVar(&ff.maxChars, "max-chars", 0, rt.cat.T("fetch.flag.max_chars"))
	fs.IntVar(&ff.offset, "offset", 0, rt.cat.T("fetch.flag.offset"))
	fs.StringSliceVarP(&ff.providers, "provider", "p", nil, rt.cat.T("fetch.flag.provider"))
	fs.StringVar(&ff.strategy, "strategy", "", rt.cat.T("fetch.flag.strategy"))
	fs.BoolVar(&ff.fresh, "fresh", false, rt.cat.T("fetch.flag.fresh"))
	fs.StringVar(&ff.format, "format", spec.FormatMarkdown, rt.cat.T("fetch.flag.format"))
	fs.BoolVar(&ff.jsonOut, "json", false, rt.cat.T("fetch.flag.json"))
	fs.StringArrayVarP(&ff.opts, "opt", "o", nil, rt.cat.T("fetch.flag.opt"))
	fs.DurationVar(&ff.timeout, "timeout", 0, rt.cat.T("fetch.flag.timeout"))

	setFlagDisplayDefault(fs, "strategy", string(core.DefaultFetchStrategy))
	setFlagDisplayDefault(fs, "timeout", spec.DefaultFetchTimeout.String())
	return cmd
}

func resolveFetchCLIURLs(args []string, stdin io.Reader) ([]string, error) {
	var out []string
	readStdinOnce := false
	for _, a := range args {
		trimmed := strings.TrimSpace(a)
		if trimmed == "" {
			continue
		}
		if trimmed == "-" {
			if !readStdinOnce && stdin != nil {
				readStdinOnce = true
				sc := bufio.NewScanner(io.LimitReader(stdin, maxStdinURLBytes))
				for sc.Scan() {
					line := strings.TrimSpace(sc.Text())
					if line == "" || strings.HasPrefix(line, "#") {
						continue
					}
					out = append(out, line)
				}
				if err := sc.Err(); err != nil {
					return nil, core.WrapError(core.CodeInvalidArgument, "failed to read URLs from stdin", err)
				}
			}
			continue
		}
		out = append(out, trimmed)
	}
	if len(out) == 0 {
		return nil, core.NewError(core.CodeInvalidArgument, "no target URLs provided").
			WithI18n("err.missing_fetch_url", "hint.missing_fetch_url")
	}
	return out, nil
}

func formatFetchContinueArgs(section, match string, notes []string, nextOffset int) string {
	activeSec, activeMatch := render.ResolveFetchContinueScope(section, match, notes)
	var parts []string
	if activeSec != "" {
		parts = append(parts, "--section "+strconv.Quote(activeSec))
	}
	if activeMatch != "" {
		parts = append(parts, "--match "+strconv.Quote(activeMatch))
	}
	parts = append(parts, "--offset "+strconv.Itoa(nextOffset))
	return strings.Join(parts, " ")
}
