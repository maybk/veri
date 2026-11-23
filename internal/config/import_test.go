package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"veri/clikit/credentials"
	"veri/internal/core"
	"veri/internal/spec"
)

func TestImportKeysFromConfigLifecycleAndSafety(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// 预先在 vault 中写入一条相同值（keenable）和一条不同值（tavily）测试冲突与幂等清理分支。
	vault := credentials.NewVault(dir)
	if err := vault.SetMany(map[string]string{
		spec.ProviderKeenable: "kn-same-secret-1111",
		spec.ProviderTavily:   "tvly-existing-vault-2222",
	}); err != nil {
		t.Fatalf("seed vault: %v", err)
	}

	origConfig := "\uFEFF# user top comment\r\n" +
		"[providers.deepseek]\r\n" +
		"api_key = \"sk-ds-pool-1111, sk-ds-pool-2222\"  # multi-key pool\r\n" +
		"timeout = \"15s\"\r\n\r\n" +
		"[providers.exa]\r\n" +
		"api_key = \"exa-only-key-3333\"\r\n\r\n" +
		"[providers.keenable]\r\n" +
		"api_key = \"kn-same-secret-1111\"\r\n\r\n" +
		"[providers.tavily]\r\n" +
		"api_key = \"tvly-new-config-4444\"\r\n\r\n" +
		"[providers.zhihu]\r\n" +
		"api_key = \"env:CUSTOM_ZHIHU_KEY\"\r\n" +
		"scope = \"zhihu\"\r\n\r\n" +
		"[providers.brave]\r\n" +
		"api_key = \"env:CUSTOM_BRAVE_KEY,BSA-mixed-plain-5555\"\r\n\r\n" +
		"[providers.github]\r\n" +
		"token = \"ghp_github_token_6666\"\r\n"
	if err := os.WriteFile(cfgPath, []byte(origConfig), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	// 1. Dry-run 必须准确分类且绝不改动任何磁盘文件。
	dryRes, err := ImportKeysFromConfig(cfgPath, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("DryRun ImportKeysFromConfig error: %v", err)
	}
	if !dryRes.DryRun || !dryRes.ConfigUpdated || dryRes.ConfigRemoved {
		t.Fatalf("unexpected dry-run flags: %+v", dryRes)
	}
	if dryRes.ImportedCount != 3 || dryRes.CleanedCount != 1 || dryRes.SkippedCount != 2 || dryRes.UpdatedCount != 0 {
		t.Fatalf("unexpected dry-run counts: %+v", dryRes)
	}
	afterDryDisk, err := os.ReadFile(cfgPath)
	if err != nil || string(afterDryDisk) != origConfig {
		t.Fatalf("dry-run must not touch config.toml: err=%v", err)
	}
	if gotVault, _ := vault.LoadAll(); len(gotVault) != 2 {
		t.Fatalf("dry-run must not mutate secrets.json, got %d entries", len(gotVault))
	}

	// 校验返回条目绝不泄露任何明文密钥片段。
	rawSecrets := []string{"sk-ds-pool-1111", "sk-ds-pool-2222", "exa-only-key-3333", "kn-same-secret-1111", "tvly-new-config-4444", "BSA-mixed-plain-5555", "ghp_github_token_6666"}
	for _, entry := range dryRes.Entries {
		for _, sec := range rawSecrets {
			if strings.Contains(entry.Masked, sec) {
				t.Fatalf("entry.Masked leaked plaintext %q: %+v", sec, entry)
			}
		}
		if entry.Provider == spec.ProviderZhihu {
			t.Fatalf("pure env: ref provider zhihu must not appear in import entries: %+v", entry)
		}
	}

	// 2. 正式执行（默认不覆盖冲突）：导入 deepseek/exa/github、清理 keenable、跳过 tavily(冲突) 与 brave(混合)。
	res, err := ImportKeysFromConfig(cfgPath, ImportOptions{})
	if err != nil {
		t.Fatalf("ImportKeysFromConfig error: %v", err)
	}
	if res.ImportedCount != 3 || res.CleanedCount != 1 || res.SkippedCount != 2 || !res.ConfigUpdated || res.ConfigRemoved {
		t.Fatalf("unexpected import result: %+v", res)
	}

	allVault, err := vault.LoadAll()
	if err != nil {
		t.Fatalf("vault.LoadAll error: %v", err)
	}
	if allVault[spec.ProviderDeepSeek] != "sk-ds-pool-1111,sk-ds-pool-2222" {
		t.Fatalf("deepseek multi-key pool = %q", allVault[spec.ProviderDeepSeek])
	}
	if allVault[spec.ProviderExa] != "exa-only-key-3333" || allVault[spec.ProviderGitHub] != "ghp_github_token_6666" {
		t.Fatalf("unexpected migrated vault values: %+v", allVault)
	}
	if allVault[spec.ProviderTavily] != "tvly-existing-vault-2222" {
		t.Fatalf("conflict without --overwrite must keep existing tavily vault key, got %q", allVault[spec.ProviderTavily])
	}
	if _, exists := allVault[spec.ProviderBrave]; exists {
		t.Fatal("mixed env:+plaintext provider brave must not be written to vault")
	}

	// 验证 config.toml：保留注释、CRLF、其他配置、纯 env: 引用、冲突项和混合项；移除已迁入项及空 section。
	updatedRaw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read updated config.toml: %v", err)
	}
	updatedText := string(updatedRaw)
	if !strings.Contains(updatedText, "\r\n") {
		t.Fatal("expected CRLF line endings to be preserved")
	}
	for _, mustKeep := range []string{
		"# user top comment",
		`# api_key = "env:DEEPSEEK_API_KEY"  # multi-key pool`,
		`timeout = "15s"`,
		`api_key = "env:CUSTOM_ZHIHU_KEY"`,
		`scope = "zhihu"`,
		`api_key = "tvly-new-config-4444"`,
		`api_key = "env:CUSTOM_BRAVE_KEY,BSA-mixed-plain-5555"`,
	} {
		if !strings.Contains(updatedText, mustKeep) {
			t.Fatalf("expected updated config.toml to keep %q, got:\n%s", mustKeep, updatedText)
		}
	}
	for _, mustRemove := range []string{
		"sk-ds-pool-1111",
		"exa-only-key-3333",
		"[providers.exa]",
		"kn-same-secret-1111",
		"[providers.keenable]",
		"ghp_github_token_6666",
		"[providers.github]",
	} {
		if strings.Contains(updatedText, mustRemove) {
			t.Fatalf("expected updated config.toml to remove %q, got:\n%s", mustRemove, updatedText)
		}
	}

	// 3. 开启 Overwrite：冲突项 tavily 覆盖入 vault 并清理 config.toml，仅剩 brave(混合) 仍跳过。
	overRes, err := ImportKeysFromConfig(cfgPath, ImportOptions{Overwrite: true})
	if err != nil {
		t.Fatalf("Overwrite ImportKeysFromConfig error: %v", err)
	}
	if overRes.UpdatedCount != 1 || overRes.SkippedCount != 1 {
		t.Fatalf("unexpected overwrite result: %+v", overRes)
	}
	loaded, err := LoadWithEnv(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv after migration: %v", err)
	}
	if loaded.Credentials[spec.ProviderTavily].Source != spec.SecretsFileName ||
		!slices.Equal(loaded.Credentials[spec.ProviderTavily].Keys, []string{"tvly-new-config-4444"}) {
		t.Fatalf("expected overwritten tavily from secrets.json, got %+v", loaded.Credentials[spec.ProviderTavily])
	}
	if !slices.Equal(loaded.PlaintextProviders, []string{spec.ProviderBrave}) {
		t.Fatalf("expected only unsplit mixed brave in PlaintextProviders, got %v", loaded.PlaintextProviders)
	}
}

func TestImportKeysFromConfigKeepConfigAndEmptyRemoval(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// 配置文件不存在时应幂等返回空结果，不创建任何文件。
	emptyRes, err := ImportKeysFromConfig(cfgPath, ImportOptions{})
	if err != nil || len(emptyRes.Entries) != 0 || emptyRes.ConfigUpdated {
		t.Fatalf("missing config should be no-op, got res=%+v err=%v", emptyRes, err)
	}

	raw := "[providers.deepseek]\napi_key = \"sk-only-secret-9999\"\n"
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// KeepConfig=true：写入 vault，保留 config.toml 原样。
	keepRes, err := ImportKeysFromConfig(cfgPath, ImportOptions{KeepConfig: true})
	if err != nil || keepRes.ImportedCount != 1 || keepRes.ConfigUpdated {
		t.Fatalf("keep-config import = (%+v, %v)", keepRes, err)
	}
	if disk, rErr := os.ReadFile(cfgPath); rErr != nil || string(disk) != raw {
		t.Fatalf("keep-config must preserve config.toml unchanged, err=%v", rErr)
	}

	// 再次 KeepConfig=true：识别已存在相同密钥并跳过。
	repeatRes, err := ImportKeysFromConfig(cfgPath, ImportOptions{KeepConfig: true})
	if err != nil || repeatRes.SkippedCount != 1 || repeatRes.Entries[0].Reason != ImportSkipAlreadyStored {
		t.Fatalf("repeat keep-config = (%+v, %v)", repeatRes, err)
	}

	// 默认模式：vault 已有相同密钥，仅清理并删除空配置文件。
	cleanRes, err := ImportKeysFromConfig(cfgPath, ImportOptions{})
	if err != nil || cleanRes.CleanedCount != 1 || !cleanRes.ConfigUpdated || !cleanRes.ConfigRemoved {
		t.Fatalf("clean empty config = (%+v, %v)", cleanRes, err)
	}
	if _, statErr := os.Stat(cfgPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected empty config.toml to be removed, stat err=%v", statErr)
	}
}

func TestImportKeysFromConfigRestoresCommentedTemplate(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := WriteTemplateForLocale(cfgPath, false, "zh-CN"); err != nil {
		t.Fatalf("WriteTemplateForLocale: %v", err)
	}
	origTemplateBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	origTemplate := string(origTemplateBytes)

	// 模拟用户从 config init 模板取消注释并填入明文：
	// - keenable 与 github 仅填写明文（段内无其他生效项，应连同表头完整还原回模板注释）
	// - zhihu 同时启用 scope = "zhihu"（应保留 [providers.zhihu] 与 scope，仅将 api_key 还原为注释默认项）
	edited := origTemplate
	edited = strings.Replace(edited, "# [providers.keenable]\n# api_key = \"env:KEENABLE_API_KEY\"", "[providers.keenable]\napi_key = \"kn-template-secret-1111\"", 1)
	edited = strings.Replace(edited, "# [providers.zhihu]\n# api_key = \"env:ZHIHU_API_KEY\"\n# scope = \"global\"", "[providers.zhihu]\napi_key = \"zh-template-secret-2222\"\nscope = \"zhihu\"", 1)
	edited = strings.Replace(edited, "# [providers.github]\n# enabled = true\n# token = \"env:GITHUB_TOKEN\"", "[providers.github]\n# enabled = true\ntoken = \"ghp_template_token_3333\"", 1)
	if edited == origTemplate {
		t.Fatal("failed to patch template test fixture")
	}
	if err := os.WriteFile(cfgPath, []byte(edited), 0o600); err != nil {
		t.Fatalf("write patched template: %v", err)
	}

	res, err := ImportKeysFromConfig(cfgPath, ImportOptions{})
	if err != nil {
		t.Fatalf("ImportKeysFromConfig on template: %v", err)
	}
	if res.ImportedCount != 3 || !res.ConfigUpdated || res.ConfigRemoved {
		t.Fatalf("unexpected template import result: %+v", res)
	}

	afterBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read restored template: %v", err)
	}
	afterText := string(afterBytes)
	for _, leaked := range []string{"kn-template-secret-1111", "zh-template-secret-2222", "ghp_template_token_3333"} {
		if strings.Contains(afterText, leaked) {
			t.Fatalf("restored template leaked plaintext %q", leaked)
		}
	}
	for _, wantSnippet := range []string{
		"# [providers.keenable]\n# api_key = \"env:KEENABLE_API_KEY\"        # 默认留存查询与结果，敏感场景可设 enabled = false",
		"[providers.zhihu]\n# api_key = \"env:ZHIHU_API_KEY\"\nscope = \"zhihu\"",
		"# [providers.github]\n# enabled = true\n# token = \"env:GITHUB_TOKEN\"              # 可选：配置 GitHub Token 提高接口配额",
	} {
		if !strings.Contains(afterText, wantSnippet) {
			t.Fatalf("expected restored template to contain:\n%s\ngot:\n%s", wantSnippet, afterText)
		}
	}

	// 验证还原后的文件加载结果：仅剩 zhihu.scope 覆盖，3 个源凭据全部生效自 secrets.json，零明文。
	loaded, err := LoadWithEnv(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv after template restore: %v", err)
	}
	if len(loaded.PlaintextProviders) != 0 {
		t.Fatalf("expected zero plaintext providers, got %v", loaded.PlaintextProviders)
	}
	if !loaded.DefinedKeys["providers.zhihu.scope"] || loaded.DefinedKeys["providers.keenable.api_key"] || loaded.DefinedKeys["providers.github.token"] {
		t.Fatalf("unexpected DefinedKeys after template restore: %v", loaded.DefinedKeys)
	}
	for prov, wantKey := range map[string]string{
		spec.ProviderKeenable: "kn-template-secret-1111",
		spec.ProviderZhihu:    "zh-template-secret-2222",
		spec.ProviderGitHub:   "ghp_template_token_3333",
	} {
		cred := loaded.Credentials[prov]
		if cred.Source != spec.SecretsFileName || !slices.Equal(cred.Keys, []string{wantKey}) {
			t.Fatalf("expected %s from secrets.json with %q, got %+v", prov, wantKey, cred)
		}
	}

	// 验证空行之后的下一段前置注释不会被误判为当前段内注释（纯稀疏段依然彻底移除空表头）。
	sparsePath := filepath.Join(t.TempDir(), "config.toml")
	sparseRaw := "[providers.exa]\napi_key = \"exa-sparse-only-4444\"\n\n# zhihu custom section\n[providers.zhihu]\nscope = \"zhihu\"\n"
	if err := os.WriteFile(sparsePath, []byte(sparseRaw), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ImportKeysFromConfig(sparsePath, ImportOptions{}); err != nil {
		t.Fatalf("sparse import with next-section comment: %v", err)
	}
	sparseAfter, _ := os.ReadFile(sparsePath)
	wantSparse := "# zhihu custom section\n[providers.zhihu]\nscope = \"zhihu\"\n"
	if string(sparseAfter) != wantSparse {
		t.Fatalf("sparse cleanup broke next-section comment:\ngot:\n%s\nwant:\n%s", string(sparseAfter), wantSparse)
	}

	// 验证多行三引号密钥与 Windows CRLF 模板均能完整清理还原并保留 CRLF 行尾。
	crlfDir := t.TempDir()
	crlfPath := filepath.Join(crlfDir, "config.toml")
	crlfRaw := "[providers.deepseek]\r\napi_key = \"\"\"\r\nsk-ml-1111,\r\nsk-ml-2222\r\n\"\"\"\r\n# timeout = \"10s\"\r\n"
	if err := os.WriteFile(crlfPath, []byte(crlfRaw), 0o600); err != nil {
		t.Fatal(err)
	}
	crlfRes, err := ImportKeysFromConfig(crlfPath, ImportOptions{})
	if err != nil || crlfRes.ImportedCount != 1 {
		t.Fatalf("multiline CRLF import = (%+v, %v)", crlfRes, err)
	}
	if got, _ := credentials.NewVault(crlfDir).Get(spec.ProviderDeepSeek); got != "sk-ml-1111,sk-ml-2222" {
		t.Fatalf("unexpected multiline vault value: %q", got)
	}
	crlfAfter, _ := os.ReadFile(crlfPath)
	wantCRLF := "# [providers.deepseek]\r\n# api_key = \"env:DEEPSEEK_API_KEY\"\r\n# timeout = \"10s\"\r\n"
	if string(crlfAfter) != wantCRLF {
		t.Fatalf("multiline CRLF template restore mismatch:\ngot:  %q\nwant: %q", string(crlfAfter), wantCRLF)
	}
}

func TestImportKeysFromConfigRejectsInlineTableBeforeWritingVault(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	raw := "providers = { deepseek = { api_key = \"sk-inline-secret-1234\" } }\n"
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write inline config: %v", err)
	}

	_, err := ImportKeysFromConfig(cfgPath, ImportOptions{})
	cErr := core.AsError(err)
	if cErr == nil || cErr.MsgKey != "err.config_edit_manual" {
		t.Fatalf("expected err.config_edit_manual for inline table cleanup, got %v", err)
	}
	// 预演拦截必须在写入 secrets.json 之前发生，绝不留下半状态。
	vault := credentials.NewVault(dir)
	if _, statErr := os.Stat(vault.SecretsPath()); !os.IsNotExist(statErr) {
		t.Fatalf("vault must not be written when config cleanup preflight fails, stat err=%v", statErr)
	}

	// 使用 KeepConfig=true 可显式跳过 config 编辑完成加密导入。
	res, err := ImportKeysFromConfig(cfgPath, ImportOptions{KeepConfig: true})
	if err != nil || res.ImportedCount != 1 {
		t.Fatalf("keep-config on inline table = (%+v, %v)", res, err)
	}
}

func TestImportKeysFromEnvParsingAndPrecedence(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	envContent := "\uFEFF# project shared env file\r\n" +
		"OPENAI_API_KEY=sk-unrelated-openai-secret\r\n" +
		"export deepseek_api_key=\"sk-ds-first\"\r\n" +
		"DEEPSEEK_API_KEY=\"sk-ds-override-1111,sk-ds-override-2222\" # inline comment\r\n" +
		"ZHIHU_ACCESS_SECRET=zh-alias-secret\r\n" +
		"ZHIHU_API_KEY='zh-primary#literal'\r\n" +
		"TENCENT_WSA_API_KEY=wsa-primary-key # trailing comment\r\n" +
		"TENCENTCLOUD_WSA_APIKEY=wsa-alias-ignored\r\n" +
		"EXA_API_KEY=\r\n" +
		"TAVILY_API_KEY=tvly-hash#inside-value\r\n" +
		"GH_TOKEN=\"ghp_token_with_eq==\"\r\n" +
		"DATABASE_URL=postgres://user:pass@localhost/db\r\n"
	if err := os.WriteFile(envPath, []byte(envContent), 0o600); err != nil {
		t.Fatalf("write .env: %v", err)
	}

	dryRes, err := ImportKeysFromEnvFile(dir, envPath, ImportOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run env import error: %v", err)
	}
	if dryRes.ImportedCount != 5 || !slices.Equal(dryRes.IgnoredKeys, []string{"DATABASE_URL", "OPENAI_API_KEY"}) {
		t.Fatalf("unexpected dry-run env result: %+v", dryRes)
	}

	res, err := ImportKeysFromEnvFile(dir, envPath, ImportOptions{})
	if err != nil {
		t.Fatalf("env import error: %v", err)
	}
	if res.ImportedCount != 5 || res.UpdatedCount != 0 || res.SkippedCount != 0 {
		t.Fatalf("unexpected env import counts: %+v", res)
	}

	all, err := credentials.NewVault(dir).LoadAll()
	if err != nil {
		t.Fatalf("vault.LoadAll: %v", err)
	}
	want := map[string]string{
		spec.ProviderDeepSeek:   "sk-ds-override-1111,sk-ds-override-2222",
		spec.ProviderZhihu:      "zh-primary#literal",
		spec.ProviderTencentWSA: "wsa-primary-key",
		spec.ProviderTavily:     "tvly-hash#inside-value",
		spec.ProviderGitHub:     "ghp_token_with_eq==",
	}
	for prov, exp := range want {
		if all[prov] != exp {
			t.Fatalf("vault[%s] = %q, want %q", prov, all[prov], exp)
		}
	}
	if _, ok := all[spec.ProviderExa]; ok {
		t.Fatal("empty EXA_API_KEY must not be imported")
	}

	// 再次导入相同值与不同值：相同跳过 already_stored，不同跳过 vault_conflict；--overwrite 仅更新不同值。
	secondEnv := "DEEPSEEK_API_KEY=sk-ds-override-1111,sk-ds-override-2222\nTAVILY_API_KEY=tvly-updated-9999\n"
	conflictRes, err := ImportKeysFromEnvReader(dir, "stdin", strings.NewReader(secondEnv), ImportOptions{})
	if err != nil || conflictRes.SkippedCount != 2 {
		t.Fatalf("expected 2 skipped entries, got res=%+v err=%v", conflictRes, err)
	}
	if conflictRes.Entries[0].Reason != ImportSkipAlreadyStored || conflictRes.Entries[1].Reason != ImportSkipVaultConflict {
		t.Fatalf("unexpected skip reasons: %+v", conflictRes.Entries)
	}

	overRes, err := ImportKeysFromEnvReader(dir, "stdin", strings.NewReader(secondEnv), ImportOptions{Overwrite: true})
	if err != nil || overRes.UpdatedCount != 1 || overRes.SkippedCount != 1 {
		t.Fatalf("overwrite env import = (%+v, %v)", overRes, err)
	}
	if got, _ := credentials.NewVault(dir).Get(spec.ProviderTavily); got != "tvly-updated-9999" {
		t.Fatalf("expected updated tavily key, got %q", got)
	}
}

func TestImportKeysFromEnvErrorsNeverLeakSecrets(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name   string
		raw    string
		secret string
	}{
		{name: "missing_equals", raw: "DEEPSEEK_API_KEY sk-never-leak-1111\n", secret: "sk-never-leak-1111"},
		{name: "unclosed_double_quote", raw: "DEEPSEEK_API_KEY=\"sk-never-leak-2222\n", secret: "sk-never-leak-2222"},
		{name: "unclosed_single_quote", raw: "DEEPSEEK_API_KEY='sk-never-leak-3333\n", secret: "sk-never-leak-3333"},
		{name: "trailing_garbage_after_quote", raw: "DEEPSEEK_API_KEY=\"sk-never-leak-4444\" oops\n", secret: "sk-never-leak-4444"},
		{name: "invalid_var_name", raw: "123BAD=sk-never-leak-5555\n", secret: "sk-never-leak-5555"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ImportKeysFromEnvReader(dir, "stdin", strings.NewReader(tc.raw), ImportOptions{})
			cErr := core.AsError(err)
			if cErr == nil || cErr.MsgKey != "err.key_import_invalid_env" {
				t.Fatalf("expected err.key_import_invalid_env, got %v", err)
			}
			if strings.Contains(err.Error(), tc.secret) {
				t.Fatalf("error message leaked secret %q: %v", tc.secret, err)
			}
		})
	}

	// 传入目录、误把密钥当路径、超过 1 MiB 均须报错且不回显密钥。
	if _, err := ImportKeysFromEnvFile(dir, dir, ImportOptions{}); core.AsError(err).MsgKey != "err.key_import_is_dir" {
		t.Fatalf("expected err.key_import_is_dir for directory, got %v", err)
	}
	fakeSecretPath := filepath.Join(dir, "sk-accidental-plaintext-key-9876543210")
	if _, err := ImportKeysFromEnvFile(dir, fakeSecretPath, ImportOptions{}); err == nil || strings.Contains(err.Error(), "sk-accidental-plaintext-key-9876543210") {
		t.Fatalf("missing file error should scrub secret-like path, got %v", err)
	}
	oversized := "DEEPSEEK_API_KEY=" + strings.Repeat("a", maxConfigFileBytes+16)
	if _, err := ImportKeysFromEnvReader(dir, "stdin", strings.NewReader(oversized), ImportOptions{}); core.AsError(err).MsgKey != "err.key_import_too_large" {
		t.Fatalf("expected err.key_import_too_large, got %v", err)
	}
}
