package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"veri/clikit/buildinfo"
	"veri/clikit/skill"
	"veri/clikit/term"
	"veri/internal/core"
	"veri/internal/mcpserver"
	"veri/internal/render"
	"veri/internal/spec"
)

const mcpTransportStdio = "stdio"

func newMCPCmd(rt *runtimeEnv) *cobra.Command {
	var transport string
	cmd := &cobra.Command{
		Use:                        "mcp",
		GroupID:                    groupAgent,
		Short:                      rt.cat.T("mcp.short"),
		Example:                    rt.cat.T("mcp.example"),
		SuggestionsMinimumDistance: 2,
		Args:                       noArgsWithSuggestions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !strings.EqualFold(strings.TrimSpace(transport), mcpTransportStdio) {
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported MCP transport %q (only stdio is supported)", transport)).
					WithMsgKey("err.unsupported_mcp_transport").
					WithParam("transport", transport)
			}
			application, err := rt.loadApp()
			if err != nil {
				return err
			}
			if term.IsTerminal(rt.stdin) && !rt.flags.quiet {
				_, _ = fmt.Fprintln(rt.stderr, rt.cat.T("mcp.stdio_ready"))
			}
			runErr := mcpserver.RunIO(cmd.Context(), application, rt.info.Version, rt.stdin, rt.stdout)
			if runErr == nil || errors.Is(runErr, context.Canceled) {
				return runErr
			}
			if se, ok := errors.AsType[*core.Error](runErr); ok {
				return se
			}
			return core.WrapError(core.CodeInternal, "MCP stdio transport error", runErr)
		},
	}
	cmd.Flags().StringVar(&transport, "transport", mcpTransportStdio, rt.cat.T("mcp.flag.transport"))
	cmd.AddCommand(newMCPConfigCmd(rt))
	return cmd
}

func newMCPConfigCmd(rt *runtimeEnv) *cobra.Command {
	var fullPath bool
	cfgCmd := &cobra.Command{
		Use:   "config",
		Short: rt.cat.T("mcp.config.short"),
		Args:  requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			bin := spec.AppName
			if fullPath {
				if exe, err := os.Executable(); err == nil && strings.TrimSpace(exe) != "" {
					bin = exe
				}
			}
			snippet := map[string]any{
				"mcpServers": map[string]any{
					spec.AppName: map[string]any{
						"command": bin,
						"args":    []string{"mcp"},
					},
				},
			}
			rendered, err := render.MarshalEnvelopeJSON(snippet)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprint(rt.stdout, rendered)
			return nil
		},
	}
	cfgCmd.Flags().BoolVar(&fullPath, "full-path", false, rt.cat.T("mcp.config.flag.full_path"))
	return cfgCmd
}

func newSkillCmd(rt *runtimeEnv) *cobra.Command {
	skillCmd := &cobra.Command{
		Use:     "skill",
		GroupID: groupAgent,
		Short:   rt.cat.T("skill.short"),
		Long:    rt.cat.T("skill.long"),
		Example: rt.cat.T("skill.example"),
	}
	requireSubcommand(skillCmd)
	printCmd := &cobra.Command{
		Use:   "print",
		Short: rt.cat.T("skill.print.short"),
		Args:  requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			md, err := skill.Read(rt.assets, spec.SkillAssetPath)
			if err != nil {
				return core.WrapError(core.CodeInternal, err.Error(), err)
			}
			_, _ = fmt.Fprint(rt.stdout, md)
			return nil
		},
	}

	var opts skill.InstallOptions
	installCmd := &cobra.Command{
		Use:   "install",
		Short: rt.cat.T("skill.install.short"),
		Args:  requireNoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("dir") {
				if strings.TrimSpace(opts.Dir) == "" {
					return core.NewError(core.CodeInvalidArgument, "`--dir` must not be empty").
						WithMsgKey("err.empty_skill_dir")
				}
				if opts.Project {
					return core.NewError(core.CodeInvalidArgument, "conflicting flags: `--dir` and `--project`").
						WithMsgKey("err.flag_conflict").
						WithParam("flags", "--dir, --project")
				}
			}
			dest, err := skill.Install(rt.assets, spec.SkillAssetPath, spec.AppName, opts)
			if err != nil {
				if errors.Is(err, skill.ErrAlreadyExists) {
					return core.NewError(core.CodeInvalidArgument, err.Error()).
						WithHint("Pass `--force` to overwrite the existing file.").
						WithI18n("err.skill_exists", "hint.use_force").
						WithParam("path", dest)
				}
				if errors.Is(err, skill.ErrInvalidPath) {
					return core.NewError(core.CodeInvalidArgument, err.Error())
				}
				return core.WrapError(core.CodeInternal, err.Error(), err)
			}
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("skill.install.done", "path", dest))
			return nil
		},
	}
	installCmd.Flags().StringVar(&opts.Dir, "dir", "", rt.cat.T("skill.install.flag.dir"))
	installCmd.Flags().BoolVar(&opts.Project, "project", false, rt.cat.T("skill.install.flag.project"))
	installCmd.Flags().BoolVar(&opts.Force, "force", false, rt.cat.T("skill.install.flag.force"))

	addOrderedCommands(skillCmd, installCmd, printCmd)
	return skillCmd
}

func newVersionCmd(rt *runtimeEnv) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "version",
		GroupID: groupAdmin,
		Short:   rt.cat.T("version.short"),
		Args:    requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if jsonOut {
				text, err := render.MarshalEnvelopeJSON(rt.info)
				if err != nil {
					return err
				}
				_, _ = fmt.Fprint(rt.stdout, text)
				return nil
			}
			_, _ = fmt.Fprintln(rt.stdout, buildinfo.FormatLine(spec.AppName, rt.info))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, rt.cat.T("version.flag.json"))
	return cmd
}
