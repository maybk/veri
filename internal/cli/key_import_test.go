package cli

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"veri/internal/app"
	"veri/internal/config"
	"veri/internal/spec"
)

func runKeyImportCLI(t *testing.T, cfgPath, stdin string, args ...string) (int, string, string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	repoFS := fstest.MapFS{"skills/veri/SKILL.md": &fstest.MapFile{Data: []byte("# veri\n")}}
	env := map[string]string{"VERI_CONFIG": cfgPath, "NO_COLOR": "1"}
	code := ExecuteContext(t.Context(), args, repoFS, BuildInfo{Version: "test"}, strings.NewReader(stdin), &outBuf, &errBuf, func(k string) string {
		return env[k]
	})
	return code, outBuf.String(), errBuf.String()
}

func TestCLIKeyImportFromConfigAndDoctorGuidance(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	raw := "[providers.deepseek]\napi_key = \"sk-cli-ds-1234567890\"\n\n[providers.zhihu]\nweight = 1.2\napi_key = \"zh-cli-plain-9876543210\"\n"
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}

	// 1. 迁入前 veri doctor 应温和提示存在明文密钥并引导 veri key import --from-config（且 JSON 携带 plaintext_providers）。
	code, docText, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "doctor")
	if code != 0 || !strings.Contains(docText, "veri key import --from-config") || !strings.Contains(docText, "deepseek, zhihu") {
		t.Fatalf("expected doctor plaintext hint, code=%d out=%s", code, docText)
	}
	code, docJSON, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "doctor", "--json")
	if code != 0 {
		t.Fatalf("doctor --json failed: code=%d out=%s", code, docJSON)
	}
	var docRep app.DoctorReport
	if err := json.Unmarshal([]byte(docJSON), &docRep); err != nil {
		t.Fatalf("unmarshal doctor json: %v", err)
	}
	if !slices.Equal(docRep.PlaintextProviders, []string{spec.ProviderDeepSeek, spec.ProviderZhihu}) {
		t.Fatalf("unexpected doctor plaintext_providers: %v", docRep.PlaintextProviders)
	}

	// 2. --from-config --dry-run --json 预览且绝不泄露明文。
	code, dryJSON, dryErr := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "key", "import", "--from-config", "--dry-run", "--json")
	if code != 0 || dryErr != "" {
		t.Fatalf("key import dry-run failed: code=%d out=%s err=%s", code, dryJSON, dryErr)
	}
	if strings.Contains(dryJSON, "sk-cli-ds-1234567890") || strings.Contains(dryJSON, "zh-cli-plain-9876543210") {
		t.Fatalf("dry-run JSON leaked plaintext secret: %s", dryJSON)
	}
	var dryRes config.ImportResult
	if err := json.Unmarshal([]byte(dryJSON), &dryRes); err != nil {
		t.Fatalf("unmarshal import result: %v", err)
	}
	if !dryRes.DryRun || dryRes.ImportedCount != 2 || !dryRes.ConfigUpdated {
		t.Fatalf("unexpected dry-run JSON result: %+v", dryRes)
	}

	// 3. 正式执行 veri key import --from-config：写入 secrets.json、清理明文、保留 zhihu.weight。
	code, impOut, impErr := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "key", "import", "--from-config")
	if code != 0 || impErr != "" {
		t.Fatalf("key import --from-config failed: code=%d out=%s err=%s", code, impOut, impErr)
	}
	if strings.Contains(impOut, "sk-cli-ds-1234567890") || strings.Contains(impOut, "zh-cli-plain-9876543210") {
		t.Fatalf("human output leaked plaintext secret: %s", impOut)
	}
	if !strings.Contains(impOut, "deepseek") || !strings.Contains(impOut, "zhihu") {
		t.Fatalf("expected migrated providers in output: %s", impOut)
	}

	code, ovOut, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "config", "show", "--overrides")
	if code != 0 || !strings.Contains(ovOut, `weight = 1.2`) || strings.Contains(ovOut, "api_key") {
		t.Fatalf("expected overrides to keep weight and drop api_key, code=%d out=%s", code, ovOut)
	}

	// 4. 迁移后 veri key list 显示来源为 secrets.json，veri doctor 不再提示明文密钥。
	code, listJSON, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "key", "list", "--json")
	if code != 0 || !strings.Contains(listJSON, `"active_source": "secrets.json"`) {
		t.Fatalf("expected secrets.json active source after import, code=%d out=%s", code, listJSON)
	}
	code, docAfter, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "doctor")
	if code != 0 || strings.Contains(docAfter, "veri key import --from-config") {
		t.Fatalf("doctor plaintext hint should disappear after migration, code=%d out=%s", code, docAfter)
	}

	// 5. 再次迁入无明文配置时应友好提示无明文密钥（零变更）。
	code, noneOut, noneErr := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "key", "import", "--from-config")
	if code != 0 || noneErr != "" || !strings.Contains(noneOut, "未在") {
		t.Fatalf("repeat import on clean config failed: code=%d out=%s err=%s", code, noneOut, noneErr)
	}

	// 6. 从 config init 模板取消注释填明文后迁入，自动还原为注释默认项与注释表头（零覆盖、零明文）。
	tplCfg := filepath.Join(t.TempDir(), "config.toml")
	if code, _, errOut := runKeyImportCLI(t, tplCfg, "", "--ui-lang", "zh", "config", "init"); code != 0 {
		t.Fatalf("config init failed: code=%d err=%s", code, errOut)
	}
	tplRaw, err := os.ReadFile(tplCfg)
	if err != nil {
		t.Fatalf("read init template: %v", err)
	}
	tplEdited := strings.Replace(string(tplRaw), "# [providers.keenable]\n# api_key = \"env:KEENABLE_API_KEY\"", "[providers.keenable]\napi_key = \"kn-cli-tpl-secret-7777\"", 1)
	if err := os.WriteFile(tplCfg, []byte(tplEdited), 0o600); err != nil {
		t.Fatalf("write patched template: %v", err)
	}
	if code, _, errOut := runKeyImportCLI(t, tplCfg, "", "--ui-lang", "zh", "key", "import", "--from-config"); code != 0 || errOut != "" {
		t.Fatalf("template key import failed: code=%d err=%s", code, errOut)
	}
	tplRestored, _ := os.ReadFile(tplCfg)
	if !strings.Contains(string(tplRestored), "# [providers.keenable]\n# api_key = \"env:KEENABLE_API_KEY\"") || strings.Contains(string(tplRestored), "kn-cli-tpl-secret-7777") {
		t.Fatalf("expected template restored to commented default without plaintext:\n%s", string(tplRestored))
	}
	code, ovJSON, _ := runKeyImportCLI(t, tplCfg, "", "config", "show", "--overrides", "--json")
	if code != 0 || strings.TrimSpace(ovJSON) != "{}" {
		t.Fatalf("expected zero overrides after restoring template key, code=%d out=%q", code, ovJSON)
	}
}

func TestCLIKeyImportFromEnvFileAndStdin(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	envFile := filepath.Join(dir, "keys.env")
	if err := os.WriteFile(envFile, []byte("DEEPSEEK_API_KEY=sk-env-file-111111\nIGNORED_FOO=bar\n"), 0o600); err != nil {
		t.Fatalf("write keys.env: %v", err)
	}

	// 1. 从 .env 文件导入。
	code, out, errOut := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "en", "key", "import", envFile)
	if code != 0 || errOut != "" {
		t.Fatalf("import .env failed: code=%d out=%s err=%s", code, out, errOut)
	}
	if !strings.Contains(out, "deepseek") || !strings.Contains(out, "IGNORED_FOO") || strings.Contains(out, "sk-env-file-111111") {
		t.Fatalf("unexpected .env import output: %s", out)
	}

	// 2. 通过 stdin '-' 批量导入（1 条冲突跳过 + 1 条新增），随后加 --overwrite 覆盖冲突。
	stdinPayload := "DEEPSEEK_API_KEY=sk-stdin-new-222222\nEXA_API_KEY=exa-stdin-333333\n"
	code, stdinJSON, _ := runKeyImportCLI(t, cfgPath, stdinPayload, "--ui-lang", "en", "key", "import", "-", "--json")
	if code != 0 {
		t.Fatalf("stdin import failed: code=%d out=%s", code, stdinJSON)
	}
	var res config.ImportResult
	if err := json.Unmarshal([]byte(stdinJSON), &res); err != nil {
		t.Fatalf("unmarshal stdin import json: %v", err)
	}
	if res.Source != "stdin" || res.ImportedCount != 1 || res.SkippedCount != 1 {
		t.Fatalf("unexpected stdin import result: %+v", res)
	}

	code, overOut, _ := runKeyImportCLI(t, cfgPath, stdinPayload, "--ui-lang", "en", "key", "import", "-", "--overwrite")
	if code != 0 || !strings.Contains(overOut, "update") || strings.Contains(overOut, "sk-stdin-new-222222") {
		t.Fatalf("stdin overwrite failed or leaked secret: code=%d out=%s", code, overOut)
	}
}

func TestCLIKeyImportValidationAndProviderCompletions(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	if code, _, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "en", "key", "import"); code != 2 {
		t.Fatalf("missing source should exit 2, got %d", code)
	}
	if code, _, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "en", "key", "import", "--from-config", "a.env"); code != 2 {
		t.Fatalf("conflicting --from-config and file should exit 2, got %d", code)
	}
	if code, _, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "en", "key", "import", "--keep-config", "a.env"); code != 2 {
		t.Fatalf("--keep-config without --from-config should exit 2, got %d", code)
	}
	if code, _, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "en", "key", "import", "a.env", "b.env"); code != 2 {
		t.Fatalf("too many args should exit 2, got %d", code)
	}
	if code, _, errOut := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "en", "key", "import", "sk-neverecho1234567890abcdef"); code != 2 || strings.Contains(errOut, "sk-neverecho1234567890abcdef") {
		t.Fatalf("secret-like bad file arg should exit 2 without echoing secret: code=%d err=%s", code, errOut)
	}

	// 验证 veri key set / unset 支持 CredentialProviders Tab 补全（不包含无需凭据的 local）。
	code, compSet, _ := runKeyImportCLI(t, cfgPath, "", "__complete", "key", "set", "d")
	if code != 0 || !strings.Contains(compSet, "deepseek") {
		t.Fatalf("expected deepseek completion for key set, code=%d out=%q", code, compSet)
	}
	code, compUnset, _ := runKeyImportCLI(t, cfgPath, "", "__complete", "key", "unset", "")
	if code != 0 || !strings.Contains(compUnset, "github") || strings.Contains(compUnset, "local\n") {
		t.Fatalf("unexpected key unset completions: code=%d out=%q", code, compUnset)
	}
}
