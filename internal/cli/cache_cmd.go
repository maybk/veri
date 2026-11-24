package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"veri/clikit/term"
	"veri/internal/app"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/render"
)

func newCacheCmd(rt *runtimeEnv) *cobra.Command {
	cacheCmd := &cobra.Command{
		Use:     "cache",
		GroupID: groupAdmin,
		Short:   rt.cat.T("cache.short"),
		Example: rt.cat.T("cache.example"),
	}
	requireSubcommand(cacheCmd)

	addOrderedCommands(cacheCmd,
		newCacheInfoCmd(rt),
		newCacheClearCmd(rt),
	)
	return cacheCmd
}

func newCacheInfoCmd(rt *runtimeEnv) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "info",
		Aliases: []string{"status"},
		Short:   rt.cat.T("cache.info.short"),
		Args:    requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			loaded, err := config.LoadWithEnv(rt.flags.configPath, rt.getenv)
			if err != nil {
				return err
			}
			info, err := app.InspectCache(loaded)
			if err != nil {
				return core.WrapError(core.CodeInternal, "failed to inspect cache", err)
			}
			if jsonOut {
				text, jErr := render.MarshalEnvelopeJSON(info)
				if jErr != nil {
					return jErr
				}
				_, _ = fmt.Fprint(rt.stdout, text)
				return nil
			}
			renderCacheInfoHuman(rt, info)
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, rt.cat.T("cache.info.flag.json"))
	return cmd
}

func newCacheClearCmd(rt *runtimeEnv) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "clear",
		Short: rt.cat.T("cache.clear.short"),
		Args:  requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			loaded, err := config.LoadWithEnv(rt.flags.configPath, rt.getenv)
			if err != nil {
				return err
			}
			res, err := app.ClearDiskCache(loaded.CacheDir)
			if err != nil {
				return core.WrapError(core.CodeInternal, "failed to clear disk cache", err)
			}
			if jsonOut {
				text, jErr := render.MarshalEnvelopeJSON(res)
				if jErr != nil {
					return jErr
				}
				_, _ = fmt.Fprint(rt.stdout, text)
				return nil
			}
			if res.RemovedEntries == 0 {
				_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("cache.clear.empty", "dir", res.Dir))
				return nil
			}
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("cache.clear.done",
				"count", strconv.Itoa(res.RemovedEntries),
				"size", formatHumanBytes(res.FreedBytes),
				"dir", res.Dir,
			))
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, rt.cat.T("cache.clear.flag.json"))
	return cmd
}

func renderCacheInfoHuman(rt *runtimeEnv, info app.CacheInfoResult) {
	pal := term.DetectPalette(rt.stdout, rt.flags.noColor, rt.getenv)
	yesNo := func(v bool) string {
		if v {
			return rt.cat.T("table.val.enabled")
		}
		return rt.cat.T("table.val.disabled")
	}

	_, _ = fmt.Fprintln(rt.stdout, pal.Bold(rt.cat.T("cache.info.header")))
	_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("cache.info.global",
		"status", yesNo(info.Enabled),
		"max_bytes", info.MaxBytes,
	))
	_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("cache.info.cli",
		"status", yesNo(info.CLI.Enabled),
		"fetch_ttl", info.CLI.FetchTTL,
		"search_ttl", info.CLI.SearchTTL,
	))
	_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("cache.info.mcp",
		"status", yesNo(info.MCP.Enabled),
		"fetch_ttl", info.MCP.FetchTTL,
		"search_ttl", info.MCP.SearchTTL,
		"max_entries", strconv.Itoa(info.MCP.MaxEntries),
	))
	_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("cache.info.dir", "dir", info.Disk.Dir))
	_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("cache.info.usage",
		"entries", strconv.Itoa(info.Disk.Entries),
		"fetch", strconv.Itoa(info.Disk.FetchEntries),
		"search", strconv.Itoa(info.Disk.SearchEntries),
		"size", formatHumanBytes(info.Disk.SizeBytes),
	))
}

func formatHumanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", max(n, 0))
	case n < 1<<20:
		return fmt.Sprintf("%.1f KB", float64(n)/1024.0)
	case n < 1<<30:
		return fmt.Sprintf("%.2f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.2f GB", float64(n)/(1<<30))
	}
}
