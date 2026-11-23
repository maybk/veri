package config

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"veri/clikit/credentials"
	"veri/clikit/redact"
	"veri/clikit/storage"
	"veri/internal/core"
	"veri/internal/spec"
)

// ImportAction 描述单条凭据在批量导入或配置明文迁移中的处理动作。
type ImportAction string

// 批量导入与迁移动作枚举。
const (
	ImportActionImported ImportAction = "imported"
	ImportActionUpdated  ImportAction = "updated"
	ImportActionCleaned  ImportAction = "cleaned"
	ImportActionSkipped  ImportAction = "skipped"
)

// ImportSkipReason 描述单条凭据被跳过时的机器可读原因。
type ImportSkipReason string

// 凭据跳过原因枚举。
const (
	ImportSkipVaultConflict ImportSkipReason = "vault_conflict"
	ImportSkipAlreadyStored ImportSkipReason = "already_stored"
	ImportSkipMixedEnvRef   ImportSkipReason = "mixed_env_ref"
)

// ImportOptions 控制批量加密导入与 config.toml 明文迁移行为。
type ImportOptions struct {
	DryRun     bool
	Overwrite  bool
	KeepConfig bool
}

// ImportEntry 描述单个 Provider 的导入或迁移结果（仅含脱敏预览，绝不含明文）。
type ImportEntry struct {
	Provider      string           `json:"provider"`
	Key           string           `json:"key,omitempty"`
	Action        ImportAction     `json:"action"`
	Reason        ImportSkipReason `json:"reason,omitempty"`
	Masked        string           `json:"masked,omitempty"`
	KeyCount      int              `json:"key_count,omitempty"`
	ConfigCleaned bool             `json:"config_cleaned,omitempty"`
}

// ImportResult 汇总批量加密导入或迁移结果（供人类表格与 --json 共用）。
type ImportResult struct {
	Source        string        `json:"source"`
	SourcePath    string        `json:"source_path,omitempty"`
	SecretsPath   string        `json:"secrets_path"`
	DryRun        bool          `json:"dry_run"`
	Entries       []ImportEntry `json:"entries"`
	IgnoredKeys   []string      `json:"ignored_keys,omitempty"`
	ImportedCount int           `json:"imported_count"`
	UpdatedCount  int           `json:"updated_count"`
	CleanedCount  int           `json:"cleaned_count"`
	SkippedCount  int           `json:"skipped_count"`
	ConfigUpdated bool          `json:"config_updated,omitempty"`
	ConfigRemoved bool          `json:"config_removed,omitempty"`
}

// ImportKeysFromConfig 扫描 config.toml 中的明文 Provider 密钥，批量加密迁入本地 secrets.json，
// 并在回读解密校验通过后按原注释排版移除 config.toml 中的明文键（保留 env:VAR 引用与其他配置）。
func ImportKeysFromConfig(cfgPath string, opts ImportOptions) (ImportResult, error) {
	cfgDir := filepath.Dir(cfgPath)
	vault := credentials.NewVault(cfgDir)
	res := ImportResult{
		Source:      "config",
		SourcePath:  cfgPath,
		SecretsPath: vault.SecretsPath(),
		DryRun:      opts.DryRun,
		Entries:     []ImportEntry{},
	}

	origRaw, fileMode, exists, err := readExistingConfigFile(cfgPath)
	if err != nil {
		return res, err
	}
	if !exists {
		return res, nil
	}

	rawOrigCfg, origMeta, decErr := decodeKnownTOML(cfgPath, origRaw)
	if decErr != nil {
		return res, decErr
	}
	origCfg := rawOrigCfg
	if err := validateConfig(&origCfg); err != nil {
		return res, err
	}

	existingVault, err := loadVaultSecrets(vault, cfgDir)
	if err != nil {
		return res, err
	}

	credSpecs := origCfg.Providers.CredentialSpecs()
	toWrite := make(map[string]string)
	var cleanEntries []keyEntry

	for _, prov := range spec.CredentialProviders() {
		entry, ok := secretKeyEntryForProvider(prov)
		if !ok || !origMeta.IsDefined(strings.Split(entry.info.Key, ".")...) {
			continue
		}
		plainKeys, envRefCount := classifyConfigSecretSpec(credSpecs[prov])
		if len(plainKeys) == 0 {
			continue
		}
		if envRefCount > 0 {
			res.addEntry(ImportEntry{
				Provider: prov,
				Key:      entry.info.Key,
				Action:   ImportActionSkipped,
				Reason:   ImportSkipMixedEnvRef,
				Masked:   maskKeyList(plainKeys),
				KeyCount: len(plainKeys),
			})
			continue
		}

		action, reason, shouldWrite, shouldClean := planImportAction(plainKeys, existingVault[prov], opts, true)
		if shouldWrite {
			toWrite[prov] = strings.Join(plainKeys, ",")
		}
		if shouldClean {
			cleanEntries = append(cleanEntries, entry)
		}
		res.addEntry(ImportEntry{
			Provider:      prov,
			Key:           entry.info.Key,
			Action:        action,
			Reason:        reason,
			Masked:        maskKeyList(plainKeys),
			KeyCount:      len(plainKeys),
			ConfigCleaned: shouldClean,
		})
	}

	// 写盘前先在内存中完成 config.toml 清理预演，杜绝写入 vault 后无法安全清理配置造成的半状态。
	lineEnding := detectLineEnding(origRaw)
	edited := normalizeNewlines(origRaw)
	if len(cleanEntries) > 0 {
		var cleanErr error
		edited, cleanErr = prepareCleanedConfigTOML(cfgPath, edited, rawOrigCfg, origMeta, cleanEntries)
		if cleanErr != nil {
			return res, cleanErr
		}
		res.ConfigUpdated = true
		if strings.TrimSpace(edited) == "" {
			if fi, lErr := os.Lstat(cfgPath); lErr == nil && fi.Mode().IsRegular() {
				res.ConfigRemoved = true
			}
		}
	}

	if opts.DryRun {
		return res, nil
	}

	if err := saveAndVerifyVaultBatch(vault, toWrite); err != nil {
		return res, err
	}

	if len(cleanEntries) > 0 {
		if res.ConfigRemoved {
			if rmErr := os.Remove(cfgPath); rmErr != nil {
				return res, core.WrapError(core.CodeInternal, fmt.Sprintf("failed to remove empty config %q", cfgPath), rmErr)
			}
			return res, nil
		}
		outBytes := restoreLineEnding(edited, lineEnding)
		perm := os.FileMode(0o600)
		if fileMode.Perm() != 0 {
			perm = fileMode.Perm()
		}
		if err := storage.WriteFileAtomic(cfgPath, []byte(outBytes), 0o700, perm); err != nil {
			return res, core.WrapError(core.CodeInternal, fmt.Sprintf("failed to write config %q", cfgPath), err)
		}
	}
	return res, nil
}

func (r *ImportResult) addEntry(e ImportEntry) {
	r.Entries = append(r.Entries, e)
	switch e.Action {
	case ImportActionImported:
		r.ImportedCount++
	case ImportActionUpdated:
		r.UpdatedCount++
	case ImportActionCleaned:
		r.CleanedCount++
	case ImportActionSkipped:
		r.SkippedCount++
	}
}

func planImportAction(newKeys []string, existingRaw string, opts ImportOptions, fromConfig bool) (action ImportAction, reason ImportSkipReason, shouldWrite, shouldClean bool) {
	existingKeys := splitKeyList(existingRaw)
	if len(existingKeys) == 0 {
		return ImportActionImported, "", true, fromConfig && !opts.KeepConfig
	}
	if slices.Equal(existingKeys, newKeys) {
		if fromConfig && !opts.KeepConfig {
			return ImportActionCleaned, "", false, true
		}
		return ImportActionSkipped, ImportSkipAlreadyStored, false, false
	}
	if opts.Overwrite {
		return ImportActionUpdated, "", true, fromConfig && !opts.KeepConfig
	}
	return ImportActionSkipped, ImportSkipVaultConflict, false, false
}

func classifyConfigSecretSpec(rawSpec string) (plainKeys []string, envRefCount int) {
	trimmed := strings.TrimSpace(rawSpec)
	if trimmed == "" {
		return nil, 0
	}
	prefixLen := len(spec.CredSourceEnvPrefix)
	for _, item := range splitKeyList(trimmed) {
		if len(item) > prefixLen && strings.EqualFold(item[:prefixLen], spec.CredSourceEnvPrefix) {
			if strings.TrimSpace(item[prefixLen:]) != "" {
				envRefCount++
				continue
			}
		}
		plainKeys = append(plainKeys, item)
	}
	return plainKeys, envRefCount
}

func secretKeyEntryForProvider(provider string) (keyEntry, bool) {
	for _, entry := range keyEntriesCached() {
		if entry.info.Secret && entry.info.Provider == provider {
			return entry, true
		}
	}
	return keyEntry{}, false
}

func prepareCleanedConfigTOML(path, normalized string, origCfg Config, origMeta toml.MetaData, cleanEntries []keyEntry) (string, error) {
	edited := normalized
	cleanedSet := make(map[string]bool, len(cleanEntries))
	for _, entry := range cleanEntries {
		next, removed := applyUnsetLineEdit(edited, entry.info.Section, entry.info.Field)
		if !removed {
			return "", manualEditError(entry.info.Key, path, "inline or dotted table syntax requires manual edit")
		}
		edited = next
		cleanedSet[entry.info.Key] = true
	}

	rawCandidate, meta, err := decodeKnownTOML(path, edited)
	if err != nil {
		return "", err
	}
	validated := rawCandidate
	if err := validateConfig(&validated); err != nil {
		return "", err
	}
	for _, entry := range cleanEntries {
		if meta.IsDefined(strings.Split(entry.info.Key, ".")...) {
			return "", manualEditError(entry.info.Key, path, "duplicate or inline definition")
		}
	}
	if err := verifyUntouchedKeysPreserved(path, origCfg, rawCandidate, origMeta, meta, cleanedSet); err != nil {
		return "", err
	}
	return edited, nil
}

func loadVaultSecrets(vault *credentials.Vault, cfgDir string) (map[string]string, error) {
	secrets, err := vault.LoadAll()
	if err != nil {
		return nil, core.WrapError(core.CodeAuthFailed, fmt.Sprintf("failed to load encrypted credentials in %q: %v", cfgDir, err), err).
			WithHint("Check file permissions or re-save the damaged credential via `veri key set <provider>`.").
			WithI18n("err.vault_decrypt_failed", "hint.vault_decrypt_failed").
			WithParam("dir", cfgDir)
	}
	return secrets, nil
}

func saveAndVerifyVaultBatch(vault *credentials.Vault, toWrite map[string]string) error {
	if len(toWrite) == 0 {
		return nil
	}
	if err := vault.SetMany(toWrite); err != nil {
		return core.WrapError(core.CodeInternal, fmt.Sprintf("failed to save encrypted credentials: %v", err), err)
	}
	verified, err := vault.LoadAll()
	if err != nil {
		return core.WrapError(core.CodeInternal, fmt.Sprintf("failed to verify encrypted credentials after write: %v", err), err)
	}
	for prov, want := range toWrite {
		if verified[prov] != want {
			return core.NewError(core.CodeInternal, fmt.Sprintf("encrypted credential verification mismatch for %s", prov))
		}
	}
	return nil
}

func maskKeyList(keys []string) string {
	masked := make([]string, len(keys))
	for i, k := range keys {
		masked[i] = redact.MaskKey(k)
	}
	return strings.Join(masked, ",")
}
