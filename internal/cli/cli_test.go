package cli

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"veri/clikit"
	"veri/clikit/i18n"
	"veri/internal/core"
)

var headerElapsedRe = regexp.MustCompile(`· \d+\.\d{2}s`)

func normalizeElapsedInHeader(s string) string {
	return headerElapsedRe.ReplaceAllString(s, "· 0.00s")
}

func TestCLIEndToEndExitCodesI18nAndGolden(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/search":
			_, _ = w.Write([]byte(`{"results":[{"title":"MCP Go SDK","url":"https://github.com/modelcontextprotocol/go-sdk","site_name":"GitHub","date":"2026-09-14T00:00:00Z","snippet":"Official SDK","summary":"Official MCP Go SDK implementation."}]}`))
		case "/v1/tools/fetch":
			_, _ = w.Write([]byte(`{"title":"Clean Guide","content":"# Guide\n\nExtracted Markdown text."}`))
		}
	}))
	defer srv.Close()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	repoFS := os.DirFS(filepath.Join("..", ".."))

	run := func(envMap map[string]string, args ...string) (int, string, string) {
		var outBuf, errBuf bytes.Buffer
		fullArgs := append([]string{"--config", cfgPath, "--no-color"}, args...)
		code := ExecuteContext(context.Background(), fullArgs, repoFS, BuildInfo{
			Version:   "1.0.0",
			Commit:    "abc1234",
			BuildDate: "2026-11-16T00:00:00Z",
		}, strings.NewReader(""), &outBuf, &errBuf, func(k string) string {
			return envMap[k]
		})
		return code, outBuf.String(), errBuf.String()
	}

	// 1. --help 自动中英文切换、排版噪音消除与分组 Usage 验证
	code, outZh, _ := run(nil, "--ui-lang", "zh", "--help")
	if code != 0 || !strings.Contains(outZh, "联网检索") || !strings.Contains(outZh, "用法:") || !strings.Contains(outZh, "常用检索参数:") || !strings.Contains(outZh, "全局参数:") {
		t.Fatalf("expected localized Chinese help sections with --ui-lang zh, code=%d out=%s", code, outZh)
	}
	if strings.Contains(outZh, "[=true|false]") || strings.Contains(outZh, "stringArray") || strings.Contains(outZh, "$VERI_CONFIG") || strings.Contains(outZh, "Additional Commands:") || strings.Contains(outZh, "help for ") || strings.Contains(outZh, "JSON 信封") || strings.Contains(outZh, "secrets.json") || !strings.Contains(outZh, "day | week | month | year") {
		t.Fatalf("Chinese help leaked internal jargon/noise or missing self-explanatory syntax: %s", outZh)
	}
	if strings.Index(outZh, "  search ") > strings.Index(outZh, "  fetch ") {
		t.Fatalf("expected 'search' to be listed before 'fetch' in root help, got:\n%s", outZh)
	}
	code, outZhEnv, _ := run(map[string]string{"LANG": "C.UTF-8", "LANGUAGE": "zh_CN:en"}, "--help")
	if code != 0 || !strings.Contains(outZhEnv, "联网检索") {
		t.Fatalf("expected Chinese help with LANG=C.UTF-8 + LANGUAGE=zh_CN:en, code=%d out=%s", code, outZhEnv)
	}
	code, outEn, _ := run(map[string]string{"LANG": "fr_FR.UTF-8"}, "--help")
	if code != 0 || !strings.Contains(outEn, "Search the web") || !strings.Contains(outEn, "Usage:") || !strings.Contains(outEn, "Common Search Flags:") || strings.Contains(outEn, "[=true|false]") {
		t.Fatalf("expected clean English fallback help for fr_FR, code=%d out=%s", code, outEn)
	}
	code, cfgHelpZh, _ := run(nil, "--ui-lang", "zh", "config", "--help")
	if code != 0 || !strings.Contains(cfgHelpZh, "veri config <command> [flags]") || strings.Contains(cfgHelpZh, "veri config [flags]") {
		t.Fatalf("expected group command 'config' to omit runnable [flags] usage line, code=%d out=%s", code, cfgHelpZh)
	}
	code, searchHelpZh, _ := run(nil, "--ui-lang", "zh", "search", "--help")
	if code != 0 || !strings.Contains(searchHelpZh, "--exclude-site <domain>") || !strings.Contains(searchHelpZh, "--opt <k=v>") || strings.Contains(searchHelpZh, "stringArray") || !strings.Contains(searchHelpZh, "（默认: 8）") || !strings.Contains(searchHelpZh, "（默认: summary）") || !strings.Contains(searchHelpZh, "（默认: 20s）") || strings.Contains(searchHelpZh, "）（") {
		t.Fatalf("expected semantic placeholders and clean defaults on search --help, code=%d out=%s", code, searchHelpZh)
	}
	code, fetchHelpZh, _ := run(nil, "--ui-lang", "zh", "fetch", "--help")
	if code != 0 || !strings.Contains(fetchHelpZh, "（默认: api_first）") || !strings.Contains(fetchHelpZh, "api_first | smart_local | local_only | api_only") || !strings.Contains(fetchHelpZh, "（默认: 30s）") || strings.Contains(fetchHelpZh, "）（") {
		t.Fatalf("expected clean defaults on fetch --help, code=%d out=%s", code, fetchHelpZh)
	}

	// 2. config init / path / show（脱敏校验）
	code, _, errStr := run(nil, "config", "init")
	if code != 0 {
		t.Fatalf("config init failed: %s", errStr)
	}
	// 追加自定义测试端点与明文 Key，验证 config show 脱敏
	rawCfg, _ := os.ReadFile(cfgPath)
	patchedCfg := strings.ReplaceAll(string(rawCfg), `base_url = "https://api.deepseek.com"`, `base_url = "`+srv.URL+`"`)
	patchedCfg = strings.ReplaceAll(patchedCfg, `api_key = "env:DEEPSEEK_API_KEY"`, `api_key = "sk-supersecret9876"`)
	_ = os.WriteFile(cfgPath, []byte(patchedCfg), 0o600)

	code, showOut, _ := run(nil, "config", "show")
	if code != 0 || strings.Contains(showOut, "supersecret") || !strings.Contains(showOut, "sk-…9876") {
		t.Fatalf("config show leaked secret or failed: code=%d out=%s", code, showOut)
	}

	// 2b. veri key set（stdin 管道输入，拒绝明文位置参数）/ list / unset 加密密钥管理验证
	var keySetOutBuf, keySetErrBuf bytes.Buffer
	code = ExecuteContext(context.Background(), []string{"--config", cfgPath, "--no-color", "key", "set", "exa"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader("exa-encrypted-key-5555\n"), &keySetOutBuf, &keySetErrBuf, func(string) string { return "" })
	keySetOut, keySetErr := keySetOutBuf.String(), keySetErrBuf.String()
	if code != 0 || !strings.Contains(keySetOut, "secrets.json") {
		t.Fatalf("key set via stdin failed: code=%d out=%s err=%s", code, keySetOut, keySetErr)
	}
	code, keyListOut, _ := run(nil, "key", "list")
	if code != 0 || !strings.Contains(keyListOut, "secrets.json") || strings.Contains(keyListOut, "encrypted-key") {
		t.Fatalf("key list failed or leaked plaintext: code=%d out=%s", code, keyListOut)
	}
	code, _, _ = run(nil, "key", "unset", "exa")
	if code != 0 {
		t.Fatalf("key unset failed: code=%d", code)
	}

	// 3. 根命令快捷搜索（`veri go mcp sdk`）与包内黄金文件（testdata/*）逐字节校验
	goldenURLs, err := os.ReadFile(filepath.Join("testdata", "search.urls"))
	if err != nil {
		t.Fatalf("read golden urls: %v", err)
	}
	code, urlsOut, _ := run(nil, "go", "mcp", "sdk", "--format", "urls")
	if code != 0 || urlsOut != string(goldenURLs) {
		t.Fatalf("golden urls mismatch: code=%d got=%q want=%q", code, urlsOut, string(goldenURLs))
	}

	goldenMD, err := os.ReadFile(filepath.Join("testdata", "search.md"))
	if err != nil {
		t.Fatalf("read golden md: %v", err)
	}
	code, mdOut, _ := run(nil, "search", "go mcp")
	// 归一动态耗时（如 0.01s -> 0.00s）后与黄金文件逐字节对比
	normalizedMD := normalizeElapsedInHeader(mdOut)
	if code != 0 || normalizedMD != string(goldenMD) {
		t.Fatalf("golden md mismatch:\ngot:\n%s\nwant:\n%s", normalizedMD, string(goldenMD))
	}

	// 验证 doctor（人类报告与 --json）及 completion 子命令
	code, docOut, _ := run(nil, "doctor")
	if code != 0 || !strings.Contains(docOut, "deepseek") {
		t.Fatalf("doctor failed: code=%d out=%s", code, docOut)
	}
	code, docJSON, _ := run(nil, "doctor", "--json")
	if code != 0 || !strings.Contains(docJSON, `"ui_locale"`) {
		t.Fatalf("doctor --json failed: code=%d out=%s", code, docJSON)
	}
	code, compOut, _ := run(nil, "completion", "zsh")
	if code != 0 || !strings.Contains(compOut, "veri") {
		t.Fatalf("completion zsh failed: code=%d", code)
	}

	code, jsonOut, _ := run(nil, "search", "go mcp", "--json")
	if code != 0 {
		t.Fatalf("search --json failed with code %d", code)
	}
	var env core.Envelope
	if err := json.Unmarshal([]byte(jsonOut), &env); err != nil || !env.OK || env.Schema != "veri/v1" || len(env.Results) != 1 {
		t.Fatalf("invalid search JSON envelope: %+v err=%v", env, err)
	}

	// 4. 默认 fetch（api_first 直走云端）、api_only 与内网地址防云端外泄拦截校验
	code, fetchMD, _ := run(nil, "fetch", "https://juejin.cn/guide", "--fresh")
	if code != 0 || !strings.Contains(fetchMD, "# Guide") {
		t.Fatalf("default api_first fetch failed: code=%d out=%q", code, fetchMD)
	}
	var pipeOut, pipeErr bytes.Buffer
	pipeCode := ExecuteContext(context.Background(), []string{"--config", cfgPath, "fetch", "-", "https://example.com/extra", "--strategy", "api_only", "--json"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader("https://example.com/a\n# comment\n"), &pipeOut, &pipeErr, func(string) string { return "" })
	if pipeCode != 0 || !strings.Contains(pipeOut.String(), `"pages"`) {
		t.Fatalf("piped batch fetch failed: code=%d out=%s err=%s", pipeCode, pipeOut.String(), pipeErr.String())
	}
	if code, _, errPriv := run(nil, "--ui-lang", "zh", "fetch", "http://127.0.0.1:8080/internal", "--strategy", "api_only"); code != 5 || !strings.Contains(errPriv, "拒绝将本地或内网地址发送至外部云端提取源") {
		t.Fatalf("expected exit code 5 and localized cloud-leak block message for private URL, got code=%d err=%q", code, errPriv)
	}
	if code, _, _ := run(nil, "fetch", "https://example.com/guide", "-o", "invalid_no_eq"); code != 2 {
		t.Fatalf("expected exit code 2 on malformed fetch -o flag, got %d", code)
	}

	// 验证 providers.local.enabled = false 时双语状态展示与内网拒绝提示
	noLocalCfgPath := filepath.Join(tmpDir, "no-local.toml")
	_ = os.WriteFile(noLocalCfgPath, []byte(strings.ReplaceAll(patchedCfg, "enabled = true                          # 设为 false 可完全禁用本地网页提取器", "enabled = false")), 0o600)
	var nlOut, nlErr bytes.Buffer
	nlCode := ExecuteContext(context.Background(), []string{"--config", noLocalCfgPath, "--ui-lang", "zh", "providers"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &nlOut, &nlErr, func(string) string { return "" })
	if nlCode != 0 || !strings.Contains(nlOut.String(), "已在配置中禁用") {
		t.Fatalf("expected localized disabled-in-config label on providers table, code=%d out=%s", nlCode, nlOut.String())
	}
	nlOut.Reset()
	nlErr.Reset()
	nlPrivCode := ExecuteContext(context.Background(), []string{"--config", noLocalCfgPath, "--ui-lang", "zh", "fetch", "http://127.0.0.1:8080/internal"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &nlOut, &nlErr, func(string) string { return "" })
	if nlPrivCode != 3 || !strings.Contains(nlErr.String(), "本地提取器已在配置中禁用") {
		t.Fatalf("expected exit code 3 and localized local-disabled message on private URL, code=%d err=%s", nlPrivCode, nlErr.String())
	}

	// 5. providers、version (-v / --version)、skill print、skill install
	code, provOutEn, _ := run(nil, "--ui-lang", "en", "providers")
	if code != 0 || !strings.Contains(provOutEn, "deepseek") || !strings.Contains(provOutEn, "tinyfish") || !strings.Contains(provOutEn, "ENDPOINT") {
		t.Fatalf("providers (en) failed: code=%d out=%s", code, provOutEn)
	}
	code, provOutZh, _ := run(nil, "--ui-lang", "zh", "providers")
	if code != 0 || !strings.Contains(provOutZh, "端点") || !strings.Contains(provOutZh, "已启用") || strings.Contains(provOutZh, "run `veri key set") {
		t.Fatalf("providers (zh) failed or leaked English detail prose: code=%d out=%s", code, provOutZh)
	}
	code, skillOut, _ := run(nil, "skill", "print")
	if code != 0 || !strings.Contains(skillOut, "name: veri") || len(strings.Split(skillOut, "\n")) > 120 {
		t.Fatalf("skill print invalid or >120 lines: code=%d", code)
	}
	skillInstallDir := filepath.Join(tmpDir, "installed-skills")
	code, installOut, installErr := run(nil, "skill", "install", "--dir", skillInstallDir)
	if code != 0 || !strings.Contains(installOut, filepath.Join("veri", "SKILL.md")) {
		t.Fatalf("skill install failed: code=%d out=%s err=%s", code, installOut, installErr)
	}
	// 重复安装未加 --force 应返回参数/状态错误退出码 2
	code, _, _ = run(nil, "skill", "install", "--dir", skillInstallDir)
	if code != 2 {
		t.Fatalf("expected exit code 2 when skill file already exists without --force, got %d", code)
	}
	code, _, installErr = run(nil, "skill", "install", "--dir", skillInstallDir, "--force")
	if code != 0 {
		t.Fatalf("skill install --force failed: code=%d err=%s", code, installErr)
	}
	code, verOut, _ := run(nil, "version")
	if code != 0 || !strings.Contains(verOut, "veri 1.0.0") {
		t.Fatalf("version failed: code=%d out=%s", code, verOut)
	}
	code, verFlagOut, _ := run(nil, "-v")
	if code != 0 || verFlagOut != verOut {
		t.Fatalf("expected -v output %q to match 'veri version' %q (code=%d)", verFlagOut, verOut, code)
	}
	code, verJSONOut, _ := run(nil, "--version", "--json")
	if code != 0 || !strings.Contains(verJSONOut, `"version": "1.0.0"`) {
		t.Fatalf("expected --version --json to emit JSON build info, code=%d out=%s", code, verJSONOut)
	}

	// 6. 退出码与 --json 错误信封验证：参数错误 = 2，无可用 Provider = 3
	code, _, _ = run(nil, "search", "go", "-n", "999")
	if code != 2 {
		t.Fatalf("expected exit code 2 for out-of-range count, got %d", code)
	}

	emptyCfgPath := filepath.Join(tmpDir, "empty.toml")
	_ = os.WriteFile(emptyCfgPath, []byte(""), 0o600)
	var errOutBuf, errStderr bytes.Buffer
	code = ExecuteContext(context.Background(), []string{"--config", emptyCfgPath, "search", "go", "--json"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &errOutBuf, &errStderr, func(string) string { return "" })
	if code != 3 {
		t.Fatalf("expected exit code 3 when no provider configured, got %d (stderr=%s)", code, errStderr.String())
	}
	var failEnv core.Envelope
	if err := json.Unmarshal(errOutBuf.Bytes(), &failEnv); err != nil || failEnv.OK || failEnv.Error == nil || failEnv.Error.Code != core.CodeNoProvider || !strings.Contains(failEnv.Error.Hint, "veri key set") {
		t.Fatalf("expected valid JSON error envelope with key-set hint on exit 3, got %s", errOutBuf.String())
	}

	missingCfgPath := filepath.Join(tmpDir, "fresh-dir", "config.toml")
	var docEmptyOut, docEmptyErr bytes.Buffer
	docCode := ExecuteContext(context.Background(), []string{"--config", missingCfgPath, "--ui-lang", "zh", "doctor"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &docEmptyOut, &docEmptyErr, func(string) string { return "" })
	if docCode != 3 || !strings.Contains(docEmptyOut.String(), "veri config init") || !strings.Contains(docEmptyOut.String(), "veri key set") {
		t.Fatalf("expected fresh doctor to mark config optional and guide key setup, got code=%d out=%s err=%s", docCode, docEmptyOut.String(), docEmptyErr.String())
	}

	// 7. 根命令防误触与防密钥泄露守卫 + JSON 管理输出
	if code, _, errOut := run(nil, "doctro"); code != 2 || !strings.Contains(errOut, "doctor") {
		t.Fatalf("expected mistyped root subcommand 'doctro' to return exit 2 with suggestion, got code=%d err=%s", code, errOut)
	}
	if code, _, errOut := run(nil, "https://go.dev/doc"); code != 2 || !strings.Contains(errOut, "fetch") {
		t.Fatalf("expected bare URL on root command to suggest 'veri fetch', got code=%d err=%s", code, errOut)
	}
	if code, _, _ := run(nil, "sk-accidentalSecretLeak1234567890"); code != 2 {
		t.Fatalf("expected apparent API key on root command to be blocked with exit 2, got %d", code)
	}
	if code, _, _ := run(nil, "--ui-lang", "klingon", "version"); code != 2 {
		t.Fatalf("expected invalid --ui-lang to return exit 2, got %d", code)
	}
	if code, _, errOut := run(nil, "--ui-lang", "zh", "config", "set-key", "exa", "x"); code != 2 || !strings.Contains(errOut, "veri key set") {
		t.Fatalf("expected removed 'config set-key' to return exit 2 with migration hint, got code=%d err=%s", code, errOut)
	}
	if code, _, errOut := run(nil, "--ui-lang", "zh", "config", "unset-key", "exa"); code != 2 || !strings.Contains(errOut, "veri key unset") {
		t.Fatalf("expected removed 'config unset-key' to return exit 2 with migration hint, got code=%d err=%s", code, errOut)
	}
	if code, _, errOut := run(nil, "config", "shwo"); code != 2 || !strings.Contains(errOut, "show") {
		t.Fatalf("expected mistyped 'config shwo' to suggest 'show', got code=%d err=%s", code, errOut)
	}
	if code, _, _ := run(nil, "key", "no-such-subcmd"); code != 2 {
		t.Fatalf("expected unknown 'key' subcommand to return exit 2, got %d", code)
	}
	if code, _, _ := run(nil, "skill", "no-such-subcmd"); code != 2 {
		t.Fatalf("expected unknown 'skill' subcommand to return exit 2, got %d", code)
	}
	if code, cfgJSON, _ := run(nil, "config", "show", "--json"); code != 0 || strings.Contains(cfgJSON, "supersecret") || !strings.Contains(cfgJSON, `"sk-…9876"`) {
		t.Fatalf("config show --json failed or leaked secret: code=%d out=%s", code, cfgJSON)
	}
	if code, klJSON, _ := run(nil, "key", "list", "--json"); code != 0 || !strings.Contains(klJSON, `"provider"`) {
		t.Fatalf("key list --json failed: code=%d out=%s", code, klJSON)
	}
	if code, mcpCfg, _ := run(nil, "mcp", "config"); code != 0 || !strings.Contains(mcpCfg, `"mcpServers"`) {
		t.Fatalf("mcp config failed: code=%d out=%s", code, mcpCfg)
	}

	// 8. 验证 zh-CN 下专属修复提示（WithI18n / Cobra 未知选项）与 en-US 英文配置模板输出
	if code, _, errZh := run(nil, "--ui-lang", "zh", "https://user:LeakPass99@go.dev/doc"); code != 2 || !strings.Contains(errZh, "veri fetch <url>") || !strings.Contains(errZh, "网页链接") || strings.Contains(errZh, "LeakPass99") {
		t.Fatalf("expected localized & sanitized zh-CN URL guard, got code=%d err=%s", code, errZh)
	}
	if code, _, errZh := run(nil, "--ui-lang", "zh", "config", "init"); code != 2 || !strings.Contains(errZh, "--force") {
		t.Fatalf("expected localized zh-CN config init existing-file --force hint, got code=%d err=%s", code, errZh)
	}
	if code, _, errZh := run(nil, "--ui-lang", "zh", "--no-such-flag"); code != 2 || !strings.Contains(errZh, "--no-such-flag") || !strings.Contains(errZh, "未知的命令行选项") {
		t.Fatalf("expected localized zh-CN unknown flag error, got code=%d err=%s", code, errZh)
	}
	if code, _, errZh := run(nil, "--ui-lang", "zh", "-Z"); code != 2 || !strings.Contains(errZh, "-Z") {
		t.Fatalf("expected localized shorthand unknown flag to retain '-Z', got code=%d err=%s", code, errZh)
	}
	if code, _, _ := run(nil, "search", "go", "-t", "7d", "--json"); code != 0 {
		t.Fatalf("expected -t 7d to succeed, got code=%d", code)
	}
	if code, _, _ := run(nil, "search", "go", "-t", "week", "--json"); code != 0 {
		t.Fatalf("expected -t week to succeed, got code=%d", code)
	}
	if code, _, errZh := run(nil, "--ui-lang", "zh", "search", "go", "-t", "5h"); code != 2 || !strings.Contains(errZh, "无效的时间范围参数") || !strings.Contains(errZh, "day | week") {
		t.Fatalf("expected localized freshness error with syntax hint on -t 5h, got code=%d err=%s", code, errZh)
	}
	if code, _, errZh := run(nil, "--ui-lang", "zh", "search", "go", "-d", "wrong"); code != 2 || !strings.Contains(errZh, "无效的详略级别") {
		t.Fatalf("expected localized detail error, got code=%d err=%s", code, errZh)
	}
	if code, _, errZh := run(nil, "--ui-lang", "zh", "search", "go", "--format", "xml"); code != 2 || !strings.Contains(errZh, "不支持的输出格式") {
		t.Fatalf("expected localized format pre-validation error, got code=%d err=%s", code, errZh)
	}
	if code, _, errZh := run(nil, "--ui-lang", "zh", "fetch", "https://go.dev", "--strategy", "wrong"); code != 2 || !strings.Contains(errZh, "无效的提取路由策略") {
		t.Fatalf("expected localized fetch strategy error, got code=%d err=%s", code, errZh)
	}

	enCfgPath := filepath.Join(t.TempDir(), "en-config.toml")
	var enOut, enErr bytes.Buffer
	enCode := ExecuteContext(context.Background(), []string{"--config", enCfgPath, "--ui-lang", "en", "config", "init"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &enOut, &enErr, func(string) string { return "" })
	enRaw, _ := os.ReadFile(enCfgPath)
	if enCode != 0 || !strings.Contains(string(enRaw), "# veri configuration file.") {
		t.Fatalf("expected English-commented config template under --ui-lang en, code=%d content=%s", enCode, string(enRaw))
	}
}

func TestKeySetInterruptExitsCleanly(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	repoFS := os.DirFS(filepath.Join("..", ".."))

	pr, pw := io.Pipe()
	defer func() {
		_ = pw.Close()
		_ = pr.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	var outBuf, errBuf bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- ExecuteContext(ctx, []string{"--config", cfgPath, "key", "set", "deepseek"}, repoFS, BuildInfo{Version: "1.0.0"}, pr, &outBuf, &errBuf, func(string) string { return "" })
	}()

	time.AfterFunc(25*time.Millisecond, cancel)

	select {
	case code := <-done:
		if code != clikit.ExitInterrupted {
			t.Fatalf("expected exit code %d (ExitInterrupted), got %d (stderr=%q)", clikit.ExitInterrupted, code, errBuf.String())
		}
		if errBuf.Len() != 0 {
			t.Fatalf("expected silent clean exit on Ctrl+C interrupt, got stderr=%q", errBuf.String())
		}
	case <-time.After(time.Second):
		t.Fatal("veri key set hung after context cancellation")
	}
}

func TestAllNonBoolFlagsDefineSemanticPlaceholder(t *testing.T) {
	rt := &runtimeEnv{
		uiLocale: i18n.LocaleEnUS,
		cat:      localeBundle.For(i18n.LocaleEnUS),
		stdin:    strings.NewReader(""),
		stdout:   io.Discard,
		stderr:   io.Discard,
		getenv:   func(string) string { return "" },
	}
	root := newRootCmd(rt)

	var checkCmd func(cmd *cobra.Command)
	checkCmd = func(cmd *cobra.Command) {
		checkSet := func(fs *pflag.FlagSet) {
			fs.VisitAll(func(f *pflag.Flag) {
				if f.Hidden || f.Value.Type() == "bool" {
					return
				}
				if flagPlaceholders[f.Name] == "" {
					t.Errorf("command %q non-bool flag --%s (%s) missing semantic placeholder in flagPlaceholders", cmd.CommandPath(), f.Name, f.Value.Type())
				}
			})
		}
		checkSet(cmd.Flags())
		checkSet(cmd.PersistentFlags())
		for _, sub := range cmd.Commands() {
			checkCmd(sub)
		}
	}
	checkCmd(root)
}
