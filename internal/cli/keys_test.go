package cli

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veri/clikit/credentials"
	"veri/internal/spec"
)

func TestCLIKeySetOverwriteAppendAndHelp(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// 1. --help 清楚说明默认覆盖、逗号多密钥与 -a/--append，且保持单一子命令入口（无 key add）。
	code, keyHelpZh, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "key", "--help")
	if code != 0 || !strings.Contains(keyHelpZh, "veri key set deepseek -a") || strings.Contains(keyHelpZh, "veri key add") {
		t.Fatalf("unexpected zh key --help: code=%d out=%s", code, keyHelpZh)
	}
	code, setHelpZh, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "key", "set", "--help")
	if code != 0 || !strings.Contains(setHelpZh, "覆盖") || !strings.Contains(setHelpZh, "-a, --append") {
		t.Fatalf("unexpected zh key set --help: code=%d out=%s", code, setHelpZh)
	}
	code, setHelpEn, _ := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "en", "key", "set", "--help")
	if code != 0 || !strings.Contains(setHelpEn, "overwrites existing by default") || !strings.Contains(setHelpEn, "-a, --append") {
		t.Fatalf("unexpected en key set --help: code=%d out=%s", code, setHelpEn)
	}

	// 2. veri key set 保存首把密钥；再通过 veri key set -a 追加第二把密钥。
	code, out1, err1 := runKeyImportCLI(t, cfgPath, "sk-ds-first-111111\n", "--ui-lang", "zh", "key", "set", "deepseek")
	if code != 0 || err1 != "" || !strings.Contains(out1, "secrets.json") || strings.Contains(out1, "first-111111") {
		t.Fatalf("key set #1 failed: code=%d out=%s err=%s", code, out1, err1)
	}
	code, out2, err2 := runKeyImportCLI(t, cfgPath, "sk-ds-second-222222\n", "--ui-lang", "zh", "key", "set", "deepseek", "-a")
	if code != 0 || err2 != "" || !strings.Contains(out2, "追加 1 把") || !strings.Contains(out2, "共 2 把") || strings.Contains(out2, "second-222222") {
		t.Fatalf("key set -a failed: code=%d out=%s err=%s", code, out2, err2)
	}

	// 3. veri key set --append 重复追加相同密钥：幂等成功、零变更提示且不泄明文。
	code, dupOut, dupErr := runKeyImportCLI(t, cfgPath, "sk-ds-first-111111\n", "--ui-lang", "zh", "key", "set", "deepseek", "--append")
	if code != 0 || dupErr != "" || !strings.Contains(dupOut, "无变更") || !strings.Contains(dupOut, "共 2 把") || strings.Contains(dupOut, "first-111111") {
		t.Fatalf("duplicate key set --append failed: code=%d out=%s err=%s", code, dupOut, dupErr)
	}

	// 4. veri key set -a 混合重复与新密钥：去重追加并提示跳过重复数量。
	code, mixOut, mixErr := runKeyImportCLI(t, cfgPath, "sk-ds-second-222222, sk-ds-third-333333\n", "--ui-lang", "en", "key", "set", "-a", "deepseek")
	if code != 0 || mixErr != "" || !strings.Contains(mixOut, "Appended 1") || !strings.Contains(mixOut, "1 duplicate(s) skipped") || !strings.Contains(mixOut, "3 total") {
		t.Fatalf("mixed key set -a failed: code=%d out=%s err=%s", code, mixOut, mixErr)
	}

	// 验证 secrets.json 与 key list --json 中 3 把密钥保序完整生效且脱敏。
	rawVaultVal, err := credentials.NewVault(dir).Get(spec.ProviderDeepSeek)
	if err != nil || rawVaultVal != "sk-ds-first-111111,sk-ds-second-222222,sk-ds-third-333333" {
		t.Fatalf("unexpected vault value after appends: (%q, %v)", rawVaultVal, err)
	}
	code, listJSON, _ := runKeyImportCLI(t, cfgPath, "", "key", "list", "--json")
	if code != 0 {
		t.Fatalf("key list --json failed: code=%d", code)
	}
	var listOut keyListJSONOutput
	if err := json.Unmarshal([]byte(listJSON), &listOut); err != nil {
		t.Fatalf("unmarshal key list: %v", err)
	}
	foundMasked := ""
	for _, k := range listOut.Keys {
		if k.Provider == spec.ProviderDeepSeek {
			foundMasked = k.Masked
		}
	}
	if foundMasked != "sk-…1111,sk-…2222,sk-…3333" {
		t.Fatalf("expected 3 masked keys for deepseek, got %q", foundMasked)
	}

	// 5. 普通 veri key set（无 -a）覆盖原有密钥池，同时支持一次逗号输入多把。
	code, overOut, overErr := runKeyImportCLI(t, cfgPath, "sk-ds-only-444444,sk-ds-only-555555\n", "--ui-lang", "zh", "key", "set", "deepseek")
	if code != 0 || overErr != "" || !strings.Contains(overOut, "2 把") {
		t.Fatalf("key set multi overwrite failed: code=%d out=%s err=%s", code, overOut, overErr)
	}
	if got, _ := credentials.NewVault(dir).Get(spec.ProviderDeepSeek); got != "sk-ds-only-444444,sk-ds-only-555555" {
		t.Fatalf("expected overwritten 2-key pool, got %q", got)
	}

	// 6. 安全守卫与补全：禁止位置参数明文、拒绝仅逗号输入、支持 Tab 补全。
	code, _, posErr := runKeyImportCLI(t, cfgPath, "", "--ui-lang", "zh", "key", "set", "-a", "deepseek", "sk-neverecho99999999")
	if code != 2 || strings.Contains(posErr, "neverecho99999999") {
		t.Fatalf("key set -a positional secret must exit 2 without echo: code=%d err=%s", code, posErr)
	}
	if code, _, _ := runKeyImportCLI(t, cfgPath, " , , \n", "--ui-lang", "zh", "key", "set", "-a", "deepseek"); code != 2 {
		t.Fatalf("comma-only secret must exit 2, got %d", code)
	}
	code, compSet, _ := runKeyImportCLI(t, cfgPath, "", "__complete", "key", "set", "d")
	if code != 0 || !strings.Contains(compSet, "deepseek") {
		t.Fatalf("expected deepseek completion for key set, code=%d out=%q", code, compSet)
	}
}

func TestCLIKeySetSharedCredentialActivatesBothZhihuProviders(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")

	// 通过共享子源名 zhihu_web / zhihu-web 录入密钥，自动归一至 zhihu 槽位并同时激活 zhihu_web 与 zhihu。
	code, out, errOut := runKeyImportCLI(t, cfgPath, "sk-zhihu-shared-888888\n", "--ui-lang", "zh", "key", "set", "zhihu_web")
	if code != 0 || errOut != "" || !strings.Contains(out, "zhihu") || strings.Contains(out, "888888") {
		t.Fatalf("key set zhihu_web failed: code=%d out=%s err=%s", code, out, errOut)
	}
	if got, err := credentials.NewVault(dir).Get(spec.ProviderZhihu); err != nil || got != "sk-zhihu-shared-888888" {
		t.Fatalf("expected zhihu vault slot to store key, got (%q, %v)", got, err)
	}

	code, provJSON, provErr := runKeyImportCLI(t, cfgPath, "", "providers", "--json")
	if code != 0 || provErr != "" {
		t.Fatalf("providers --json failed: code=%d err=%s", code, provErr)
	}
	var provOut providersJSONOutput
	if err := json.Unmarshal([]byte(provJSON), &provOut); err != nil {
		t.Fatalf("unmarshal providers --json: %v", err)
	}
	enabled := make(map[string]bool)
	for _, p := range provOut.Providers {
		if p.Enabled {
			enabled[p.Name] = true
		}
	}
	if !enabled[spec.ProviderZhihu] || !enabled[spec.ProviderZhihuWeb] {
		t.Fatalf("expected single ZHIHU key to enable both zhihu and zhihu_web, got enabled=%v", enabled)
	}

	code, listJSON, _ := runKeyImportCLI(t, cfgPath, "", "key", "list", "--json")
	if code != 0 {
		t.Fatalf("key list --json failed: code=%d", code)
	}
	var listOut keyListJSONOutput
	if err := json.Unmarshal([]byte(listJSON), &listOut); err != nil {
		t.Fatalf("unmarshal key list: %v", err)
	}
	if listOut.SecretsPath != filepath.Join(dir, spec.SecretsFileName) {
		t.Fatalf("expected secrets_path=%q, got %q", filepath.Join(dir, spec.SecretsFileName), listOut.SecretsPath)
	}
	for _, k := range listOut.Keys {
		if k.Provider == spec.ProviderZhihuWeb {
			t.Fatalf("shared child provider zhihu_web must not occupy a duplicate key list slot: %+v", listOut.Keys)
		}
	}

	// VERI_SECRETS_DIR 允许自定义 --config 复用共享加密凭据库，并在 key list --json 中显式返回路径
	otherCfg := filepath.Join(t.TempDir(), "isolated.toml")
	cacheDir := t.TempDir()
	var sharedOutBuf, sharedErrBuf bytes.Buffer
	sharedCode := ExecuteContext(t.Context(), []string{"--config", otherCfg, "key", "list", "--json"}, nil, BuildInfo{Version: "test"}, strings.NewReader(""), &sharedOutBuf, &sharedErrBuf, func(k string) string {
		switch k {
		case spec.EnvSecretsDir:
			return dir
		case spec.EnvCacheDir:
			return cacheDir
		default:
			return ""
		}
	})
	if sharedCode != 0 || sharedErrBuf.Len() > 0 {
		t.Fatalf("key list --json with VERI_SECRETS_DIR failed: code=%d err=%s", sharedCode, sharedErrBuf.String())
	}
	var sharedOut keyListJSONOutput
	if err := json.Unmarshal(sharedOutBuf.Bytes(), &sharedOut); err != nil {
		t.Fatalf("unmarshal shared key list: %v\n%s", err, sharedOutBuf.String())
	}
	if sharedOut.SecretsPath != filepath.Join(dir, spec.SecretsFileName) {
		t.Fatalf("expected shared secrets_path=%q, got %q", filepath.Join(dir, spec.SecretsFileName), sharedOut.SecretsPath)
	}
	foundZhihu := false
	for _, k := range sharedOut.Keys {
		if k.Provider == spec.ProviderZhihu && k.EncryptedVault {
			foundZhihu = true
		}
	}
	if !foundZhihu {
		t.Fatalf("expected shared vault key for zhihu visible via VERI_SECRETS_DIR, got %+v", sharedOut.Keys)
	}
}

func TestCLIKeyCrossDeviceCopyAndErrorDiagnostics(t *testing.T) {
	srcDir := t.TempDir()
	srcCfg := filepath.Join(srcDir, spec.ConfigFileName)

	// 1. 模拟在源设备配置多个 Provider 密钥。
	for _, item := range []struct {
		prov string
		key  string
	}{
		{spec.ProviderDeepSeek, "sk-ds-copy-111111"},
		{spec.ProviderExa, "exa-copy-222222"},
		{spec.ProviderTavily, "tvly-copy-333333"},
	} {
		code, _, errOut := runKeyImportCLI(t, srcCfg, item.key+"\n", "--ui-lang", "zh", "key", "set", item.prov)
		if code != 0 || errOut != "" {
			t.Fatalf("seed key set %s failed: code=%d err=%s", item.prov, code, errOut)
		}
	}

	rawKey, err := os.ReadFile(filepath.Join(srcDir, spec.SecretKeyFileName))
	if err != nil {
		t.Fatalf("read src secret.key: %v", err)
	}
	rawSecrets, err := os.ReadFile(filepath.Join(srcDir, spec.SecretsFileName))
	if err != nil {
		t.Fatalf("read src secrets.json: %v", err)
	}

	// 2. 成对复制到新目录（含 BOM 与 CRLF）：veri key list 与追加写入全部直接生效。
	dstDir := t.TempDir()
	dstCfg := filepath.Join(dstDir, spec.ConfigFileName)
	bom := []byte{0xEF, 0xBB, 0xBF}
	if err := os.WriteFile(filepath.Join(dstDir, spec.SecretKeyFileName), append(append([]byte{}, bom...), bytes.ReplaceAll(rawKey, []byte("\n"), []byte("\r\n"))...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, spec.SecretsFileName), append(append([]byte{}, bom...), bytes.ReplaceAll(rawSecrets, []byte("\n"), []byte("\r\n"))...), 0o600); err != nil {
		t.Fatal(err)
	}

	code, listJSON, listErr := runKeyImportCLI(t, dstCfg, "", "key", "list", "--json")
	if code != 0 || listErr != "" {
		t.Fatalf("copied key list --json failed: code=%d err=%s", code, listErr)
	}
	var listOut keyListJSONOutput
	if err := json.Unmarshal([]byte(listJSON), &listOut); err != nil {
		t.Fatalf("unmarshal copied key list: %v", err)
	}
	vaultCount := 0
	for _, k := range listOut.Keys {
		if k.EncryptedVault {
			vaultCount++
		}
	}
	if vaultCount != 3 {
		t.Fatalf("expected 3 decrypted vault keys after cross-device copy, got %d (%+v)", vaultCount, listOut.Keys)
	}
	code, _, appendErr := runKeyImportCLI(t, dstCfg, "sk-ds-copy-444444\n", "--ui-lang", "zh", "key", "set", "deepseek", "-a")
	if code != 0 || appendErr != "" {
		t.Fatalf("key set -a on copied vault failed: code=%d err=%s", code, appendErr)
	}

	// 3. 仅复制 secrets.json 漏拷 secret.key：精准报错、明确指引成对复制且不产生垃圾 secret.key。
	missingDir := t.TempDir()
	missingCfg := filepath.Join(missingDir, spec.ConfigFileName)
	if err := os.WriteFile(filepath.Join(missingDir, spec.SecretsFileName), rawSecrets, 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, missingErr := runKeyImportCLI(t, missingCfg, "", "--ui-lang", "zh", "key", "list")
	if code != 3 || !strings.Contains(missingErr, "缺少 secret.key") || !strings.Contains(missingErr, "一并复制") {
		t.Fatalf("expected clear missing-key diagnostic (exit 3), got code=%d err=%s", code, missingErr)
	}
	if _, statErr := os.Stat(filepath.Join(missingDir, spec.SecretKeyFileName)); !os.IsNotExist(statErr) {
		t.Fatalf("veri key list without secret.key must not create secret.key, stat err=%v", statErr)
	}

	// 4. 损坏 secrets.json 时执行 veri key unset：返回 exit 3 并输出本地化损坏诊断。
	corruptDir := t.TempDir()
	corruptCfg := filepath.Join(corruptDir, spec.ConfigFileName)
	if err := os.WriteFile(filepath.Join(corruptDir, spec.SecretsFileName), []byte("{corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, unsetErr := runKeyImportCLI(t, corruptCfg, "", "--ui-lang", "zh", "key", "unset", "deepseek")
	if code != 3 || !strings.Contains(unsetErr, "secrets.json 已损坏") {
		t.Fatalf("expected localized corrupted-vault error on key unset (exit 3), got code=%d err=%s", code, unsetErr)
	}
}

func TestKeyHelpListsSupportedProviders(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), spec.ConfigFileName)
	providers := spec.CredentialProviders()
	if len(providers) == 0 {
		t.Fatal("spec.CredentialProviders() returned empty list")
	}
	wantList := strings.Join(providers, ", ")

	cases := []struct {
		name string
		args []string
	}{
		{name: "key-zh", args: []string{"--ui-lang", "zh", "key", "--help"}},
		{name: "key-en", args: []string{"--ui-lang", "en", "key", "--help"}},
		{name: "key-set-zh", args: []string{"--ui-lang", "zh", "key", "set", "--help"}},
		{name: "key-set-en", args: []string{"--ui-lang", "en", "key", "set", "--help"}},
		{name: "key-unset-zh", args: []string{"--ui-lang", "zh", "key", "unset", "--help"}},
		{name: "key-unset-en", args: []string{"--ui-lang", "en", "key", "unset", "--help"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errOut := runKeyImportCLI(t, cfgPath, "", tc.args...)
			if code != 0 {
				t.Fatalf("expected exit 0, got code=%d stderr=%s", code, errOut)
			}
			if strings.Contains(out, "{providers}") {
				t.Fatalf("%v help output contains unrendered placeholder:\n%s", tc.args, out)
			}
			if !strings.Contains(out, wantList) {
				t.Errorf("%v help output missing full provider list %q:\n%s", tc.args, wantList, out)
			}
			if strings.Contains(out, spec.ProviderLocal+",") || strings.Contains(out, ", "+spec.ProviderLocal) {
				t.Errorf("%v help output must not list keyless provider %q:\n%s", tc.args, spec.ProviderLocal, out)
			}
		})
	}
}
