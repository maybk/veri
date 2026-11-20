package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIValidationLocalizationConflictsAndGuards(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	repoFS := os.DirFS(filepath.Join("..", ".."))

	runZH := func(args ...string) (int, string, string) {
		var outBuf, errBuf bytes.Buffer
		fullArgs := append([]string{"--config", cfgPath, "--ui-lang", "zh", "--no-color"}, args...)
		code := ExecuteContext(context.Background(), fullArgs, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &outBuf, &errBuf, func(string) string { return "" })
		return code, outBuf.String(), errBuf.String()
	}

	// 1. 全部常见非法输入在 zh-CN 下必须返回 exit 2 且不含未本地化英文原句
	cases := []struct {
		name           string
		args           []string
		forbidEnglish  string
		mustContainAny []string
	}{
		{"search_missing_query", []string{"search"}, "requires at least", []string{"缺少检索关键词"}},
		{"fetch_missing_url", []string{"fetch"}, "requires at least", []string{"缺少目标 URL"}},
		{"fetch_neg_offset", []string{"fetch", "--offset", "-5", "https://example.com"}, "must be >=", []string{"起始偏移量"}},
		{"fetch_neg_max_chars", []string{"fetch", "--max-chars", "-1", "https://example.com"}, "must be >=", []string{"字符数上限"}},
		{"search_neg_max_chars", []string{"search", "--max-chars", "-1", "golang"}, "must be >=", []string{"字符数上限"}},
		{"search_zero_count", []string{"search", "-n", "0", "golang"}, "out of valid range", []string{"超出有效范围"}},
		{"search_json_format_conflict", []string{"search", "--json", "--format", "urls", "golang"}, "conflicting", []string{"参数冲突"}},
		{"fetch_json_format_conflict", []string{"fetch", "--json", "--format", "md", "https://example.com"}, "conflicting", []string{"参数冲突"}},
		{"fetch_provider_strategy_conflict", []string{"fetch", "--strategy", "local_only", "-p", "tinyfish", "https://example.com"}, "cannot specify", []string{"不能同时指定"}},
		{"root_multiword_typo_serach", []string{"serach", "golang"}, "unknown command", []string{"search"}},
		{"mcp_typo_confg", []string{"mcp", "confg"}, "unknown subcommand", []string{"config"}},
		{"key_set_missing_provider", []string{"key", "set"}, "requires at least", []string{"缺少 Provider"}},
		{"key_unset_missing_provider", []string{"key", "unset"}, "requires at least", []string{"缺少 Provider"}},
		{"key_unset_too_many_args", []string{"key", "unset", "deepseek", "tavily"}, "accepts 1 argument", []string{"位置参数过多"}},
		{"version_too_many_args", []string{"version", "extra"}, "accepts 0 arg", []string{"位置参数过多"}},
		{"providers_too_many_args", []string{"providers", "extra"}, "accepts 0 arg", []string{"位置参数过多"}},
		{"config_show_too_many_args", []string{"config", "show", "extra"}, "accepts 0 arg", []string{"位置参数过多"}},
		{"key_list_too_many_args", []string{"key", "list", "extra"}, "accepts 0 arg", []string{"位置参数过多"}},
		{"completion_unknown_sub", []string{"completion", "bogus"}, "unknown subcommand", []string{"未知的子命令"}},
		{"completion_typo_zhs", []string{"completion", "zhs"}, "unknown subcommand", []string{"veri completion zsh"}},
		{"completion_zsh_extra", []string{"completion", "zsh", "extra"}, "accepts 0 arg", []string{"位置参数过多"}},
		{"mcp_invalid_transport", []string{"mcp", "--transport", "http"}, "unsupported MCP transport", []string{"不支持的 MCP 传输方式"}},
		{"mcp_unknown_arg", []string{"mcp", "unknown-sub"}, "unknown subcommand", []string{"未知的子命令"}},
		{"skill_dir_project_conflict", []string{"skill", "install", "--dir", filepath.Join(t.TempDir(), "demo"), "--project"}, "conflicting", []string{"命令行参数冲突"}},
		{"skill_empty_dir", []string{"skill", "install", "--dir", "   "}, "must not be empty", []string{"不能为空"}},
		{"skill_install_extra_arg", []string{"skill", "install", "extra"}, "accepts 0 arg", []string{"位置参数过多"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out, errStr := runZH(tc.args...)
			combined := errStr + out
			if code != 2 {
				t.Fatalf("expected exit 2 for %v, got %d (out=%q err=%q)", tc.args, code, out, errStr)
			}
			if tc.forbidEnglish != "" && strings.Contains(combined, tc.forbidEnglish) {
				t.Fatalf("unlocalized English phrase %q leaked in zh-CN output: %s", tc.forbidEnglish, combined)
			}
			for _, want := range tc.mustContainAny {
				if !strings.Contains(combined, want) {
					t.Fatalf("expected %q in output, got: %s", want, combined)
				}
			}
		})
	}

	// 2. key set 传入位置明文密钥必须拒绝且严禁在任何输出中回显密钥内容
	const secret = "sk-never-echo-secret-9876543210"
	code, out, errStr := runZH("key", "set", "deepseek", secret)
	if code != 2 {
		t.Fatalf("expected exit 2 for positional secret, got %d", code)
	}
	if strings.Contains(out, secret) || strings.Contains(errStr, secret) {
		t.Fatalf("positional secret echoed in output! out=%q err=%q", out, errStr)
	}
	if !strings.Contains(errStr, "不支持通过命令行位置参数传入 API Key") {
		t.Fatalf("unexpected positional secret error message: %s", errStr)
	}

	// 3. 合法短词多词查询（如 "map reduce"）不应被误判为 mcp 拼写错误
	code, _, errStr = runZH("map", "reduce")
	if strings.Contains(errStr, "mcp") {
		t.Fatalf("legitimate multi-word query 'map reduce' falsely tripped typo guard: %s", errStr)
	}
	_ = code

	// 4. key set 通过 stdin 管道写入，再通过 key unset 删除并验证幂等删除
	var outBuf, errBuf bytes.Buffer
	code = ExecuteContext(
		context.Background(),
		[]string{"--config", cfgPath, "--ui-lang", "zh", "--no-color", "key", "set", "deepseek"},
		repoFS,
		BuildInfo{Version: "1.0.0"},
		strings.NewReader("sk-test-pipe-secret-0001\n"),
		&outBuf,
		&errBuf,
		func(string) string { return "" },
	)
	if code != 0 || !strings.Contains(outBuf.String(), "deepseek") {
		t.Fatalf("piped key set failed: code=%d out=%q err=%q", code, outBuf.String(), errBuf.String())
	}
	if code, out, errStr := runZH("key", "unset", "deepseek"); code != 0 || !strings.Contains(out, "deepseek") {
		t.Fatalf("key unset failed: code=%d out=%q err=%q", code, out, errStr)
	}
	if code, out, errStr := runZH("key", "unset", "deepseek"); code != 0 || out == "" {
		t.Fatalf("idempotent key unset failed: code=%d out=%q err=%q", code, out, errStr)
	}
	if code, _, _ := runZH("key", "unset", "no-such-provider"); code != 2 {
		t.Fatalf("unsupported provider in key unset should exit 2, got %d", code)
	}

	// 5. version --json 输出合法 JSON 且包含版本号
	if code, out, errStr := runZH("version", "--json"); code != 0 || !strings.Contains(out, `"version": "1.0.0"`) {
		t.Fatalf("version --json failed: code=%d out=%q err=%q", code, out, errStr)
	}

	// 6. 配置文件未知键、TOML 语法错、目录路径与非法 VERI_TIMEOUT 均在 zh-CN 下完全本地化
	badKeyCfg := filepath.Join(t.TempDir(), "badkey.toml")
	if err := os.WriteFile(badKeyCfg, []byte("[search]\nbogus_field = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var o1, e1 bytes.Buffer
	if c := ExecuteContext(context.Background(), []string{"--config", badKeyCfg, "--ui-lang", "zh", "--no-color", "config", "show"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &o1, &e1, func(string) string { return "" }); c != 2 || !strings.Contains(e1.String(), "包含未知配置项") {
		t.Fatalf("expected localized unknown config keys error, code=%d err=%q", c, e1.String())
	}

	badTOMLCfg := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(badTOMLCfg, []byte("[search\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var o2, e2 bytes.Buffer
	if c := ExecuteContext(context.Background(), []string{"--config", badTOMLCfg, "--ui-lang", "zh", "--no-color", "config", "show"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &o2, &e2, func(string) string { return "" }); c != 2 || !strings.Contains(e2.String(), "语法错误") {
		t.Fatalf("expected localized invalid TOML error, code=%d err=%q", c, e2.String())
	}

	dirCfg := t.TempDir()
	var o3, e3 bytes.Buffer
	if c := ExecuteContext(context.Background(), []string{"--config", dirCfg, "--ui-lang", "zh", "--no-color", "config", "show"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &o3, &e3, func(string) string { return "" }); c != 2 || !strings.Contains(e3.String(), "是目录") {
		t.Fatalf("expected localized config is dir error, code=%d err=%q", c, e3.String())
	}

	var o4, e4 bytes.Buffer
	envBadTimeout := func(k string) string {
		if k == "VERI_TIMEOUT" {
			return "not-a-duration"
		}
		return ""
	}
	if c := ExecuteContext(context.Background(), []string{"--config", cfgPath, "--ui-lang", "zh", "--no-color", "config", "show"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &o4, &e4, envBadTimeout); c != 2 || !strings.Contains(e4.String(), "VERI_TIMEOUT") || strings.Contains(e4.String(), "invalid VERI_TIMEOUT") {
		t.Fatalf("expected localized env timeout error, code=%d err=%q", c, e4.String())
	}
}
