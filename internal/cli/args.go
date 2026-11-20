package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"veri/internal/core"
)

// requireNoArgs 拒绝任意位置参数并返回本地化错误，替代 cobra.NoArgs 的纯英文报错。
func requireNoArgs(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("accepts 0 arguments, received %d", len(args))).
		WithMsgKey("err.too_many_args").
		WithParam("max", "0").
		WithParam("count", strconv.Itoa(len(args)))
}

// requireExactArgs 校验固定数量的位置参数并返回本地化缺参/多参错误。
func requireExactArgs(want int, missingMsgKey, missingHintKey string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) < want {
			se := core.NewError(core.CodeInvalidArgument, fmt.Sprintf("requires %d argument(s), received %d", want, len(args))).WithMsgKey(missingMsgKey)
			if missingHintKey != "" {
				se = se.WithHintKey(missingHintKey)
			}
			return se
		}
		if len(args) > want {
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("accepts %d argument(s), received %d", want, len(args))).
				WithMsgKey("err.too_many_args").
				WithParam("max", strconv.Itoa(want)).
				WithParam("count", strconv.Itoa(len(args)))
		}
		return nil
	}
}

// requireMinArgs 返回带本地化错误键的最少位置参数校验器，替代 cobra.MinimumNArgs 的纯英文报错。
func requireMinArgs(minArgs int, msgKey, hintKey string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) >= minArgs {
			return nil
		}
		se := core.NewError(core.CodeInvalidArgument, fmt.Sprintf("requires at least %d argument(s), received %d", minArgs, len(args))).
			WithMsgKey(msgKey)
		if hintKey != "" {
			se = se.WithHintKey(hintKey)
		}
		return se
	}
}

// noArgsWithSuggestions 用于既可直接执行又包含子命令的命令（如 `veri mcp`），对未知子命令提供拼写纠正提示。
func noArgsWithSuggestions(c *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return unknownSubcommandError(c, strings.TrimSpace(args[0]))
}

// requireSubcommand 为仅作分组的父命令绑定帮助与未知子命令拦截，防止 `veri config typo` 或 `veri completion bogus` 静默返回退出码 0。
func requireSubcommand(cmd *cobra.Command) {
	markGroupCommand(cmd)
	cmd.SuggestionsMinimumDistance = 2
	cmd.Args = cobra.ArbitraryArgs
	cmd.RunE = func(c *cobra.Command, args []string) error {
		if len(args) == 0 {
			return c.Help()
		}
		sub := strings.TrimSpace(args[0])
		lower := strings.ToLower(sub)
		if c.Name() == "config" && (lower == "set-key" || lower == "unset-key") {
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown subcommand %q for %q", sub, c.CommandPath())).
				WithHint("Use `veri key set <provider>` or `veri key unset <provider>`.").
				WithI18n("err.unknown_command", "hint.config_key_moved").
				WithParam("cmd", sub)
		}
		return unknownSubcommandError(c, sub)
	}
}

func unknownSubcommandError(c *cobra.Command, sub string) error {
	if suggestions := c.SuggestionsFor(sub); len(suggestions) > 0 {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown subcommand %q for %q (did you mean %q?)", sub, c.CommandPath(), suggestions[0])).
			WithHint(fmt.Sprintf("Run `%s %s`.", c.CommandPath(), suggestions[0])).
			WithI18n("err.unknown_subcommand_suggest", "hint.run_subcommand").
			WithParam("cmd", sub).
			WithParam("path", c.CommandPath()).
			WithParam("suggest", suggestions[0])
	}
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown subcommand %q for %q", sub, c.CommandPath())).
		WithMsgKey("err.unknown_command").
		WithParam("cmd", sub)
}

func closestSubcommandTypo(parent *cobra.Command, token string) string {
	for _, sub := range parent.Commands() {
		if sub.Hidden {
			continue
		}
		name := sub.Name()
		if len(name) >= 4 && damerauLevenshtein(token, name) == 1 {
			return name
		}
	}
	return ""
}

func damerauLevenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			v := min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				v = min(v, d[i-2][j-2]+1)
			}
			d[i][j] = v
		}
	}
	return d[la][lb]
}
