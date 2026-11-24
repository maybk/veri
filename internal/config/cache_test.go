package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"veri/internal/spec"
)

func TestCacheConfigDefaultsAndInheritance(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	cfgPath := filepath.Join(tmpHome, ".config", "veri", "config.toml")
	loaded, err := LoadWithEnv(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv default: %v", err)
	}

	wantCacheDir := filepath.Join(tmpHome, ".cache", "veri")
	if loaded.CacheDir != wantCacheDir {
		t.Fatalf("CacheDir = %q, want %q", loaded.CacheDir, wantCacheDir)
	}

	cliPolicy := loaded.Config.Cache.EffectiveCLI()
	if !cliPolicy.Enabled || cliPolicy.FetchTTL != spec.DefaultCacheFetchTTL || cliPolicy.SearchTTL != 0 {
		t.Fatalf("unexpected default CLI cache policy: %+v", cliPolicy)
	}
	mcpPolicy := loaded.Config.Cache.EffectiveMCP()
	if !mcpPolicy.Enabled || mcpPolicy.FetchTTL != spec.DefaultCacheFetchTTL ||
		mcpPolicy.SearchTTL != spec.DefaultCacheMCPSearchTTL || mcpPolicy.MaxEntries != spec.DefaultCacheMCPMaxEntries {
		t.Fatalf("unexpected default MCP cache policy: %+v", mcpPolicy)
	}

	// 未覆写子键应通过 GetValue 返回继承值并标注 source=inherited。
	gotCLI, err := GetValue(loaded, "cache.cli.fetch_ttl")
	if err != nil {
		t.Fatalf("GetValue(cache.cli.fetch_ttl): %v", err)
	}
	if gotCLI.Formatted != "15m0s" || gotCLI.Source != "inherited" {
		t.Fatalf("GetValue(cache.cli.fetch_ttl) = %+v, want 15m0s/inherited", gotCLI)
	}

	// MCP 默认 search_ttl 为内置显式覆写值 5m0s。
	gotMCPSearch, err := GetValue(loaded, "cache.mcp.search_ttl")
	if err != nil {
		t.Fatalf("GetValue(cache.mcp.search_ttl): %v", err)
	}
	if gotMCPSearch.Formatted != "5m0s" || gotMCPSearch.Source != "default" {
		t.Fatalf("GetValue(cache.mcp.search_ttl) = %+v, want 5m0s/default", gotMCPSearch)
	}
}

func TestCacheConfigOverridesSetUnsetAndLegacyRejection(t *testing.T) {
	noEnv := func(string) string { return "" }
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")

	// 1. 旧 [mcp].cache_ttl / cache_size 已彻底废弃，应触发严格未知键拒绝。
	legacyPath := filepath.Join(tmp, "legacy.toml")
	if err := os.WriteFile(legacyPath, []byte("[mcp]\ncache_ttl = \"5m\"\ncache_size = 128\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithEnv(legacyPath, noEnv); err == nil || !strings.Contains(err.Error(), "cache_ttl") {
		t.Fatalf("expected legacy mcp.cache_ttl to be rejected as unknown key, got %v", err)
	}

	// 2. 顶层修改 cache.fetch_ttl = "30m" 同步影响未覆写的 CLI 与 MCP。
	if _, err := SetValue(cfgPath, "cache.fetch_ttl", "30m", noEnv); err != nil {
		t.Fatalf("SetValue cache.fetch_ttl: %v", err)
	}
	loaded, err := LoadWithEnv(cfgPath, noEnv)
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	if loaded.Config.Cache.EffectiveCLI().FetchTTL != 30*time.Minute || loaded.Config.Cache.EffectiveMCP().FetchTTL != 30*time.Minute {
		t.Fatalf("expected both CLI and MCP to inherit 30m fetch_ttl, got cli=%+v mcp=%+v",
			loaded.Config.Cache.EffectiveCLI(), loaded.Config.Cache.EffectiveMCP())
	}

	// 3. 单独关闭 CLI fetch_ttl（显式 0s）与 MCP search_ttl（显式 0s），互不干扰。
	if _, err := SetValue(cfgPath, "cache.cli.fetch_ttl", "0s", noEnv); err != nil {
		t.Fatalf("SetValue cache.cli.fetch_ttl: %v", err)
	}
	if _, err := SetValue(cfgPath, "cache.cli.search_ttl", "2m", noEnv); err != nil {
		t.Fatalf("SetValue cache.cli.search_ttl: %v", err)
	}
	if _, err := SetValue(cfgPath, "cache.mcp.search_ttl", "0s", noEnv); err != nil {
		t.Fatalf("SetValue cache.mcp.search_ttl: %v", err)
	}
	loaded, err = LoadWithEnv(cfgPath, noEnv)
	if err != nil {
		t.Fatalf("LoadWithEnv after sub-overrides: %v", err)
	}
	cliPol := loaded.Config.Cache.EffectiveCLI()
	mcpPol := loaded.Config.Cache.EffectiveMCP()
	if cliPol.FetchTTL != 0 || cliPol.SearchTTL != 2*time.Minute {
		t.Fatalf("unexpected CLI override policy: %+v", cliPol)
	}
	if mcpPol.FetchTTL != 30*time.Minute || mcpPol.SearchTTL != 0 {
		t.Fatalf("expected MCP fetch_ttl=30m decoupled from search_ttl=0s, got %+v", mcpPol)
	}
	gotCLIFetch, err := GetValue(loaded, "cache.cli.fetch_ttl")
	if err != nil || gotCLIFetch.Formatted != "0s" || gotCLIFetch.Source != "config" {
		t.Fatalf("GetValue(cache.cli.fetch_ttl) = (%+v, %v), want 0s/config", gotCLIFetch, err)
	}

	// 4. unset cache.cli.fetch_ttl 后自动恢复继承顶层 30m。
	if res, err := UnsetValue(cfgPath, "cache.cli.fetch_ttl"); err != nil || !res.Changed {
		t.Fatalf("UnsetValue cache.cli.fetch_ttl = (%+v, %v)", res, err)
	}
	loaded, err = LoadWithEnv(cfgPath, noEnv)
	if err != nil {
		t.Fatalf("LoadWithEnv after unset: %v", err)
	}
	if loaded.Config.Cache.EffectiveCLI().FetchTTL != 30*time.Minute {
		t.Fatalf("expected CLI fetch_ttl to resume inheriting 30m after unset, got %+v", loaded.Config.Cache.EffectiveCLI())
	}

	// 5. 顶层 enabled = false 同步关闭两端，而子域 enabled = true 可单独放行一端。
	if _, err := SetValue(cfgPath, "cache.enabled", "false", noEnv); err != nil {
		t.Fatalf("SetValue cache.enabled: %v", err)
	}
	loaded, err = LoadWithEnv(cfgPath, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Cache.EffectiveCLI().Enabled || loaded.Config.Cache.EffectiveMCP().Enabled {
		t.Fatalf("expected cache.enabled=false to disable both CLI and MCP")
	}
	if _, err := SetValue(cfgPath, "cache.mcp.enabled", "true", noEnv); err != nil {
		t.Fatalf("SetValue cache.mcp.enabled: %v", err)
	}
	loaded, err = LoadWithEnv(cfgPath, noEnv)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Cache.EffectiveCLI().Enabled || !loaded.Config.Cache.EffectiveMCP().Enabled {
		t.Fatalf("expected only MCP enabled, got cli=%v mcp=%v", loaded.Config.Cache.EffectiveCLI().Enabled, loaded.Config.Cache.EffectiveMCP().Enabled)
	}
}

func TestCacheEnvOverridesAndValidation(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	raw := `
[cache]
enabled = true
dir = "/from/config"
fetch_ttl = "10m"
max_bytes = "64MB"

[cache.cli]
enabled = true
fetch_ttl = "5m"

[cache.mcp]
enabled = true
search_ttl = "3m"
max_entries = 128
`
	if err := os.WriteFile(cfgPath, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	envCacheDir := filepath.Join(tmp, "from-env-cache")
	env := map[string]string{
		spec.EnvCacheDir:       envCacheDir,
		spec.EnvCacheFetchTTL:  "0s",
		spec.EnvCacheSearchTTL: "90s",
		spec.EnvNoCache:        "1",
	}
	loaded, err := LoadWithEnv(cfgPath, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("LoadWithEnv with cache envs: %v", err)
	}
	if loaded.CacheDir != envCacheDir {
		t.Fatalf("expected VERI_CACHE_DIR to win over config.toml cache.dir: got %q, want %q", loaded.CacheDir, envCacheDir)
	}
	cliPol := loaded.Config.Cache.EffectiveCLI()
	mcpPol := loaded.Config.Cache.EffectiveMCP()
	if cliPol.Enabled || mcpPol.Enabled {
		t.Fatalf("expected VERI_NO_CACHE=1 to disable both CLI and MCP even when sub-scopes enabled=true: cli=%+v mcp=%+v", cliPol, mcpPol)
	}
	if cliPol.FetchTTL != 0 || mcpPol.FetchTTL != 0 {
		t.Fatalf("expected VERI_CACHE_FETCH_TTL=0s to override both scopes: cli=%+v mcp=%+v", cliPol, mcpPol)
	}
	if cliPol.SearchTTL != 90*time.Second || mcpPol.SearchTTL != 90*time.Second {
		t.Fatalf("expected VERI_CACHE_SEARCH_TTL=90s to override both scopes: cli=%+v mcp=%+v", cliPol, mcpPol)
	}
	gotEnvVal, err := GetValue(loaded, "cache.cli.fetch_ttl")
	if err != nil || gotEnvVal.Source != "env" || gotEnvVal.EnvVar != spec.EnvCacheFetchTTL || gotEnvVal.Formatted != "0s" {
		t.Fatalf("GetValue(cache.cli.fetch_ttl) under env = (%+v, %v)", gotEnvVal, err)
	}

	// 非法环境变量与非法配置项应严格报错。
	for envKey, badVal := range map[string]string{
		spec.EnvCacheFetchTTL:  "-5s",
		spec.EnvCacheSearchTTL: "not-a-duration",
		spec.EnvNoCache:        "maybe",
	} {
		if _, err := LoadWithEnv(cfgPath, func(k string) string {
			if k == envKey {
				return badVal
			}
			return ""
		}); err == nil {
			t.Errorf("expected invalid %s=%q to fail", envKey, badVal)
		}
	}

	badEntriesPath := filepath.Join(tmp, "bad_entries.toml")
	if err := os.WriteFile(badEntriesPath, []byte("[cache.mcp]\nmax_entries = -1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWithEnv(badEntriesPath, func(string) string { return "" }); err == nil {
		t.Fatal("expected negative cache.mcp.max_entries to fail validation")
	}

	// FormatRedactedTOML 正常序列化完整统一 [cache] 树。
	plainLoaded, err := LoadWithEnv(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv plain: %v", err)
	}
	tomlOut, err := plainLoaded.FormatRedactedTOML()
	if err != nil {
		t.Fatalf("FormatRedactedTOML: %v", err)
	}
	for _, want := range []string{"[cache]", "[cache.cli]", "[cache.mcp]", "max_entries = 128"} {
		if !strings.Contains(tomlOut, want) {
			t.Errorf("FormatRedactedTOML missing %q:\n%s", want, tomlOut)
		}
	}
}
