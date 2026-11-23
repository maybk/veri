package config

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"

	"veri/clikit/credentials"
	"veri/internal/core"
	"veri/internal/spec"
)

func TestSetGetUnsetAllValueTypes(t *testing.T) {
	noEnv := func(string) string { return "" }
	cfgPath := filepath.Join(t.TempDir(), "config.toml")

	cases := []struct {
		key       string
		input     string
		wantFmt   string
		checkLoad func(*testing.T, *Loaded)
	}{
		{
			key: "providers.zhihu.scope", input: "ZHIHU", wantFmt: "zhihu",
			checkLoad: func(t *testing.T, l *Loaded) {
				if l.Config.Providers.Zhihu.Scope != spec.ZhihuScopeZhihu {
					t.Fatalf("zhihu.scope = %q, want zhihu", l.Config.Providers.Zhihu.Scope)
				}
			},
		},
		{
			key: "search.strict_filters", input: "true", wantFmt: "true",
			checkLoad: func(t *testing.T, l *Loaded) {
				if !l.Config.Search.StrictFilters {
					t.Fatal("expected search.strict_filters = true")
				}
			},
		},
		{
			key: "search.count", input: "12", wantFmt: "12",
			checkLoad: func(t *testing.T, l *Loaded) {
				if l.Config.Search.Count != 12 {
					t.Fatalf("search.count = %d, want 12", l.Config.Search.Count)
				}
			},
		},
		{
			key: "providers.deepseek.weight", input: "1.5", wantFmt: "1.5",
			checkLoad: func(t *testing.T, l *Loaded) {
				if l.Config.Providers.DeepSeek.Weight != 1.5 {
					t.Fatalf("deepseek.weight = %v, want 1.5", l.Config.Providers.DeepSeek.Weight)
				}
			},
		},
		{
			key: "search.timeout", input: "30s", wantFmt: "30s",
			checkLoad: func(t *testing.T, l *Loaded) {
				if l.Config.Search.Timeout.Duration != 30*time.Second {
					t.Fatalf("search.timeout = %v, want 30s", l.Config.Search.Timeout.Duration)
				}
			},
		},
		{
			key: "fetch.max_bytes", input: "8MB", wantFmt: "8MB",
			checkLoad: func(t *testing.T, l *Loaded) {
				if l.Config.Fetch.MaxBytes != 8*1024*1024 {
					t.Fatalf("fetch.max_bytes = %d, want 8MB", l.Config.Fetch.MaxBytes)
				}
			},
		},
		{
			key: "search.providers", input: "deepseek, tencent-wsa, zhihu", wantFmt: "deepseek,tencent_wsa,zhihu",
			checkLoad: func(t *testing.T, l *Loaded) {
				want := []string{"deepseek", "tencent_wsa", "zhihu"}
				if !slices.Equal(l.Config.Search.Providers, want) {
					t.Fatalf("search.providers = %v, want %v", l.Config.Search.Providers, want)
				}
			},
		},
		{
			key: "search.fusion_providers", input: `["keenable", "exa"]`, wantFmt: "keenable,exa",
			checkLoad: func(t *testing.T, l *Loaded) {
				want := []string{"keenable", "exa"}
				if !slices.Equal(l.Config.Search.FusionProviders, want) {
					t.Fatalf("search.fusion_providers = %v, want %v", l.Config.Search.FusionProviders, want)
				}
			},
		},
	}

	for _, tc := range cases {
		res, err := SetValue(cfgPath, tc.key, tc.input, noEnv)
		if err != nil {
			t.Fatalf("SetValue(%s, %q): %v", tc.key, tc.input, err)
		}
		if res.Key != tc.key || res.Formatted != tc.wantFmt {
			t.Fatalf("SetValue(%s) = %+v, want formatted=%q", tc.key, res, tc.wantFmt)
		}
		loaded, err := LoadWithEnv(cfgPath, noEnv)
		if err != nil {
			t.Fatalf("LoadWithEnv after %s: %v", tc.key, err)
		}
		tc.checkLoad(t, loaded)
		got, err := GetValue(loaded, tc.key)
		if err != nil {
			t.Fatalf("GetValue(%s): %v", tc.key, err)
		}
		if got.Formatted != tc.wantFmt || got.Source != "config" {
			t.Fatalf("GetValue(%s) = %+v, want formatted=%q source=config", tc.key, got, tc.wantFmt)
		}
	}

	// 逐个 unset 后，最后一个键移除时应自动清理空配置文件
	for i, tc := range cases {
		uRes, err := UnsetValue(cfgPath, tc.key)
		if err != nil {
			t.Fatalf("UnsetValue(%s): %v", tc.key, err)
		}
		if !uRes.Changed {
			t.Fatalf("UnsetValue(%s) expected Changed=true", tc.key)
		}
		wantRemoved := i == len(cases)-1
		if uRes.RemovedFile != wantRemoved {
			t.Fatalf("UnsetValue(%s) RemovedFile=%v, want %v", tc.key, uRes.RemovedFile, wantRemoved)
		}
	}
	if _, err := os.Stat(cfgPath); !os.IsNotExist(err) {
		t.Fatalf("expected empty config.toml to be removed, stat err=%v", err)
	}
}

func TestStringSliceEmptyVsUnsetSemantics(t *testing.T) {
	noEnv := func(string) string { return "" }
	cfgPath := filepath.Join(t.TempDir(), "config.toml")

	// 1. 设 "" 或 "[]" 应显式写入 []，从而关闭默认非中文分链
	for _, rawEmpty := range []string{"", "[]"} {
		res, err := SetValue(cfgPath, "search.providers_global", rawEmpty, noEnv)
		if err != nil {
			t.Fatalf("SetValue empty (%q): %v", rawEmpty, err)
		}
		if res.Formatted != "[]" {
			t.Fatalf("expected Formatted=\"[]\", got %q", res.Formatted)
		}
		loaded, err := LoadWithEnv(cfgPath, noEnv)
		if err != nil {
			t.Fatalf("LoadWithEnv: %v", err)
		}
		if !loaded.DefinedKeys["search.providers_global"] || len(loaded.Config.Search.ProvidersGlobal) != 0 {
			t.Fatalf("expected explicit empty providers_global, defined=%v val=%v",
				loaded.DefinedKeys["search.providers_global"], loaded.Config.Search.ProvidersGlobal)
		}
		got, err := GetValue(loaded, "search.providers_global")
		if err != nil || got.Source != "config" || got.Formatted != "[]" {
			t.Fatalf("GetValue empty slice = %+v err=%v", got, err)
		}
	}

	// 2. unset 恢复跟随内置默认非中文分链，且再次 unset 幂等
	uRes, err := UnsetValue(cfgPath, "search.providers_global")
	if err != nil || !uRes.Changed || !uRes.RemovedFile {
		t.Fatalf("expected unset to remove sparse file, got %+v err=%v", uRes, err)
	}
	loaded, err := LoadWithEnv(cfgPath, noEnv)
	if err != nil {
		t.Fatalf("LoadWithEnv after unset: %v", err)
	}
	if !slices.Equal(loaded.Config.Search.ProvidersGlobal, spec.DefaultSearchProvidersGlobal()) {
		t.Fatalf("expected default providers_global restored, got %v", loaded.Config.Search.ProvidersGlobal)
	}
	got, err := GetValue(loaded, "search.providers_global")
	if err != nil || got.Source != "default" {
		t.Fatalf("expected default source after unset, got %+v err=%v", got, err)
	}
	uRes2, err := UnsetValue(cfgPath, "search.providers_global")
	if err != nil || uRes2.Changed {
		t.Fatalf("expected idempotent unset on missing file, got %+v err=%v", uRes2, err)
	}
}

func TestPreserveCommentsMultilineCRLFAndSectionCleanup(t *testing.T) {
	noEnv := func(string) string { return "" }
	dir := t.TempDir()

	// 1. UTF-8 BOM + CRLF + 缩进保留 + 顶部注释 + 行尾注释 + 多行数组原地更新
	crlfPath := filepath.Join(dir, "crlf.toml")
	orig := "\uFEFF# custom header comment\r\n" +
		"[search]\r\n" +
		"  providers = [\r\n" +
		"    \"deepseek\",\r\n" +
		"    \"zhihu\",\r\n" +
		"  ]\r\n" +
		"  count = 6 # my custom count\r\n"
	if err := os.WriteFile(crlfPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetValue(crlfPath, "search.count", "10", noEnv); err != nil {
		t.Fatalf("SetValue count: %v", err)
	}
	if _, err := SetValue(crlfPath, "search.providers", "keenable,zhihu", noEnv); err != nil {
		t.Fatalf("SetValue providers: %v", err)
	}
	rawBytes, err := os.ReadFile(crlfPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(rawBytes)
	if strings.HasPrefix(text, "\uFEFF") || !strings.Contains(text, "\r\n") {
		t.Fatalf("expected BOM stripped and CRLF preserved, got %q", text)
	}
	if !strings.Contains(text, "# custom header comment") || !strings.Contains(text, "  count = 10 # my custom count") {
		t.Fatalf("expected header, indent, and trailing comments preserved:\n%s", text)
	}
	if strings.Contains(text, `"deepseek"`) || !strings.Contains(text, `  providers = ["keenable", "zhihu"]`) {
		t.Fatalf("expected multiline array cleanly replaced with indent preserved:\n%s", text)
	}

	// 2. 全注释参考模板上 set -> unset 保留模板本身，不删除文件
	tplPath := filepath.Join(dir, "template.toml")
	if err := WriteTemplateForLocale(tplPath, false, "en-US"); err != nil {
		t.Fatal(err)
	}
	if _, err := SetValue(tplPath, "providers.zhihu.scope", "zhihu", noEnv); err != nil {
		t.Fatalf("SetValue on commented template: %v", err)
	}
	loadedTpl, err := LoadWithEnv(tplPath, noEnv)
	if err != nil || !loadedTpl.DefinedKeys["providers.zhihu.scope"] || loadedTpl.Config.Providers.Zhihu.Scope != spec.ZhihuScopeZhihu {
		t.Fatalf("unexpected template override load: defined=%v scope=%q err=%v",
			loadedTpl.DefinedKeys, loadedTpl.Config.Providers.Zhihu.Scope, err)
	}
	uRes, err := UnsetValue(tplPath, "providers.zhihu.scope")
	if err != nil || !uRes.Changed || uRes.RemovedFile {
		t.Fatalf("expected unset on commented template to preserve file, got %+v err=%v", uRes, err)
	}
	afterTpl, err := os.ReadFile(tplPath)
	if err != nil || !strings.Contains(string(afterTpl), "# [providers.zhihu]") || strings.Contains(string(afterTpl), "\n[providers.zhihu]\n") {
		t.Fatalf("expected commented template intact after unset:\n%s", string(afterTpl))
	}

	// 3. 段内追加新键不拆散下一段前置注释；移除本段最后一项时清理空表头且保留下一段前置注释
	secPath := filepath.Join(dir, "sections.toml")
	secOrig := "[search]\ncount = 8\n\n# zhihu section comment\n[providers.zhihu]\nscope = \"zhihu\"\n"
	if err := os.WriteFile(secPath, []byte(secOrig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetValue(secPath, "search.detail", "content", noEnv); err != nil {
		t.Fatalf("SetValue search.detail: %v", err)
	}
	secAfterSet, _ := os.ReadFile(secPath)
	wantAfterSet := "[search]\ncount = 8\ndetail = \"content\"\n\n# zhihu section comment\n[providers.zhihu]\nscope = \"zhihu\"\n"
	if string(secAfterSet) != wantAfterSet {
		t.Fatalf("section insert displaced next header comment:\ngot:\n%s\nwant:\n%s", string(secAfterSet), wantAfterSet)
	}
	if _, err := UnsetValue(secPath, "search.count"); err != nil {
		t.Fatal(err)
	}
	if _, err := UnsetValue(secPath, "search.detail"); err != nil {
		t.Fatal(err)
	}
	secAfterUnset, _ := os.ReadFile(secPath)
	wantAfterUnset := "# zhihu section comment\n[providers.zhihu]\nscope = \"zhihu\"\n"
	if string(secAfterUnset) != wantAfterUnset {
		t.Fatalf("empty section prune broke next header comment:\ngot:\n%s\nwant:\n%s", string(secAfterUnset), wantAfterUnset)
	}
}

func TestSecretGuardRedactionAndOverridesIsolation(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	noEnv := func(string) string { return "" }

	// 1. 严格拒绝明文 api_key / token，放行 env:VAR 并规范化前缀
	for _, key := range []string{"providers.deepseek.api_key", "providers.github.token"} {
		_, err := SetValue(cfgPath, key, "sk-plaintext-secret-1234567890", noEnv)
		se := core.AsError(err)
		if se == nil || se.Code != core.CodeInvalidArgument || se.MsgKey != "err.config_secret_plaintext" {
			t.Fatalf("expected plaintext guard for %s, got %+v", key, err)
		}
		if strings.Contains(err.Error(), "plaintext-secret") {
			t.Fatalf("error message leaked plaintext secret: %v", err)
		}
	}
	resEnv, err := SetValue(cfgPath, "providers.deepseek.api_key", "ENV:CUSTOM_DS_KEY", noEnv)
	if err != nil || resEnv.Formatted != "env:CUSTOM_DS_KEY" {
		t.Fatalf("expected env:VAR reference allowed & normalized, got %+v err=%v", resEnv, err)
	}

	// 2. 代理密码脱敏、Duration/ByteSize 覆盖导出、环境变量遮蔽与 vault 隔离
	proxyRes, err := SetValue(cfgPath, "network.proxy", "http://alice:TopSecretProxy99@127.0.0.1:7890", noEnv)
	if err != nil || strings.Contains(proxyRes.Formatted, "TopSecretProxy99") {
		t.Fatalf("SetValue proxy leaked password or failed: %+v err=%v", proxyRes, err)
	}
	if _, err := SetValue(cfgPath, "search.timeout", "25s", noEnv); err != nil {
		t.Fatalf("SetValue search.timeout: %v", err)
	}
	if _, err := SetValue(cfgPath, "fetch.max_bytes", "6mb", noEnv); err != nil {
		t.Fatalf("SetValue fetch.max_bytes: %v", err)
	}
	shadowEnv := func(k string) string {
		if k == spec.EnvProviders {
			return "deepseek"
		}
		return ""
	}
	shadowRes, err := SetValue(cfgPath, "search.providers", "zhihu,keenable", shadowEnv)
	if err != nil || shadowRes.ShadowedByEnv != spec.EnvProviders {
		t.Fatalf("expected ShadowedByEnv=%s, got %+v err=%v", spec.EnvProviders, shadowRes, err)
	}

	// 写入 secrets.json 验证 GetValue 返回 vault，同时 OverridesMap 仍保留 config.toml 真实文件值
	if err := credentials.NewVault(dir).Set("deepseek", "sk-vault-secret-4321"); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadWithEnv(cfgPath, shadowEnv)
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	gotProv, err := GetValue(loaded, "search.providers")
	if err != nil || gotProv.Source != "env" || gotProv.EnvVar != spec.EnvProviders || gotProv.Formatted != "deepseek" {
		t.Fatalf("GetValue search.providers under env = %+v err=%v", gotProv, err)
	}
	gotKey, err := GetValue(loaded, "providers.deepseek.api_key")
	if err != nil || gotKey.Source != "vault" || strings.Contains(gotKey.Formatted, "vault-secret") {
		t.Fatalf("GetValue deepseek.api_key under vault = %+v err=%v", gotKey, err)
	}
	gotProxy, err := GetValue(loaded, "network.proxy")
	if err != nil || strings.Contains(gotProxy.Formatted, "TopSecretProxy99") {
		t.Fatalf("GetValue proxy leaked password: %+v err=%v", gotProxy, err)
	}

	// OverridesMap / FormatOverridesTOML 必须精准反映 config.toml 文件内容且零明文泄露
	ovTOML, ovCount := FormatOverridesTOML(loaded)
	if ovCount != 5 || strings.Contains(ovTOML, "TopSecretProxy99") || strings.Contains(ovTOML, "vault-secret") {
		t.Fatalf("unexpected overrides TOML (count=%d):\n%s", ovCount, ovTOML)
	}
	if !strings.Contains(ovTOML, `providers = ["zhihu", "keenable"]`) ||
		!strings.Contains(ovTOML, `api_key = "env:CUSTOM_DS_KEY"`) ||
		!strings.Contains(ovTOML, `timeout = "25s"`) ||
		!strings.Contains(ovTOML, `max_bytes = "6MB"`) {
		t.Fatalf("overrides TOML should reflect exact file values:\n%s", ovTOML)
	}
	ovMap := OverridesMap(loaded)
	if _, ok := ovMap["network"]; !ok {
		t.Fatalf("expected network section in OverridesMap, got %+v", ovMap)
	}

	// 3. 混合凭据来源（env:MY_KEY + 明文）不得把复合来源串误填为 env_var
	mixedPath := filepath.Join(t.TempDir(), "mixed.toml")
	if err := os.WriteFile(mixedPath, []byte("[providers.deepseek]\napi_key = \"env:MY_DS_KEY,sk-plainsecret123456\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mixedLoaded, err := LoadWithEnv(mixedPath, func(k string) string {
		if k == "MY_DS_KEY" {
			return "sk-envsecret999999"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadWithEnv mixed: %v", err)
	}
	mixedVal, err := GetValue(mixedLoaded, "providers.deepseek.api_key")
	if err != nil || mixedVal.Source != "config" || mixedVal.EnvVar != "" || strings.Contains(mixedVal.Formatted, "plainsecret") {
		t.Fatalf("expected mixed credential source=config with empty env_var and masked value, got %+v err=%v", mixedVal, err)
	}
}

func TestSuggestKeyAndPreWriteValidationSafety(t *testing.T) {
	noEnv := func(string) string { return "" }

	for in, want := range map[string]string{
		"zhihu.scope":         "providers.zhihu.scope",
		"tencent-wsa.edition": "providers.tencent_wsa.edition",
		"search.cout":         "search.count",
		"zhihu.scop":          "providers.zhihu.scope",
		"completely_x":        "",
	} {
		if got := SuggestKey(in); got != want {
			t.Errorf("SuggestKey(%q) = %q, want %q", in, got, want)
		}
	}

	// 非法值不得写坏既有配置文件
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "safe.toml")
	orig := "[search]\ncount = 6\n"
	if err := os.WriteFile(cfgPath, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range [][2]string{
		{"providers.zhihu.scope", "not_a_scope"},
		{"search.count", "999"},
		{"search.strict_filters", "yes"},
		{"search.timeout", "-5s"},
	} {
		if _, err := SetValue(cfgPath, tc[0], tc[1], noEnv); err == nil {
			t.Fatalf("expected SetValue(%s, %q) to fail validation", tc[0], tc[1])
		}
	}
	after, _ := os.ReadFile(cfgPath)
	if string(after) != orig {
		t.Fatalf("invalid SetValue modified existing file:\ngot:  %q\nwant: %q", string(after), orig)
	}

	// 行内表语法冲突必须触发安全拒绝且原文件保持字节级不变
	inlinePath := filepath.Join(dir, "inline.toml")
	inlineOrig := "[providers]\nzhihu = { scope = \"global\" }\n"
	if err := os.WriteFile(inlinePath, []byte(inlineOrig), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetValue(inlinePath, "providers.zhihu.scope", "zhihu", noEnv); err == nil {
		t.Fatal("expected inline table conflict to fail safely")
	}
	if _, err := UnsetValue(inlinePath, "providers.zhihu.scope"); core.AsError(err).MsgKey != "err.config_edit_manual" {
		t.Fatalf("expected manual edit error for inline table unset, got %v", err)
	}
	afterInline, _ := os.ReadFile(inlinePath)
	if string(afterInline) != inlineOrig {
		t.Fatalf("inline table rejection altered file:\ngot:  %q\nwant: %q", string(afterInline), inlineOrig)
	}

	// 误把明文密钥填到非 secret 字段值中时，报错绝不得原样回显明文。
	accidentalKey := "sk-accidental-secret-value-987654"
	for _, key := range []string{"search.count", "providers.zhihu.scope", "ui.lang", "search.providers"} {
		_, err := SetValue(cfgPath, key, accidentalKey, noEnv)
		if err == nil || strings.Contains(err.Error(), accidentalKey) {
			t.Fatalf("SetValue(%s) leaked accidental secret in error: %v", key, err)
		}
	}

	// UnsetValue 遇到含未知键的配置必须报错拦截，不能静默返回未设置；
	// 同时支持通过 SetValue / UnsetValue 修复单个越界值。
	unknownPath := filepath.Join(dir, "unknown.toml")
	if err := os.WriteFile(unknownPath, []byte("[providers.zhihu]\nscop = \"zhihu\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UnsetValue(unknownPath, "providers.zhihu.scope"); core.AsError(err) == nil || core.AsError(err).MsgKey != "err.config_unknown_keys" {
		t.Fatalf("expected UnsetValue to reject unknown keys in file, got %v", err)
	}

	brokenPath := filepath.Join(dir, "broken_val.toml")
	if err := os.WriteFile(brokenPath, []byte("[search]\ncount = 999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetValue(brokenPath, "search.count", "8", noEnv); err != nil {
		t.Fatalf("expected SetValue to repair out-of-range value, got %v", err)
	}
	if err := os.WriteFile(brokenPath, []byte("[search]\ncount = 999\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if uRes, err := UnsetValue(brokenPath, "search.count"); err != nil || !uRes.Changed || !uRes.RemovedFile {
		t.Fatalf("expected UnsetValue to repair out-of-range value, got %+v err=%v", uRes, err)
	}
}

func TestBilingualCommentedTemplatesZeroDrift(t *testing.T) {
	var uncommentedCfgs []Config
	var keyLists [][]string
	for _, loc := range []string{"zh-CN", "en-US"} {
		tplPath := filepath.Join(t.TempDir(), "config.toml")
		if err := WriteTemplateForLocale(tplPath, false, loc); err != nil {
			t.Fatalf("WriteTemplateForLocale(%s): %v", loc, err)
		}
		tplLoaded, err := LoadWithEnv(tplPath, func(string) string { return "" })
		if err != nil {
			t.Fatalf("generated %s template failed strict LoadWithEnv: %v", loc, err)
		}
		if len(tplLoaded.DefinedKeys) != 0 || tplLoaded.SearchProvidersExplicit || tplLoaded.FetchProvidersExplicit {
			t.Fatalf("%s template must define 0 active keys, got defined=%v searchExp=%v fetchExp=%v",
				loc, tplLoaded.DefinedKeys, tplLoaded.SearchProvidersExplicit, tplLoaded.FetchProvidersExplicit)
		}
		if !reflect.DeepEqual(tplLoaded.Config, Default()) {
			t.Fatalf("%s template drifted from Default() config", loc)
		}

		uncommented := uncommentTemplateDefaults(TemplateForLocale(loc))
		rawCfg := Default()
		meta, decErr := toml.Decode(uncommented, &rawCfg)
		if decErr != nil {
			t.Fatalf("decode uncommented %s template: %v\n%s", loc, decErr, uncommented)
		}
		if len(meta.Undecoded()) > 0 {
			t.Fatalf("uncommented %s template has unknown keys: %v", loc, meta.Undecoded())
		}
		if !reflect.DeepEqual(rawCfg, Default()) {
			t.Fatalf("uncommented %s template drifted from Default():\ngot:  %+v\nwant: %+v", loc, rawCfg, Default())
		}
		uncommentedCfgs = append(uncommentedCfgs, rawCfg)

		var keys []string
		for _, k := range meta.Keys() {
			keys = append(keys, k.String())
		}
		slices.Sort(keys)
		if len(keys) == 0 {
			t.Fatalf("expected uncommented %s template to define reference keys", loc)
		}
		keyLists = append(keyLists, keys)
	}
	if !reflect.DeepEqual(uncommentedCfgs[0], uncommentedCfgs[1]) || !slices.Equal(keyLists[0], keyLists[1]) {
		t.Fatalf("zh-CN.toml and en-US.toml keys/values drifted:\nzh=%v\nen=%v", keyLists[0], keyLists[1])
	}
}

func uncommentTemplateDefaults(raw string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(raw, "\n") {
		if strings.HasPrefix(line, "##") {
			continue
		}
		if rest, ok := strings.CutPrefix(line, "# "); ok {
			trimmed := strings.TrimSpace(rest)
			if _, isSec := parseSectionHeader(trimmed); isSec {
				b.WriteString(rest + "\n")
				continue
			}
			if eq := strings.IndexByte(trimmed, '='); eq > 0 && isBareTOMLKey(strings.TrimSpace(trimmed[:eq])) {
				b.WriteString(rest + "\n")
			}
		}
	}
	return b.String()
}
