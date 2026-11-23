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

func assertContainsAll(t *testing.T, label, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("%s missing %q:\n%s", label, want, text)
		}
	}
}

func assertContainsNone(t *testing.T, label, text string, forbiddens ...string) {
	t.Helper()
	for _, forbidden := range forbiddens {
		if strings.Contains(text, forbidden) {
			t.Fatalf("%s unexpectedly contained %q:\n%s", label, forbidden, text)
		}
	}
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
	buildInfo := BuildInfo{
		Version:   "1.0.0",
		Commit:    "abc1234",
		BuildDate: "2026-11-16T00:00:00Z",
	}

	execCLI := func(t *testing.T, configFile string, stdin string, envMap map[string]string, args ...string) (int, string, string) {
		t.Helper()
		var outBuf, errBuf bytes.Buffer
		fullArgs := append([]string{"--config", configFile}, args...)
		code := ExecuteContext(
			t.Context(),
			fullArgs,
			repoFS,
			buildInfo,
			strings.NewReader(stdin),
			&outBuf,
			&errBuf,
			func(k string) string { return envMap[k] },
		)
		return code, outBuf.String(), errBuf.String()
	}

	// 在父测试预初始化共享只读配置，保证每个子测试均完全独立、可单独 -run 执行。
	if code, _, errStr := execCLI(t, cfgPath, "", nil, "--no-color", "config", "init"); code != 0 {
		t.Fatalf("seed config init failed: %s", errStr)
	}
	rawSeedCfg, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read seed config.toml: %v", err)
	}
	seededCfg := string(rawSeedCfg) + "\n[providers.deepseek]\nbase_url = \"" + srv.URL + "\"\napi_key = \"sk-supersecret9876\"\n"
	if err := os.WriteFile(cfgPath, []byte(seededCfg), 0o600); err != nil {
		t.Fatalf("write seeded config.toml: %v", err)
	}

	run := func(t *testing.T, envMap map[string]string, args ...string) (int, string, string) {
		t.Helper()
		return execCLI(t, cfgPath, "", envMap, append([]string{"--no-color"}, args...)...)
	}

	t.Run("help_localization_and_flag_formatting", func(t *testing.T) {
		code, outZh, _ := run(t, nil, "--ui-lang", "zh", "--help")
		if code != 0 {
			t.Fatalf("zh --help failed: code=%d out=%s", code, outZh)
		}
		assertContainsAll(t, "zh --help", outZh,
			"联网检索", "用法:", "常用检索参数:", "全局参数:", "day | week | month | year",
		)
		assertContainsNone(t, "zh --help", outZh,
			"[=true|false]", "stringArray", "$VERI_CONFIG",
			"Additional Commands:", "help for ", "JSON 信封", "secrets.json",
		)
		if strings.Index(outZh, "  search ") > strings.Index(outZh, "  fetch ") {
			t.Fatalf("expected 'search' to be listed before 'fetch' in root help, got:\n%s", outZh)
		}

		code, outZhEnv, _ := run(t, map[string]string{"LANG": "C.UTF-8", "LANGUAGE": "zh_CN:en"}, "--help")
		if code != 0 || !strings.Contains(outZhEnv, "联网检索") {
			t.Fatalf("expected Chinese help with LANG=C.UTF-8 + LANGUAGE=zh_CN:en, code=%d out=%s", code, outZhEnv)
		}

		code, outEn, _ := run(t, map[string]string{"LANG": "fr_FR.UTF-8"}, "--help")
		if code != 0 {
			t.Fatalf("fr_FR fallback --help failed: code=%d out=%s", code, outEn)
		}
		assertContainsAll(t, "fr_FR fallback --help", outEn, "Search the web", "Usage:", "Common Search Flags:")
		assertContainsNone(t, "fr_FR fallback --help", outEn, "[=true|false]")

		code, cfgHelpZh, _ := run(t, nil, "--ui-lang", "zh", "config", "--help")
		if code != 0 || !strings.Contains(cfgHelpZh, "veri config <command> [flags]") || strings.Contains(cfgHelpZh, "veri config [flags]") {
			t.Fatalf("expected group command 'config' to omit runnable [flags] usage line, code=%d out=%s", code, cfgHelpZh)
		}

		code, searchHelpZh, _ := run(t, nil, "--ui-lang", "zh", "search", "--help")
		if code != 0 {
			t.Fatalf("search --help failed: code=%d out=%s", code, searchHelpZh)
		}
		assertContainsAll(t, "search --help", searchHelpZh,
			"--exclude-site <domain>", "--opt <k=v>", "（默认: 8）", "（默认: summary）", "（默认: 20s）",
		)
		assertContainsNone(t, "search --help", searchHelpZh, "stringArray", "）（")

		code, fetchHelpZh, _ := run(t, nil, "--ui-lang", "zh", "fetch", "--help")
		if code != 0 {
			t.Fatalf("fetch --help failed: code=%d out=%s", code, fetchHelpZh)
		}
		assertContainsAll(t, "fetch --help", fetchHelpZh,
			"（默认: api_first）", "api_first | smart_local | local_only | api_only", "（默认: 45s）",
		)
		assertContainsNone(t, "fetch --help", fetchHelpZh, "）（")
	})

	t.Run("config_init_show_and_key_vault", func(t *testing.T) {
		isolatedCfg := filepath.Join(t.TempDir(), "config.toml")
		code, _, errStr := execCLI(t, isolatedCfg, "", nil, "--no-color", "config", "init")
		if code != 0 {
			t.Fatalf("config init failed: %s", errStr)
		}
		code, ovEmptyOut, _ := execCLI(t, isolatedCfg, "", nil, "--no-color", "config", "show", "--overrides", "--json")
		if code != 0 || strings.TrimSpace(ovEmptyOut) != "{}" {
			t.Fatalf("expected commented template to have zero overrides, code=%d out=%q", code, ovEmptyOut)
		}

		// 追加自定义测试端点与明文 Key，验证 config show 脱敏
		rawCfg, err := os.ReadFile(isolatedCfg)
		if err != nil {
			t.Fatalf("read config.toml: %v", err)
		}
		patched := string(rawCfg) + "\n[providers.deepseek]\nbase_url = \"" + srv.URL + "\"\napi_key = \"sk-supersecret9876\"\n"
		if err := os.WriteFile(isolatedCfg, []byte(patched), 0o600); err != nil {
			t.Fatalf("write patched config.toml: %v", err)
		}

		code, showOut, _ := execCLI(t, isolatedCfg, "", nil, "--no-color", "config", "show")
		if code != 0 || strings.Contains(showOut, "supersecret") || !strings.Contains(showOut, "sk-…9876") {
			t.Fatalf("config show leaked secret or failed: code=%d out=%s", code, showOut)
		}

		// veri key set（stdin 管道输入）/ list / unset 加密密钥管理验证
		code, keySetOut, keySetErr := execCLI(t, isolatedCfg, "exa-encrypted-key-5555\n", nil, "--no-color", "key", "set", "exa")
		if code != 0 || !strings.Contains(keySetOut, "secrets.json") {
			t.Fatalf("key set via stdin failed: code=%d out=%s err=%s", code, keySetOut, keySetErr)
		}
		code, keyListOut, _ := execCLI(t, isolatedCfg, "", nil, "--no-color", "key", "list")
		if code != 0 || !strings.Contains(keyListOut, "secrets.json") || strings.Contains(keyListOut, "encrypted-key") {
			t.Fatalf("key list failed or leaked plaintext: code=%d out=%s", code, keyListOut)
		}
		if code, _, _ := execCLI(t, isolatedCfg, "", nil, "--no-color", "key", "unset", "exa"); code != 0 {
			t.Fatalf("key unset failed: code=%d", code)
		}
	})

	t.Run("search_golden_doctor_and_completion", func(t *testing.T) {
		goldenURLs, err := os.ReadFile(filepath.Join("testdata", "search.urls"))
		if err != nil {
			t.Fatalf("read golden urls: %v", err)
		}
		code, urlsOut, _ := run(t, nil, "go", "mcp", "sdk", "--format", "urls")
		if code != 0 || urlsOut != string(goldenURLs) {
			t.Fatalf("golden urls mismatch: code=%d got=%q want=%q", code, urlsOut, string(goldenURLs))
		}

		goldenMD, err := os.ReadFile(filepath.Join("testdata", "search.md"))
		if err != nil {
			t.Fatalf("read golden md: %v", err)
		}
		code, mdOut, _ := run(t, nil, "search", "go mcp")
		normalizedMD := normalizeElapsedInHeader(mdOut)
		if code != 0 || normalizedMD != string(goldenMD) {
			t.Fatalf("golden md mismatch:\ngot:\n%s\nwant:\n%s", normalizedMD, string(goldenMD))
		}

		code, docOut, _ := run(t, nil, "doctor")
		if code != 0 || !strings.Contains(docOut, "deepseek") {
			t.Fatalf("doctor failed: code=%d out=%s", code, docOut)
		}
		code, docJSON, _ := run(t, nil, "doctor", "--json")
		if code != 0 || !strings.Contains(docJSON, `"ui_locale"`) {
			t.Fatalf("doctor --json failed: code=%d out=%s", code, docJSON)
		}
		code, compOut, _ := run(t, nil, "completion", "zsh")
		if code != 0 || !strings.Contains(compOut, "veri") {
			t.Fatalf("completion zsh failed: code=%d", code)
		}

		code, jsonOut, _ := run(t, nil, "search", "go mcp", "--json")
		if code != 0 {
			t.Fatalf("search --json failed with code %d", code)
		}
		var env core.Envelope
		if err := json.Unmarshal([]byte(jsonOut), &env); err != nil || !env.OK || env.Schema != "veri/v1" || len(env.Results) != 1 {
			t.Fatalf("invalid search JSON envelope: %+v err=%v", env, err)
		}
	})

	t.Run("fetch_batch_private_guards_and_local_disabled", func(t *testing.T) {
		code, fetchMD, _ := run(t, nil, "fetch", "https://juejin.cn/guide", "--fresh")
		if code != 0 || !strings.Contains(fetchMD, "# Guide") {
			t.Fatalf("default api_first fetch failed: code=%d out=%q", code, fetchMD)
		}

		pipeCode, pipeOut, pipeErr := execCLI(
			t,
			cfgPath,
			"https://example.com/a\n# comment\n",
			nil,
			"fetch", "-", "https://example.com/extra", "--strategy", "api_only", "--json",
		)
		if pipeCode != 0 || !strings.Contains(pipeOut, `"pages"`) {
			t.Fatalf("piped batch fetch failed: code=%d out=%s err=%s", pipeCode, pipeOut, pipeErr)
		}

		code, _, errPriv := run(t, nil, "--ui-lang", "zh", "fetch", "http://127.0.0.1:8080/internal", "--strategy", "api_only")
		if code != 5 || !strings.Contains(errPriv, "拒绝将本地或内网地址发送至外部云端提取源") {
			t.Fatalf("expected exit code 5 and localized cloud-leak block message for private URL, got code=%d err=%q", code, errPriv)
		}
		if code, _, _ := run(t, nil, "fetch", "https://example.com/guide", "-o", "invalid_no_eq"); code != 2 {
			t.Fatalf("expected exit code 2 on malformed fetch -o flag, got %d", code)
		}

		// providers.local.enabled = false 时双语状态展示与内网拒绝提示
		noLocalCfgPath := filepath.Join(t.TempDir(), "no-local.toml")
		if err := os.WriteFile(noLocalCfgPath, []byte(seededCfg), 0o600); err != nil {
			t.Fatal(err)
		}
		if nlSetCode, _, nlSetErr := execCLI(t, noLocalCfgPath, "", nil, "config", "set", "providers.local.enabled", "false"); nlSetCode != 0 {
			t.Fatalf("config set providers.local.enabled=false failed: code=%d err=%s", nlSetCode, nlSetErr)
		}
		nlCode, nlOut, _ := execCLI(t, noLocalCfgPath, "", nil, "--ui-lang", "zh", "providers")
		if nlCode != 0 || !strings.Contains(nlOut, "已在配置中禁用") {
			t.Fatalf("expected localized disabled-in-config label on providers table, code=%d out=%s", nlCode, nlOut)
		}
		nlPrivCode, _, nlPrivErr := execCLI(t, noLocalCfgPath, "", nil, "--ui-lang", "zh", "fetch", "http://127.0.0.1:8080/internal")
		if nlPrivCode != 3 || !strings.Contains(nlPrivErr, "本地提取器已在配置中禁用") {
			t.Fatalf("expected exit code 3 and localized local-disabled message on private URL, code=%d err=%s", nlPrivCode, nlPrivErr)
		}
	})

	t.Run("providers_skill_and_version", func(t *testing.T) {
		code, provOutEn, _ := run(t, nil, "--ui-lang", "en", "providers")
		if code != 0 {
			t.Fatalf("providers (en) failed: code=%d out=%s", code, provOutEn)
		}
		assertContainsAll(t, "providers (en)", provOutEn, "deepseek", "tinyfish", "ENDPOINT")

		code, provOutZh, _ := run(t, nil, "--ui-lang", "zh", "providers")
		if code != 0 {
			t.Fatalf("providers (zh) failed: code=%d out=%s", code, provOutZh)
		}
		assertContainsAll(t, "providers (zh)", provOutZh, "端点", "已启用")
		assertContainsNone(t, "providers (zh)", provOutZh, "run `veri key set")

		code, skillOut, _ := run(t, nil, "skill", "print")
		if code != 0 || !strings.Contains(skillOut, "name: veri") || len(strings.Split(skillOut, "\n")) > 120 {
			t.Fatalf("skill print invalid or >120 lines: code=%d", code)
		}

		skillInstallDir := filepath.Join(t.TempDir(), "installed-skills")
		code, installOut, installErr := run(t, nil, "skill", "install", "--dir", skillInstallDir)
		if code != 0 || !strings.Contains(installOut, filepath.Join("veri", "SKILL.md")) {
			t.Fatalf("skill install failed: code=%d out=%s err=%s", code, installOut, installErr)
		}
		if code, _, _ := run(t, nil, "skill", "install", "--dir", skillInstallDir); code != 2 {
			t.Fatalf("expected exit code 2 when skill file already exists without --force, got %d", code)
		}
		if code, _, installErr := run(t, nil, "skill", "install", "--dir", skillInstallDir, "--force"); code != 0 {
			t.Fatalf("skill install --force failed: code=%d err=%s", code, installErr)
		}

		code, verOut, _ := run(t, nil, "version")
		if code != 0 || !strings.Contains(verOut, "veri 1.0.0") {
			t.Fatalf("version failed: code=%d out=%s", code, verOut)
		}
		code, verFlagOut, _ := run(t, nil, "-v")
		if code != 0 || verFlagOut != verOut {
			t.Fatalf("expected -v output %q to match 'veri version' %q (code=%d)", verFlagOut, verOut, code)
		}
		code, verJSONOut, _ := run(t, nil, "--version", "--json")
		if code != 0 || !strings.Contains(verJSONOut, `"version": "1.0.0"`) {
			t.Fatalf("expected --version --json to emit JSON build info, code=%d out=%s", code, verJSONOut)
		}
	})

	t.Run("exit_codes_and_unconfigured_guidance", func(t *testing.T) {
		if code, _, _ := run(t, nil, "search", "go", "-n", "999"); code != 2 {
			t.Fatalf("expected exit code 2 for out-of-range count, got %d", code)
		}

		emptyCfgPath := filepath.Join(t.TempDir(), "empty.toml")
		if err := os.WriteFile(emptyCfgPath, []byte(""), 0o600); err != nil {
			t.Fatal(err)
		}
		code, errOut, errStderr := execCLI(t, emptyCfgPath, "", nil, "search", "go", "--json")
		if code != 3 {
			t.Fatalf("expected exit code 3 when no provider configured, got %d (stderr=%s)", code, errStderr)
		}
		var failEnv core.Envelope
		if err := json.Unmarshal([]byte(errOut), &failEnv); err != nil ||
			failEnv.OK ||
			failEnv.Error == nil ||
			failEnv.Error.Code != core.CodeNoProvider ||
			!strings.Contains(failEnv.Error.Hint, "veri key set") {
			t.Fatalf("expected valid JSON error envelope with key-set hint on exit 3, got %s", errOut)
		}

		missingCfgPath := filepath.Join(t.TempDir(), "fresh-dir", "config.toml")
		docCode, docEmptyOut, docEmptyErr := execCLI(t, missingCfgPath, "", nil, "--ui-lang", "zh", "doctor")
		if docCode != 3 {
			t.Fatalf("expected fresh doctor exit 3, got code=%d out=%s err=%s", docCode, docEmptyOut, docEmptyErr)
		}
		assertContainsAll(t, "fresh doctor", docEmptyOut, "veri config init", "veri key set")
	})

	t.Run("root_guards_and_json_admin_output", func(t *testing.T) {
		if code, _, errOut := run(t, nil, "doctro"); code != 2 || !strings.Contains(errOut, "doctor") {
			t.Fatalf("expected mistyped root subcommand 'doctro' to return exit 2 with suggestion, got code=%d err=%s", code, errOut)
		}
		if code, _, errOut := run(t, nil, "https://go.dev/doc"); code != 2 || !strings.Contains(errOut, "fetch") {
			t.Fatalf("expected bare URL on root command to suggest 'veri fetch', got code=%d err=%s", code, errOut)
		}
		if code, _, _ := run(t, nil, "sk-accidentalSecretLeak1234567890"); code != 2 {
			t.Fatalf("expected apparent API key on root command to be blocked with exit 2, got %d", code)
		}
		if code, _, _ := run(t, nil, "--ui-lang", "klingon", "version"); code != 2 {
			t.Fatalf("expected invalid --ui-lang to return exit 2, got %d", code)
		}
		if code, _, errOut := run(t, nil, "--ui-lang", "zh", "config", "set-key", "exa", "x"); code != 2 || !strings.Contains(errOut, "veri key set") {
			t.Fatalf("expected removed 'config set-key' to return exit 2 with migration hint, got code=%d err=%s", code, errOut)
		}
		if code, _, errOut := run(t, nil, "--ui-lang", "zh", "config", "unset-key", "exa"); code != 2 || !strings.Contains(errOut, "veri key unset") {
			t.Fatalf("expected removed 'config unset-key' to return exit 2 with migration hint, got code=%d err=%s", code, errOut)
		}
		if code, _, errOut := run(t, nil, "config", "shwo"); code != 2 || !strings.Contains(errOut, "show") {
			t.Fatalf("expected mistyped 'config shwo' to suggest 'show', got code=%d err=%s", code, errOut)
		}
		if code, _, _ := run(t, nil, "key", "no-such-subcmd"); code != 2 {
			t.Fatalf("expected unknown 'key' subcommand to return exit 2, got %d", code)
		}
		if code, _, _ := run(t, nil, "skill", "no-such-subcmd"); code != 2 {
			t.Fatalf("expected unknown 'skill' subcommand to return exit 2, got %d", code)
		}

		code, cfgJSON, _ := run(t, nil, "config", "show", "--json")
		if code != 0 || strings.Contains(cfgJSON, "supersecret") || !strings.Contains(cfgJSON, `"sk-…9876"`) {
			t.Fatalf("config show --json failed or leaked secret: code=%d out=%s", code, cfgJSON)
		}
		code, klJSON, _ := run(t, nil, "key", "list", "--json")
		if code != 0 || !strings.Contains(klJSON, `"keys"`) || !strings.Contains(klJSON, `"provider"`) {
			t.Fatalf("key list --json failed: code=%d out=%s", code, klJSON)
		}
		code, mcpCfg, _ := run(t, nil, "mcp", "config")
		if code != 0 || !strings.Contains(mcpCfg, `"mcpServers"`) {
			t.Fatalf("mcp config failed: code=%d out=%s", code, mcpCfg)
		}
	})

	t.Run("zh_validation_hints_and_en_template", func(t *testing.T) {
		code, _, errZh := run(t, nil, "--ui-lang", "zh", "https://user:LeakPass99@go.dev/doc")
		if code != 2 {
			t.Fatalf("expected exit 2 on zh URL guard, got %d (err=%s)", code, errZh)
		}
		assertContainsAll(t, "zh URL guard", errZh, "veri fetch <url>", "网页链接")
		assertContainsNone(t, "zh URL guard", errZh, "LeakPass99")

		if code, _, errZh := run(t, nil, "--ui-lang", "zh", "config", "init"); code != 2 || !strings.Contains(errZh, "--force") {
			t.Fatalf("expected localized zh-CN config init existing-file --force hint, got code=%d err=%s", code, errZh)
		}
		if code, _, errZh := run(t, nil, "--ui-lang", "zh", "--no-such-flag"); code != 2 ||
			!strings.Contains(errZh, "--no-such-flag") || !strings.Contains(errZh, "未知的命令行选项") {
			t.Fatalf("expected localized zh-CN unknown flag error, got code=%d err=%s", code, errZh)
		}
		if code, _, errZh := run(t, nil, "--ui-lang", "zh", "-Z"); code != 2 || !strings.Contains(errZh, "-Z") {
			t.Fatalf("expected localized shorthand unknown flag to retain '-Z', got code=%d err=%s", code, errZh)
		}

		if code, _, _ := run(t, nil, "search", "go", "-t", "7d", "--json"); code != 0 {
			t.Fatalf("expected -t 7d to succeed, got code=%d", code)
		}
		if code, _, _ := run(t, nil, "search", "go", "-t", "week", "--json"); code != 0 {
			t.Fatalf("expected -t week to succeed, got code=%d", code)
		}
		if code, _, errZh := run(t, nil, "--ui-lang", "zh", "search", "go", "-t", "5h"); code != 2 ||
			!strings.Contains(errZh, "无效的时间范围参数") || !strings.Contains(errZh, "day | week") {
			t.Fatalf("expected localized freshness error with syntax hint on -t 5h, got code=%d err=%s", code, errZh)
		}
		if code, _, errZh := run(t, nil, "--ui-lang", "zh", "search", "go", "-d", "wrong"); code != 2 || !strings.Contains(errZh, "无效的详略级别") {
			t.Fatalf("expected localized detail error, got code=%d err=%s", code, errZh)
		}
		if code, _, errZh := run(t, nil, "--ui-lang", "zh", "search", "go", "--format", "xml"); code != 2 || !strings.Contains(errZh, "不支持的输出格式") {
			t.Fatalf("expected localized format pre-validation error, got code=%d err=%s", code, errZh)
		}
		if code, _, errZh := run(t, nil, "--ui-lang", "zh", "fetch", "https://go.dev", "--strategy", "wrong"); code != 2 || !strings.Contains(errZh, "无效的提取路由策略") {
			t.Fatalf("expected localized fetch strategy error, got code=%d err=%s", code, errZh)
		}

		enCfgPath := filepath.Join(t.TempDir(), "en-config.toml")
		enCode, _, _ := execCLI(t, enCfgPath, "", nil, "--ui-lang", "en", "config", "init")
		enRaw, err := os.ReadFile(enCfgPath)
		if err != nil {
			t.Fatalf("read en-config.toml: %v", err)
		}
		if enCode != 0 {
			t.Fatalf("en config init failed: code=%d", enCode)
		}
		assertContainsAll(t, "en config template", string(enRaw),
			"# veri configuration reference template.",
			"# [providers.local]\n# enabled = true",
		)
	})
}

func TestKeySetInterruptExitsCleanly(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	repoFS := os.DirFS(filepath.Join("..", ".."))

	pr, pw := io.Pipe()
	defer func() {
		_ = pw.Close()
		_ = pr.Close()
	}()

	ctx, cancel := context.WithCancel(t.Context())
	var outBuf, errBuf bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- ExecuteContext(
			ctx,
			[]string{"--config", cfgPath, "key", "set", "deepseek"},
			repoFS,
			BuildInfo{Version: "1.0.0"},
			pr,
			&outBuf,
			&errBuf,
			func(string) string { return "" },
		)
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

func TestProviderFlagValidationAndRoleSeparation(t *testing.T) {
	tmpDir := t.TempDir()
	emptyCfg := filepath.Join(tmpDir, "empty.toml")
	if err := os.WriteFile(emptyCfg, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	repoFS := os.DirFS(filepath.Join("..", ".."))
	run := func(args ...string) (int, string, string) {
		var out, errBuf bytes.Buffer
		fullArgs := append([]string{"--config", emptyCfg, "--ui-lang", "en"}, args...)
		code := ExecuteContext(
			t.Context(),
			fullArgs,
			repoFS,
			BuildInfo{Version: "1.0.0"},
			strings.NewReader(""),
			&out,
			&errBuf,
			func(string) string { return "" },
		)
		return code, out.String(), errBuf.String()
	}

	// 未知 provider 名、显式空值、多选含拼写错误、以及角色错配均返回 rc=2 (invalid_argument)
	badCases := [][]string{
		{"search", "-p", "", "go"},
		{"search", "-p", " , ", "go"},
		{"search", "-p", "no_such_prov", "go"},
		{"search", "-p", "deepseke,exa", "go"},
		{"search", "-p", "local", "go"},
		{"search", "-p", "github", "go"},
		{"fetch", "-p", "", "https://example.com"},
		{"fetch", "-p", "no_such_prov", "https://example.com"},
		{"fetch", "-p", "zhihu", "https://example.com"},
		{"fetch", "-p", "brave,local", "https://example.com"},
	}
	for _, args := range badCases {
		code, _, errOut := run(args...)
		if code != clikit.ExitUsage || !strings.Contains(errOut, "unsupported provider") {
			t.Fatalf("args %v: got code=%d stderr=%q, want code=%d with unsupported provider", args, code, errOut, clikit.ExitUsage)
		}
	}

	// --json 模式下拼错 provider 名输出标准 invalid_argument 错误信封与 rc=2，且 search/fetch 均保持对称的默认 strategy
	code, jsonOut, _ := run("search", "-p", "no_such_prov", "--json", "go")
	var env core.Envelope
	if code != clikit.ExitUsage ||
		json.Unmarshal([]byte(jsonOut), &env) != nil ||
		env.OK ||
		env.Error == nil ||
		env.Error.Code != core.CodeInvalidArgument ||
		env.Meta.Strategy != string(core.StrategyAuto) {
		t.Fatalf("expected JSON invalid_argument search envelope on exit 2, got code=%d out=%s", code, jsonOut)
	}
	fCode, fJSONOut, _ := run("fetch", "-p", "no_such_prov", "--json", "https://example.com")
	var fEnv core.Envelope
	if fCode != clikit.ExitUsage ||
		json.Unmarshal([]byte(fJSONOut), &fEnv) != nil ||
		fEnv.OK ||
		fEnv.Error == nil ||
		fEnv.Error.Code != core.CodeInvalidArgument ||
		fEnv.Meta.Strategy != string(core.DefaultFetchStrategy) {
		t.Fatalf("expected JSON invalid_argument fetch envelope with default strategy on exit 2, got code=%d out=%s", fCode, fJSONOut)
	}

	// 合法内置名与官方别名在未配密钥时通过参数校验，交由路由返回 rc=3 (no_provider)
	for _, prov := range []string{"brave", "tencent-wsa", "TencentWSA"} {
		code, _, errOut := run("search", "-p", prov, "go")
		if code != clikit.ExitConfig {
			t.Fatalf("valid unconfigured provider %q: got code=%d stderr=%q, want code=%d (no_provider)", prov, code, errOut, clikit.ExitConfig)
		}
	}
}
