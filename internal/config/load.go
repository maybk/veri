package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"veri/clikit/credentials"
	"veri/clikit/redact"
	"veri/clikit/storage"
	"veri/internal/core"
	"veri/internal/spec"
)

// Loaded 携带合并校验后的完整配置、来源元数据与安全审计警告。
type Loaded struct {
	Config                        Config
	Path                          string
	Dir                           string
	FileExists                    bool
	FileMode                      os.FileMode
	SearchProvidersExplicit       bool // 用户在 config.toml 或 VERI_PROVIDERS 中显式配置了 search.providers
	FusionProvidersGlobalExplicit bool // 用户在 config.toml 中显式配置了 search.fusion_providers_global（含设 [] 关闭分池）
	FetchProvidersExplicit        bool // 用户在 config.toml 或 VERI_FETCH_PROVIDERS 中显式配置了 fetch.providers
	PermWarning                   bool // 配置文件含明文密钥且权限宽于 0600
	VaultPermWarning              bool // secrets.json 或 secret.key 权限宽于 0600
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
	if err != nil || info.IsDir() || info.Size() > 1<<20 {
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

// ResolveDir 返回当前生效的配置与加密凭据目录（默认 ~/.config/veri）。
func ResolveDir(explicitConfigPath string, getenv func(string) string) (string, error) {
	return storage.ResolveConfigDir(spec.ConfigDirName, spec.ConfigFileName, explicitConfigPath, spec.EnvConfig, getenv)
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

	credProviders := spec.CredentialProviders()
	cfg := Default()
	out := &Loaded{
		Path:           cfgPath,
		Dir:            cfgDir,
		LogLevel:       resolveLogLevel(getenv(spec.EnvLogLevel)),
		LogFormat:      resolveLogFormat(getenv(spec.EnvLogFormat)),
		Credentials:    make(map[string]Credential, len(credProviders)),
		VaultProviders: make(map[string]bool, len(credProviders)),
	}
	searchGlobalDefined := false
	fetchGlobalDefined := false

	if info, statErr := os.Stat(cfgPath); statErr == nil {
		if info.IsDir() {
			return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("config path %q is a directory", cfgPath)).
				WithMsgKey("err.config_is_dir").
				WithParam("path", cfgPath)
		}
		out.FileExists = true
		out.FileMode = info.Mode()
		raw, readErr := os.ReadFile(filepath.Clean(cfgPath)) // #nosec G304 -- local CLI user-specified config file path
		if readErr != nil {
			return nil, core.WrapError(core.CodeInvalidArgument, fmt.Sprintf("failed to read config %q", cfgPath), readErr).
				WithMsgKey("err.config_read_failed").
				WithParam("path", cfgPath)
		}
		meta, decErr := toml.Decode(string(raw), &cfg)
		if decErr != nil {
			safeDetail := redact.Scrub(decErr.Error(), nil)
			safeCause := decErr
			if safeDetail != decErr.Error() {
				safeCause = errors.New(safeDetail)
			}
			return nil, core.WrapError(core.CodeInvalidArgument, fmt.Sprintf("invalid TOML in %q: %s", cfgPath, safeDetail), safeCause).
				WithMsgKey("err.config_invalid_toml").
				WithParam("path", cfgPath).
				WithParam("detail", safeDetail)
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
			keys := make([]string, len(undecoded))
			for i, k := range undecoded {
				keys[i] = k.String()
			}
			keyList := strings.Join(keys, ", ")
			return nil, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown configuration key(s) in %q: %s", cfgPath, keyList)).
				WithHint("Check spelling in config.toml or run `veri config show` to inspect supported fields.").
				WithI18n("err.config_unknown_keys", "hint.unknown_config_keys").
				WithParam("path", cfgPath).
				WithParam("keys", keyList)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return nil, core.WrapError(core.CodeInvalidArgument, fmt.Sprintf("cannot access config %q", cfgPath), statErr).
			WithMsgKey("err.config_read_failed").
			WithParam("path", cfgPath)
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

	vaultSecrets, err := credentials.NewVault(cfgDir).LoadAll()
	if err != nil {
		return nil, core.WrapError(core.CodeAuthFailed, fmt.Sprintf("failed to load encrypted credentials in %q: %v", cfgDir, err), err).
			WithHint("Check file permissions or re-save the damaged credential via `veri key set <provider>`.").
			WithI18n("err.vault_decrypt_failed", "hint.vault_decrypt_failed").
			WithParam("dir", cfgDir)
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
	for _, fname := range []string{spec.SecretsFileName, spec.SecretKeyFileName} {
		if fi, sErr := os.Stat(filepath.Join(cfgDir, fname)); sErr == nil && !fi.IsDir() && storage.HasBroadPermissions(fi.Mode()) {
			out.VaultPermWarning = true
			break
		}
	}

	out.Config = cfg
	return out, nil
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
	if v := strings.TrimSpace(getenv(spec.EnvLang)); v != "" {
		cfg.UI.Lang = v
	}
	if list := splitEnvList(getenv(spec.EnvProviders)); len(list) > 0 {
		cfg.Search.Providers = list
		if loaded != nil {
			loaded.SearchProvidersExplicit = true
		}
	}
	if list := splitEnvList(getenv(spec.EnvFetchProviders)); len(list) > 0 {
		cfg.Fetch.Providers = list
		if loaded != nil {
			loaded.FetchProvidersExplicit = true
		}
	}
	if v := strings.TrimSpace(getenv(spec.EnvFetchStrategy)); v != "" {
		cfg.Fetch.Strategy = v
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
	}
	return nil
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
