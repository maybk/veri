package i18n

import (
	"strings"
	"testing"
	"testing/fstest"
)

func TestResolvePrecedenceAndPosixCGuard(t *testing.T) {
	cases := []struct {
		name     string
		flagLang string
		cfgLang  string
		env      map[string]string
		osTags   []string
		wantLoc  Locale
		wantSrc  string
	}{
		{
			name:     "flag overrides env and os",
			flagLang: "zh",
			env:      map[string]string{"APP_LANG": "en", "LC_ALL": "C"},
			osTags:   []string{"en-US"},
			wantLoc:  LocaleZhCN,
			wantSrc:  "flag:--ui-lang",
		},
		{
			name:    "app env overrides config and posix",
			cfgLang: "en",
			env:     map[string]string{"APP_LANG": "zh_CN.UTF-8", "LC_ALL": "C"},
			wantLoc: LocaleZhCN,
			wantSrc: "env:APP_LANG",
		},
		{
			name:    "config overrides posix and os",
			cfgLang: "zh",
			env:     map[string]string{"LANG": "en_US.UTF-8"},
			osTags:  []string{"en-US"},
			wantLoc: LocaleZhCN,
			wantSrc: "config:ui.lang",
		},
		{
			name:    "LC_ALL=C forces en-US and ignores LANGUAGE and OS",
			env:     map[string]string{"LC_ALL": "C", "LANGUAGE": "zh_CN:en_US"},
			osTags:  []string{"zh-Hans-CN"},
			wantLoc: LocaleEnUS,
			wantSrc: "env:LC_ALL=C",
		},
		{
			name:    "LC_ALL=C.UTF-8 explicit override forces en-US",
			env:     map[string]string{"LC_ALL": "C.UTF-8", "LANGUAGE": "zh_CN:en_US"},
			osTags:  []string{"zh-Hans-CN"},
			wantLoc: LocaleEnUS,
			wantSrc: "env:LC_ALL=C",
		},
		{
			name:    "LC_MESSAGES=C.UTF-8 explicit override forces en-US",
			env:     map[string]string{"LC_MESSAGES": "C.UTF-8", "LANG": "zh_CN.UTF-8"},
			osTags:  []string{"zh-Hans-CN"},
			wantLoc: LocaleEnUS,
			wantSrc: "env:LC_MESSAGES=C",
		},
		{
			name:    "strict ASCII LANG=C forces en-US and ignores LANGUAGE and OS",
			env:     map[string]string{"LANG": "C", "LANGUAGE": "zh_CN:en_US"},
			osTags:  []string{"zh-Hans-CN"},
			wantLoc: LocaleEnUS,
			wantSrc: "env:LANG=C",
		},
		{
			name:    "neutral LANG=C.UTF-8 falls through to macOS/Windows OS locale",
			env:     map[string]string{"LANG": "C.UTF-8"},
			osTags:  []string{"zh-Hans-CN", "en-US"},
			wantLoc: LocaleZhCN,
			wantSrc: "os",
		},
		{
			name:    "neutral LANG=C.utf8 falls through to OS locale",
			env:     map[string]string{"LANG": "C.utf8"},
			osTags:  []string{"zh-CN"},
			wantLoc: LocaleZhCN,
			wantSrc: "os",
		},
		{
			name:    "neutral LANG=UTF-8 falls through to OS locale",
			env:     map[string]string{"LANG": "UTF-8"},
			osTags:  []string{"zh-Hans-US"},
			wantLoc: LocaleZhCN,
			wantSrc: "os",
		},
		{
			name:    "neutral LANG=C.UTF-8 honors LANGUAGE preference",
			env:     map[string]string{"LANG": "C.UTF-8", "LANGUAGE": "zh_CN:en"},
			osTags:  []string{"en-US"},
			wantLoc: LocaleZhCN,
			wantSrc: "env:LANGUAGE",
		},
		{
			name:    "neutral LANG=C.UTF-8 without OS locale defaults to en-US",
			env:     map[string]string{"LANG": "C.UTF-8"},
			osTags:  nil,
			wantLoc: LocaleEnUS,
			wantSrc: "default",
		},
		{
			name:    "LC_ALL unsupported locale overrides LANG=zh_CN and blocks OS fallback",
			env:     map[string]string{"LC_ALL": "fr_FR.UTF-8", "LANG": "zh_CN.UTF-8"},
			osTags:  []string{"zh-Hans-CN"},
			wantLoc: LocaleEnUS,
			wantSrc: "default",
		},
		{
			name:    "LC_ALL=zh_CN.UTF-8 overrides LANG=C",
			env:     map[string]string{"LC_ALL": "zh_CN.UTF-8", "LANG": "C"},
			osTags:  []string{"en-US"},
			wantLoc: LocaleZhCN,
			wantSrc: "env:LC_ALL",
		},
		{
			name:    "empty env falls back to OS locale",
			env:     nil,
			osTags:  []string{"zh-Hans-CN", "en-US"},
			wantLoc: LocaleZhCN,
			wantSrc: "os",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			loc, src := Resolve(ResolveOptions{
				FlagLang:   tc.flagLang,
				EnvLangVar: "APP_LANG",
				ConfigLang: tc.cfgLang,
				Getenv: func(k string) string {
					if tc.env == nil {
						return ""
					}
					return tc.env[k]
				},
				DetectOS: func() []string { return tc.osTags },
			})
			if loc != tc.wantLoc || src != tc.wantSrc {
				t.Fatalf("Resolve() = (%s, %s), want (%s, %s)", loc, src, tc.wantLoc, tc.wantSrc)
			}
		})
	}

	if err := ValidateExplicit("auto"); err != nil {
		t.Fatalf("ValidateExplicit(auto) failed: %v", err)
	}
	if err := ValidateExplicit("invalid-lang"); err == nil {
		t.Fatal("expected ValidateExplicit(invalid-lang) to fail")
	}
}

func TestBundleSymmetryAndInterpolation(t *testing.T) {
	memFS := fstest.MapFS{
		"locales/en-US.json": &fstest.MapFile{Data: []byte(`{
			"cli": {
				"hello": "Hello {name}",
				"err.custom": "bad flag {flag}",
				"hint.custom": "Try {cmd} instead."
			},
			"errors": {"ERR_AUTH": {"title": "Auth failed", "hint": "Check {provider}"}}
		}`)},
		"locales/zh-CN.json": &fstest.MapFile{Data: []byte(`{
			"cli": {
				"hello": "你好 {name}",
				"err.custom": "参数 {flag} 无效",
				"hint.custom": "请改用 {cmd}。"
			},
			"errors": {"ERR_AUTH": {"title": "鉴权失败", "hint": "请检查 {provider}"}}
		}`)},
	}
	bundle, err := LoadBundle(memFS, "locales")
	if err != nil {
		t.Fatalf("LoadBundle failed: %v", err)
	}
	if err := bundle.ValidateSymmetry(); err != nil {
		t.Fatalf("ValidateSymmetry failed: %v", err)
	}

	zh := bundle.For(LocaleZhCN)
	if got := zh.T("hello", "name", "{other}"); got != "你好 {other}" {
		t.Fatalf("single-pass interpolation failed: %q", got)
	}
	formatted := zh.FormatError(ErrorView{
		Code:     "ERR_AUTH",
		Provider: "demo",
		Message:  "401",
	})
	if !strings.Contains(formatted, "鉴权失败 [demo]: 401") || !strings.Contains(formatted, "请检查 demo") {
		t.Fatalf("FormatError fallback = %q", formatted)
	}

	specific := zh.FormatError(ErrorView{
		Code:       "ERR_AUTH",
		Message:    "english fallback",
		MessageKey: "err.custom",
		RawHint:    "english hint",
		HintKey:    "hint.custom",
		Params:     map[string]string{"flag": "--foo", "cmd": "veri --help"},
	})
	if !strings.Contains(specific, "鉴权失败: 参数 --foo 无效") || !strings.Contains(specific, "请改用 veri --help。") {
		t.Fatalf("FormatError localized MsgKey/HintKey = %q", specific)
	}
}
