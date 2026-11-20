package cli

import (
	"cmp"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"veri/clikit/i18n"
	"veri/clikit/term"
)

const (
	annKind    = "veri_kind"
	annOrder   = "veri_order"
	annDefault = "veri_default"
	kindGroup  = "group"
)

func setFlagDisplayDefault(fs *pflag.FlagSet, name, val string) {
	_ = fs.SetAnnotation(name, annDefault, []string{val})
}

// flagPlaceholders 为非布尔命令行标志提供语义化参数占位符，替代 pflag 反射内部类型名（如 stringArray）。
var flagPlaceholders = map[string]string{
	"config":       "<path>",
	"ui-lang":      "<lang>",
	"count":        "<n>",
	"detail":       "<level>",
	"provider":     "<list>",
	"strategy":     "<mode>",
	"site":         "<domain>",
	"exclude-site": "<domain>",
	"freshness":    "<range>",
	"lang":         "<codes>",
	"question":     "<text>",
	"opt":          "<k=v>",
	"format":       "<fmt>",
	"max-chars":    "<n>",
	"offset":       "<n>",
	"section":      "<heading>",
	"match":        "<query>",
	"timeout":      "<duration>",
	"transport":    "<mode>",
	"target":       "<agent>",
	"dir":          "<path>",
	"version":      "<ver>",
	"mirror":       "<url>",
}

// rootCoreSearchFlags 定义根命令首屏帮助渐进式披露的高频检索标志集合（完整列表在 `veri search --help` 展示）。
var rootCoreSearchFlags = []string{
	"count",
	"detail",
	"provider",
	"site",
	"freshness",
	"format",
	"json",
}

func addOrderedCommands(parent *cobra.Command, cmds ...*cobra.Command) {
	base := len(parent.Commands()) * 10
	for idx, c := range cmds {
		setAnnotation(c, annOrder, strconv.Itoa(base+(idx+1)*10))
		parent.AddCommand(c)
	}
}

func markGroupCommand(cmd *cobra.Command) {
	setAnnotation(cmd, annKind, kindGroup)
}

func isGroupCommand(cmd *cobra.Command) bool {
	return cmd != nil && cmd.Annotations[annKind] == kindGroup
}

func setAnnotation(cmd *cobra.Command, key, val string) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string, 2)
	}
	cmd.Annotations[key] = val
}

func commandOrder(cmd *cobra.Command) int {
	if cmd == nil {
		return 0
	}
	if v, err := strconv.Atoi(cmd.Annotations[annOrder]); err == nil && v > 0 {
		return v
	}
	return 1000
}

// installHelpRenderer 接管命令树的内置补全/帮助子命令与 Help/Usage 渲染器，确保双语排版与跨平台展示完全一致。
func installHelpRenderer(rootCmd *cobra.Command, rt *runtimeEnv) {
	rootCmd.InitDefaultHelpCmd()
	rootCmd.InitDefaultCompletionCmd()

	for _, sub := range rootCmd.Commands() {
		switch sub.Name() {
		case "help":
			sub.Hidden = true
		case "completion":
			sub.GroupID = groupAdmin
			setAnnotation(sub, annOrder, "65")
			sub.Short = rt.cat.T("completion.short")
			sub.Long = rt.cat.T("completion.short")
			sub.Example = ""
			requireSubcommand(sub)
			for sIdx, shCmd := range sub.Commands() {
				setAnnotation(shCmd, annOrder, strconv.Itoa((sIdx+1)*10))
				shCmd.Short = rt.cat.T("completion.shell.short", "shell", shCmd.Name())
				shCmd.Long = shCmd.Short
				shCmd.Args = requireNoArgs
				if nd := shCmd.Flags().Lookup("no-descriptions"); nd != nil {
					nd.Usage = rt.cat.T("completion.flag.no_descriptions")
				}
			}
		}
	}

	configureCommandTree(rootCmd, rt)
}

func configureCommandTree(cmd *cobra.Command, rt *runtimeEnv) {
	cmd.Flags().SortFlags = false
	cmd.PersistentFlags().SortFlags = false
	cmd.InitDefaultHelpFlag()
	if hf := cmd.Flags().Lookup("help"); hf != nil {
		hf.Usage = rt.cat.T("flag.help")
	}

	cmd.SetHelpFunc(func(c *cobra.Command, _ []string) {
		renderCommandHelp(c.OutOrStdout(), c, rt)
	})
	cmd.SetUsageFunc(func(c *cobra.Command) error {
		renderCommandHelp(c.ErrOrStderr(), c, rt)
		return nil
	})

	for _, sub := range cmd.Commands() {
		configureCommandTree(sub, rt)
	}
}

func renderCommandHelp(w io.Writer, cmd *cobra.Command, rt *runtimeEnv) {
	pal := term.DetectPalette(w, rt.flags.noColor, rt.getenv)
	cat := rt.cat

	desc := strings.TrimSpace(cmd.Long)
	if desc == "" {
		desc = strings.TrimSpace(cmd.Short)
	}
	if desc != "" {
		_, _ = fmt.Fprintln(w, desc)
		_, _ = fmt.Fprintln(w)
	}

	_, _ = fmt.Fprintln(w, pal.Bold(cat.T("help.section.usage")))
	for _, uLine := range buildUsageLines(cmd) {
		_, _ = fmt.Fprintln(w, "  "+uLine)
	}

	if len(cmd.Aliases) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, pal.Bold(cat.T("help.section.aliases")))
		allNames := append([]string{cmd.Name()}, cmd.Aliases...)
		_, _ = fmt.Fprintln(w, "  "+strings.Join(allNames, ", "))
	}

	if ex := strings.TrimRight(cmd.Example, "\r\n"); strings.TrimSpace(ex) != "" {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, pal.Bold(cat.T("help.section.examples")))
		_, _ = fmt.Fprintln(w, ex)
	}

	visibleSubs := visibleSubcommands(cmd)
	if len(visibleSubs) > 0 {
		maxCmdWidth := 0
		for _, sub := range visibleSubs {
			if dw := term.DisplayWidth(sub.Name()); dw > maxCmdWidth {
				maxCmdWidth = dw
			}
		}
		if len(cmd.Groups()) > 0 {
			for _, grp := range cmd.Groups() {
				var inGroup []*cobra.Command
				for _, sub := range visibleSubs {
					if sub.GroupID == grp.ID {
						inGroup = append(inGroup, sub)
					}
				}
				if len(inGroup) == 0 {
					continue
				}
				_, _ = fmt.Fprintln(w)
				_, _ = fmt.Fprintln(w, pal.Bold(grp.Title))
				writeCommandRows(w, inGroup, maxCmdWidth)
			}
		}
		var ungrouped []*cobra.Command
		for _, sub := range visibleSubs {
			if sub.GroupID == "" {
				ungrouped = append(ungrouped, sub)
			}
		}
		if len(ungrouped) > 0 {
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, pal.Bold(cat.T("help.section.commands")))
			writeCommandRows(w, ungrouped, maxCmdWidth)
		}
	}

	if cmd == cmd.Root() {
		coreFlags := lookupFlagsByName(cmd.LocalFlags(), rootCoreSearchFlags)
		globalFlags := collectFlags(cmd.PersistentFlags(), nil)
		globalFlags = append(globalFlags, lookupFlagsByName(cmd.Flags(), []string{"version", "help"})...)
		layout := measureFlagLayout(coreFlags, globalFlags)
		if len(coreFlags) > 0 {
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, pal.Bold(cat.T("help.section.search_flags")))
			writeFlagRows(w, coreFlags, layout, cat)
			_, _ = fmt.Fprintln(w, pal.Dim(cat.T("help.more_search_flags")))
		}
		if len(globalFlags) > 0 {
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, pal.Bold(cat.T("help.section.global_flags")))
			writeFlagRows(w, globalFlags, layout, cat)
		}
	} else {
		localFlags := collectFlags(cmd.NonInheritedFlags(), map[string]bool{"help": true})
		globalFlags := collectFlags(cmd.Root().PersistentFlags(), nil)
		if hf := cmd.Flags().Lookup("help"); hf != nil && !hf.Hidden {
			globalFlags = append(globalFlags, hf)
		}
		layout := measureFlagLayout(localFlags, globalFlags)
		if len(localFlags) > 0 {
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, pal.Bold(cat.T("help.section.flags")))
			writeFlagRows(w, localFlags, layout, cat)
		}
		if len(globalFlags) > 0 {
			_, _ = fmt.Fprintln(w)
			_, _ = fmt.Fprintln(w, pal.Bold(cat.T("help.section.global_flags")))
			writeFlagRows(w, globalFlags, layout, cat)
		}
	}

	if len(visibleSubs) > 0 {
		_, _ = fmt.Fprintln(w)
		_, _ = fmt.Fprintln(w, cat.T("help.footer", "cmd", cmd.CommandPath()))
	}
}

func buildUsageLines(cmd *cobra.Command) []string {
	path := cmd.CommandPath()
	if cmd == cmd.Root() {
		return []string{
			path + " [query...] [flags]",
			path + " <command> [flags]",
		}
	}
	if isGroupCommand(cmd) {
		return []string{path + " <command> [flags]"}
	}
	var lines []string
	if cmd.Runnable() {
		_, argSpec, hasArgs := strings.Cut(strings.TrimSpace(cmd.Use), " ")
		line := path
		if hasArgs && strings.TrimSpace(argSpec) != "" {
			line += " " + strings.TrimSpace(argSpec)
		}
		line += " [flags]"
		lines = append(lines, line)
	}
	if len(visibleSubcommands(cmd)) > 0 {
		lines = append(lines, path+" <command> [flags]")
	}
	return lines
}

func visibleSubcommands(cmd *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, sub := range cmd.Commands() {
		if sub.Hidden || (!sub.IsAvailableCommand() && sub.Name() != "completion") {
			continue
		}
		out = append(out, sub)
	}
	slices.SortStableFunc(out, func(a, b *cobra.Command) int {
		if c := cmp.Compare(commandOrder(a), commandOrder(b)); c != 0 {
			return c
		}
		return cmp.Compare(a.Name(), b.Name())
	})
	return out
}

func writeCommandRows(w io.Writer, cmds []*cobra.Command, maxNameWidth int) {
	for _, sub := range cmds {
		pad := max(maxNameWidth-term.DisplayWidth(sub.Name())+3, 2)
		_, _ = fmt.Fprintln(w, "  "+sub.Name()+strings.Repeat(" ", pad)+sub.Short)
	}
}

func lookupFlagsByName(fs *pflag.FlagSet, names []string) []*pflag.Flag {
	out := make([]*pflag.Flag, 0, len(names))
	for _, name := range names {
		if f := fs.Lookup(name); f != nil && !f.Hidden {
			out = append(out, f)
		}
	}
	return out
}

func collectFlags(fs *pflag.FlagSet, skip map[string]bool) []*pflag.Flag {
	var out []*pflag.Flag
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Deprecated != "" || skip[f.Name] {
			return
		}
		out = append(out, f)
	})
	return out
}

type flagColumnLayout struct {
	alignShorthand bool
	maxLeft        int
}

func measureFlagLayout(groups ...[]*pflag.Flag) flagColumnLayout {
	var layout flagColumnLayout
	for _, flags := range groups {
		for _, f := range flags {
			if f.Shorthand != "" {
				layout.alignShorthand = true
				break
			}
		}
		if layout.alignShorthand {
			break
		}
	}
	for _, flags := range groups {
		for _, f := range flags {
			if dw := term.DisplayWidth(formatFlagSpec(f, layout.alignShorthand)); dw > layout.maxLeft {
				layout.maxLeft = dw
			}
		}
	}
	return layout
}

func writeFlagRows(w io.Writer, flags []*pflag.Flag, layout flagColumnLayout, cat i18n.Catalog) {
	for _, f := range flags {
		left := formatFlagSpec(f, layout.alignShorthand)
		desc := f.Usage
		if defVal, ok := resolveFlagDefault(f); ok {
			desc += cat.T("help.default", "value", defVal)
		}
		pad := max(layout.maxLeft-term.DisplayWidth(left)+3, 2)
		_, _ = fmt.Fprintln(w, left+strings.Repeat(" ", pad)+desc)
	}
}

func formatFlagSpec(f *pflag.Flag, alignShorthand bool) string {
	var b strings.Builder
	switch {
	case f.Shorthand != "":
		b.WriteString("  -")
		b.WriteString(f.Shorthand)
		b.WriteString(", --")
		b.WriteString(f.Name)
	case alignShorthand:
		b.WriteString("      --")
		b.WriteString(f.Name)
	default:
		b.WriteString("  --")
		b.WriteString(f.Name)
	}
	if f.Value.Type() != "bool" {
		ph := flagPlaceholders[f.Name]
		if ph == "" {
			ph = "<value>"
		}
		b.WriteByte(' ')
		b.WriteString(ph)
	}
	return b.String()
}

func resolveFlagDefault(f *pflag.Flag) (string, bool) {
	if f.Value.Type() == "bool" {
		return "", false
	}
	if vals := f.Annotations[annDefault]; len(vals) > 0 && vals[0] != "" {
		return vals[0], true
	}
	switch f.DefValue {
	case "", "0", "0s", "[]", "<nil>":
		return "", false
	default:
		return f.DefValue, true
	}
}
