package cli

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"veri/clikit"
	"veri/clikit/i18n"
	"veri/clikit/term"
	"veri/internal/app"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/render"
	"veri/internal/spec"
)

func newProvidersCmd(rt *runtimeEnv) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "providers",
		GroupID: groupAdmin,
		Short:   rt.cat.T("providers.short"),
		Args:    requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			application, err := rt.loadApp()
			if err != nil {
				return err
			}
			statuses := application.ProviderStatuses()
			if jsonOut {
				text, jErr := render.MarshalEnvelopeJSON(statuses)
				if jErr != nil {
					return jErr
				}
				_, _ = fmt.Fprint(rt.stdout, text)
				return nil
			}
			printProviderTable(rt, statuses)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, rt.cat.T("providers.flag.json"))
	return cmd
}

func printProviderTable(rt *runtimeEnv, statuses []app.ProviderStatus) {
	pal := term.DetectPalette(rt.stdout, rt.flags.noColor, rt.getenv)
	cat := rt.cat
	headers := []string{
		cat.T("table.providers.name"),
		cat.T("table.providers.role"),
		cat.T("table.providers.status"),
		cat.T("table.providers.source"),
		cat.T("table.providers.endpoint"),
		cat.T("table.providers.timeout"),
		cat.T("table.providers.details"),
	}
	enabledLabel := cat.T("table.val.enabled")
	disabledLabel := cat.T("table.val.disabled")
	missingLabel := cat.T("table.val.missing")
	disabledConfigLabel := cat.T("providers.detail.disabled_config")

	rows := make([][]string, 0, len(statuses))
	for _, s := range statuses {
		statusText := disabledLabel
		if s.Enabled {
			statusText = enabledLabel
		}
		sourceText := formatProviderSource(cat, s.Source)
		timeoutText := s.Timeout
		if timeoutText == "" {
			timeoutText = "-"
		}
		detailText := s.Details
		if !s.Enabled {
			if s.Details == spec.DetailDisabledInConfig {
				detailText = disabledConfigLabel
			} else if info, ok := spec.LookupProvider(s.Name); ok && info.NeedsCredential() {
				detailText = cat.T("providers.detail.need_key", "provider", info.Name, "env", info.EnvVars[0])
			}
		}
		rows = append(rows, []string{s.Name, s.Role, statusText, sourceText, s.Endpoint, timeoutText, detailText})
	}
	term.WriteTable(rt.stdout, headers, rows, term.TableStyle{
		Header: pal.Bold,
		Cell: func(col int, val string) string {
			switch col {
			case 2:
				if val == enabledLabel {
					return pal.Green(val)
				}
				return pal.Dim(val)
			case 3:
				if val == missingLabel || val == "-" {
					return pal.Dim(val)
				}
			case 4, 5:
				if val == "-" {
					return pal.Dim(val)
				}
			case 6:
				if val == disabledConfigLabel {
					return pal.Dim(val)
				}
			}
			return val
		},
	})
}

func formatProviderSource(cat i18n.Catalog, raw string) string {
	s := strings.TrimSpace(raw)
	switch s {
	case "", "-":
		return "-"
	case spec.CredSourceMissing:
		return cat.T("table.val.missing")
	case spec.CredSourceOptional:
		return cat.T("table.val.optional")
	}
	parts := strings.Split(s, "+")
	labels := make([]string, 0, len(parts))
	for _, part := range parts {
		label := mapProviderSourcePart(cat, strings.TrimSpace(part))
		if label != "" && !slices.Contains(labels, label) {
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return "-"
	}
	return strings.Join(labels, "+")
}

func mapProviderSourcePart(cat i18n.Catalog, part string) string {
	switch {
	case part == "":
		return ""
	case part == spec.SecretsFileName:
		return cat.T("table.val.source_vault")
	case part == spec.ConfigFileName:
		return cat.T("table.val.source_config")
	case strings.HasPrefix(part, spec.CredSourceEnvPrefix):
		return cat.T("table.val.source_env")
	default:
		return part
	}
}

func newDoctorCmd(rt *runtimeEnv) *cobra.Command {
	var live bool
	var yes bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:     "doctor",
		GroupID: groupAdmin,
		Short:   rt.cat.T("doctor.short"),
		Example: rt.cat.T("doctor.example"),
		Args:    requireNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			application, err := rt.loadApp()
			if err != nil {
				return err
			}

			if live && !yes {
				confirmed, cErr := term.Confirm(cmd.Context(), rt.stdin, rt.stderr, rt.cat.T("doctor.confirm_live"))
				if cErr != nil {
					if isInputInterrupted(cErr) {
						return handledExitError{code: clikit.ExitInterrupted}
					}
					return core.NewError(core.CodeInvalidArgument, "doctor --live requires --yes when running non-interactively").
						WithHint("Pass `--yes` to confirm live requests: `veri doctor --live --yes`.").
						WithI18n("err.doctor_live_noninteractive", "hint.doctor_live_noninteractive")
				}
				if !confirmed {
					_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("doctor.aborted"))
					return nil
				}
			}

			rep := application.RunDoctor(cmd.Context(), string(rt.uiLocale), rt.uiSource, live)
			exitErr := doctorExitError(rep)

			if jsonOut {
				text, jErr := render.MarshalEnvelopeJSON(rep)
				if jErr != nil {
					return jErr
				}
				_, _ = fmt.Fprint(rt.stdout, text)
				return exitErr
			}

			pal := term.DetectPalette(rt.stdout, rt.flags.noColor, rt.getenv)
			_, _ = fmt.Fprintln(rt.stdout, pal.Bold("# "+rt.cat.T("doctor.header")))
			_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.runtime",
				"goVersion", rep.GoVersion, "os", rep.OS, "arch", rep.Arch, "version", rep.Version))
			_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.ui_locale",
				"locale", rep.UILocale, "source", rep.UILocaleSource))

			if rep.ConfigExists {
				if rep.ConfigMode != "" {
					_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.config_found",
						"path", rep.ConfigPath, "mode", rep.ConfigMode))
				} else {
					_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.config_found_plain",
						"path", rep.ConfigPath))
				}
			} else {
				_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.config_missing",
					"path", rep.ConfigPath))
			}
			if rep.PermWarning {
				_, _ = fmt.Fprintln(rt.stdout, "- "+pal.Yellow(rt.cat.T("doctor.perm_warn",
					"mode", rep.ConfigMode, "path", rep.ConfigPath)))
			}
			if rep.VaultPermWarning {
				_, _ = fmt.Fprintln(rt.stdout, "- "+pal.Yellow(rt.cat.T("doctor.vault_perm_warn")))
			}
			if rep.AllowPrivate {
				_, _ = fmt.Fprintln(rt.stdout, "- "+pal.Yellow(rt.cat.T("doctor.private_warn")))
			}
			if rep.EnabledSearcher == 0 {
				_, _ = fmt.Fprintln(rt.stdout, "- "+pal.Yellow(rt.cat.T("doctor.no_searcher_warn")))
			}
			if rep.DNSError != "" {
				_, _ = fmt.Fprintln(rt.stdout, "- "+pal.Red(rt.cat.T("doctor.dns_fail",
					"host", rep.ProbeHost, "error", rep.DNSError)))
			} else if rep.FakeIPDetected {
				_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.fake_ip_detected",
					"host", rep.ProbeHost, "ip", rep.ProbeIP))
			} else {
				_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.dns_ok",
					"host", rep.ProbeHost, "ip", rep.ProbeIP))
			}

			noneLabel := rt.cat.T("table.val.none")
			searchLabel := noneLabel
			if len(rep.SearchChain) > 0 {
				searchLabel = strings.Join(rep.SearchChain, " -> ")
			}
			fetchLabel := noneLabel
			if len(rep.FetchChain) > 0 {
				fetchLabel = strings.Join(rep.FetchChain, " -> ")
			}
			_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.search_chain", "strategy", rep.SearchStrategy, "chain", searchLabel))
			if len(rep.SearchChainEN) > 0 {
				_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.search_chain_en", "chain", strings.Join(rep.SearchChainEN, " -> ")))
			}
			if len(rep.FusionChain) > 0 {
				_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.fusion_chain", "chain", strings.Join(rep.FusionChain, " -> ")))
			}
			_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.fetch_chain", "strategy", rep.FetchStrategy, "chain", fetchLabel))
			if len(rep.FetchChainGlobal) > 0 {
				_, _ = fmt.Fprintln(rt.stdout, "- "+rt.cat.T("doctor.fetch_chain_global", "chain", strings.Join(rep.FetchChainGlobal, " -> ")))
			}

			_, _ = fmt.Fprintln(rt.stdout)
			printProviderTable(rt, rep.Providers)

			if len(rep.LiveChecks) > 0 {
				_, _ = fmt.Fprintln(rt.stdout)
				liveHeaders := []string{
					rt.cat.T("table.live.provider"),
					rt.cat.T("table.live.status"),
					rt.cat.T("table.live.latency"),
					rt.cat.T("table.live.cost"),
					rt.cat.T("table.live.detail"),
				}
				liveRows := make([][]string, 0, len(rep.LiveChecks))
				for _, lc := range rep.LiveChecks {
					costLabel := lc.Cost
					if costLabel == "" {
						costLabel = "-"
					}
					liveRows = append(liveRows, []string{
						lc.Provider,
						lc.Status,
						fmt.Sprintf("%dms", lc.LatencyMS),
						costLabel,
						lc.Error,
					})
				}
				term.WriteTable(rt.stdout, liveHeaders, liveRows, term.TableStyle{
					Header: pal.Bold,
					Cell: func(col int, val string) string {
						switch col {
						case 1:
							if val == "ok" {
								return pal.Green(val)
							}
							return pal.Red(val)
						case 3:
							if val == "-" {
								return pal.Dim(val)
							}
						}
						return val
					},
				})
			}

			_, _ = fmt.Fprintln(rt.stdout)
			if rep.Issues == 0 {
				_, _ = fmt.Fprintln(rt.stdout, pal.Green(rt.cat.T("doctor.summary_ok",
					"enabled", strconv.Itoa(rep.EnabledSearcher),
					"fetchers", strconv.Itoa(rep.EnabledFetcher))))
				return nil
			}
			_, _ = fmt.Fprintln(rt.stdout, pal.Yellow(rt.cat.T("doctor.summary_issues",
				"issues", strconv.Itoa(rep.Issues))))
			return exitErr
		},
	}

	cmd.Flags().BoolVar(&live, "live", false, rt.cat.T("doctor.flag.live"))
	cmd.Flags().BoolVar(&yes, "yes", false, rt.cat.T("doctor.flag.yes"))
	cmd.Flags().BoolVar(&jsonOut, "json", false, rt.cat.T("doctor.flag.json"))
	return cmd
}

func doctorExitError(rep app.DoctorReport) error {
	if rep.EnabledSearcher == 0 {
		return handledExitError{code: clikit.ExitConfig}
	}
	for _, lc := range rep.LiveChecks {
		if lc.Status != "ok" {
			return handledExitError{code: clikit.ExitUnavailable}
		}
	}
	return nil
}

func newConfigCmd(rt *runtimeEnv) *cobra.Command {
	cfgCmd := &cobra.Command{
		Use:     "config",
		GroupID: groupAdmin,
		Short:   rt.cat.T("config.short"),
		Example: rt.cat.T("config.example"),
	}
	requireSubcommand(cfgCmd)

	var force bool
	initCmd := &cobra.Command{
		Use:   "init",
		Short: rt.cat.T("config.init.short"),
		Args:  requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path, err := config.ResolvePath(rt.flags.configPath, rt.getenv)
			if err != nil {
				return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
			}
			if err := config.WriteTemplateForLocale(path, force, string(rt.uiLocale)); err != nil {
				return err
			}
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("config.init.done", "path", path))
			return nil
		},
	}
	initCmd.Flags().BoolVar(&force, "force", false, rt.cat.T("config.init.flag.force"))

	pathCmd := &cobra.Command{
		Use:   "path",
		Short: rt.cat.T("config.path.short"),
		Args:  requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			path, err := config.ResolvePath(rt.flags.configPath, rt.getenv)
			if err != nil {
				return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
			}
			_, _ = fmt.Fprintln(rt.stdout, path)
			return nil
		},
	}

	var showJSON bool
	showCmd := &cobra.Command{
		Use:   "show",
		Short: rt.cat.T("config.show.short"),
		Args:  requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			loaded, err := config.LoadWithEnv(rt.flags.configPath, rt.getenv)
			if err != nil {
				return err
			}
			if showJSON {
				jsonText, jErr := render.MarshalEnvelopeJSON(loaded.RedactedConfig())
				if jErr != nil {
					return jErr
				}
				_, _ = fmt.Fprint(rt.stdout, jsonText)
				return nil
			}
			redacted, err := loaded.FormatRedactedTOML()
			if err != nil {
				return core.WrapError(core.CodeInternal, "failed to render redacted config", err)
			}
			_, _ = fmt.Fprint(rt.stdout, redacted)
			return nil
		},
	}
	showCmd.Flags().BoolVar(&showJSON, "json", false, rt.cat.T("config.show.flag.json"))

	addOrderedCommands(cfgCmd,
		initCmd,
		pathCmd,
		showCmd,
	)
	return cfgCmd
}
