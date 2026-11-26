package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"veri/clikit"
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
		Long:    rt.cat.T("key.long", "providers", supportedCredentialProvidersText()),
		Example: rt.cat.T("key.example"),
	}
	requireSubcommand(keyCmd)
	addOrderedCommands(keyCmd,
		newKeySetCmd(rt),
		newKeyImportCmd(rt),
		newKeyListCmd(rt),
		newKeyUnsetCmd(rt),
	)
	return keyCmd
}

func supportedCredentialProvidersText() string {
	return strings.Join(spec.CredentialProviders(), ", ")
}

func newKeySetCmd(rt *runtimeEnv) *cobra.Command {
	var appendMode bool
	cmd := &cobra.Command{
		Use:               "set <provider>",
		Short:             rt.cat.T("key.set.short"),
		Long:              rt.cat.T("key.set.long", "providers", supportedCredentialProvidersText()),
		Example:           rt.cat.T("key.set.example"),
		ValidArgsFunction: completeCredentialProviderArg,
		Args:              validateKeyProviderArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runKeySaveCmd(cmd, rt, args[0], appendMode)
		},
	}
	cmd.Flags().BoolVarP(&appendMode, "append", "a", false, rt.cat.T("key.set.flag.append"))
	return cmd
}

func validateKeyProviderArgs(_ *cobra.Command, args []string) error {
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
}

func normalizeCredentialProviderArg(raw string) (string, error) {
	provider, ok := spec.NormalizeCredentialProvider(raw)
	if !ok {
		valid := supportedCredentialProvidersText()
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported provider %q (valid: %s)", raw, valid)).
			WithMsgKey("err.unsupported_provider").
			WithParam("provider", raw).
			WithParam("valid", valid)
	}
	return provider, nil
}

func runKeySaveCmd(cmd *cobra.Command, rt *runtimeEnv, rawProvider string, appendMode bool) error {
	provider, err := normalizeCredentialProviderArg(rawProvider)
	if err != nil {
		return err
	}

	prompt := rt.cat.T("key.set.prompt", "provider", provider)
	if appendMode {
		prompt = rt.cat.T("key.set.append_prompt", "provider", provider)
	}
	secret, err := term.ReadSecret(cmd.Context(), rt.stdin, rt.stderr, prompt, false)
	if err != nil {
		if isInputInterrupted(err) {
			return handledExitError{code: clikit.ExitInterrupted}
		}
		return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
	}

	secretsDir, err := config.ResolveSecretsDir(rt.flags.configPath, rt.getenv)
	if err != nil {
		return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
	}
	res, err := config.SaveVaultKey(secretsDir, provider, secret, appendMode)
	if err != nil {
		return err
	}

	if appendMode {
		switch {
		case !res.Changed:
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.set.append_duplicate",
				"provider", res.Provider,
				"total", strconv.Itoa(res.Total),
				"path", res.SecretsPath,
			))
		case res.Duplicates > 0:
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.set.append_done_dedup",
				"provider", res.Provider,
				"added", strconv.Itoa(res.Added),
				"skipped", strconv.Itoa(res.Duplicates),
				"total", strconv.Itoa(res.Total),
				"path", res.SecretsPath,
			))
		default:
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.set.append_done",
				"provider", res.Provider,
				"added", strconv.Itoa(res.Added),
				"total", strconv.Itoa(res.Total),
				"path", res.SecretsPath,
			))
		}
		return nil
	}

	if res.Total > 1 {
		_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.set.done_multi",
			"provider", res.Provider,
			"total", strconv.Itoa(res.Total),
			"path", res.SecretsPath,
		))
		return nil
	}
	_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.set.done",
		"provider", res.Provider,
		"path", res.SecretsPath,
	))
	return nil
}

func isInputInterrupted(err error) bool {
	return errors.Is(err, term.ErrInterrupted) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func newKeyUnsetCmd(rt *runtimeEnv) *cobra.Command {
	return &cobra.Command{
		Use:               "unset <provider>",
		Aliases:           []string{"rm", "delete"},
		Short:             rt.cat.T("key.unset.short"),
		Long:              rt.cat.T("key.unset.long", "providers", supportedCredentialProvidersText()),
		ValidArgsFunction: completeCredentialProviderArg,
		Args:              requireExactArgs(1, "err.missing_key_provider", ""),
		RunE: func(_ *cobra.Command, args []string) error {
			provider, err := normalizeCredentialProviderArg(args[0])
			if err != nil {
				return err
			}
			secretsDir, err := config.ResolveSecretsDir(rt.flags.configPath, rt.getenv)
			if err != nil {
				return core.WrapError(core.CodeInvalidArgument, err.Error(), err)
			}
			removed, secretsPath, err := config.DeleteVaultKey(secretsDir, provider)
			if err != nil {
				return err
			}
			if !removed {
				_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.unset.none", "provider", provider))
				return nil
			}
			_, _ = fmt.Fprintln(rt.stdout, rt.cat.T("key.unset.done", "provider", provider, "path", secretsPath))
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
	SecretsPath string         `json:"secrets_path"`
	Keys        []keyListEntry `json:"keys"`
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
				text, jErr := render.MarshalEnvelopeJSON(keyListJSONOutput{
					SecretsPath: loaded.SecretsPath,
					Keys:        entries,
				})
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
			_, _ = fmt.Fprintln(rt.stdout, pal.Dim(rt.cat.T("key.list.vault_path", "path", loaded.SecretsPath)))
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

func completeCredentialProviderArg(_ *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	prefix := strings.ToLower(strings.TrimSpace(toComplete))
	providers := spec.CredentialProviders()
	out := make([]string, 0, len(providers))
	for _, p := range providers {
		if strings.HasPrefix(p, prefix) {
			out = append(out, p)
		}
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
