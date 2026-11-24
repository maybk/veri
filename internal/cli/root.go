package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"veri/clikit"
	"veri/clikit/buildinfo"
	"veri/clikit/i18n"
	"veri/clikit/redact"
	"veri/internal/app"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/render"
	"veri/internal/spec"
	"veri/internal/update"
)

// BuildInfo 复用 clikit/buildinfo.Info 构建与版本元数据契约。
type BuildInfo = buildinfo.Info

const (
	groupQuery = "query"
	groupAdmin = "admin"
	groupAgent = "agent"
)

type globalFlags struct {
	configPath string
	uiLang     string
	noColor    bool
	quiet      bool
}

type runtimeEnv struct {
	assets     fs.FS
	info       BuildInfo
	flags      globalFlags
	uiLocale   i18n.Locale
	uiSource   string
	cat        i18n.Catalog
	stdin      io.Reader
	stdout     io.Writer
	stderr     io.Writer
	getenv     func(string) string
	updateOpts []update.Option
	scrubber   *strings.Replacer
	cmdKind    string // 推断或执行中的命令类别，供 --json 终端错误信封填写 kind
	jsonError  bool   // 当前子命令处于 --json 模式且已向 stdout 写出完整错误信封
}

type handledExitError struct {
	code int
}

func (e handledExitError) Error() string { return fmt.Sprintf("exit %d", e.code) }

// Execute 解析命令行参数、执行对应命令并返回标准进程退出码（0 / 1 / 2 / 3 / 4 / 5 / 130）。
func Execute(args []string, assets fs.FS, info BuildInfo, stdin io.Reader, stdout, stderr io.Writer) int {
	if info.Version == "" {
		info = buildinfo.Resolve(assets, info.Version, info.Commit, info.BuildDate)
	}
	if stdin == nil {
		stdin = os.Stdin
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	ctx, stop := clikit.NotifyContext(context.Background())
	defer stop()

	return ExecuteContext(ctx, args, assets, info, stdin, stdout, stderr, os.Getenv)
}

// ExecuteContext 支持在测试中注入自定义 Context、静态资产 FS、参数与环境变量读取器。
func ExecuteContext(ctx context.Context, args []string, assets fs.FS, info BuildInfo, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	rt, rootCmd, wantsJSON, earlyCode, ok := prepareRootCmd(args, assets, info, stdin, stdout, stderr, getenv) //nolint:contextcheck // Cobra injects ctx via rootCmd.ExecuteContext -> cmd.Context()
	if !ok {
		return earlyCode
	}

	err := rootCmd.ExecuteContext(ctx)
	if err == nil {
		return clikit.ExitOK
	}

	if he, ok := errors.AsType[handledExitError](err); ok {
		return he.code
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return clikit.ExitInterrupted
	}

	if se, ok := errors.AsType[*core.Error](err); ok {
		return emitTerminalError(rt, wantsJSON, se)
	}

	// Cobra 未知参数或用法错误统一走结构化本地化渲染并返回退出码 2
	usageErr := wrapCobraUsageError(err)
	return emitTerminalError(rt, wantsJSON, usageErr)
}

func wrapCobraUsageError(err error) *core.Error {
	msg := strings.TrimSpace(err.Error())
	se := core.NewError(core.CodeInvalidArgument, msg)
	if after, ok := strings.CutPrefix(msg, "unknown flag: "); ok {
		return se.WithMsgKey("err.unknown_flag").WithParam("flag", strings.TrimSpace(after))
	}
	if after, ok := strings.CutPrefix(msg, "unknown shorthand flag: "); ok {
		rest := strings.TrimSpace(after)
		if flag, _, hasIn := strings.Cut(rest, " in "); hasIn {
			rest = strings.Trim(flag, `"' `)
			if rest != "" && !strings.HasPrefix(rest, "-") {
				rest = "-" + rest
			}
		}
		return se.WithMsgKey("err.unknown_flag").WithParam("flag", rest)
	}
	if after, ok := strings.CutPrefix(msg, `unknown command "`); ok {
		if sub, _, hasEnd := strings.Cut(after, `"`); hasEnd && sub != "" {
			return se.WithMsgKey("err.unknown_command").WithParam("cmd", sub)
		}
	}
	return se
}

func prepareRootCmd(args []string, assets fs.FS, info BuildInfo, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) (*runtimeEnv, *cobra.Command, bool, int, bool) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if info.Version == "" {
		info = buildinfo.Resolve(assets, info.Version, info.Commit, info.BuildDate)
	}
	preCfgPath := clikit.ScanFlagValue(args, "config")
	preUILang := clikit.ScanFlagValue(args, "ui-lang")
	preFmt := strings.TrimSpace(clikit.ScanFlagValue(args, "format"))
	hasJSONFlag := clikit.HasBoolFlag(args, "json")
	wantsJSON := (hasJSONFlag && (preFmt == "" || strings.EqualFold(preFmt, spec.FormatJSON))) ||
		(!hasJSONFlag && strings.EqualFold(preFmt, spec.FormatJSON))

	var cfgLang string
	if isAutoLangSetting(preUILang) && isAutoLangSetting(getenv(spec.EnvLang)) {
		cfgLang = config.PeekUILang(preCfgPath, getenv)
	}
	loc, src := i18n.Resolve(i18n.ResolveOptions{
		FlagLang:   preUILang,
		EnvLangVar: spec.EnvLang,
		ConfigLang: cfgLang,
		Getenv:     getenv,
	})

	rt := &runtimeEnv{
		assets:   assets,
		info:     info,
		uiLocale: loc,
		uiSource: src,
		cat:      localeBundle.For(loc),
		stdin:    stdin,
		stdout:   stdout,
		stderr:   stderr,
		getenv:   getenv,
		cmdKind:  inferCommandKind(args),
	}

	if err := i18n.ValidateExplicit(preUILang); err != nil {
		se := core.NewError(core.CodeInvalidArgument, err.Error()).
			WithMsgKey("err.invalid_ui_lang").
			WithParam("lang", strings.TrimSpace(preUILang))
		return rt, nil, wantsJSON, emitTerminalError(rt, wantsJSON, se), false
	}

	rootCmd := newRootCmd(rt)
	rootCmd.SetArgs(args)
	return rt, rootCmd, wantsJSON, 0, true
}

func newErrorEnvelope(kind, query string, se *core.Error) *core.Envelope {
	if kind == "" {
		kind = spec.KindSearch
	}
	meta := core.Meta{
		Providers: []core.ProviderReport{},
		Cost:      []core.Cost{},
	}
	switch kind {
	case spec.KindSearch:
		meta.Strategy = string(core.StrategyAuto)
	case spec.KindFetch:
		meta.Strategy = string(core.DefaultFetchStrategy)
	}
	return &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     false,
		Kind:   kind,
		Query:  query,
		Meta:   meta,
		Error:  se.ToErrorInfo(),
	}
}

func emitTerminalError(rt *runtimeEnv, wantsJSON bool, se *core.Error) int {
	se = rt.scrubCoreError(se)
	if wantsJSON && !rt.jsonError {
		env := newErrorEnvelope(rt.cmdKind, "", se)
		if jsonText, jErr := render.MarshalEnvelopeJSON(env); jErr == nil {
			_, _ = fmt.Fprint(rt.stdout, jsonText)
			rt.jsonError = true
		}
	}
	if !rt.jsonError {
		_, _ = fmt.Fprintln(rt.stderr, formatCoreError(rt.cat, se))
	}
	return se.Code.ExitCode()
}

func inferCommandKind(args []string) string {
	for i := 0; i < len(args); i++ {
		a := strings.TrimSpace(args[i])
		if a == "" {
			continue
		}
		if a == "--" {
			return spec.KindSearch
		}
		if strings.HasPrefix(a, "-") {
			if !strings.Contains(a, "=") && globalOrRootValueFlag(a) {
				i++
			}
			continue
		}
		switch lower := strings.ToLower(a); lower {
		case "fetch":
			return spec.KindFetch
		case "update":
			return spec.KindUpdate
		case "search":
			return spec.KindSearch
		case "doctor", "providers", "config", "cache", "version", "mcp", "skill", "completion":
			return lower
		case "key", "keys":
			return "key"
		default:
			return spec.KindSearch
		}
	}
	return spec.KindSearch
}

func globalOrRootValueFlag(flag string) bool {
	switch flag {
	case "--config", "--ui-lang", "--format", "--timeout",
		"-n", "--count", "-d", "--detail", "-p", "--provider",
		"-s", "--site", "-x", "--exclude-site", "-t", "--freshness",
		"--lang", "--question", "--max-chars", "-o", "--opt":
		return true
	default:
		return false
	}
}

func (rt *runtimeEnv) scrubText(s string) string {
	if s == "" {
		return ""
	}
	var rep *strings.Replacer
	if rt != nil {
		rep = rt.scrubber
	}
	return redact.ScrubWith(s, rep)
}

func (rt *runtimeEnv) scrubCoreError(se *core.Error) *core.Error {
	var rep *strings.Replacer
	if rt != nil {
		rep = rt.scrubber
	}
	return app.ScrubCoreErrorWith(se, rep)
}

func newRootCmd(rt *runtimeEnv) *cobra.Command {
	var rootSearch searchFlags
	var showVersion bool

	cmd := &cobra.Command{
		Use:                        spec.AppName + " [query...]",
		Short:                      rt.cat.T("root.short"),
		Long:                       rt.cat.T("root.long"),
		Example:                    rt.cat.T("root.example"),
		SilenceErrors:              true,
		SilenceUsage:               true,
		SuggestionsMinimumDistance: 2,
		Args:                       cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if showVersion {
				outFormat := strings.ToLower(strings.TrimSpace(rootSearch.format))
				if rootSearch.jsonOut || outFormat == spec.FormatJSON {
					text, err := render.MarshalEnvelopeJSON(rt.info)
					if err != nil {
						return err
					}
					_, _ = fmt.Fprint(rt.stdout, text)
					return nil
				}
				_, _ = fmt.Fprintln(rt.stdout, buildinfo.FormatLine(spec.AppName, rt.info))
				return nil
			}
			if len(args) == 0 {
				return cmd.Help()
			}
			if gErr := guardRootShorthandArgs(cmd, args); gErr != nil {
				outFormat := strings.ToLower(strings.TrimSpace(rootSearch.format))
				hasConflict := cmd.Flags().Changed("format") && rootSearch.jsonOut && outFormat != "" && outFormat != spec.FormatJSON
				asJSON := !hasConflict && (rootSearch.jsonOut || outFormat == spec.FormatJSON)
				return handleCommandError(rt, asJSON, spec.KindSearch, "", nil, gErr)
			}
			return runSearchCommand(cmd.Context(), cmd.Flags(), rt, rootSearch, args)
		},
	}

	cmd.AddGroup(
		&cobra.Group{ID: groupQuery, Title: rt.cat.T("group.query")},
		&cobra.Group{ID: groupAdmin, Title: rt.cat.T("group.admin")},
		&cobra.Group{ID: groupAgent, Title: rt.cat.T("group.agent")},
	)

	pf := cmd.PersistentFlags()
	pf.StringVar(&rt.flags.configPath, "config", "", rt.cat.T("flag.config"))
	pf.StringVar(&rt.flags.uiLang, "ui-lang", spec.LangAuto, rt.cat.T("flag.ui_lang"))
	pf.BoolVar(&rt.flags.noColor, "no-color", false, rt.cat.T("flag.no_color"))
	pf.BoolVarP(&rt.flags.quiet, "quiet", "q", false, rt.cat.T("flag.quiet"))

	bindSearchFlags(cmd.Flags(), &rootSearch, rt.cat)
	cmd.Flags().BoolVarP(&showVersion, "version", "v", false, rt.cat.T("flag.version"))

	addOrderedCommands(cmd,
		newSearchCmd(rt),
		newFetchCmd(rt),
		newKeyCmd(rt),
		newConfigCmd(rt),
		newCacheCmd(rt),
		newProvidersCmd(rt),
		newDoctorCmd(rt),
		newVersionCmd(rt),
		newUpdateCmd(rt),
		newMCPCmd(rt),
		newSkillCmd(rt),
	)
	cmd.SetIn(rt.stdin)
	cmd.SetOut(rt.stdout)
	cmd.SetErr(rt.stderr)
	installHelpRenderer(cmd, rt)
	return cmd
}

func guardRootShorthandArgs(cmd *cobra.Command, args []string) error {
	if slices.ContainsFunc(args, redact.LooksLikeSecret) {
		return core.NewError(core.CodeInvalidArgument, "refusing to send an apparent API key or token as a search query").
			WithHint("To save an API key, run `veri key set <provider>`.").
			WithI18n("err.secret_as_query", "hint.secret_as_query")
	}

	first := strings.TrimSpace(args[0])
	lowerFirst := strings.ToLower(first)

	switch lowerFirst {
	case "set-key", "unset-key", "init", "install":
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown top-level command %q", first)).
			WithHint("Use `veri key set <provider>`, `veri config init`, or `veri skill install`.").
			WithI18n("err.legacy_subcommand", "hint.legacy_subcommand").
			WithParam("cmd", first)
	}

	if !strings.ContainsAny(first, " \t") {
		var suggest string
		if len(args) == 1 {
			if strings.HasPrefix(lowerFirst, "http://") || strings.HasPrefix(lowerFirst, "https://") {
				safeURL := redact.SanitizeURL(first)
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("argument %q looks like a URL (use 'veri fetch <url>')", safeURL)).
					WithHint("Run `veri fetch <url>` to read a page, or `veri search -- <url>` to search literally.").
					WithI18n("err.url_as_query", "hint.url_as_query").
					WithParam("url", safeURL)
			}
			if suggestions := cmd.SuggestionsFor(first); len(suggestions) > 0 {
				suggest = suggestions[0]
			}
		} else if len(lowerFirst) >= 4 {
			// 多词根命令快捷搜索仅在首词长度 >= 4 且与顶层子命令 Damerau-Levenshtein 距离为 1（如 serach/fetc）时拦截，避免误伤如 "map reduce" 等正常查询。
			suggest = closestSubcommandTypo(cmd, lowerFirst)
		}
		if suggest != "" {
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown command %q for %q (did you mean %q?)", first, spec.AppName, suggest)).
				WithHint(fmt.Sprintf("Run `veri %s`, or use `veri search -- %s` to search literally.", suggest, first)).
				WithI18n("err.unknown_subcommand_suggest", "hint.unknown_subcommand_suggest").
				WithParam("cmd", first).
				WithParam("suggest", suggest)
		}
	}
	return nil
}

func isAutoLangSetting(raw string) bool {
	v := strings.ToLower(strings.TrimSpace(raw))
	return v == "" || v == spec.LangAuto || v == "system"
}

func (rt *runtimeEnv) loadApp() (*app.App, error) {
	loaded, err := config.LoadWithEnv(rt.flags.configPath, rt.getenv)
	if err != nil {
		return nil, err
	}
	rt.scrubber = redact.NewScrubber(loaded.AllSecrets)
	if loaded.PermWarning && !rt.flags.quiet {
		_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("note.perm_warn", "path", loaded.Path))
	}
	if loaded.VaultPermWarning && !rt.flags.quiet {
		_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("doctor.vault_perm_warn"))
	}
	return app.NewWithStderr(loaded, rt.info.Version, rt.stderr)
}
