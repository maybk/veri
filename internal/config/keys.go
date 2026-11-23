package config

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"

	"veri/internal/core"
	"veri/internal/spec"
)

// ValueType 描述配置项在 TOML 与 CLI 交互中的值类型。
type ValueType string

// 配置项支持的标量与列表值类型常量。
const (
	TypeString      ValueType = "string"
	TypeBool        ValueType = "bool"
	TypeInt         ValueType = "int"
	TypeFloat       ValueType = "float"
	TypeDuration    ValueType = "duration"
	TypeByteSize    ValueType = "bytesize"
	TypeStringSlice ValueType = "[]string"
)

// KeyInfo 描述配置树中单个叶子键的规范路径、类型、安全属性与补全元信息。
type KeyInfo struct {
	Key        string
	Section    string
	Field      string
	Provider   string
	Type       ValueType
	Secret     bool
	EnvVars    []string
	EnumValues []string
}

// ValueResult 描述单键查询（veri config get）返回的脱敏值与来源元数据。
type ValueResult struct {
	Key       string `json:"key"`
	Value     any    `json:"value"`
	Formatted string `json:"-"`
	Source    string `json:"source"`
	EnvVar    string `json:"env_var,omitempty"`
}

type keyEntry struct {
	info       KeyInfo
	fieldIndex []int
}

var (
	durationReflectType = reflect.TypeFor[Duration]()
	byteSizeReflectType = reflect.TypeFor[ByteSize]()
	strSliceReflectType = reflect.TypeFor[[]string]()

	// keyEntriesCached 按字典序惰性缓存 Config 全部配置键与反射字段偏移（零 init、并发安全）。
	keyEntriesCached = sync.OnceValue(func() []keyEntry {
		var zero Config
		var out []keyEntry
		collectStructKeys(reflect.TypeOf(zero), nil, nil, "", &out)
		slices.SortFunc(out, func(a, b keyEntry) int {
			return strings.Compare(a.info.Key, b.info.Key)
		})
		return out
	})

	keyIndexCached = sync.OnceValue(func() map[string]keyEntry {
		all := keyEntriesCached()
		idx := make(map[string]keyEntry, len(all))
		for _, entry := range all {
			idx[entry.info.Key] = entry
		}
		return idx
	})
)

// AllKeys 通过反射 Config 结构体 toml 标签返回按字典序排列的全部合法配置键（单点真相）。
func AllKeys() []KeyInfo {
	entries := keyEntriesCached()
	out := make([]KeyInfo, len(entries))
	for i, e := range entries {
		out[i] = e.info
	}
	return out
}

func collectStructKeys(t reflect.Type, path []string, fieldIndex []int, provider string, out *[]keyEntry) {
	for f := range t.Fields() {
		tag := tomlTagName(f)
		if tag == "" || tag == "-" {
			continue
		}
		nextPath := append(slices.Clone(path), tag)
		nextIndex := append(slices.Clone(fieldIndex), f.Index...)
		nextProvider := provider
		if len(nextPath) == 2 && nextPath[0] == "providers" {
			nextProvider = tag
		}
		ft := f.Type
		if ft.Kind() == reflect.Struct && ft != durationReflectType {
			collectStructKeys(ft, nextPath, nextIndex, nextProvider, out)
			continue
		}
		valType, ok := classifyReflectType(ft)
		if !ok {
			continue
		}
		fullKey := strings.Join(nextPath, ".")
		section := strings.Join(nextPath[:len(nextPath)-1], ".")
		*out = append(*out, keyEntry{
			info: KeyInfo{
				Key:        fullKey,
				Section:    section,
				Field:      tag,
				Provider:   nextProvider,
				Type:       valType,
				Secret:     nextProvider != "" && isSecretTOMLTag(tag),
				EnvVars:    keyEnvVars(fullKey, nextProvider, tag),
				EnumValues: keyEnumValues(fullKey, valType),
			},
			fieldIndex: nextIndex,
		})
	}
}

func classifyReflectType(t reflect.Type) (ValueType, bool) {
	switch t {
	case durationReflectType:
		return TypeDuration, true
	case byteSizeReflectType:
		return TypeByteSize, true
	case strSliceReflectType:
		return TypeStringSlice, true
	}
	switch t.Kind() {
	case reflect.String:
		return TypeString, true
	case reflect.Bool:
		return TypeBool, true
	case reflect.Int:
		return TypeInt, true
	case reflect.Float64:
		return TypeFloat, true
	default:
		return "", false
	}
}

func keyEnvVars(fullKey, provider, fieldTag string) []string {
	if provider != "" && isSecretTOMLTag(fieldTag) {
		return spec.ProviderCredentialEnvVars(provider)
	}
	switch fullKey {
	case "ui.lang":
		return []string{spec.EnvLang}
	case "search.providers":
		return []string{spec.EnvProviders}
	case "search.timeout":
		return []string{spec.EnvTimeout}
	case "fetch.providers":
		return []string{spec.EnvFetchProviders}
	case "fetch.strategy":
		return []string{spec.EnvFetchStrategy}
	default:
		return nil
	}
}

func keyEnumValues(fullKey string, vt ValueType) []string {
	if vt == TypeBool {
		return []string{"true", "false"}
	}
	if allowed := strictEnumAllowed(fullKey); len(allowed) > 0 {
		return slices.Clone(allowed)
	}
	switch fullKey {
	case "ui.lang":
		return []string{"auto", "zh", "en"}
	case "search.strategy":
		return []string{string(core.StrategyAuto), string(core.StrategyFusion)}
	case "search.detail":
		return []string{string(core.DetailSnippet), string(core.DetailSummary), string(core.DetailContent)}
	case "fetch.strategy":
		return []string{string(core.FetchAPIFirst), string(core.FetchSmartLocal), string(core.FetchLocalOnly), string(core.FetchAPIOnly)}
	case "fetch.user_agent":
		return []string{"browser", "veri"}
	default:
		return nil
	}
}

// LookupKey 按规范点号路径 O(1) 查找配置键元数据。
func LookupKey(raw string) (KeyInfo, bool) {
	entry, ok := lookupKeyEntry(raw)
	return entry.info, ok
}

func lookupKeyEntry(raw string) (keyEntry, bool) {
	target := strings.ToLower(strings.TrimSpace(raw))
	if target == "" {
		return keyEntry{}, false
	}
	entry, ok := keyIndexCached()[target]
	return entry, ok
}

// SuggestKey 为拼写错误或省略 providers. 前缀的键名推荐最相近的合法配置键。
func SuggestKey(raw string) string {
	cand := strings.ToLower(strings.TrimSpace(raw))
	if cand == "" {
		return ""
	}
	if info, ok := LookupKey("providers." + cand); ok {
		return info.Key
	}
	// 兼容厂商别名省略前缀（如 tencent-wsa.edition -> providers.tencent_wsa.edition）。
	if head, tail, ok := strings.Cut(cand, "."); ok {
		if norm, exists := spec.NormalizeProviderName(head); exists {
			if info, found := LookupKey("providers." + norm + "." + tail); found {
				return info.Key
			}
		}
	}
	bestKey := ""
	bestDist := 3
	for _, entry := range keyEntriesCached() {
		key := entry.info.Key
		d := editDistance(cand, key)
		if d < bestDist {
			bestDist = d
			bestKey = key
		}
		if short, ok := strings.CutPrefix(key, "providers."); ok {
			if d2 := editDistance(cand, short); d2 < bestDist {
				bestDist = d2
				bestKey = key
			}
		}
	}
	return bestKey
}

func unknownKeyError(raw string) *core.Error {
	trimmed := strings.TrimSpace(raw)
	if suggest := SuggestKey(trimmed); suggest != "" {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown configuration key %q (did you mean %q?)", trimmed, suggest)).
			WithHint(fmt.Sprintf("Use %q, or run `veri config show` to inspect supported keys.", suggest)).
			WithI18n("err.config_unknown_key_suggest", "hint.config_unknown_key_suggest").
			WithParam("key", trimmed).
			WithParam("suggest", suggest)
	}
	return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unknown configuration key %q", trimmed)).
		WithHint("Run `veri config show` to inspect supported configuration keys.").
		WithI18n("err.config_unknown_key", "hint.config_unknown_key").
		WithParam("key", trimmed)
}

// GetValue 读取单个配置项的脱敏生效值及生效来源（env / vault / config / default）。
func GetValue(loaded *Loaded, rawKey string) (ValueResult, error) {
	entry, ok := lookupKeyEntry(rawKey)
	if !ok {
		return ValueResult{}, unknownKeyError(rawKey)
	}
	if loaded == nil {
		return ValueResult{}, core.NewError(core.CodeInternal, "loaded configuration must not be nil")
	}

	info := entry.info
	redacted := loaded.RedactedConfig()
	val, formatted, ok := readConfigEntry(&redacted, entry)
	if !ok {
		return ValueResult{}, core.NewError(core.CodeInternal, fmt.Sprintf("failed to read configuration key %q", info.Key))
	}

	source := "default"
	envVar := ""
	if info.Secret && info.Provider != "" {
		cred := loaded.Credentials[info.Provider]
		switch {
		case !strings.Contains(cred.Source, "+") && strings.HasPrefix(cred.Source, spec.CredSourceEnvPrefix):
			source = "env"
			envVar = strings.TrimPrefix(cred.Source, spec.CredSourceEnvPrefix)
		case cred.Source == spec.SecretsFileName:
			source = "vault"
		case loaded.DefinedKeys[info.Key]:
			source = "config"
		}
	} else if ev := loaded.EnvOverrides[info.Key]; ev != "" {
		source = "env"
		envVar = ev
	} else if loaded.DefinedKeys[info.Key] {
		source = "config"
	}

	return ValueResult{
		Key:       info.Key,
		Value:     val,
		Formatted: formatted,
		Source:    source,
		EnvVar:    envVar,
	}, nil
}

// OverridesMap 返回当前 config.toml 中显式定义的脱敏覆盖树（用于 veri config show --overrides --json）。
func OverridesMap(loaded *Loaded) map[string]any {
	out := make(map[string]any)
	forEachOverride(loaded, func(info KeyInfo, val any) {
		parts := strings.Split(info.Key, ".")
		cur := out
		for _, seg := range parts[:len(parts)-1] {
			next, exists := cur[seg].(map[string]any)
			if !exists {
				next = make(map[string]any)
				cur[seg] = next
			}
			cur = next
		}
		cur[parts[len(parts)-1]] = val
	})
	return out
}

// FormatOverridesTOML 将当前 config.toml 中显式定义的覆盖项渲染为脱敏后的紧凑 TOML 文本。
func FormatOverridesTOML(loaded *Loaded) (string, int) {
	var b strings.Builder
	curSection := ""
	count := 0
	forEachOverride(loaded, func(info KeyInfo, val any) {
		if info.Section != curSection {
			if count > 0 {
				b.WriteByte('\n')
			}
			b.WriteString("[" + info.Section + "]\n")
			curSection = info.Section
		}
		b.WriteString(info.Field + " = " + formatTOMLLiteral(info.Type, val) + "\n")
		count++
	})
	return b.String(), count
}

func forEachOverride(loaded *Loaded, fn func(KeyInfo, any)) {
	if loaded == nil || len(loaded.DefinedKeys) == 0 {
		return
	}
	fileRedacted := loaded.fileConfig.Redacted(nil)
	for _, entry := range keyEntriesCached() {
		info := entry.info
		if !loaded.DefinedKeys[info.Key] {
			continue
		}
		if val, _, ok := readConfigEntry(&fileRedacted, entry); ok {
			fn(info, val)
		}
	}
}

func readConfigEntry(cfg *Config, entry keyEntry) (any, string, bool) {
	rv := reflect.ValueOf(cfg).Elem().FieldByIndex(entry.fieldIndex)
	return exportReflectValue(entry.info.Type, rv)
}

func exportReflectValue(vt ValueType, rv reflect.Value) (any, string, bool) {
	switch vt {
	case TypeString:
		s := rv.String()
		return s, s, true
	case TypeBool:
		b := rv.Bool()
		return b, strconv.FormatBool(b), true
	case TypeInt:
		n := int(rv.Int())
		return n, strconv.Itoa(n), true
	case TypeFloat:
		f := rv.Float()
		return f, strconv.FormatFloat(f, 'f', -1, 64), true
	case TypeDuration:
		d, ok := reflect.TypeAssert[Duration](rv)
		if !ok {
			return nil, "", false
		}
		s := d.String()
		return s, s, true
	case TypeByteSize:
		bs, ok := reflect.TypeAssert[ByteSize](rv)
		if !ok {
			return nil, "", false
		}
		raw, _ := bs.MarshalText()
		s := string(raw)
		return s, s, true
	case TypeStringSlice:
		items, ok := reflect.TypeAssert[[]string](rv)
		if !ok {
			return nil, "", false
		}
		cp := make([]string, len(items))
		copy(cp, items)
		if len(cp) == 0 {
			return cp, "[]", true
		}
		return cp, strings.Join(cp, ","), true
	default:
		return nil, "", false
	}
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	la, lb := len(ra), len(rb)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	d := make([][]int, la+1)
	for i := range d {
		d[i] = make([]int, lb+1)
		d[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		d[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			v := min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				v = min(v, d[i-2][j-2]+1)
			}
			d[i][j] = v
		}
	}
	return d[la][lb]
}
