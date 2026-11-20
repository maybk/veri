package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"veri/clikit/redact"
	"veri/clikit/term"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/render"
	"veri/internal/spec"
	"veri/internal/update"
)

type updateFlags struct {
	check   bool
	version string
	mirror  string
	force   bool
	jsonOut bool
}

func newUpdateCmd(rt *runtimeEnv) *cobra.Command {
	var f updateFlags
	cmd := &cobra.Command{
		Use:     "update",
		GroupID: groupAdmin,
		Short:   rt.cat.T("update.short"),
		Long:    rt.cat.T("update.long"),
		Example: rt.cat.T("update.example"),
		Args:    requireNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rt.cmdKind = spec.KindUpdate
			if f.check && f.force {
				return core.NewError(core.CodeInvalidArgument, "conflicting flags: `--check` and `--force`").
					WithMsgKey("err.flag_conflict").
					WithParam("flags", "--check, --force")
			}

			updater, err := rt.newUpdater(f.mirror)
			if err != nil {
				return err
			}

			if f.check {
				info, cErr := updater.Check(cmd.Context(), f.version)
				if cErr != nil {
					return cErr
				}
				return renderUpdateCheck(rt, info, f.jsonOut)
			}

			progressFn, finishProgress := buildUpdateProgressPrinter(rt, f.jsonOut)
			res, aErr := updater.Apply(cmd.Context(), update.ApplyOptions{
				TargetVersion: f.version,
				Force:         f.force,
				OnProgress:    progressFn,
			})
			finishProgress()
			if aErr != nil {
				return aErr
			}
			return renderUpdateApply(rt, res, f.jsonOut)
		},
	}

	cmd.Flags().BoolVar(&f.check, "check", false, rt.cat.T("update.flag.check"))
	cmd.Flags().StringVar(&f.version, "version", "", rt.cat.T("update.flag.version"))
	cmd.Flags().StringVar(&f.mirror, "mirror", "", rt.cat.T("update.flag.mirror"))
	cmd.Flags().BoolVar(&f.force, "force", false, rt.cat.T("update.flag.force"))
	cmd.Flags().BoolVar(&f.jsonOut, "json", false, rt.cat.T("update.flag.json"))
	return cmd
}

func (rt *runtimeEnv) newUpdater(flagMirror string) (*update.Updater, error) {
	proxyURL := ""
	explicitConfig := strings.TrimSpace(rt.flags.configPath) != "" ||
		(rt.getenv != nil && strings.TrimSpace(rt.getenv(spec.EnvConfig)) != "")
	if loaded, err := config.LoadWithEnv(rt.flags.configPath, rt.getenv); err == nil {
		proxyURL = loaded.Config.Network.Proxy
		rt.scrubber = redact.NewScrubber(loaded.AllSecrets)
	} else if explicitConfig {
		// 显式指定了 --config 或 VERI_CONFIG 时，配置文件异常应直接暴露给用户。
		return nil, err
	}

	mirror := strings.TrimSpace(flagMirror)
	if mirror == "" && rt.getenv != nil {
		mirror = strings.TrimSpace(rt.getenv(spec.EnvUpdateMirror))
	}

	opts := make([]update.Option, 0, 2+len(rt.updateOpts))
	if proxyURL != "" {
		opts = append(opts, update.WithProxyURL(proxyURL))
	}
	if mirror != "" {
		opts = append(opts, update.WithMirrorPrefix(mirror))
	}
	opts = append(opts, rt.updateOpts...)
	return update.New(rt.info.Version, opts...)
}

func buildUpdateProgressPrinter(rt *runtimeEnv, jsonOut bool) (update.ProgressFunc, func()) {
	if jsonOut || rt.flags.quiet {
		return nil, func() {}
	}
	isTTY := term.IsTerminal(rt.stderr)
	lastStage := ""
	inlineActive := false

	finish := func() {
		if inlineActive {
			_, _ = fmt.Fprintln(rt.stderr)
			inlineActive = false
		}
	}

	cb := func(p update.Progress) {
		switch p.Stage {
		case update.StageChecking:
			if lastStage != p.Stage {
				lastStage = p.Stage
				_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("update.progress.checking"))
			}
		case update.StageDownloading:
			if isTTY && p.TotalBytes > 0 {
				lastStage = p.Stage
				inlineActive = true
				_, _ = fmt.Fprintf(rt.stderr, "\r%s", rt.cat.T("update.progress.downloading_pct", "percent", fmt.Sprintf("%3d", p.Percent)))
			} else if lastStage != p.Stage {
				lastStage = p.Stage
				_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("update.progress.downloading"))
			}
		case update.StageVerifying:
			if lastStage != p.Stage {
				finish()
				lastStage = p.Stage
				_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("update.progress.verifying"))
			}
		case update.StageInstalling:
			if lastStage != p.Stage {
				finish()
				lastStage = p.Stage
				_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("update.progress.installing"))
			}
		}
	}
	return cb, finish
}

func formatDisplayVersion(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "dev"
	}
	if update.IsDevVersion(v) {
		return v
	}
	return "v" + strings.TrimPrefix(v, "v")
}

func renderUpdateCheck(rt *runtimeEnv, info update.ReleaseInfo, jsonOut bool) error {
	if jsonOut {
		text, err := render.MarshalEnvelopeJSON(info)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(rt.stdout, text)
		return nil
	}

	pal := term.DetectPalette(rt.stdout, rt.flags.noColor, rt.getenv)
	curDisp := formatDisplayVersion(info.CurrentVersion)
	latDisp := formatDisplayVersion(info.LatestVersion)

	switch {
	case update.IsDevVersion(info.CurrentVersion):
		_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("update.check.dev_build",
			"current", curDisp,
			"latest", latDisp,
		))
		if info.ReleaseURL != "" {
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("update.check.release_url", "url", info.ReleaseURL))
		}
		return nil
	case info.HasUpdate:
		_, _ = fmt.Fprintln(rt.stdout, pal.Green(rt.cat.T("update.check.available",
			"current", curDisp,
			"latest", latDisp,
		)))
		if info.ReleaseURL != "" {
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("update.check.release_url", "url", info.ReleaseURL))
		}
		switch {
		case info.ManagedCommand != "":
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("update.check.hint_managed", "command", info.ManagedCommand))
		case info.InstallMethod == update.MethodSystem:
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("update.check.hint_system"))
		case info.CanSelfUpdate:
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("update.check.hint_self"))
		}
		return nil
	case info.IsSameVersion:
		_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("update.check.up_to_date", "current", curDisp))
		return nil
	default:
		_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("update.check.target_older",
			"current", curDisp,
			"target", latDisp,
		))
		return nil
	}
}

func renderUpdateApply(rt *runtimeEnv, res update.ApplyResult, jsonOut bool) error {
	if jsonOut {
		text, err := render.MarshalEnvelopeJSON(res)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprint(rt.stdout, text)
		return nil
	}

	pal := term.DetectPalette(rt.stdout, rt.flags.noColor, rt.getenv)
	curDisp := formatDisplayVersion(res.CurrentVersion)
	if res.AlreadyLatest {
		_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("update.apply.already_latest", "version", curDisp))
		return nil
	}
	_, _ = fmt.Fprintln(rt.stdout, pal.Green(rt.cat.T("update.apply.done",
		"current", curDisp,
		"target", formatDisplayVersion(res.TargetVersion),
		"path", res.ExecutablePath,
	)))
	return nil
}
