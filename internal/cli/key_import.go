package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"veri/clikit/term"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/render"
)

func newKeyImportCmd(rt *runtimeEnv) *cobra.Command {
	var fromConfig bool
	var dryRun bool
	var overwrite bool
	var keepConfig bool
	var jsonOut bool

	cmd := &cobra.Command{
		Use:     "import [<file> | -]",
		Short:   rt.cat.T("key.import.short"),
		Long:    rt.cat.T("key.import.long"),
		Example: rt.cat.T("key.import.example"),
		ValidArgsFunction: func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
			if fromConfig || len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return nil, cobra.ShellCompDirectiveDefault
		},
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 1 {
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("accepts at most 1 argument(s), received %d", len(args))).
					WithMsgKey("err.too_many_args").
					WithParam("max", "1").
					WithParam("count", strconv.Itoa(len(args)))
			}
			return nil
		},
		RunE: func(_ *cobra.Command, args []string) error {
			if fromConfig && len(args) > 0 {
				return core.NewError(core.CodeInvalidArgument, "cannot combine --from-config with a file argument").
					WithMsgKey("err.flag_conflict").
					WithParam("flags", "--from-config, <file>")
			}
			if !fromConfig && len(args) == 0 {
				return core.NewError(core.CodeInvalidArgument, "missing import source").
					WithI18n("err.missing_key_import_source", "hint.key_import_usage")
			}
			if keepConfig && !fromConfig {
				return core.NewError(core.CodeInvalidArgument, "--keep-config requires --from-config").
					WithI18n("err.key_import_keep_config_conflict", "hint.key_import_usage")
			}

			opts := config.ImportOptions{
				DryRun:     dryRun,
				Overwrite:  overwrite,
				KeepConfig: keepConfig,
			}

			secretsDir, err := config.ResolveSecretsDir(rt.flags.configPath, rt.getenv)
			if err != nil {
				return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
			}

			var res config.ImportResult
			if fromConfig {
				cfgPath, err := config.ResolvePath(rt.flags.configPath, rt.getenv)
				if err != nil {
					return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
				}
				res, err = config.ImportKeysFromConfigToVault(cfgPath, secretsDir, opts)
				if err != nil {
					return err
				}
			} else {
				if args[0] == "-" {
					res, err = config.ImportKeysFromEnvReader(secretsDir, "stdin", rt.stdin, opts)
				} else {
					res, err = config.ImportKeysFromEnvFile(secretsDir, args[0], opts)
				}
				if err != nil {
					return err
				}
			}

			if jsonOut {
				text, jErr := render.MarshalEnvelopeJSON(res)
				if jErr != nil {
					return jErr
				}
				_, _ = fmt.Fprint(rt.stdout, text)
				return nil
			}
			renderKeyImportResult(rt, res)
			return nil
		},
	}

	cmd.Flags().BoolVar(&fromConfig, "from-config", false, rt.cat.T("key.import.flag.from_config"))
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, rt.cat.T("key.import.flag.dry_run"))
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, rt.cat.T("key.import.flag.overwrite"))
	cmd.Flags().BoolVar(&keepConfig, "keep-config", false, rt.cat.T("key.import.flag.keep_config"))
	cmd.Flags().BoolVar(&jsonOut, "json", false, rt.cat.T("key.import.flag.json"))
	return cmd
}

func renderKeyImportResult(rt *runtimeEnv, res config.ImportResult) {
	cat := rt.cat
	pal := term.DetectPalette(rt.stdout, rt.flags.noColor, rt.getenv)

	if len(res.Entries) == 0 {
		if res.Source == "config" {
			_, _ = fmt.Fprintln(rt.stdout, cat.T("key.import.none_config", "path", res.SourcePath))
		} else {
			_, _ = fmt.Fprintln(rt.stdout, cat.T("key.import.none_env"))
		}
		printIgnoredEnvKeys(rt, pal, res.IgnoredKeys)
		return
	}

	importedLabel := cat.T("key.import.action.imported")
	updatedLabel := cat.T("key.import.action.updated")
	cleanedLabel := cat.T("key.import.action.cleaned")
	skippedLabel := cat.T("key.import.action.skipped")

	headers := []string{
		cat.T("table.import.provider"),
		cat.T("table.import.action"),
		cat.T("table.import.masked"),
		cat.T("table.import.detail"),
	}
	rows := make([][]string, 0, len(res.Entries))
	for _, entry := range res.Entries {
		actionText := formatImportAction(entry.Action, importedLabel, updatedLabel, cleanedLabel, skippedLabel)
		masked := entry.Masked
		if masked == "" {
			masked = "-"
		}
		rows = append(rows, []string{
			entry.Provider,
			actionText,
			masked,
			formatImportDetail(rt, entry),
		})
	}

	term.WriteTable(rt.stdout, headers, rows, term.TableStyle{
		Header: pal.Bold,
		Cell: func(col int, val string) string {
			switch col {
			case 1:
				switch val {
				case importedLabel, updatedLabel:
					return pal.Green(val)
				case cleanedLabel:
					return pal.Cyan(val)
				case skippedLabel:
					return pal.Yellow(val)
				}
			case 2:
				if val == "-" {
					return pal.Dim(val)
				}
			}
			return val
		},
	})

	_, _ = fmt.Fprintln(rt.stdout)
	if res.DryRun {
		_, _ = fmt.Fprintln(rt.stdout, pal.Cyan(cat.T("key.import.dry_run_summary",
			"imported", strconv.Itoa(res.ImportedCount),
			"updated", strconv.Itoa(res.UpdatedCount),
			"cleaned", strconv.Itoa(res.CleanedCount),
			"skipped", strconv.Itoa(res.SkippedCount),
		)))
	} else {
		summary := cat.T("key.import.summary",
			"path", res.SecretsPath,
			"imported", strconv.Itoa(res.ImportedCount),
			"updated", strconv.Itoa(res.UpdatedCount),
			"cleaned", strconv.Itoa(res.CleanedCount),
			"skipped", strconv.Itoa(res.SkippedCount),
		)
		if res.ImportedCount == 0 && res.UpdatedCount == 0 && res.CleanedCount == 0 {
			_, _ = fmt.Fprintln(rt.stdout, pal.Yellow(summary))
		} else {
			_, _ = fmt.Fprintln(rt.stdout, pal.Green(summary))
		}
	}

	if res.Source == "config" && res.ConfigUpdated {
		switch {
		case res.DryRun && res.ConfigRemoved:
			_, _ = fmt.Fprintln(rt.stdout, cat.T("key.import.config_dry_remove", "path", res.SourcePath))
		case res.DryRun:
			_, _ = fmt.Fprintln(rt.stdout, cat.T("key.import.config_dry_clean", "path", res.SourcePath))
		case res.ConfigRemoved:
			_, _ = fmt.Fprintln(rt.stdout, cat.T("key.import.config_removed", "path", res.SourcePath))
		default:
			_, _ = fmt.Fprintln(rt.stdout, cat.T("key.import.config_cleaned", "path", res.SourcePath))
		}
	}

	printIgnoredEnvKeys(rt, pal, res.IgnoredKeys)
}

func printIgnoredEnvKeys(rt *runtimeEnv, pal term.Palette, keys []string) {
	if len(keys) == 0 {
		return
	}
	_, _ = fmt.Fprintln(rt.stdout, pal.Dim(rt.cat.T("key.import.ignored_note",
		"count", strconv.Itoa(len(keys)),
		"keys", strings.Join(keys, ", "),
	)))
}

func formatImportAction(action config.ImportAction, importedLabel, updatedLabel, cleanedLabel, skippedLabel string) string {
	switch action {
	case config.ImportActionImported:
		return importedLabel
	case config.ImportActionUpdated:
		return updatedLabel
	case config.ImportActionCleaned:
		return cleanedLabel
	case config.ImportActionSkipped:
		return skippedLabel
	default:
		return string(action)
	}
}

func formatImportDetail(rt *runtimeEnv, entry config.ImportEntry) string {
	cat := rt.cat
	switch entry.Action {
	case config.ImportActionImported:
		if entry.ConfigCleaned {
			return cat.T("key.import.detail.imported_cleaned")
		}
		return cat.T("key.import.detail.imported")
	case config.ImportActionUpdated:
		if entry.ConfigCleaned {
			return cat.T("key.import.detail.updated_cleaned")
		}
		return cat.T("key.import.detail.updated")
	case config.ImportActionCleaned:
		return cat.T("key.import.detail.cleaned")
	case config.ImportActionSkipped:
		switch entry.Reason {
		case config.ImportSkipVaultConflict:
			return cat.T("key.import.detail.conflict")
		case config.ImportSkipAlreadyStored:
			return cat.T("key.import.detail.already_stored")
		case config.ImportSkipMixedEnvRef:
			return cat.T("key.import.detail.mixed_env")
		}
	}
	return "-"
}
