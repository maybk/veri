package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"veri/clikit"
	"veri/clikit/credentials"
	"veri/clikit/redact"
	"veri/clikit/term"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/render"
	"veri/internal/spec"
)

func newKeyCmd(rt *runtimeEnv) *cobra.Command {
	keyCmd := &cobra.Command{
		Use:     "key",
		Aliases: []string{"keys"},
		GroupID: groupAdmin,
		Short:   rt.cat.T("key.short"),
		Example: rt.cat.T("key.example"),
	}
	requireSubcommand(keyCmd)
	addOrderedCommands(keyCmd,
		newKeySetCmd(rt),
		newKeyListCmd(rt),
		newKeyUnsetCmd(rt),
	)
	return keyCmd
}

func newKeySetCmd(rt *runtimeEnv) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "set <provider>",
		Short:   rt.cat.T("key.set.short"),
		Long:    rt.cat.T("key.set.long"),
		Example: rt.cat.T("key.set.example"),
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) == 0 {
				return core.NewError(core.CodeInvalidArgument, "provider name is required").
					WithMsgKey("err.missing_key_provider")
			}
			if len(args) > 1 {
				// 严禁在错误消息中回显可能包含明文密钥的额外位置参数
				return core.NewError(core.CodeInvalidArgument, "API key cannot be passed as a positional argument").
					WithHint("Run `veri key set <provider>` interactively or pipe the key via stdin.").
					WithI18n("err.key_positional_secret", "hint.key_positional_secret")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			provider, ok := spec.NormalizeCredentialProvider(args[0])
			if !ok {
				valid := strings.Join(spec.CredentialProviders(), ", ")
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported provider %q (valid: %s)", args[0], valid)).
					WithMsgKey("err.unsupported_provider").
					WithParam("provider", args[0]).
					WithParam("valid", valid)
			}

			prompt := rt.cat.T("key.set.prompt", "provider", provider)
			secret, err := term.ReadSecret(cmd.Context(), rt.stdin, rt.stderr, prompt, false)
			if err != nil {
				if isInputInterrupted(err) {
					return handledExitError{code: clikit.ExitInterrupted}
				}
				return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
			}

			if secret == "" {
				return core.NewError(core.CodeInvalidArgument, "API key must not be empty").
					WithHint("Paste a valid API key and press Enter, or press Ctrl+C to cancel.").
					WithI18n("err.empty_api_key", "hint.empty_api_key")
			}

			cfgDir, err := config.ResolveDir(rt.flags.configPath, rt.getenv)
			if err != nil {
				return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
			}
			vault := credentials.NewVault(cfgDir)
			if err := vault.Set(provider, secret); err != nil {
				return core.WrapError(core.CodeInternal, err.Error(), err)
			}
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.set.done",
				"provider", provider,
				"path", vault.SecretsPath(),
			))
			return nil
		},
	}
	return cmd
}

func isInputInterrupted(err error) bool {
	return errors.Is(err, term.ErrInterrupted) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func newKeyUnsetCmd(rt *runtimeEnv) *cobra.Command {
	return &cobra.Command{
		Use:     "unset <provider>",
		Aliases: []string{"rm", "delete"},
		Short:   rt.cat.T("key.unset.short"),
		Args:    requireExactArgs(1, "err.missing_key_provider", ""),
		RunE: func(_ *cobra.Command, args []string) error {
			provider, ok := spec.NormalizeCredentialProvider(args[0])
			if !ok {
				valid := strings.Join(spec.CredentialProviders(), ", ")
				return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported provider %q (valid: %s)", args[0], valid)).
					WithMsgKey("err.unsupported_provider").
					WithParam("provider", args[0]).
					WithParam("valid", valid)
			}
			cfgDir, err := config.ResolveDir(rt.flags.configPath, rt.getenv)
			if err != nil {
				return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
			}
			vault := credentials.NewVault(cfgDir)
			removed, err := vault.Delete(provider)
			if err != nil {
				return core.WrapError(core.CodeInternal, err.Error(), err)
			}
			if !removed {
				_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.unset.none", "provider", provider))
				return nil
			}
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.unset.done", "provider", provider, "path", vault.SecretsPath()))
			return nil
		},
	}
}

type keyListEntry struct {
	Provider       string `json:"provider"`
	ActiveSource   string `json:"active_source"`
	EncryptedVault bool   `json:"encrypted_vault"`
	Masked         string `json:"masked"`
}

type keyListJSONOutput struct {
	Keys []keyListEntry `json:"keys"`
}

func newKeyListCmd(rt *runtimeEnv) *cobra.Command {
	var jsonOut bool
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   rt.cat.T("key.list.short"),
		Args:    requireNoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			loaded, err := config.LoadWithEnv(rt.flags.configPath, rt.getenv)
			if err != nil {
				return err
			}

			missingLabel := rt.cat.T("table.val.missing")
			yesLabel := rt.cat.T("table.val.yes")
			noLabel := rt.cat.T("table.val.no")

			providers := spec.CredentialProviders()
			entries := make([]keyListEntry, 0, len(providers))
			rows := make([][]string, 0, len(providers))
			for _, prov := range providers {
				cred := loaded.Credentials[prov]
				inVault := loaded.VaultProviders[prov]
				masked := "-"
				if len(cred.Keys) > 0 {
					parts := make([]string, 0, len(cred.Keys))
					for _, k := range cred.Keys {
						parts = append(parts, redact.MaskKey(k))
					}
					masked = strings.Join(parts, ",")
				}
				entries = append(entries, keyListEntry{
					Provider:       prov,
					ActiveSource:   cred.Source,
					EncryptedVault: inVault,
					Masked:         masked,
				})
				srcText := cred.Source
				switch srcText {
				case "":
					srcText = "-"
				case spec.CredSourceMissing:
					srcText = missingLabel
				}
				vaultLabel := noLabel
				if inVault {
					vaultLabel = yesLabel
				}
				rows = append(rows, []string{prov, srcText, vaultLabel, masked})
			}

			if jsonOut {
				text, jErr := render.MarshalEnvelopeJSON(keyListJSONOutput{Keys: entries})
				if jErr != nil {
					return jErr
				}
				_, _ = fmt.Fprint(rt.stdout, text)
				return nil
			}

			headers := []string{
				rt.cat.T("table.keys.provider"),
				rt.cat.T("table.keys.source"),
				rt.cat.T("table.keys.vault"),
				rt.cat.T("table.keys.masked"),
			}
			pal := term.DetectPalette(rt.stdout, rt.flags.noColor, rt.getenv)
			term.WriteTable(rt.stdout, headers, rows, term.TableStyle{
				Header: pal.Bold,
				Cell: func(col int, val string) string {
					switch col {
					case 1:
						if val == missingLabel || val == "-" {
							return pal.Dim(val)
						}
					case 2:
						if val == yesLabel {
							return pal.Green(val)
						}
						if val == noLabel {
							return pal.Dim(val)
						}
					case 3:
						if val == "-" {
							return pal.Dim(val)
						}
					}
					return val
				},
			})
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOut, "json", false, rt.cat.T("key.list.flag.json"))
	return cmd
}
