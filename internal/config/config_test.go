package config

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/BurntSushi/toml"

	"veri/clikit/credentials"
	"veri/internal/spec"
)

func TestResolvePathUnifiedHomeDotConfig(t *testing.T) {
	p, err := ResolvePath("", func(string) string { return "" })
	if err != nil {
		t.Fatalf("ResolvePath default: %v", err)
	}
	wantSuffix := filepath.Join(".config", "veri", "config.toml")
	if !strings.HasSuffix(p, wantSuffix) {
		t.Fatalf("ResolvePath = %q, want suffix %q across all platforms", p, wantSuffix)
	}
}

func TestWriteTemplateEncryptedVaultAndMCPEnvPrecedence(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, ".config", "veri")
	cfgPath := filepath.Join(cfgDir, "config.toml")

	if err := WriteTemplate(cfgPath, false); err != nil {
		t.Fatalf("WriteTemplate: %v", err)
	}
	if err := WriteTemplate(cfgPath, false); err == nil {
		t.Fatal("expected WriteTemplate without force to fail on existing file")
	}

	// 在 ~/.config/veri/secrets.json 中写入加密密钥（模拟 `veri key set`）
	vault := credentials.NewVault(cfgDir)
	if err := vault.Set("deepseek", "sk-vault-key-1111"); err != nil {
		t.Fatalf("vault.Set deepseek: %v", err)
	}
	if err := vault.Set("exa", "exa-vault-key-2222"); err != nil {
		t.Fatalf("vault.Set exa: %v", err)
	}

	// 1. 未传环境变量（如 MCP 未单独配 env）时自动使用 ~/.config/veri/secrets.json 中的加密密钥
	loadedVault, err := LoadWithEnv(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv from vault: %v", err)
	}
	if got := loadedVault.Credentials["deepseek"]; len(got.Keys) != 1 || got.Keys[0] != "sk-vault-key-1111" || got.Source != spec.SecretsFileName {
		t.Fatalf("expected deepseek from secrets.json, got %+v", got)
	}
	if got := loadedVault.Credentials["exa"]; len(got.Keys) != 1 || got.Keys[0] != "exa-vault-key-2222" || got.Source != spec.SecretsFileName {
		t.Fatalf("expected exa from secrets.json, got %+v", got)
	}

	// 2. MCP 或 Shell 显式传入环境变量时，环境变量优先于 secrets.json 与 config.toml 明文
	env := map[string]string{
		"DEEPSEEK_API_KEY":        "sk-mcp-override-9999",
		"BRAVE_SEARCH_API_KEY":    "BSA-alias-key-9999",
		"TENCENTCLOUD_WSA_APIKEY": "wsa-alias-8888",
	}
	loadedEnv, err := LoadWithEnv(cfgPath, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("LoadWithEnv with MCP env: %v", err)
	}
	if got := loadedEnv.Credentials["deepseek"]; len(got.Keys) != 1 || got.Keys[0] != "sk-mcp-override-9999" || got.Source != "env:DEEPSEEK_API_KEY" {
		t.Fatalf("expected MCP env to override secrets.json, got %+v", got)
	}
	// 未被 MCP env 覆盖的其他源继续使用 ~/.config/veri/secrets.json
	if got := loadedEnv.Credentials["exa"]; len(got.Keys) != 1 || got.Keys[0] != "exa-vault-key-2222" || got.Source != spec.SecretsFileName {
		t.Fatalf("expected exa to still load from secrets.json, got %+v", got)
	}
	if got := loadedEnv.Credentials["brave"]; len(got.Keys) != 1 || got.Source != "env:BRAVE_SEARCH_API_KEY" {
		t.Fatalf("unexpected brave alias credential: %+v", got)
	}
	if got := loadedEnv.Credentials["tencent_wsa"]; len(got.Keys) != 1 || got.Source != "env:TENCENTCLOUD_WSA_APIKEY" {
		t.Fatalf("unexpected tencent_wsa alias credential: %+v", got)
	}
	if got := loadedEnv.Credentials["tavily"]; got.Source != "missing" {
		t.Fatalf("expected tavily to be missing, got %+v", got)
	}
}

func TestUnknownKeyRejectionAndPermissionWarning(t *testing.T) {
	tmp := t.TempDir()
	badPath := filepath.Join(tmp, "bad.toml")
	if err := os.WriteFile(badPath, []byte("[search]\ntypo_option = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithEnv(badPath, func(string) string { return "" }); err == nil || !strings.Contains(err.Error(), "typo_option") {
		t.Fatalf("expected unknown key error mentioning typo_option, got %v", err)
	}

	plainPath := filepath.Join(tmp, "plain.toml")
	if err := os.WriteFile(plainPath, []byte("[providers.deepseek]\napi_key = \"sk-plaintextsecret1234\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chmod(plainPath, 0o644)

	loaded, err := LoadWithEnv(plainPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv plain: %v", err)
	}
	if runtime.GOOS != "windows" && !loaded.PermWarning {
		t.Fatal("expected PermWarning=true when plaintext key is stored with 0644")
	}
	if got := loaded.Credentials["deepseek"]; got.Source != spec.ConfigFileName {
		t.Fatalf("expected plaintext key source %q, got %+v", spec.ConfigFileName, got)
	}

	// 即便 config.toml 写了明文，当 MCP 内传入 DEEPSEEK_API_KEY 时仍优先生效
	mcpOverride, err := LoadWithEnv(plainPath, func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-mcp-wins-5678"
		}
		return ""
	})
	if err != nil || mcpOverride.Credentials["deepseek"].Keys[0] != "sk-mcp-wins-5678" {
		t.Fatalf("expected MCP env to override plaintext config.toml, got %+v err=%v", mcpOverride, err)
	}

	if runtime.GOOS != "windows" && !mcpOverride.PermWarning {
		t.Fatal("expected PermWarning=true even when env overrides plaintext config.toml")
	}

	redactedTOML, err := loaded.FormatRedactedTOML()
	if err != nil {
		t.Fatalf("FormatRedactedTOML: %v", err)
	}
	if strings.Contains(redactedTOML, "plaintextsecret") || !strings.Contains(redactedTOML, "sk-…1234") {
		t.Fatalf("FormatRedactedTOML leaked secret: %s", redactedTOML)
	}
}

func TestByteSizeOverflowMultilineMaskAndValidation(t *testing.T) {
	if _, err := ParseByteSize("999999999999GB"); err == nil {
		t.Fatal("expected ParseByteSize overflow to fail")
	}
	if masked := maskSpec("sk-firstsecret1111\nsk-secondsecret2222"); !strings.Contains(masked, ",") || strings.Contains(masked, "firstsecret") {
		t.Fatalf("expected multiline maskSpec to mask each key individually, got %q", masked)
	}

	tmp := t.TempDir()
	for name, body := range map[string]string{
		"bad_prov.toml":        "[search]\nproviders = [\"no_such_provider\"]\n",
		"bad_search_role.toml": "[search]\nproviders = [\"local\"]\n",
		"bad_fetch_role.toml":  "[fetch]\nproviders = [\"brave\"]\n",
		"bad_weight.toml":      "[providers.deepseek]\nweight = 0\n",
		"bad_qps.toml":         "[providers.exa]\nqps = -1\n",
		"bad_cidr.toml":        "[network]\nfake_ip_cidrs = [\"not-a-cidr\"]\n",
		"bad_proxy.toml":       "[network]\nproxy = \"ftp://127.0.0.1:21\"\n",
	} {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadWithEnv(p, func(string) string { return "" }); err == nil {
			t.Fatalf("expected %s to fail validation", name)
		}
	}

	// 验证畸形 network.proxy 与含 URL 凭据的 TOML 解码错误不会回显明文凭据
	badSecretProxy := filepath.Join(tmp, "bad_secret_proxy.toml")
	if err := os.WriteFile(badSecretProxy, []byte("[network]\nproxy = \"http://proxyadmin:UltraSecretProxy77@bad host\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithEnv(badSecretProxy, func(string) string { return "" }); err == nil || strings.Contains(err.Error(), "UltraSecretProxy77") {
		t.Fatalf("expected invalid proxy error with redacted password, got %v", err)
	}
	badTOMLWithURL := filepath.Join(tmp, "bad_toml_url.toml")
	if err := os.WriteFile(badTOMLWithURL, []byte("[search]\ntimeout = \"socks5://u:TomlLeakPass88@127.0.0.1:1080\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithEnv(badTOMLWithURL, func(string) string { return "" }); err == nil || strings.Contains(err.Error(), "TomlLeakPass88") {
		t.Fatalf("expected TOML decode error to redact embedded proxy credentials, got %v", err)
	}
}

func TestPeekUILangPosixLocaleAndBilingualTemplates(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte("[ui]\nlang = \"en\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// 写入损坏的 secrets.json，验证 PeekUILang 完全不触碰或解密凭据库即可读取语言
	if err := os.WriteFile(filepath.Join(dir, "secrets.json"), []byte("{corrupted-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := PeekUILang(cfgPath, func(string) string { return "" }); got != "en" {
		t.Fatalf("PeekUILang = %q, want en", got)
	}

	// 验证标准 POSIX Locale（如 zh_CN.UTF-8 / system）及环境变量代理密码收集
	cleanDir := t.TempDir()
	cleanCfg := filepath.Join(cleanDir, "config.toml")
	loaded, err := LoadWithEnv(cleanCfg, func(k string) string {
		switch k {
		case "VERI_LANG":
			return "zh_CN.UTF-8"
		case "HTTPS_PROXY":
			return "http://corp:EnvProxySecret888@127.0.0.1:7890"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadWithEnv with VERI_LANG=zh_CN.UTF-8 failed: %v", err)
	}
	if !slices.Contains(loaded.AllSecrets, "EnvProxySecret888") {
		t.Fatal("expected HTTPS_PROXY password to be registered in Loaded.AllSecrets")
	}

	// 验证内嵌中英文配置模板均符合严格 Schema、与 Default() 完全一致，且双语激活键集合 100% 对称
	var loadedCfgs []Config
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
		if !reflect.DeepEqual(tplLoaded.Config, Default()) {
			t.Fatalf("%s template drifted from Default() config:\ngot:  %+v\nwant: %+v", loc, tplLoaded.Config, Default())
		}
		loadedCfgs = append(loadedCfgs, tplLoaded.Config)

		var rawCfg Config
		meta, decErr := toml.Decode(TemplateForLocale(loc), &rawCfg)
		if decErr != nil {
			t.Fatalf("decode %s template: %v", loc, decErr)
		}
		var keys []string
		for _, k := range meta.Keys() {
			keys = append(keys, k.String())
		}
		slices.Sort(keys)
		keyLists = append(keyLists, keys)
	}
	if !reflect.DeepEqual(loadedCfgs[0], loadedCfgs[1]) || !slices.Equal(keyLists[0], keyLists[1]) {
		t.Fatalf("zh-CN.toml and en-US.toml keys/values drifted:\nzh=%v\nen=%v", keyLists[0], keyLists[1])
	}
}

func TestAllProviderSecretsRedactedAndRegisteredInConfig(t *testing.T) {
	cfg := Default()
	canary := "CanaryLeakCheckSecret99887766"
	cfg.Network.Proxy = "socks5://proxyuser:" + canary + "@127.0.0.1:1080"
	cfg.Providers.DeepSeek.BaseURL = "https://relay.example.com/v1?api_key=" + canary
	cfg.Providers.TinyFish.SearchURL = "https://relay.example.com/search?token=" + canary

	secretFieldCount := 0
	seenProviders := make(map[string]bool)
	_ = walkProviderFields(&cfg.Providers, func(provName, fieldTag string, val reflect.Value) error {
		seenProviders[provName] = true
		if isSecretTOMLTag(fieldTag) && val.Kind() == reflect.String {
			secretFieldCount++
			val.SetString("sk-" + canary + "-tail")
		}
		return nil
	})
	if secretFieldCount != len(spec.CredentialProviders()) {
		t.Fatalf("expected %d secret fields on ProvidersConfig, got %d", len(spec.CredentialProviders()), secretFieldCount)
	}
	for _, info := range spec.Providers() {
		if !seenProviders[info.Name] {
			t.Errorf("provider %q from spec.Providers() missing toml section on config.ProvidersConfig", info.Name)
		}
		if !Default().Providers.IsEnabled(info.Name) {
			t.Errorf("Default() provider %s should have enabled=true", info.Name)
		}
		if info.NeedsCredential() && Default().Providers.CredentialSpec(info.Name) != spec.DefaultEnvRef(info.Name) {
			t.Errorf("Default() credential spec for %s = %q, want %q", info.Name, Default().Providers.CredentialSpec(info.Name), spec.DefaultEnvRef(info.Name))
		}
	}

	customPath := filepath.Join(t.TempDir(), "custom.toml")
	customBody := "[fetch]\nproviders_global = [\"exa\", \"tinyfish\"]\n[providers.local]\nenabled = false\n"
	if err := os.WriteFile(customPath, []byte(customBody), 0o600); err != nil {
		t.Fatal(err)
	}
	loadedCustom, err := LoadWithEnv(customPath, func(k string) string {
		if k == spec.EnvFetchProviders {
			return "tinyfish, deepseek"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadWithEnv custom: %v", err)
	}
	if loadedCustom.Config.Fetch.Strategy != "api_first" {
		t.Fatalf("default fetch.strategy = %q, want api_first", loadedCustom.Config.Fetch.Strategy)
	}
	if !slices.Equal(loadedCustom.Config.Fetch.Providers, []string{"tinyfish", "deepseek"}) {
		t.Fatalf("VERI_FETCH_PROVIDERS override failed: %v", loadedCustom.Config.Fetch.Providers)
	}
	if !slices.Equal(loadedCustom.Config.Fetch.ProvidersGlobal, []string{"exa", "tinyfish"}) {
		t.Fatalf("fetch.providers_global decode failed: %v", loadedCustom.Config.Fetch.ProvidersGlobal)
	}
	if loadedCustom.Config.Providers.IsEnabled(spec.ProviderLocal) || !loadedCustom.Config.Providers.IsEnabled(spec.ProviderGitHub) {
		t.Fatalf("unexpected enabled flags: local=%v github=%v", loadedCustom.Config.Providers.Local.Enabled, loadedCustom.Config.Providers.GitHub.Enabled)
	}

	loaded := &Loaded{Config: cfg}
	tomlOut, err := loaded.FormatRedactedTOML()
	if err != nil {
		t.Fatalf("FormatRedactedTOML: %v", err)
	}
	if strings.Contains(tomlOut, canary) {
		t.Fatalf("Redacted TOML leaked canary secret:\n%s", tomlOut)
	}

	// 验证无协议前缀的非法 network.proxy 报错脱敏，以及无协议 HTTPS_PROXY 环境变量密码被纳入 AllSecrets
	badProxyPath := filepath.Join(t.TempDir(), "bad-proxy.toml")
	if err := os.WriteFile(badProxyPath, []byte("[network]\nproxy = \"proxyuser:NoSchemeProxyPass99@proxy.local:8080\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithEnv(badProxyPath, func(string) string { return "" }); err == nil || strings.Contains(err.Error(), "NoSchemeProxyPass99") {
		t.Fatalf("expected redacted scheme-less proxy validation error, got %v", err)
	}

	envLoaded, err := LoadWithEnv(filepath.Join(t.TempDir(), "missing.toml"), func(k string) string {
		if k == "HTTPS_PROXY" {
			return "proxyuser:EnvNoSchemePass99@127.0.0.1:8080"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadWithEnv with scheme-less HTTPS_PROXY: %v", err)
	}
	if !slices.Contains(envLoaded.AllSecrets, "EnvNoSchemePass99") {
		t.Fatalf("expected scheme-less HTTPS_PROXY password collected in AllSecrets, got %v", envLoaded.AllSecrets)
	}
}
