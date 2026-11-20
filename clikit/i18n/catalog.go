package i18n

import (
	"encoding/json/v2"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
)

// ErrorText 保存结构化错误码对应的人类可读标题与修复建议模板。
type ErrorText struct {
	Title string `json:"title"`
	Hint  string `json:"hint"`
}

// Catalog 表示单种语言的终端界面词表（含 cli 文案命名空间与可选 errors 命名空间）。
type Catalog struct {
	Locale Locale               `json:"-"`
	Errors map[string]ErrorText `json:"errors"`
	CLI    map[string]string    `json:"cli"`
}

// Bundle 管理一组已加载的多语言词表（如 zh-CN 与 en-US）。
type Bundle struct {
	catalogs map[Locale]Catalog
}

var placeholderRe = regexp.MustCompile(`\{[a-zA-Z0-9_]+\}`)

// LoadBundle 从给定的 fs.FS 指定目录（如 "locales"）加载全部受支持语言的 JSON 词表。
func LoadBundle(fsys fs.FS, dir string) (*Bundle, error) {
	if fsys == nil {
		return nil, fmt.Errorf("nil locale filesystem")
	}
	cleanDir := strings.Trim(strings.TrimSpace(dir), "/")
	catalogs := make(map[Locale]Catalog, len(SupportedLocales()))
	for _, loc := range SupportedLocales() {
		filePath := string(loc) + ".json"
		if cleanDir != "" {
			filePath = path.Join(cleanDir, filePath)
		}
		raw, err := fs.ReadFile(fsys, filePath)
		if err != nil {
			return nil, fmt.Errorf("read locale file %s: %w", filePath, err)
		}
		var cat Catalog
		if err := json.Unmarshal(raw, &cat); err != nil {
			return nil, fmt.Errorf("parse locale file %s: %w", filePath, err)
		}
		cat.Locale = loc
		catalogs[loc] = cat
	}
	return &Bundle{catalogs: catalogs}, nil
}

// MustLoadBundle 加载多语言词表，若内嵌资源损坏则在启动初始化阶段直接 panic。
func MustLoadBundle(fsys fs.FS, dir string) *Bundle {
	b, err := LoadBundle(fsys, dir)
	if err != nil {
		panic(err)
	}
	return b
}

// For 返回指定语言的 Catalog；未知语言时回退至 en-US。
func (b *Bundle) For(locale Locale) Catalog {
	if b == nil {
		return Catalog{Locale: LocaleEnUS}
	}
	if cat, ok := b.catalogs[locale]; ok {
		return cat
	}
	return b.catalogs[LocaleEnUS]
}

// ValidateSymmetry 校验 zh-CN 与 en-US 两份词表的 cli / errors 键集合及 {placeholder} 集合是否 100% 对称且非空。
func (b *Bundle) ValidateSymmetry() error {
	zh := b.For(LocaleZhCN)
	en := b.For(LocaleEnUS)

	if err := compareStringMaps("cli", en.CLI, zh.CLI); err != nil {
		return err
	}
	if len(en.Errors) != len(zh.Errors) {
		return fmt.Errorf("errors key count mismatch: en-US=%d zh-CN=%d", len(en.Errors), len(zh.Errors))
	}
	for code, enErr := range en.Errors {
		zhErr, ok := zh.Errors[code]
		if !ok {
			return fmt.Errorf("zh-CN missing errors.%s", code)
		}
		if strings.TrimSpace(enErr.Title) == "" || strings.TrimSpace(zhErr.Title) == "" {
			return fmt.Errorf("errors.%s has empty title", code)
		}
		if !samePlaceholders(enErr.Title, zhErr.Title) || !samePlaceholders(enErr.Hint, zhErr.Hint) {
			return fmt.Errorf("errors.%s placeholder mismatch between en-US and zh-CN", code)
		}
	}
	return nil
}

// T 从词表的 CLI 命名空间读取文案并对成对键值参数执行单遍 {placeholder} 插值。
func (c Catalog) T(key string, params ...string) string {
	tpl, ok := c.CLI[key]
	if !ok || tpl == "" {
		return key
	}
	if len(params) == 0 {
		return tpl
	}
	kv := make(map[string]string, len(params)/2)
	for i := 0; i+1 < len(params); i += 2 {
		kv[params[i]] = params[i+1]
	}
	return Interpolate(tpl, kv)
}

// ErrorView 描述供人类终端渲染的结构化错误输入视图（与具体业务包解耦）。
type ErrorView struct {
	Code         string
	FallbackCode string
	Provider     string
	Message      string
	MessageKey   string
	RawHint      string
	HintKey      string
	Params       map[string]string
}

// Has 判断词表的 CLI 命名空间中是否存在指定非空键。
func (c Catalog) Has(key string) bool {
	if key == "" || c.CLI == nil {
		return false
	}
	return c.CLI[key] != ""
}

// Keys 返回词表 CLI 命名空间中的全部键列表（按字典序排序）。
func (c Catalog) Keys() []string {
	if len(c.CLI) == 0 {
		return nil
	}
	return slices.Sorted(maps.Keys(c.CLI))
}

// FormatError 将结构化错误视图渲染为当前终端语言的多行友好提示。
// 当提供有效的 MessageKey 或 HintKey 时优先使用当前语言的本地化模板插值，确保中英文均能展示精准的上下文专属提示。
func (c Catalog) FormatError(v ErrorView) string {
	entry, ok := c.Errors[v.Code]
	if !ok && v.FallbackCode != "" {
		entry = c.Errors[v.FallbackCode]
	}
	merged := make(map[string]string, len(v.Params)+1)
	maps.Copy(merged, v.Params)
	if v.Provider != "" {
		merged["provider"] = v.Provider
	}
	title := Interpolate(entry.Title, merged)
	if title == "" {
		title = v.Code
	}

	msg := v.Message
	if v.MessageKey != "" && c.Has(v.MessageKey) {
		msg = Interpolate(c.CLI[v.MessageKey], merged)
	}

	var resolvedHint string
	switch {
	case v.HintKey != "" && c.Has(v.HintKey):
		resolvedHint = Interpolate(c.CLI[v.HintKey], merged)
	case v.RawHint != "" && c.Locale == LocaleEnUS:
		resolvedHint = v.RawHint
	default:
		resolvedHint = Interpolate(entry.Hint, merged)
	}

	var b strings.Builder
	b.WriteString(title)
	if v.Provider != "" {
		b.WriteString(" [")
		b.WriteString(v.Provider)
		b.WriteByte(']')
	}
	if msg != "" {
		b.WriteString(": ")
		b.WriteString(msg)
	}
	if resolvedHint != "" {
		b.WriteString("\n  -> ")
		b.WriteString(resolvedHint)
	}
	return b.String()
}

// Interpolate 对模板中的 {key} 占位符执行单遍字面量替换，防止参数值引发二次展开。
func Interpolate(template string, params map[string]string) string {
	if template == "" || len(params) == 0 {
		return template
	}
	pairs := make([]string, 0, len(params)*2)
	for k, v := range params {
		pairs = append(pairs, "{"+k+"}", v)
	}
	return strings.NewReplacer(pairs...).Replace(template)
}

func compareStringMaps(ns string, enMap, zhMap map[string]string) error {
	if len(enMap) != len(zhMap) {
		return fmt.Errorf("%s key count mismatch: en-US=%d zh-CN=%d", ns, len(enMap), len(zhMap))
	}
	for k, enVal := range enMap {
		zhVal, ok := zhMap[k]
		if !ok {
			return fmt.Errorf("zh-CN missing %s.%s", ns, k)
		}
		if strings.TrimSpace(enVal) == "" || strings.TrimSpace(zhVal) == "" {
			return fmt.Errorf("%s.%s has empty translation", ns, k)
		}
		if !samePlaceholders(enVal, zhVal) {
			return fmt.Errorf("%s.%s placeholder mismatch: en=%q zh=%q", ns, k, enVal, zhVal)
		}
	}
	return nil
}

func samePlaceholders(a, b string) bool {
	pa := placeholderRe.FindAllString(a, -1)
	pb := placeholderRe.FindAllString(b, -1)
	slices.Sort(pa)
	slices.Sort(pb)
	return slices.Equal(slices.Compact(pa), slices.Compact(pb))
}
