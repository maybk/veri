package cli

import (
	"bytes"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veri/clikit/credentials"
	"veri/clikit/i18n"
	"veri/clikit/term"
	"veri/internal/app"
	"veri/internal/spec"
)

func TestFormatProviderSource(t *testing.T) {
	zh := localeBundle.For(i18n.LocaleZhCN)
	en := localeBundle.For(i18n.LocaleEnUS)

	cases := []struct {
		raw    string
		wantZH string
		wantEN string
	}{
		{spec.SecretsFileName, "加密库", "vault"},
		{spec.CredSourceEnvPrefix + "DEEPSEEK_API_KEY", "环境变量", "env"},
		{spec.ConfigFileName, "配置文件", "config"},
		{"env:KEY_A+env:KEY_B", "环境变量", "env"},
		{"env:KEY_A+" + spec.ConfigFileName, "环境变量+配置文件", "env+config"},
		{spec.CredSourceOptional, "可选", "optional"},
		{spec.CredSourceMissing, "未配置", "missing"},
		{"-", "-", "-"},
		{"", "-", "-"},
	}
	for _, tc := range cases {
		if got := formatProviderSource(zh, tc.raw); got != tc.wantZH {
			t.Errorf("formatProviderSource(zh, %q) = %q, want %q", tc.raw, got, tc.wantZH)
		}
		if got := formatProviderSource(en, tc.raw); got != tc.wantEN {
			t.Errorf("formatProviderSource(en, %q) = %q, want %q", tc.raw, got, tc.wantEN)
		}
	}
}

func TestProvidersKeyListDoctorAndHelpAlignment(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	repoFS := os.DirFS(filepath.Join("..", ".."))

	// deepseek = config.toml 明文，exa/tavily = 环境变量，tinyfish = 加密凭据库
	if err := os.WriteFile(cfgPath, []byte("[providers.deepseek]\napi_key = \"sk-plain-config-1234\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	vault := credentials.NewVault(tmpDir)
	if err := vault.Set("tinyfish", "sk-vault-secret-5678"); err != nil {
		t.Fatal(err)
	}

	env := map[string]string{
		"EXA_API_KEY":    "exa-env-secret-9999",
		"TAVILY_API_KEY": "tvly-env-secret-8888",
	}
	run := func(args ...string) (int, string, string) {
		var outBuf, errBuf bytes.Buffer
		fullArgs := append([]string{"--config", cfgPath, "--no-color"}, args...)
		code := ExecuteContext(t.Context(), fullArgs, repoFS, BuildInfo{Version: "0.1.2"}, strings.NewReader(""), &outBuf, &errBuf, func(k string) string {
			return env[k]
		})
		return code, outBuf.String(), errBuf.String()
	}

	// 1. veri providers 中文 7 列布局与对齐检查
	code, provZH, errOut := run("--ui-lang", "zh", "providers")
	if code != 0 {
		t.Fatalf("providers zh failed: code=%d err=%s", code, errOut)
	}
	if strings.Contains(provZH, "·") {
		t.Fatalf("providers zh should not contain middle dot: %s", provZH)
	}
	for _, want := range []string{"来源", "超时", "配置文件", "环境变量", "加密库", "10s/25s", "concurrency=4", "min_chars=300, max_bytes=5MB"} {
		if !strings.Contains(provZH, want) {
			t.Fatalf("providers zh missing %q:\n%s", want, provZH)
		}
	}
	assertColumnAligned(t, provZH, "说明", "concurrency=4", "type=auto", "min_chars=300, max_bytes=5MB")

	// 2. veri providers 英文 7 列布局与对齐检查
	code, provEN, errOut := run("--ui-lang", "en", "providers")
	if code != 0 {
		t.Fatalf("providers en failed: code=%d err=%s", code, errOut)
	}
	for _, want := range []string{"SOURCE", "TIMEOUT", "config", "env", "vault", "10s/25s", "DETAILS"} {
		if !strings.Contains(provEN, want) {
			t.Fatalf("providers en missing %q:\n%s", want, provEN)
		}
	}
	assertColumnAligned(t, provEN, "DETAILS", "concurrency=4", "type=auto", "min_chars=300, max_bytes=5MB")

	// 3. veri providers --json 输出顶层对象及统一的 source 与 timeout 字段
	code, provJSON, _ := run("providers", "--json")
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(provJSON), "{") {
		t.Fatalf("providers --json failed or not top-level object: code=%d out=%s", code, provJSON)
	}
	var provEnvelope providersJSONOutput
	if err := json.Unmarshal([]byte(provJSON), &provEnvelope); err != nil {
		t.Fatalf("unmarshal providers json: %v", err)
	}
	statuses := provEnvelope.Providers
	byName := make(map[string]app.ProviderStatus, len(statuses))
	for _, s := range statuses {
		byName[s.Name] = s
	}
	if byName["deepseek"].Source != spec.ConfigFileName || byName["deepseek"].Timeout != "10s" {
		t.Fatalf("unexpected deepseek JSON status: %+v", byName["deepseek"])
	}
	if byName["exa"].Source != "env:EXA_API_KEY" || byName["exa"].Timeout != "10s/25s" {
		t.Fatalf("unexpected exa JSON status: %+v", byName["exa"])
	}
	if byName["tinyfish"].Source != spec.SecretsFileName || byName["tinyfish"].Timeout != "10s/25s" {
		t.Fatalf("unexpected tinyfish JSON status: %+v", byName["tinyfish"])
	}
	if byName["brave"].Timeout != "" {
		t.Fatalf("expected disabled brave JSON status to omit timeout, got %+v", byName["brave"])
	}

	// 4. veri key list 表格与 --json（顶层对象）均统一输出 config.toml / secrets.json / env:...
	code, klZH, _ := run("--ui-lang", "zh", "key", "list")
	if code != 0 || !strings.Contains(klZH, spec.ConfigFileName) || !strings.Contains(klZH, spec.SecretsFileName) || !strings.Contains(klZH, "env:EXA_API_KEY") {
		t.Fatalf("unexpected key list output (code=%d):\n%s", code, klZH)
	}
	code, klJSON, _ := run("key", "list", "--json")
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(klJSON), "{") {
		t.Fatalf("key list --json failed or not top-level object: code=%d out=%s", code, klJSON)
	}
	var klEnvelope keyListJSONOutput
	if err := json.Unmarshal([]byte(klJSON), &klEnvelope); err != nil {
		t.Fatalf("unmarshal key list json: %v", err)
	}
	for _, e := range klEnvelope.Keys {
		if e.Provider == "deepseek" && e.ActiveSource != spec.ConfigFileName {
			t.Fatalf("expected key list --json active_source=%q, got %q", spec.ConfigFileName, e.ActiveSource)
		}
	}

	// 5. veri doctor 独立路由行与内嵌表格（无中点错位，并清晰展示中英文检索路由、双融合池与全球提取路由）
	code, docZH, _ := run("--ui-lang", "zh", "doctor")
	if code != 0 || strings.Contains(docZH, "·") {
		t.Fatalf("doctor zh failed or contained middle dot (code=%d):\n%s", code, docZH)
	}
	for _, want := range []string{"- 运行环境: veri 0.1.2", "- 检索路由（auto，中文）:", "- 检索路由（非中文）:", "- 默认融合池（-F，中文）:", "- 默认融合池（-F，非中文）:", "- 提取路由（api_first，中文站）:", "- 提取路由（非中文站）:", "超时"} {
		if !strings.Contains(docZH, want) {
			t.Fatalf("doctor zh missing %q:\n%s", want, docZH)
		}
	}
	code, docEN, _ := run("--ui-lang", "en", "doctor")
	if code != 0 || strings.Contains(docEN, "·") {
		t.Fatalf("doctor en failed or contained middle dot (code=%d):\n%s", code, docEN)
	}
	for _, want := range []string{"- Runtime: veri 0.1.2", "- Search chain (auto, Chinese):", "- Search chain (non-Chinese):", "- Fusion pool (-F, Chinese):", "- Fusion pool (-F, non-Chinese):", "- Fetch chain (api_first, CN sites):", "- Fetch chain (global sites):", "TIMEOUT"} {
		if !strings.Contains(docEN, want) {
			t.Fatalf("doctor en missing %q:\n%s", want, docEN)
		}
	}

	// 5b. 关闭中英/全球分链时 doctor 展示简洁单链标签
	singleChainCfg := filepath.Join(tmpDir, "single-chain.toml")
	if err := os.WriteFile(singleChainCfg, []byte("[search]\nproviders = [\"deepseek\"]\n[fetch]\nproviders = [\"deepseek\"]\n[providers.deepseek]\napi_key = \"sk-single-chain-1234\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var scOut, scErr bytes.Buffer
	if c := ExecuteContext(t.Context(), []string{"--config", singleChainCfg, "--no-color", "--ui-lang", "zh", "doctor"}, repoFS, BuildInfo{Version: "0.1.2"}, strings.NewReader(""), &scOut, &scErr, func(string) string { return "" }); c != 0 || !strings.Contains(scOut.String(), "- 检索路由（auto）:") || !strings.Contains(scOut.String(), "- 默认融合池（-F）:") || !strings.Contains(scOut.String(), "- 提取路由（api_first）:") {
		t.Fatalf("expected unsplit doctor labels when no split chain active (code=%d):\n%s", c, scOut.String())
	}

	// 6. veri update --help 跨分组标志与描述列对齐
	code, updHelp, _ := run("--ui-lang", "zh", "update", "--help")
	if code != 0 {
		t.Fatalf("update --help failed: code=%d", code)
	}
	_, updFlagsSection, hasFlags := strings.Cut(updHelp, "\n参数:\n")
	if !hasFlags {
		t.Fatalf("update --help missing flag section:\n%s", updHelp)
	}
	assertColumnAligned(t, updFlagsSection, "--check", "--mirror", "--config", "--ui-lang", "--quiet", "--help")
	assertColumnAligned(t, updFlagsSection, "仅检查版本状态", "指定配置文件路径", "显示当前命令的帮助说明")

	// 7. veri --help 常用检索参数（含 -F/--fusion）与全局参数描述列对齐
	code, rootHelp, _ := run("--ui-lang", "zh", "--help")
	if code != 0 {
		t.Fatalf("root --help failed: code=%d", code)
	}
	_, rootFlagsSection, hasRootFlags := strings.Cut(rootHelp, "\n常用检索参数:\n")
	if !hasRootFlags {
		t.Fatalf("root --help missing search flags section:\n%s", rootHelp)
	}
	assertColumnAligned(t, rootFlagsSection, "--count", "--fusion", "--format", "--config", "--quiet", "--version")
	assertColumnAligned(t, rootFlagsSection, "返回结果条数", "多源并发检索", "指定配置文件路径", "显示当前命令的帮助说明")

	// 8. formatFetchContinueArgs 精确拼装带 --section / --match 的续读参数，未命中回退时不重复携带
	if got := formatFetchContinueArgs("", "", nil, 2000); got != "--offset 2000" {
		t.Fatalf("unexpected plain continue args: %q", got)
	}
	if got := formatFetchContinueArgs("Concurrency", "", nil, 2484); got != `--section "Concurrency" --offset 2484` {
		t.Fatalf("unexpected section continue args: %q", got)
	}
	if got := formatFetchContinueArgs("Missing", "", []string{`section "Missing" not found; showing page preview and heading outline`}, 2000); got != "--offset 2000" {
		t.Fatalf("expected section miss to drop --section from continue args, got %q", got)
	}
	if got := formatFetchContinueArgs("Missing", "chan", []string{`section "Missing" not found; showing page preview and heading outline`}, 2000); got != "--offset 2000" {
		t.Fatalf("expected section miss with match to drop both --section and --match from continue args, got %q", got)
	}
	if got := formatFetchContinueArgs("Concurrency", "chan", []string{`match "chan" had 0 hits in section "Concurrency"; showing section preview and outline`}, 800); got != `--section "Concurrency" --offset 800` {
		t.Fatalf("expected match miss inside section to keep --section only, got %q", got)
	}
}

func assertColumnAligned(t *testing.T, text string, tokens ...string) {
	t.Helper()
	lines := strings.Split(text, "\n")
	wantCol := -1
	for _, tok := range tokens {
		found := false
		for _, line := range lines {
			before, _, ok := strings.Cut(line, tok)
			if !ok {
				continue
			}
			col := term.DisplayWidth(before)
			if wantCol < 0 {
				wantCol = col
			} else if col != wantCol {
				t.Errorf("token %q starts at display column %d, want %d (matching %q)\nfull output:\n%s", tok, col, wantCol, tokens[0], text)
			}
			found = true
			break
		}
		if !found {
			t.Errorf("token %q not found in output:\n%s", tok, text)
		}
	}
}
