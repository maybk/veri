package router

import (
	"fmt"
	"strings"

	"veri/internal/core"
)

// summarizeChainFailure 汇总多源链式失败报告，按错误码确定性优先级选取主错误码，避免链尾瞬态超时掩盖前序明确的目标或凭据错误。
func summarizeChainFailure(prefix string, reports []core.ProviderReport, lastErr *core.Error) *core.Error {
	var failed []string
	var codes []core.Code
	for _, rep := range reports {
		if rep.Status != core.ProviderStatusError && rep.Status != core.ProviderStatusTimeout {
			continue
		}
		detail := string(rep.Status)
		if rep.Error != nil && rep.Error.Code != "" {
			detail = string(rep.Error.Code)
			codes = append(codes, rep.Error.Code)
		}
		failed = append(failed, fmt.Sprintf("%s: %s", rep.Name, detail))
	}
	if len(failed) <= 1 {
		return lastErr
	}

	code := core.MostDecisiveCode(codes)
	if code == "" && lastErr != nil {
		code = lastErr.Code
	}
	if code == "" {
		code = core.CodeInternal
	}

	se := core.NewError(code, fmt.Sprintf("%s (%s)", prefix, strings.Join(failed, "; ")))
	switch code {
	case core.CodeNotFound, core.CodeBlockedByPolicy, core.CodeContentUnavailable:
		// 目标页面自身属性错误使用该错误码的精准默认提示，避免误导用户去排查密钥或配额
		return se
	default:
		return se.WithHint("Run `veri key list` or `veri doctor` to inspect provider credentials, quota, and connectivity.").
			WithHintKey("hint.multi_provider_failed")
	}
}

func noMatchedProviderError(strict bool) *core.Error {
	if strict {
		return core.NewError(core.CodeNoProvider, "no provider matched requested filter capabilities").
			WithHint("Disable strict filter mode or relax filter conditions.").
			WithHintKey("hint.strict_filter_empty")
	}
	return core.NewError(core.CodeNoProvider, "no enabled search provider supports the requested query constraints").
		WithHint("Shorten the query, relax site filters, or enable another general search provider.").
		WithHintKey("hint.provider_capability_mismatch")
}
