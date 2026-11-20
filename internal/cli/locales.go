package cli

import (
	"embed"

	"veri/clikit/i18n"
	"veri/internal/core"
)

//go:embed locales/*.json
var localeFS embed.FS

var localeBundle = i18n.MustLoadBundle(localeFS, "locales")

func formatCoreError(cat i18n.Catalog, err error) string {
	se := core.AsError(err)
	if se == nil {
		return ""
	}
	return cat.FormatError(i18n.ErrorView{
		Code:         string(se.Code),
		FallbackCode: string(core.CodeInternal),
		Provider:     se.Provider,
		Message:      se.Message,
		MessageKey:   se.MsgKey,
		RawHint:      se.Hint,
		HintKey:      se.HintKey,
		Params:       se.Params,
	})
}
