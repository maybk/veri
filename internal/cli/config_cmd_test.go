package cli

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCLIConfigSetGetUnsetOverridesAndCompletion(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	repoFS := fstest.MapFS{"skills/veri/SKILL.md": &fstest.MapFile{Data: []byte("# veri\n")}}

	run := func(env map[string]string, args ...string) (int, string, string) {
		t.Helper()
		var outBuf, errBuf bytes.Buffer
		full := append([]string{"--config", cfgPath, "--no-color"}, args...)
		code := ExecuteContext(context.Background(), full, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &outBuf, &errBuf, func(k string) string {
			return env[k]
		})
		return code, outBuf.String(), errBuf.String()
	}

	// 1. 零配置文件状态下 get 返回内置默认，show --overrides 提示无覆盖
	code, getDef, errStr := run(nil, "config", "get", "providers.zhihu.scope")
	if code != 0 || strings.TrimSpace(getDef) != "global" {
		t.Fatalf("config get default failed: code=%d out=%q err=%q", code, getDef, errStr)
	}
	code, ovNoneZh, _ := run(nil, "--ui-lang", "zh", "config", "show", "--overrides")
	if code != 0 || !strings.Contains(ovNoneZh, "尚未设置任何自定义项") {
		t.Fatalf("expected zh no_overrides note, code=%d out=%q", code, ovNoneZh)
	}
	code, ovNoneJSON, _ := run(nil, "config", "show", "--overrides", "--json")
	if code != 0 || strings.TrimSpace(ovNoneJSON) != "{}" {
		t.Fatalf("expected empty JSON object for no overrides, code=%d out=%q", code, ovNoneJSON)
	}

	// 2. config set 生成最小 2 行稀疏 config.toml，并用 get --json 与 show --overrides 验证
	code, setOut, errStr := run(nil, "--ui-lang", "zh", "config", "set", "providers.zhihu.scope", "zhihu")
	if code != 0 || !strings.Contains(setOut, "providers.zhihu.scope = zhihu") || errStr != "" {
		t.Fatalf("config set failed: code=%d out=%q err=%q", code, setOut, errStr)
	}
	rawBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read sparse config.toml: %v", err)
	}
	if got, want := string(rawBytes), "[providers.zhihu]\nscope = \"zhihu\"\n"; got != want {
		t.Fatalf("sparse config.toml =\n%q\nwant:\n%q", got, want)
	}

	code, getJSON, _ := run(nil, "config", "get", "providers.zhihu.scope", "--json")
	if code != 0 {
		t.Fatalf("config get --json failed: code=%d", code)
	}
	var gotVal struct {
		Key    string `json:"key"`
		Value  string `json:"value"`
		Source string `json:"source"`
	}
	if err := json.Unmarshal([]byte(getJSON), &gotVal); err != nil || gotVal.Key != "providers.zhihu.scope" || gotVal.Value != "zhihu" || gotVal.Source != "config" {
		t.Fatalf("unexpected get --json payload: %s (err=%v)", getJSON, err)
	}

	code, ovTOML, _ := run(nil, "config", "show", "--overrides")
	if code != 0 || strings.TrimSpace(ovTOML) != "[providers.zhihu]\nscope = \"zhihu\"" {
		t.Fatalf("unexpected show --overrides TOML: code=%d out=%q", code, ovTOML)
	}

	// 3. 环境变量遮蔽提醒与 --quiet 静默抑制
	env := map[string]string{"VERI_PROVIDERS": "deepseek"}
	code, _, shadowErr := run(env, "--ui-lang", "en", "config", "set", "search.providers", "zhihu,keenable")
	if code != 0 || !strings.Contains(shadowErr, "VERI_PROVIDERS") {
		t.Fatalf("expected env shadow warning on stderr, code=%d err=%q", code, shadowErr)
	}
	code, _, quietErr := run(env, "--quiet", "config", "set", "search.providers", "zhihu,keenable")
	if code != 0 || quietErr != "" {
		t.Fatalf("expected --quiet to suppress env shadow warning, code=%d err=%q", code, quietErr)
	}
	code, getEnvJSON, _ := run(env, "config", "get", "search.providers", "--json")
	if code != 0 || !strings.Contains(getEnvJSON, `"source": "env"`) || !strings.Contains(getEnvJSON, `"env_var": "VERI_PROVIDERS"`) {
		t.Fatalf("expected env metadata in get --json, code=%d out=%s", code, getEnvJSON)
	}

	// 4. 明文密钥拦截、拼写纠错、缺参报错
	code, _, secretErr := run(nil, "--ui-lang", "zh", "config", "set", "providers.deepseek.api_key", "sk-neverwritethis123456")
	if code != 2 || !strings.Contains(secretErr, "veri key set deepseek") || strings.Contains(secretErr, "neverwritethis") {
		t.Fatalf("expected plaintext secret rejection with hint, code=%d err=%q", code, secretErr)
	}
	code, _, suggestErr := run(nil, "--ui-lang", "en", "config", "set", "zhihu.scope", "zhihu")
	if code != 2 || !strings.Contains(suggestErr, "providers.zhihu.scope") {
		t.Fatalf("expected key suggestion for zhihu.scope, code=%d err=%q", code, suggestErr)
	}
	code, _, unknownErr := run(nil, "--ui-lang", "zh", "config", "set", "completely_unknown_key", "1")
	if code != 2 || !strings.Contains(unknownErr, "veri config show") || strings.Contains(unknownErr, "config.toml 中的字段拼写") {
		t.Fatalf("unexpected unknown key hint, code=%d err=%q", code, unknownErr)
	}
	for _, badArgs := range [][]string{
		{"config", "set"},
		{"config", "set", "search.count"},
		{"config", "get"},
		{"config", "unset"},
	} {
		if c, _, _ := run(nil, badArgs...); c != 2 {
			t.Fatalf("expected exit 2 for %v, got %d", badArgs, c)
		}
	}

	// 5. unset 移除覆盖项并在文件为空时自动清理
	code, _, _ = run(nil, "config", "unset", "search.providers")
	if code != 0 {
		t.Fatalf("unset search.providers failed: code=%d", code)
	}
	code, rmOut, _ := run(nil, "--ui-lang", "zh", "config", "unset", "providers.zhihu.scope")
	if code != 0 || !strings.Contains(rmOut, "删除空配置文件") {
		t.Fatalf("expected empty file cleanup notice on last unset, code=%d out=%q", code, rmOut)
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("expected sparse config.toml removed after last unset, err=%v", err)
	}
	code, noopOut, _ := run(nil, "--ui-lang", "en", "config", "unset", "providers.zhihu.scope")
	if code != 0 || !strings.Contains(noopOut, "is not set in config.toml") {
		t.Fatalf("expected idempotent unset output, code=%d out=%q", code, noopOut)
	}

	// 6. Cobra Shell 自动补全校验（配置键与枚举值）
	code, compKeys, _ := run(nil, "__complete", "config", "set", "providers.zhihu.")
	if code != 0 || !strings.Contains(compKeys, "providers.zhihu.scope") || !strings.Contains(compKeys, "providers.zhihu.search_db") {
		t.Fatalf("expected config set key completion, code=%d out=%q", code, compKeys)
	}
	code, compVals, _ := run(nil, "__complete", "config", "set", "providers.zhihu.scope", "")
	if code != 0 || !strings.Contains(compVals, "zhihu") || !strings.Contains(compVals, "global") {
		t.Fatalf("expected config set enum value completion, code=%d out=%q", code, compVals)
	}
	code, compGet, _ := run(nil, "__complete", "config", "get", "search.str")
	if code != 0 || !strings.Contains(compGet, "search.strategy") || !strings.Contains(compGet, "search.strict_filters") {
		t.Fatalf("expected config get key completion, code=%d out=%q", code, compGet)
	}
}
