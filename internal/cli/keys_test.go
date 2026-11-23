package cli

import (
	"encoding/json/v2"
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
	for _, k := range listOut.Keys {
		if k.Provider == spec.ProviderZhihuWeb {
			t.Fatalf("shared child provider zhihu_web must not occupy a duplicate key list slot: %+v", listOut.Keys)
		}
	}
}
