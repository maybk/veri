package exa

import (
	"strings"

	"veri/internal/spec"
)

const (
	// ProviderName 是 Exa 适配器的唯一标识。
	ProviderName = spec.ProviderExa

	// DefaultBaseURL 是 Exa 官方 API 根地址。
	DefaultBaseURL = spec.DefaultExaBaseURL
	// DefaultSearchType 默认检索类型（auto）。
	DefaultSearchType = spec.ExaTypeAuto
	// DefaultSearchTimeout 默认检索超时。
	DefaultSearchTimeout = spec.DefaultProviderSearchTimeout
	// DefaultFetchTimeout 默认内容提取超时。
	DefaultFetchTimeout = spec.DefaultProviderFetchTimeout
	// DefaultConcurrency 默认并发上限。
	DefaultConcurrency = spec.DefaultProviderConcurrency

	defaultMaxCount      = 10
	extendedMaxCount     = 100
	maxDomainFilters     = 1200
	maxFetchURLLen       = 2048
	maxContentsAgeHours  = 720
	defaultSearchTextCap = 20000

	categoryCompany = "company"
	categoryPeople  = "people"
)

func normalizeExaCategory(raw string) string {
	norm := strings.Join(strings.Fields(strings.ToLower(strings.ReplaceAll(raw, "_", " "))), " ")
	switch norm {
	case "research paper", "paper", "papers":
		return "publication"
	default:
		return norm
	}
}

func isValidKnownExaCategory(cat string) bool {
	switch cat {
	case "company", "publication", "news", "pdf", "github", "personal site", "people", "financial report":
		return true
	default:
		return false
	}
}

// extractInlineExaCategory 从查询串首部剥离形如 `category:people ...` 的内联类别算子，避免污染向量检索词。
func extractInlineExaCategory(query string) (cleanQuery, category string) {
	trimmed := strings.TrimSpace(query)
	if len(trimmed) <= len("category:") || !strings.EqualFold(trimmed[:len("category:")], "category:") {
		return trimmed, ""
	}
	rest := strings.TrimSpace(trimmed[len("category:"):])
	if rest == "" {
		return trimmed, ""
	}
	var rawCat, tail string
	if rest[0] == '"' || rest[0] == '\'' {
		quote := rest[0]
		endIdx := strings.IndexByte(rest[1:], quote)
		if endIdx < 0 {
			return trimmed, ""
		}
		rawCat = rest[1 : 1+endIdx]
		tail = strings.TrimSpace(rest[2+endIdx:])
	} else {
		tok, rem, ok := strings.Cut(rest, " ")
		if !ok {
			return trimmed, ""
		}
		rawCat = tok
		tail = strings.TrimSpace(rem)
	}
	cat := normalizeExaCategory(rawCat)
	if tail == "" || !isValidKnownExaCategory(cat) {
		return trimmed, ""
	}
	return tail, cat
}
