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

func newConfigCmd(rt *runtimeEnv) *cobra.Command {
	cfgCmd := &cobra.Command{
		Use:     "config",
		GroupID: groupAdmin,
		Short:   rt.cat.T("config.short"),
		Example: rt.cat.T("config.example"),
	}
	requireSubcommand(cfgCmd)

	addOrderedCommands(cfgCmd,
		newConfigSetCmd(rt),
		newConfigGetCmd(rt),
		newConfigUnsetCmd(rt),
		newConfigShowCmd(rt),
		newConfigInitCmd(rt),
		newConfigPathCmd(rt),
	)
	return cfgCmd
}

func newConfigSetCmd(rt *runtimeEnv) *cobra.Command {
	return &cobra.Command{
		Use:               "set <key> <value>",
		Short:             rt.cat.T("config.set.short"),
		Long:              rt.cat.T("config.set.long"),
		Example:           rt.cat.T("config.set.example"),
		ValidArgsFunction: completeConfigSetArgs,
		Args: func(_ *cobra.Command, args []string) error {
			switch len(args) {
			case 0:
				return core.NewError(core.CodeInvalidArgument, "configuration key is required").
					WithI18n("err.missing_config_key", "hint.config_set_usage")
			case 1:
				return core.NewError(core.CodeInvalidArgument, "configuration value is required").
					WithI18n("err.missing_config_value", "hint.config_set_usage")
			case 2:
				return nil
			default:
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("accepts 2 argument(s), received %d", len(args))).
					WithMsgKey("err.too_many_args").
					WithParam("max", "2").
					WithParam("count", strconv.Itoa(len(args)))
			}
		},
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := config.ResolvePath(rt.flags.configPath, rt.getenv)
			if err != nil {
				return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
			}
			res, err := config.SetValue(path, args[0], args[1], rt.getenv)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("config.set.done",
				"key", res.Key,
				"value", res.Formatted,
				"path", res.Path,
			))
			if res.ShadowedByEnv != "" && !rt.flags.quiet {
				pal := term.DetectPalette(rt.stderr, rt.flags.noColor, rt.getenv)
				_, _ = fmt.Fprintln(rt.stderr, pal.Yellow(rt.cat.T("config.set.env_shadow_warn",
					"key", res.Key,
					"env", res.ShadowedByEnv,
				)))
			}
			return nil
		},
	}
}

func newConfigGetCmd(rt *runtimeEnv) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:               "get <key>",
		Short:             rt.cat.T("config.get.short"),
		Example:           rt.cat.T("config.get.example"),
		ValidArgsFunction: completeConfigKeyArg,
		Args:              requireExactArgs(1, "err.missing_config_key", "hint.config_get_usage"),
		RunE: func(_ *cobra.Command, args []string) error {
			loaded, err := config.LoadWithEnv(rt.flags.configPath, rt.getenv)
			if err != nil {
				return err
			}
			res, err := config.GetValue(loaded, args[0])
			if err != nil {
				return err
			}
			if jsonOut {
				text, jErr := render.MarshalEnvelopeJSON(res)
				if jErr != nil {
					return jErr
				}
				_, _ = fmt.Fprint(rt.stdout, text)
				return nil
			}
			_, _ = fmt.Fprintln(rt.stdout, res.Formatted)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, rt.cat.T("config.get.flag.json"))
	return cmd
}

func newConfigUnsetCmd(rt *runtimeEnv) *cobra.Command {
	return &cobra.Command{
		Use:               "unset <key>",
		Aliases:           []string{"rm", "delete"},
		Short:             rt.cat.T("config.unset.short"),
		Example:           rt.cat.T("config.unset.example"),
		ValidArgsFunction: completeConfigKeyArg,
		Args:              requireExactArgs(1, "err.missing_config_key", "hint.config_unset_usage"),
		RunE: func(_ *cobra.Command, args []string) error {
			path, err := config.ResolvePath(rt.flags.configPath, rt.getenv)
			if err != nil {
				return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
			}
			res, err := config.UnsetValue(path, args[0])
			if err != nil {
				return err
			}
			if !res.Changed {
				_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("config.unset.none", "key", res.Key))
				return nil
			}
			if res.RemovedFile {
				_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("config.unset.done_removed",
					"key", res.Key,
					"path", res.Path,
				))
				return nil
			}
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("config.unset.done",
				"key", res.Key,
				"path", res.Path,
			))
			return nil
		},
	}
}

func newConfigShowCmd(rt *runtimeEnv) *cobra.Command {
	var showJSON bool
	var overridesOnly bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: rt.cat.T("config.show.short"),
		Args:  requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			loaded, err := config.LoadWithEnv(rt.flags.configPath, rt.getenv)
			if err != nil {
				return err
			}
			if overridesOnly {
				if showJSON {
					jsonText, jErr := render.MarshalEnvelopeJSON(config.OverridesMap(loaded))
					if jErr != nil {
						return jErr
					}
					_, _ = fmt.Fprint(rt.stdout, jsonText)
					return nil
				}
				tomlText, count := config.FormatOverridesTOML(loaded)
				if count == 0 {
					_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("config.show.no_overrides"))
					return nil
				}
				_, _ = fmt.Fprint(rt.stdout, tomlText)
				return nil
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
	cmd.Flags().BoolVar(&showJSON, "json", false, rt.cat.T("config.show.flag.json"))
	cmd.Flags().BoolVar(&overridesOnly, "overrides", false, rt.cat.T("config.show.flag.overrides"))
	return cmd
}

func newConfigInitCmd(rt *runtimeEnv) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
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
	cmd.Flags().BoolVar(&force, "force", false, rt.cat.T("config.init.flag.force"))
	return cmd
}

func newConfigPathCmd(rt *runtimeEnv) *cobra.Command {
	return &cobra.Command{
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
}

func completeConfigKeyArg(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return matchConfigKeys(toComplete), cobra.ShellCompDirectiveNoFileComp
}

func completeConfigSetArgs(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	switch len(args) {
	case 0:
		return matchConfigKeys(toComplete), cobra.ShellCompDirectiveNoFileComp
	case 1:
		info, ok := config.LookupKey(args[0])
		if !ok || len(info.EnumValues) == 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		prefix := strings.ToLower(strings.TrimSpace(toComplete))
		var out []string
		for _, v := range info.EnumValues {
			if strings.HasPrefix(strings.ToLower(v), prefix) {
				out = append(out, v)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	default:
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
}

func matchConfigKeys(toComplete string) []string {
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	keys := config.AllKeys()
	out := make([]string, 0, len(keys))
	for _, info := range keys {
		if strings.HasPrefix(info.Key, prefix) {
			out = append(out, info.Key)
		}
	}
	return out
}
