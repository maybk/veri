package config

import (
	"fmt"
	"reflect"
	"slices"
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
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
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
	case "cache.dir":
		return []string{spec.EnvCacheDir}
	case "cache.enabled", "cache.cli.enabled", "cache.mcp.enabled":
		return []string{spec.EnvNoCache}
	case "cache.fetch_ttl", "cache.cli.fetch_ttl", "cache.mcp.fetch_ttl":
		return []string{spec.EnvCacheFetchTTL}
	case "cache.search_ttl", "cache.cli.search_ttl", "cache.mcp.search_ttl":
		return []string{spec.EnvCacheSearchTTL}
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
