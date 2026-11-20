package i18n

import (
	"os"
	"strings"
	"sync"
)

// Locale 表示解析后的受支持人类界面语言标识。
type Locale string

const (
	// LocaleZhCN 简体中文界面。
	LocaleZhCN Locale = "zh-CN"
	// LocaleEnUS 英文界面（默认回退语言）。
	LocaleEnUS Locale = "en-US"
)

// SupportedLocales 返回内置支持的界面语言列表。
func SupportedLocales() []Locale {
	return []Locale{LocaleZhCN, LocaleEnUS}
}

var (
	platformOnce sync.Once
	platformTags []string
)

// ResolveOptions 汇总各优先级的界面语言输入，不硬编码特定应用的环境变量名。
type ResolveOptions struct {
	FlagLang   string              // 优先级 1：命令行显式参数（如 --ui-lang）
	EnvLangVar string              // 优先级 2：应用专属语言环境变量名（如 "VERI_LANG"，可为空）
	ConfigLang string              // 优先级 3：配置文件语言项（非 auto 时）
	Getenv     func(string) string // 环境变量读取器，为空时使用 os.Getenv
	DetectOS   func() []string     // 平台级语言探测函数，为空时惰性调用平台实现
}

// Resolve 按跨平台统一优先级解析最终人类终端界面语言并返回命中的来源描述：
//  1. 命令行显式参数（FlagLang）
//  2. 应用专属环境变量（EnvLangVar）
//  3. 配置文件（ConfigLang）
//  4. POSIX / GNU 环境变量：
//     - 若 LC_ALL / LC_MESSAGES 为 C / POSIX / C.*，或 LANG 为纯 ASCII 的 C / POSIX，则强制返回 en-US 并忽略 LANGUAGE；
//     - 若仅设置 LANG=C.UTF-8（或 UTF-8）等中性编码兜底值，则不阻断 LANGUAGE 偏好与操作系统语言探测；
//     - 否则优先按 LANGUAGE 列表匹配，其次匹配生效的 POSIX 消息类别（LC_ALL -> LC_MESSAGES -> LANG）
//  5. 操作系统无 CGO 语言偏好兜底（macOS / Linux / Windows）
//  6. 默认保底 en-US
func Resolve(opts ResolveOptions) (Locale, string) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}

	if loc, ok := matchExplicitSetting(opts.FlagLang); ok {
		return loc, "flag:--ui-lang"
	}

	if envName := strings.TrimSpace(opts.EnvLangVar); envName != "" {
		if raw := strings.TrimSpace(getenv(envName)); raw != "" {
			if loc, ok := matchExplicitSetting(raw); ok {
				return loc, "env:" + envName
			}
		}
	}

	if loc, ok := matchExplicitSetting(opts.ConfigLang); ok {
		return loc, "config:ui.lang"
	}

	posixKey, posixVal := effectivePosixMessageLocale(getenv)
	if posixVal != "" && isCPosixLocale(posixVal) && (posixKey != "LANG" || !isUTF8NeutralLocale(posixVal)) {
		return LocaleEnUS, "env:" + posixKey + "=C"
	}

	if rawLanguage := strings.TrimSpace(getenv("LANGUAGE")); rawLanguage != "" {
		if loc, ok := resolveFromTagList([]string{rawLanguage}); ok {
			return loc, "env:LANGUAGE"
		}
		if hasConcreteLocaleTag(rawLanguage) {
			return LocaleEnUS, "default"
		}
	}

	if posixVal != "" && !isNeutralOrCPosix(posixVal) {
		if loc, ok := matchLocaleTag(posixVal); ok {
			return loc, "env:" + posixKey
		}
		return LocaleEnUS, "default"
	}

	detectFn := opts.DetectOS
	if detectFn == nil {
		detectFn = cachedOSLocaleTags
	}
	if tags := detectFn(); len(tags) > 0 {
		if loc, ok := resolveFromTagList(tags); ok {
			return loc, "os"
		}
	}

	return LocaleEnUS, "default"
}

func cachedOSLocaleTags() []string {
	platformOnce.Do(func() {
		platformTags = detectPlatformLocaleTags()
	})
	return platformTags
}

// ValidateExplicit 校验显式传入的界面语言选项（允许为空、auto、system、zh*、en*）。
func ValidateExplicit(raw string) error {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	if cleaned == "" || cleaned == "auto" || cleaned == "system" {
		return nil
	}
	if strings.ContainsAny(cleaned, `"'()[] ,`) {
		return errUnsupportedLocale(raw)
	}
	if _, ok := matchLocaleTag(cleaned); !ok {
		return errUnsupportedLocale(raw)
	}
	return nil
}

type unsupportedLocaleError string

func (e unsupportedLocaleError) Error() string {
	return "unsupported UI language " + `"` + string(e) + `"` + " (valid: auto, zh, en)"
}

func errUnsupportedLocale(raw string) error {
	return unsupportedLocaleError(strings.TrimSpace(raw))
}

func matchExplicitSetting(raw string) (Locale, bool) {
	cleaned := strings.ToLower(strings.TrimSpace(raw))
	if cleaned == "" || cleaned == "auto" || cleaned == "system" {
		return "", false
	}
	return matchLocaleTag(cleaned)
}

func effectivePosixMessageLocale(getenv func(string) string) (string, string) {
	for _, catKey := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if raw := strings.TrimSpace(getenv(catKey)); raw != "" {
			return catKey, raw
		}
	}
	return "", ""
}

func cleanLocaleToken(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	return strings.Trim(s, `"'()[] ,`)
}

func isCPosixLocale(raw string) bool {
	s := cleanLocaleToken(raw)
	return s == "c" || s == "posix" ||
		strings.HasPrefix(s, "c.") || strings.HasPrefix(s, "c-") ||
		strings.HasPrefix(s, "posix.") || strings.HasPrefix(s, "posix-")
}

func isUTF8NeutralLocale(raw string) bool {
	s := cleanLocaleToken(raw)
	if idx := strings.IndexAny(s, "@;"); idx >= 0 {
		s = s[:idx]
	}
	switch s {
	case "utf-8", "utf8",
		"c.utf-8", "c.utf8", "c-utf-8", "c-utf8",
		"posix.utf-8", "posix.utf8", "posix-utf-8", "posix-utf8":
		return true
	default:
		return false
	}
}

func isNeutralOrCPosix(raw string) bool {
	return isCPosixLocale(raw) || isUTF8NeutralLocale(raw)
}

func hasConcreteLocaleTag(rawList string) bool {
	for part := range strings.SplitSeq(rawList, ":") {
		tok := cleanLocaleToken(part)
		if tok == "" || isNeutralOrCPosix(tok) {
			continue
		}
		return true
	}
	return false
}

func resolveFromTagList(tags []string) (Locale, bool) {
	for _, tag := range tags {
		for part := range strings.SplitSeq(tag, ":") {
			if loc, ok := matchLocaleTag(part); ok {
				return loc, true
			}
		}
	}
	return "", false
}

func matchLocaleTag(raw string) (Locale, bool) {
	cleaned := cleanLocaleToken(raw)
	if cleaned == "" {
		return "", false
	}
	if idx := strings.IndexAny(cleaned, ".@;"); idx >= 0 {
		cleaned = cleaned[:idx]
	}
	cleaned = strings.ReplaceAll(cleaned, "_", "-")
	base, _, _ := strings.Cut(cleaned, "-")
	switch base {
	case "zh":
		return LocaleZhCN, true
	case "en":
		return LocaleEnUS, true
	default:
		return "", false
	}
}
