package config

import (
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"slices"
	"strings"

	"veri/clikit/credentials"
	"veri/clikit/redact"
	"veri/internal/core"
	"veri/internal/spec"
)

// SaveKeyResult 记录单个 Provider 加密保存或追加去重后的统计结果（不含明文）。
type SaveKeyResult struct {
	Provider    string
	SecretsPath string
	Added       int
	Duplicates  int
	Total       int
	Changed     bool
}

// SaveVaultKey 将指定 Provider 的 1 把或多把（逗号/换行分隔）API Key 规范去重后加密写入 secrets.json。
// appendMode=false 覆盖原有密钥；appendMode=true 读取已有密钥并保序去重追加（全重复时幂等不改盘）。
func SaveVaultKey(cfgDir, provider, rawSecret string, appendMode bool) (SaveKeyResult, error) {
	canon, err := normalizeCredentialProvider(provider)
	if err != nil {
		return SaveKeyResult{}, err
	}
	rawItems := splitKeyList(rawSecret)
	if len(rawItems) == 0 {
		return SaveKeyResult{}, core.NewError(core.CodeInvalidArgument, "API key must not be empty").
			WithHint("Paste a valid API key and press Enter, or press Ctrl+C to cancel.").
			WithI18n("err.empty_api_key", "hint.empty_api_key")
	}
	incoming, inputDups := dedupeKeysPreserveOrder(rawItems)
	vault := credentials.NewVault(cfgDir)
	res := SaveKeyResult{
		Provider:    canon,
		SecretsPath: vault.SecretsPath(),
	}

	if !appendMode {
		if err := vault.Set(canon, strings.Join(incoming, ",")); err != nil {
			return res, mapVaultWriteError(cfgDir, err)
		}
		res.Added = len(incoming)
		res.Duplicates = inputDups
		res.Total = len(incoming)
		res.Changed = true
		return res, nil
	}

	existingRaw, err := vault.Get(canon)
	if err != nil && !errors.Is(err, credentials.ErrNotFound) {
		return res, vaultAccessError(cfgDir, err)
	}

	merged, _ := dedupeKeysPreserveOrder(splitKeyList(existingRaw))
	added := 0
	dups := inputDups
	for _, k := range incoming {
		if slices.Contains(merged, k) {
			dups++
			continue
		}
		merged = append(merged, k)
		added++
	}

	res.Added = added
	res.Duplicates = dups
	res.Total = len(merged)
	if added == 0 {
		return res, nil
	}
	if err := vault.Set(canon, strings.Join(merged, ",")); err != nil {
		return res, mapVaultWriteError(cfgDir, err)
	}
	res.Changed = true
	return res, nil
}

// DeleteVaultKey 从指定凭据目录的 secrets.json 中移除指定 Provider 的加密密钥。
func DeleteVaultKey(cfgDir, provider string) (removed bool, secretsPath string, err error) {
	canon, err := normalizeCredentialProvider(provider)
	if err != nil {
		return false, "", err
	}
	vault := credentials.NewVault(cfgDir)
	removed, err = vault.Delete(canon)
	if err != nil {
		return false, vault.SecretsPath(), mapVaultWriteError(cfgDir, err)
	}
	return removed, vault.SecretsPath(), nil
}

func normalizeCredentialProvider(provider string) (string, error) {
	canon, ok := spec.NormalizeCredentialProvider(provider)
	if !ok {
		valid := strings.Join(spec.CredentialProviders(), ", ")
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported provider %q (valid: %s)", provider, valid)).
			WithMsgKey("err.unsupported_provider").
			WithParam("provider", provider).
			WithParam("valid", valid)
	}
	return canon, nil
}

func isVaultAccessError(err error) bool {
	return errors.Is(err, credentials.ErrMasterKeyMissing) ||
		errors.Is(err, credentials.ErrMasterKeyInvalid) ||
		errors.Is(err, credentials.ErrVaultCorrupted) ||
		errors.Is(err, credentials.ErrDecryptFailed) ||
		errors.Is(err, fs.ErrPermission)
}

func mapVaultWriteError(cfgDir string, err error) error {
	if isVaultAccessError(err) {
		return vaultAccessError(cfgDir, err)
	}
	return core.WrapError(core.CodeInternal, err.Error(), err)
}

func vaultAccessError(cfgDir string, err error) error {
	switch {
	case errors.Is(err, credentials.ErrMasterKeyMissing):
		return core.WrapError(core.CodeAuthFailed, fmt.Sprintf("%s is missing in %q: %v", spec.SecretKeyFileName, cfgDir, err), err).
			WithHint("Copy both `secret.key` and `secrets.json` together when migrating across devices; to reset, delete `secrets.json` and run `veri key set <provider>` or `veri key import`.").
			WithI18n("err.vault_key_missing", "hint.vault_key_missing").
			WithParam("dir", cfgDir)
	case errors.Is(err, credentials.ErrMasterKeyInvalid):
		return core.WrapError(core.CodeAuthFailed, fmt.Sprintf("%s in %q is invalid or corrupted: %v", spec.SecretKeyFileName, cfgDir, err), err).
			WithHint("Re-copy both `secret.key` and `secrets.json` from the source device; to reset, delete both files and run `veri key set <provider>` or `veri key import`.").
			WithI18n("err.vault_key_invalid", "hint.vault_key_invalid").
			WithParam("dir", cfgDir)
	case errors.Is(err, credentials.ErrVaultCorrupted):
		return core.WrapError(core.CodeAuthFailed, fmt.Sprintf("%s in %q is corrupted: %v", spec.SecretsFileName, cfgDir, err), err).
			WithHint("Re-copy `secrets.json` from the source device; to reset, delete the damaged file and run `veri key set <provider>` or `veri key import`.").
			WithI18n("err.vault_corrupted", "hint.vault_corrupted").
			WithParam("dir", cfgDir)
	case errors.Is(err, fs.ErrPermission):
		return core.WrapError(core.CodeAuthFailed, fmt.Sprintf("permission denied accessing credentials in %q: %v", cfgDir, err), err).
			WithHint("Check ownership and read/write permissions for `secrets.json` and `secret.key` in that directory.").
			WithI18n("err.vault_permission_denied", "hint.vault_permission_denied").
			WithParam("dir", cfgDir)
	default:
		return core.WrapError(core.CodeAuthFailed, fmt.Sprintf("failed to decrypt %s in %q: %v", spec.SecretsFileName, cfgDir, err), err).
			WithHint("Ensure `secrets.json` and `secret.key` were copied together from the same device; to reset, delete both files and run `veri key set <provider>` or `veri key import`.").
			WithI18n("err.vault_decrypt_failed", "hint.vault_decrypt_failed").
			WithParam("dir", cfgDir)
	}
}

func dedupeKeysPreserveOrder(items []string) (unique []string, duplicates int) {
	unique = make([]string, 0, len(items))
	for _, item := range items {
		if slices.Contains(unique, item) {
			duplicates++
			continue
		}
		unique = append(unique, item)
	}
	return unique, duplicates
}

// Credential 记录单个 Provider 解析后的密钥列表及其来源说明（不含明文展示）。
type Credential struct {
	Keys           []string
	Source         string // e.g. "env:DEEPSEEK_API_KEY", "secrets.json", "config.toml", "missing"
	HasPlainInFile bool
}

func resolveCred(rawSpec string, envNames []string, vaultVal string, getenv func(string) string) (Credential, []string) {
	trimmed := strings.TrimSpace(rawSpec)
	plainKeys, specEnvKeys, specEnvSources, hasPlain := parseSpecItems(trimmed, getenv)
	vKeys := splitKeyList(vaultVal)

	var allKnown []string
	allKnown = append(allKnown, plainKeys...)
	allKnown = append(allKnown, specEnvKeys...)
	allKnown = append(allKnown, vKeys...)

	// 1. 进程或 MCP 客户端显式注入的标准环境变量始终最高优先（同时保留 HasPlainInFile 以便文件权限告警生效）。
	var winningEnvKeys []string
	winningSource := ""
	for _, envName := range envNames {
		if val := strings.TrimSpace(getenv(envName)); val != "" {
			keys := splitKeyList(val)
			allKnown = append(allKnown, keys...)
			if len(keys) > 0 && winningSource == "" {
				winningEnvKeys = keys
				winningSource = spec.CredSourceEnvPrefix + envName
			}
		}
	}
	if winningSource != "" {
		return Credential{
			Keys:           winningEnvKeys,
			Source:         winningSource,
			HasPlainInFile: hasPlain,
		}, allKnown
	}

	// 2. 本地加密凭据库 secrets.json 次之。
	if len(vKeys) > 0 {
		return Credential{
			Keys:           vKeys,
			Source:         spec.SecretsFileName,
			HasPlainInFile: hasPlain,
		}, allKnown
	}

	// 3. 回退使用 config.toml 中的自定义 env:VAR 或明文 api_key。
	var combined []string
	var sources []string
	if len(specEnvKeys) > 0 {
		combined = append(combined, specEnvKeys...)
		sources = append(sources, specEnvSources...)
	}
	if len(plainKeys) > 0 {
		combined = append(combined, plainKeys...)
		sources = appendUnique(sources, spec.ConfigFileName)
	}
	if len(combined) == 0 {
		return Credential{Source: spec.CredSourceMissing, HasPlainInFile: hasPlain}, allKnown
	}
	return Credential{
		Keys:           combined,
		Source:         strings.Join(sources, "+"),
		HasPlainInFile: hasPlain,
	}, allKnown
}

func parseSpecItems(trimmed string, getenv func(string) string) (plainKeys, envKeys, envSources []string, hasPlain bool) {
	if trimmed == "" {
		return nil, nil, nil, false
	}
	prefixLen := len(spec.CredSourceEnvPrefix)
	for _, item := range splitKeyList(trimmed) {
		if len(item) > prefixLen && strings.EqualFold(item[:prefixLen], spec.CredSourceEnvPrefix) {
			envName := strings.TrimSpace(item[prefixLen:])
			if envName == "" {
				continue
			}
			val := strings.TrimSpace(getenv(envName))
			if parsed := splitKeyList(val); len(parsed) > 0 {
				envKeys = append(envKeys, parsed...)
				envSources = appendUnique(envSources, spec.CredSourceEnvPrefix+envName)
			}
			continue
		}
		hasPlain = true
		plainKeys = append(plainKeys, item)
	}
	return plainKeys, envKeys, envSources, hasPlain
}

func splitKeyList(raw string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	}) {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func maskProviderView(rawSpec string, cred Credential) string {
	if cred.Source == spec.SecretsFileName && len(cred.Keys) > 0 {
		masked := make([]string, 0, len(cred.Keys))
		for _, k := range cred.Keys {
			masked = append(masked, redact.MaskKey(k))
		}
		return "encrypted:" + strings.Join(masked, ",")
	}
	return maskSpec(rawSpec)
}

func maskSpec(spec string) string {
	items := splitKeyList(spec)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, redact.MaskKey(item))
	}
	return strings.Join(out, ",")
}

func sanitizeConfigURL(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return redact.SanitizeURL(trimmed)
}

func extractProxyPassword(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.Contains(trimmed, "://") && !strings.HasPrefix(trimmed, "//") {
		trimmed = "http://" + trimmed
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.User == nil {
		return ""
	}
	pw, ok := u.User.Password()
	if !ok {
		return ""
	}
	return pw
}

func appendUnique(slice []string, val string) []string {
	if slices.Contains(slice, val) {
		return slice
	}
	return append(slice, val)
}
