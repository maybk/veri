package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"veri/internal/core"
	"veri/internal/spec"
)

// ValueResult 描述单键查询（veri config get）返回的脱敏值与来源元数据。
type ValueResult struct {
	Key       string `json:"key"`
	Value     any    `json:"value"`
	Formatted string `json:"-"`
	Source    string `json:"source"`
	EnvVar    string `json:"env_var,omitempty"`
}

// GetValue 读取单个配置项的脱敏生效值及生效来源（env / vault / config / inherited / default）。
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
	inherited := false
	val, formatted, ok := readConfigEntry(&redacted, entry)
	if !ok {
		if parentKey := inheritedParentKey(info.Key); parentKey != "" {
			if parentEntry, pFound := lookupKeyEntry(parentKey); pFound {
				val, formatted, ok = readConfigEntry(&redacted, parentEntry)
				inherited = ok
			}
		}
	}
	if !ok {
		return ValueResult{}, core.NewError(core.CodeInternal, fmt.Sprintf("failed to read configuration key %q", info.Key))
	}

	source := "default"
	if inherited {
		source = "inherited"
	}
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

func inheritedParentKey(key string) string {
	switch key {
	case "cache.cli.enabled", "cache.mcp.enabled":
		return "cache.enabled"
	case "cache.cli.fetch_ttl", "cache.mcp.fetch_ttl":
		return "cache.fetch_ttl"
	case "cache.cli.search_ttl", "cache.mcp.search_ttl":
		return "cache.search_ttl"
	default:
		return ""
	}
}

func readConfigEntry(cfg *Config, entry keyEntry) (any, string, bool) {
	rv := reflect.ValueOf(cfg).Elem().FieldByIndex(entry.fieldIndex)
	return exportReflectValue(entry.info.Type, rv)
}

func exportReflectValue(vt ValueType, rv reflect.Value) (any, string, bool) {
	if rv.Kind() == reflect.Pointer {
		if rv.IsNil() {
			return nil, "", false
		}
		rv = rv.Elem()
	}
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
