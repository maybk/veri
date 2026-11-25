package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"veri/clikit/credentials"
	"veri/clikit/storage"
	"veri/internal/core"
	"veri/internal/spec"
)

const maxConfigFileBytes = 1 << 20 // 单个 config.toml 读取上限（1 MiB），防止误传超大文件引发内存抖动

// Loaded 携带合并校验后的完整配置、来源元数据与安全审计警告。
type Loaded struct {
	Config                        Config
	Path                          string
	Dir                           string
	SecretsDir                    string
	SecretsPath                   string
	SecretsExists                 bool
	CacheDir                      string
	FileExists                    bool
	FileMode                      os.FileMode
	DefinedKeys                   map[string]bool   // config.toml 中显式定义的键路径集合
	EnvOverrides                  map[string]string // 被 VERI_* 行为环境变量覆盖的配置键 -> 变量名
	fileConfig                    Config            // config.toml 解码后的原始文件态配置（不受环境变量与 vault 覆盖影响）
	SearchProvidersExplicit       bool              // 用户在 config.toml 或 VERI_PROVIDERS 中显式配置了 search.providers
	FusionProvidersGlobalExplicit bool              // 用户在 config.toml 中显式配置了 search.fusion_providers_global（含设 [] 关闭分池）
	FetchProvidersExplicit        bool              // 用户在 config.toml 或 VERI_FETCH_PROVIDERS 中显式配置了 fetch.providers
	PermWarning                   bool              // 配置文件含明文密钥且权限宽于 0600
	VaultPermWarning              bool              // secrets.json 或 secret.key 权限过宽（Unix 宽于 0600 或 Windows ACL 暴露）
	PlaintextProviders            []string          // config.toml 中存有明文密钥的 Provider 列表（不含明文值）
	LogLevel                      string
	LogFormat                     string
	Credentials                   map[string]Credential
	VaultProviders                map[string]bool
	AllSecrets                    []string
}

// PeekUILang 仅从配置文件轻量读取 [ui] lang 字段，不校验完整配置树，也绝不访问或解密 secrets.json / secret.key。
// 供 CLI 启动阶段解析界面语言使用，确保 --help、version 等无需凭据的命令保持零密钥接触与毫秒级启动。
func PeekUILang(explicitPath string, getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfgPath, err := ResolvePath(explicitPath, getenv)
	if err != nil {
		return ""
	}
	info, err := os.Stat(cfgPath)
	if err != nil || info.IsDir() || info.Size() > maxConfigFileBytes {
		return ""
	}
	raw, err := os.ReadFile(filepath.Clean(cfgPath)) // #nosec G304 -- local CLI user-specified config file path
	if err != nil {
		return ""
	}
	var partial struct {
		UI UIConfig `toml:"ui"`
	}
	if _, err := toml.Decode(string(raw), &partial); err != nil {
		return ""
	}
	return strings.TrimSpace(partial.UI.Lang)
}

// ResolvePath 按三端（macOS / Windows / Linux）统一优先级解析配置文件路径：
// 1. explicitPath（--config）或 $VERI_CONFIG
// 2. $XDG_CONFIG_HOME/veri/config.toml（显式设置 XDG_CONFIG_HOME 时）
// 3. ~/.config/veri/config.toml
func ResolvePath(explicitPath string, getenv func(string) string) (string, error) {
	return storage.ResolveConfigFile(spec.ConfigDirName, spec.ConfigFileName, explicitPath, spec.EnvConfig, getenv)
}

// ResolveDir 返回当前生效的配置文件目录（默认 ~/.config/veri）。
func ResolveDir(explicitConfigPath string, getenv func(string) string) (string, error) {
	return storage.ResolveConfigDir(spec.ConfigDirName, spec.ConfigFileName, explicitConfigPath, spec.EnvConfig, getenv)
}

// ResolveSecretsDir 返回当前生效的本地加密凭据目录：
// 1. 显式设置 VERI_SECRETS_DIR 时使用该目录（允许自定义 --config 复用共享加密凭据库）
// 2. 否则跟随生效配置文件所在目录（保持多 Profile 与测试环境的默认隔离）
func ResolveSecretsDir(explicitConfigPath string, getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if raw := strings.TrimSpace(getenv(spec.EnvSecretsDir)); raw != "" {
		return storage.ExpandHome(raw)
	}
	return ResolveDir(explicitConfigPath, getenv)
}

func resolveSecretsDirFor(cfgDir string, getenv func(string) string) (string, error) {
	if raw := strings.TrimSpace(getenv(spec.EnvSecretsDir)); raw != "" {
		return storage.ExpandHome(raw)
	}
	return cfgDir, nil
}

// ResolveCacheDir 按三端统一优先级解析磁盘缓存目录（默认 ~/.cache/veri）。
func ResolveCacheDir(explicitDir string, getenv func(string) string) (string, error) {
	return storage.ResolveCacheDir(spec.ConfigDirName, explicitDir, spec.EnvCacheDir, getenv)
}

// Load 加载、合并并严格校验配置（内置默认 <- config.toml <- secrets.json <- 环境变量）。
func Load(explicitPath string) (*Loaded, error) {
	return LoadWithEnv(explicitPath, os.Getenv)
}

// LoadWithEnv 使用注入的环境变量读取函数加载配置（便于无副作用单元测试）。
func LoadWithEnv(explicitPath string, getenv func(string) string) (*Loaded, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	cfgPath, err := ResolvePath(explicitPath, getenv)
	if err != nil {
		return nil, core.WrapError(core.CodeInvalidArgument, err.Error(), err)
	}
	cfgDir := filepath.Dir(cfgPath)
	secretsDir, err := resolveSecretsDirFor(cfgDir, getenv)
	if err != nil {
		return nil, core.WrapError(core.CodeInvalidArgument, err.Error(), err)
	}
	vault := credentials.NewVault(secretsDir)

	credProviders := spec.CredentialProviders()
	cfg := Default()
	out := &Loaded{
		Path:           cfgPath,
		Dir:            cfgDir,
		SecretsDir:     secretsDir,
		SecretsPath:    vault.SecretsPath(),
		DefinedKeys:    make(map[string]bool),
		EnvOverrides:   make(map[string]string),
		LogLevel:       resolveLogLevel(getenv(spec.EnvLogLevel)),
		LogFormat:      resolveLogFormat(getenv(spec.EnvLogFormat)),
		Credentials:    make(map[string]Credential, len(credProviders)),
		VaultProviders: make(map[string]bool, len(credProviders)),
	}
	searchGlobalDefined := false
	fetchGlobalDefined := false

	raw, fileMode, exists, readErr := readExistingConfigFile(cfgPath)
	if readErr != nil {
		return nil, readErr
	}
	if exists {
		out.FileExists = true
		out.FileMode = fileMode
		meta, decErr := toml.Decode(raw, &cfg)
		if decErr != nil {
			return nil, invalidTOMLError(cfgPath, decErr)
		}
		for _, k := range meta.Keys() {
			out.DefinedKeys[k.String()] = true
		}
		if meta.IsDefined("search", "providers") {
			out.SearchProvidersExplicit = true
		}
		if meta.IsDefined("search", "providers_global") {
			searchGlobalDefined = true
		}
		if meta.IsDefined("search", "fusion_providers_global") {
			out.FusionProvidersGlobalExplicit = true
		}
		if meta.IsDefined("fetch", "providers") {
			out.FetchProvidersExplicit = true
		}
		if meta.IsDefined("fetch", "providers_global") {
			fetchGlobalDefined = true
		}
		if undecoded := meta.Undecoded(); len(undecoded) > 0 {
			return nil, unknownConfigKeysError(cfgPath, undecoded)
		}
		out.fileConfig = cfg
	}

	if err := applyBehaviorEnv(&cfg, out, getenv); err != nil {
		return nil, err
	}
	// 用户显式指定了 search.providers（文件或环境变量）而未显式指定 search.providers_global 时，
	// 关闭内置默认非中文分链，严格尊重用户指定的统一顺序。
	if out.SearchProvidersExplicit && !searchGlobalDefined {
		cfg.Search.ProvidersGlobal = nil
	}
	// 用户显式指定了 fetch.providers（文件或环境变量）而未显式指定 fetch.providers_global 时，
	// 关闭内置默认全球分链，严格尊重用户指定的统一顺序。
	if out.FetchProvidersExplicit && !fetchGlobalDefined {
		cfg.Fetch.ProvidersGlobal = nil
	}
	if err := validateConfig(&cfg); err != nil {
		return nil, err
	}
	cacheDir, err := ResolveCacheDir(cfg.Cache.Dir, getenv)
	if err != nil {
		return nil, core.WrapError(core.CodeInvalidArgument, err.Error(), err)
	}
	out.CacheDir = cacheDir

	vaultSecrets, err := loadVaultSecrets(vault, secretsDir)
	if err != nil {
		return nil, err
	}
	for provName := range vaultSecrets {
		out.VaultProviders[provName] = true
	}

	hasPlain := false
	seenSecrets := make(map[string]struct{}, 16)
	addSecrets := func(items []string) {
		for _, s := range items {
			if s == "" {
				continue
			}
			if _, ok := seenSecrets[s]; !ok {
				seenSecrets[s] = struct{}{}
				out.AllSecrets = append(out.AllSecrets, s)
			}
		}
	}

	credSpecs := cfg.Providers.CredentialSpecs()
	for _, prov := range credProviders {
		cred, allCandidateKeys := resolveCred(credSpecs[prov], spec.ProviderCredentialEnvVars(prov), vaultSecrets[prov], getenv)
		out.Credentials[prov] = cred
		if cred.HasPlainInFile {
			hasPlain = true
			out.PlaintextProviders = append(out.PlaintextProviders, prov)
		}
		addSecrets(allCandidateKeys)
	}
	for _, proxySpec := range []string{
		cfg.Network.Proxy,
		getenv("HTTPS_PROXY"), getenv("https_proxy"),
		getenv("HTTP_PROXY"), getenv("http_proxy"),
		getenv("ALL_PROXY"), getenv("all_proxy"),
	} {
		if uSecret := extractProxyPassword(proxySpec); uSecret != "" {
			addSecrets([]string{uSecret})
		}
	}

	if out.FileExists && hasPlain && storage.HasBroadPermissions(out.FileMode) {
		out.PermWarning = true
	}
	if fi, sErr := os.Stat(out.SecretsPath); sErr == nil && !fi.IsDir() {
		out.SecretsExists = true
	}
	for _, fname := range []string{spec.SecretsFileName, spec.SecretKeyFileName} {
		if credentials.HasBroadPermissions(filepath.Join(secretsDir, fname)) {
			out.VaultPermWarning = true
			break
		}
	}

	out.Config = cfg
	return out, nil
}

// CredentialFor 返回指定 Provider 生效的解析凭据（共享凭据源自动映射至主凭据归属项）。
func (l *Loaded) CredentialFor(provider string) Credential {
	if l == nil {
		return Credential{}
	}
	owner := provider
	if info, ok := spec.LookupProvider(provider); ok {
		if o := info.CredentialOwner(); o != "" {
			owner = o
		}
	}
	return l.Credentials[owner]
}

// RedactedConfig 返回已脱敏的生效配置树副本（供 TOML 与 JSON 输出共享）。
func (l *Loaded) RedactedConfig() Config {
	return l.Config.Redacted(l.Credentials)
}

// FormatRedactedTOML 将合并生效后的配置编码为完全脱敏的 TOML 文本（供 veri config show 输出）。
func (l *Loaded) FormatRedactedTOML() (string, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(l.RedactedConfig()); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func resolveLogLevel(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "debug", "info", "warn", "error":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "warn"
	}
}

func resolveLogFormat(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "json") {
		return "json"
	}
	return "text"
}

func applyBehaviorEnv(cfg *Config, loaded *Loaded, getenv func(string) string) error {
	recordEnv := func(key, envName string) {
		if loaded != nil && loaded.EnvOverrides != nil {
			loaded.EnvOverrides[key] = envName
		}
	}
	if v := strings.TrimSpace(getenv(spec.EnvLang)); v != "" {
		cfg.UI.Lang = v
		recordEnv("ui.lang", spec.EnvLang)
	}
	if list := splitEnvList(getenv(spec.EnvProviders)); len(list) > 0 {
		cfg.Search.Providers = list
		recordEnv("search.providers", spec.EnvProviders)
		if loaded != nil {
			loaded.SearchProvidersExplicit = true
		}
	}
	if list := splitEnvList(getenv(spec.EnvFetchProviders)); len(list) > 0 {
		cfg.Fetch.Providers = list
		recordEnv("fetch.providers", spec.EnvFetchProviders)
		if loaded != nil {
			loaded.FetchProvidersExplicit = true
		}
	}
	if v := strings.TrimSpace(getenv(spec.EnvFetchStrategy)); v != "" {
		cfg.Fetch.Strategy = v
		recordEnv("fetch.strategy", spec.EnvFetchStrategy)
	}
	if v := strings.TrimSpace(getenv(spec.EnvTimeout)); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid %s %q", spec.EnvTimeout, v)).
				WithMsgKey("err.invalid_env_timeout").
				WithParam("env", spec.EnvTimeout).
				WithParam("value", v)
		}
		cfg.Search.Timeout = Duration{d}
		recordEnv("search.timeout", spec.EnvTimeout)
	}
	if v := strings.TrimSpace(getenv(spec.EnvCacheDir)); v != "" {
		cfg.Cache.Dir = v
		recordEnv("cache.dir", spec.EnvCacheDir)
	}
	if d, set, err := parseNonNegDurationEnv(spec.EnvCacheFetchTTL, getenv(spec.EnvCacheFetchTTL)); err != nil {
		return err
	} else if set {
		cfg.Cache.FetchTTL = Duration{d}
		cfg.Cache.CLI.FetchTTL = nil
		cfg.Cache.MCP.FetchTTL = nil
		recordEnv("cache.fetch_ttl", spec.EnvCacheFetchTTL)
		recordEnv("cache.cli.fetch_ttl", spec.EnvCacheFetchTTL)
		recordEnv("cache.mcp.fetch_ttl", spec.EnvCacheFetchTTL)
	}
	if d, set, err := parseNonNegDurationEnv(spec.EnvCacheSearchTTL, getenv(spec.EnvCacheSearchTTL)); err != nil {
		return err
	} else if set {
		cfg.Cache.SearchTTL = Duration{d}
		cfg.Cache.CLI.SearchTTL = nil
		cfg.Cache.MCP.SearchTTL = nil
		recordEnv("cache.search_ttl", spec.EnvCacheSearchTTL)
		recordEnv("cache.cli.search_ttl", spec.EnvCacheSearchTTL)
		recordEnv("cache.mcp.search_ttl", spec.EnvCacheSearchTTL)
	}
	if v := strings.TrimSpace(getenv(spec.EnvNoCache)); v != "" {
		disable, ok := parseEnvBool(v)
		if !ok {
			return invalidEnvValueError(spec.EnvNoCache, v, "1 | 0 | true | false")
		}
		if disable {
			cfg.Cache.Enabled = false
			cfg.Cache.CLI.Enabled = nil
			cfg.Cache.MCP.Enabled = nil
			recordEnv("cache.enabled", spec.EnvNoCache)
			recordEnv("cache.cli.enabled", spec.EnvNoCache)
			recordEnv("cache.mcp.enabled", spec.EnvNoCache)
		}
	}
	return nil
}

func parseNonNegDurationEnv(envName, raw string) (time.Duration, bool, error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return 0, false, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		return 0, false, invalidEnvValueError(envName, v, "duration (e.g. 15m, 0s)")
	}
	return d, true, nil
}

func invalidEnvValueError(envName, value, expected string) *core.Error {
	safeVal := scrubConfigScalar(value)
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid %s %q: expected %s", envName, safeVal, expected)).
		WithMsgKey("err.invalid_env_value").
		WithParam("env", envName).
		WithParam("value", safeVal).
		WithParam("expected", expected)
}

func parseEnvBool(raw string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	default:
		return false, false
	}
}

func splitEnvList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var list []string
	for item := range strings.SplitSeq(raw, ",") {
		if p := strings.TrimSpace(item); p != "" {
			list = append(list, p)
		}
	}
	return list
}
