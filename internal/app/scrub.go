package app

import (
	"errors"
	"strings"

	"veri/clikit/redact"
	"veri/internal/core"
)

// ScrubText 使用应用内已加载密钥表与通用凭据规则对任意文本脱敏（接收者为 nil 时仍执行通用 URL 与认证头清洗）。
func (a *App) ScrubText(s string) string {
	if s == "" {
		return ""
	}
	var rep *strings.Replacer
	if a != nil {
		rep = a.scrubber
	}
	return redact.ScrubWith(s, rep)
}

func (a *App) scrubError(err error) error {
	if err == nil {
		return nil
	}
	return a.ScrubCoreError(core.AsError(err))
}

// ScrubCoreError 返回字段（Message / Hint / Params / Cause）均已脱敏的 *core.Error 副本。
func (a *App) ScrubCoreError(se *core.Error) *core.Error {
	var rep *strings.Replacer
	if a != nil {
		rep = a.scrubber
	}
	return ScrubCoreErrorWith(se, rep)
}

// ScrubCoreErrorWith 使用指定密钥替换器（可为 nil）对 *core.Error 的全部可读字段执行脱敏并返回独立副本。
func ScrubCoreErrorWith(se *core.Error, rep *strings.Replacer) *core.Error {
	if se == nil {
		return core.NewError(core.CodeInternal, "unknown internal error")
	}
	scrub := func(s string) string { return redact.ScrubWith(s, rep) }
	cp := *se
	cp.Message = scrub(cp.Message)
	cp.Hint = scrub(cp.Hint)
	if len(se.Params) > 0 {
		cp.Params = make(map[string]string, len(se.Params))
		for k, v := range se.Params {
			cp.Params[k] = scrub(v)
		}
	}
	if cp.Cause != nil {
		rawCause := cp.Cause.Error()
		cleanedCause := scrub(rawCause)
		if cp.Message == "" {
			cp.Message = cleanedCause
		}
		if cleanedCause != rawCause {
			cp.Cause = errors.New(cleanedCause)
		}
	}
	return &cp
}

func (a *App) scrubEnvelopeSecrets(env *core.Envelope) {
	if env == nil {
		return
	}
	env.Query = a.ScrubText(env.Query)
	if env.Page != nil {
		env.Page.URL = a.ScrubText(env.Page.URL)
		env.Page.FinalURL = a.ScrubText(env.Page.FinalURL)
	}
	for i := range env.Pages {
		env.Pages[i].URL = a.ScrubText(env.Pages[i].URL)
		env.Pages[i].FinalURL = a.ScrubText(env.Pages[i].FinalURL)
		if env.Pages[i].Error != nil {
			env.Pages[i].Error.Message = a.ScrubText(env.Pages[i].Error.Message)
			env.Pages[i].Error.Hint = a.ScrubText(env.Pages[i].Error.Hint)
		}
	}
	if env.Error != nil {
		env.Error.Message = a.ScrubText(env.Error.Message)
		env.Error.Hint = a.ScrubText(env.Error.Hint)
	}
	for i := range env.Meta.Notes {
		env.Meta.Notes[i] = a.ScrubText(env.Meta.Notes[i])
	}
	for i := range env.Meta.Providers {
		if env.Meta.Providers[i].Error != nil {
			env.Meta.Providers[i].Error.Message = a.ScrubText(env.Meta.Providers[i].Error.Message)
			env.Meta.Providers[i].Error.Hint = a.ScrubText(env.Meta.Providers[i].Error.Hint)
		}
		for j := range env.Meta.Providers[i].Warnings {
			env.Meta.Providers[i].Warnings[j] = a.ScrubText(env.Meta.Providers[i].Warnings[j])
		}
	}
}
