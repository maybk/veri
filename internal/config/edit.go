package config

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"veri/clikit/redact"
	"veri/clikit/storage"
	"veri/internal/core"
	"veri/internal/spec"
)

// SetResult 记录单键稀疏写入后的脱敏结果与环境变量遮蔽提醒。
type SetResult struct {
	Path          string
	Key           string
	Formatted     string
	ShadowedByEnv string
}

// UnsetResult 记录单键移除后的状态。
type UnsetResult struct {
	Path        string
	Key         string
	Changed     bool
	RemovedFile bool
}

// SetValue 将单个配置项以稀疏覆盖方式写入 config.toml，完整保留现有注释与排版，并在落盘前通过完整解码与合法性校验。
// 对 api_key / token 字段严禁写入明文，仅允许 env:<VAR> 引用。
func SetValue(path, rawKey, rawVal string, getenv func(string) string) (SetResult, error) {
	entry, ok := lookupKeyEntry(rawKey)
	if !ok {
		return SetResult{}, unknownKeyError(rawKey)
	}
	info := entry.info
	if getenv == nil {
		getenv = os.Getenv
	}
	if info.Secret {
		if err := validateSecretEnvRef(info, rawVal); err != nil {
			return SetResult{}, err
		}
	}

	parsed, err := parseTypedInput(info, rawVal)
	if err != nil {
		return SetResult{}, err
	}

	// 预先规范化 Provider 列表别名（如 tencent-wsa -> tencent_wsa），确保落盘始终使用规范标识。
	if info.Type == TypeStringSlice {
		items, ok := parsed.([]string)
		if !ok {
			return SetResult{}, core.NewError(core.CodeInternal, fmt.Sprintf("unexpected slice value type for key %q", info.Key))
		}
		norm, nErr := normalizeListKeyValue(info.Key, items)
		if nErr != nil {
			return SetResult{}, nErr
		}
		parsed = norm
	}

	origRaw, fileMode, exists, err := readExistingConfigFile(path)
	if err != nil {
		return SetResult{}, err
	}
	var (
		origCfg  Config
		origMeta toml.MetaData
	)
	if exists {
		var decErr error
		origCfg, origMeta, decErr = decodeKnownTOML(path, origRaw)
		if decErr != nil {
			return SetResult{}, decErr
		}
	}

	lineEnding := detectLineEnding(origRaw)
	literal := formatTOMLLiteral(info.Type, parsed)
	edited := applySetLineEdit(normalizeNewlines(origRaw), info.Section, info.Field, literal)

	rawCandidate, candidateCfg, candidateMeta, err := verifyEditedTOML(path, edited, entry, parsed, true)
	if err != nil {
		return SetResult{}, err
	}
	if exists {
		if err := verifyUntouchedKeysPreserved(path, origCfg, rawCandidate, origMeta, candidateMeta, map[string]bool{info.Key: true}); err != nil {
			return SetResult{}, err
		}
	}

	outBytes := restoreLineEnding(edited, lineEnding)
	perm := os.FileMode(0o600)
	if exists && fileMode.Perm() != 0 {
		perm = fileMode.Perm()
	}
	if err := storage.WriteFileAtomic(path, []byte(outBytes), 0o700, perm); err != nil {
		return SetResult{}, core.WrapError(core.CodeInternal, fmt.Sprintf("failed to write config %q", path), err)
	}

	redactedCfg := candidateCfg.Redacted(nil)
	_, formatted, _ := readConfigEntry(&redactedCfg, entry)
	return SetResult{
		Path:          path,
		Key:           info.Key,
		Formatted:     formatted,
		ShadowedByEnv: activeShadowingEnv(info, getenv),
	}, nil
}

// UnsetValue 从 config.toml 移除单个配置项覆盖，使该项恢复跟随二进制内置默认值升级；
// 移除后若所属 section 为空则同步清理空表头，若文件彻底为空且为普通文件则移除空文件。
func UnsetValue(path, rawKey string) (UnsetResult, error) {
	entry, ok := lookupKeyEntry(rawKey)
	if !ok {
		return UnsetResult{}, unknownKeyError(rawKey)
	}
	info := entry.info

	origRaw, fileMode, exists, err := readExistingConfigFile(path)
	if err != nil {
		return UnsetResult{}, err
	}
	if !exists {
		return UnsetResult{Path: path, Key: info.Key, Changed: false}, nil
	}

	// 先严格解码并检查未知键与目标键定义，允许用 unset 修复单个越界值，未定义时幂等返回。
	origCfg, origMeta, decErr := decodeKnownTOML(path, origRaw)
	if decErr != nil {
		return UnsetResult{}, decErr
	}
	if !origMeta.IsDefined(strings.Split(info.Key, ".")...) {
		return UnsetResult{Path: path, Key: info.Key, Changed: false}, nil
	}

	lineEnding := detectLineEnding(origRaw)
	edited, removed := applyUnsetLineEdit(normalizeNewlines(origRaw), info.Section, info.Field)
	if !removed {
		return UnsetResult{}, manualEditError(info.Key, path, "inline or dotted table syntax requires manual edit")
	}
	rawCandidate, _, candidateMeta, err := verifyEditedTOML(path, edited, entry, nil, false)
	if err != nil {
		return UnsetResult{}, err
	}
	if err := verifyUntouchedKeysPreserved(path, origCfg, rawCandidate, origMeta, candidateMeta, map[string]bool{info.Key: true}); err != nil {
		return UnsetResult{}, err
	}

	if strings.TrimSpace(edited) == "" {
		if fi, lErr := os.Lstat(path); lErr == nil && fi.Mode().IsRegular() {
			if rmErr := os.Remove(path); rmErr != nil {
				return UnsetResult{}, core.WrapError(core.CodeInternal, fmt.Sprintf("failed to remove empty config %q", path), rmErr)
			}
			return UnsetResult{Path: path, Key: info.Key, Changed: true, RemovedFile: true}, nil
		}
	}

	outBytes := restoreLineEnding(edited, lineEnding)
	perm := os.FileMode(0o600)
	if fileMode.Perm() != 0 {
		perm = fileMode.Perm()
	}
	if err := storage.WriteFileAtomic(path, []byte(outBytes), 0o700, perm); err != nil {
		return UnsetResult{}, core.WrapError(core.CodeInternal, fmt.Sprintf("failed to write config %q", path), err)
	}
	return UnsetResult{Path: path, Key: info.Key, Changed: true}, nil
}

func validateSecretEnvRef(info KeyInfo, rawVal string) error {
	trimmed := strings.TrimSpace(rawVal)
	prefixLen := len(spec.CredSourceEnvPrefix)
	if len(trimmed) > prefixLen && strings.EqualFold(trimmed[:prefixLen], spec.CredSourceEnvPrefix) {
		envName := strings.TrimSpace(trimmed[prefixLen:])
		if isValidEnvVarName(envName) {
			return nil
		}
	}
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("refusing to write plaintext credential to %s", info.Key)).
		WithHint(fmt.Sprintf("Use `veri key set %s` to encrypt and store the key securely, or pass `env:VAR_NAME`.", info.Provider)).
		WithI18n("err.config_secret_plaintext", "hint.config_secret_plaintext").
		WithParam("key", info.Key).
		WithParam("provider", info.Provider)
}

func isValidEnvVarName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		isLetter := (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == '_'
		isDigit := c >= '0' && c <= '9'
		if i == 0 && !isLetter {
			return false
		}
		if i > 0 && !isLetter && !isDigit {
			return false
		}
	}
	return true
}

func parseTypedInput(info KeyInfo, raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)
	switch info.Type {
	case TypeString:
		if info.Secret {
			// 规范化前缀小写 env: 并保留环境变量名大小写。
			return spec.CredSourceEnvPrefix + strings.TrimSpace(trimmed[len(spec.CredSourceEnvPrefix):]), nil
		}
		// 对已知枚举项做大小写规范化（如 ZHIHU -> zhihu），自定义字符串保持原样。
		for _, ev := range info.EnumValues {
			if strings.EqualFold(trimmed, ev) {
				return ev, nil
			}
		}
		return trimmed, nil
	case TypeBool:
		switch strings.ToLower(trimmed) {
		case "true":
			return true, nil
		case "false":
			return false, nil
		default:
			return nil, invalidValueError(info.Key, raw, "true | false")
		}
	case TypeInt:
		n, err := strconv.Atoi(trimmed)
		if err != nil {
			return nil, invalidValueError(info.Key, raw, "integer")
		}
		return n, nil
	case TypeFloat:
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, invalidValueError(info.Key, raw, "number")
		}
		return f, nil
	case TypeDuration:
		d, err := time.ParseDuration(trimmed)
		if err != nil || d < 0 {
			return nil, invalidValueError(info.Key, raw, "duration (e.g. 20s, 5m)")
		}
		return Duration{d}, nil
	case TypeByteSize:
		bs, err := ParseByteSize(trimmed)
		if err != nil {
			return nil, invalidValueError(info.Key, raw, "byte size (e.g. 5MB, 512KB)")
		}
		return bs, nil
	case TypeStringSlice:
		return parseStringSliceInput(info.Key, trimmed)
	default:
		return nil, core.NewError(core.CodeInternal, fmt.Sprintf("unsupported type for key %q", info.Key))
	}
}

func parseStringSliceInput(key, trimmed string) ([]string, error) {
	if trimmed == "" || trimmed == "[]" {
		return []string{}, nil
	}
	if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
		var box struct {
			V []string `toml:"v"`
		}
		if _, err := toml.Decode("v = "+trimmed+"\n", &box); err != nil {
			return nil, invalidValueError(key, trimmed, "comma-separated list or TOML string array")
		}
		out := make([]string, 0, len(box.V))
		for _, item := range box.V {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	}
	out := make([]string, 0, 4)
	for part := range strings.SplitSeq(trimmed, ",") {
		if s := strings.TrimSpace(part); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

func normalizeListKeyValue(key string, items []string) ([]string, error) {
	switch key {
	case "search.providers", "search.providers_global", "search.fusion_providers", "search.fusion_providers_global":
		return normalizeProviderList(key, items, spec.RoleSearch)
	case "fetch.providers", "fetch.providers_global":
		return normalizeProviderList(key, items, spec.RoleFetch)
	default:
		return items, nil
	}
}

func invalidValueError(key, raw, expected string) *core.Error {
	safeRaw := scrubConfigScalar(raw)
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid value %q for %s: expected %s", safeRaw, key, expected)).
		WithMsgKey("err.config_invalid_value").
		WithParam("key", key).
		WithParam("value", safeRaw).
		WithParam("expected", expected)
}

func invalidTOMLError(path string, decErr error) *core.Error {
	safeDetail := redact.Scrub(decErr.Error(), nil)
	safeCause := decErr
	if safeDetail != decErr.Error() {
		safeCause = errors.New(safeDetail)
	}
	return core.WrapError(core.CodeInvalidArgument, fmt.Sprintf("invalid TOML in %q: %s", path, safeDetail), safeCause).
		WithMsgKey("err.config_invalid_toml").
		WithParam("path", path).
		WithParam("detail", safeDetail)
}

func unknownConfigKeysError(path string, undecoded []toml.Key) *core.Error {
	keys := make([]string, len(undecoded))
	for i, k := range undecoded {
		keys[i] = k.String()
	}
	keyList := strings.Join(keys, ", ")
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown configuration key(s) in %q: %s", path, keyList)).
		WithHint("Check spelling in config.toml or run `veri config show` to inspect supported fields.").
		WithI18n("err.config_unknown_keys", "hint.unknown_config_keys").
		WithParam("path", path).
		WithParam("keys", keyList)
}

func manualEditError(key, path, detail string) *core.Error {
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("cannot safely update %q in %q (%s)", key, path, detail)).
		WithMsgKey("err.config_edit_manual").
		WithParam("key", key).
		WithParam("path", path)
}

func decodeKnownTOML(path, raw string) (Config, toml.MetaData, error) {
	cfg := Default()
	meta, err := toml.Decode(raw, &cfg)
	if err != nil {
		return Config{}, toml.MetaData{}, invalidTOMLError(path, err)
	}
	if undecoded := meta.Undecoded(); len(undecoded) > 0 {
		return Config{}, toml.MetaData{}, unknownConfigKeysError(path, undecoded)
	}
	return cfg, meta, nil
}

func readExistingConfigFile(path string) (string, os.FileMode, bool, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, core.WrapError(core.CodeInvalidArgument, fmt.Sprintf("cannot access config %q", path), err).
			WithMsgKey("err.config_read_failed").
			WithParam("path", path)
	}
	if fi.IsDir() {
		return "", 0, false, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("config path %q is a directory", path)).
			WithMsgKey("err.config_is_dir").
			WithParam("path", path)
	}
	if fi.Size() > maxConfigFileBytes {
		return "", 0, false, core.NewError(core.CodeInvalidArgument, fmt.Sprintf("config file %q exceeds %d bytes", path, maxConfigFileBytes)).
			WithMsgKey("err.config_read_failed").
			WithParam("path", path)
	}
	raw, err := os.ReadFile(filepath.Clean(path)) // #nosec G304 -- local CLI user-specified config file path
	if err != nil {
		return "", 0, false, core.WrapError(core.CodeInvalidArgument, fmt.Sprintf("failed to read config %q", path), err).
			WithMsgKey("err.config_read_failed").
			WithParam("path", path)
	}
	return string(raw), fi.Mode(), true, nil
}

func verifyEditedTOML(path, edited string, entry keyEntry, expected any, wantDefined bool) (Config, Config, toml.MetaData, error) {
	info := entry.info
	rawCfg, meta, err := decodeKnownTOML(path, edited)
	if err != nil {
		return Config{}, Config{}, toml.MetaData{}, err
	}
	validated := rawCfg
	if err := validateConfig(&validated); err != nil {
		return Config{}, Config{}, toml.MetaData{}, err
	}
	parts := strings.Split(info.Key, ".")
	if wantDefined {
		if !meta.IsDefined(parts...) {
			return Config{}, Config{}, toml.MetaData{}, manualEditError(info.Key, path, "failed to apply cleanly")
		}
		got, _, ok := readConfigEntry(&validated, entry)
		if !ok || !valuesEqual(info.Type, got, expected) {
			return Config{}, Config{}, toml.MetaData{}, manualEditError(info.Key, path, "conflicting table syntax")
		}
	} else if meta.IsDefined(parts...) {
		return Config{}, Config{}, toml.MetaData{}, manualEditError(info.Key, path, "duplicate or inline definition")
	}
	return rawCfg, validated, meta, nil
}

// verifyUntouchedKeysPreserved 强校验编辑后所有非目标配置键保持定义且值不变。
func verifyUntouchedKeysPreserved(path string, origCfg, candidateCfg Config, origMeta, newMeta toml.MetaData, skipKeys map[string]bool) error {
	for _, entry := range keyEntriesCached() {
		if skipKeys[entry.info.Key] {
			continue
		}
		parts := strings.Split(entry.info.Key, ".")
		if !origMeta.IsDefined(parts...) {
			continue
		}
		if !newMeta.IsDefined(parts...) {
			return manualEditError(entry.info.Key, path, "unexpected key loss during edit")
		}
		origVal, _, _ := readConfigEntry(&origCfg, entry)
		newVal, _, _ := readConfigEntry(&candidateCfg, entry)
		if !valuesEqual(entry.info.Type, newVal, origVal) {
			return manualEditError(entry.info.Key, path, "unexpected value change during edit")
		}
	}
	return nil
}

func valuesEqual(vt ValueType, got, expected any) bool {
	switch vt {
	case TypeDuration:
		if d, ok := expected.(Duration); ok {
			return got == d.String()
		}
		return got == expected
	case TypeByteSize:
		if bs, ok := expected.(ByteSize); ok {
			raw, _ := bs.MarshalText()
			return got == string(raw)
		}
		return got == expected
	default:
		return reflect.DeepEqual(got, expected)
	}
}

func activeShadowingEnv(info KeyInfo, getenv func(string) string) string {
	for _, ev := range info.EnvVars {
		v := strings.TrimSpace(getenv(ev))
		if v == "" {
			continue
		}
		if ev == spec.EnvNoCache {
			if disable, ok := parseEnvBool(v); !ok || !disable {
				continue
			}
		}
		return ev
	}
	return ""
}
