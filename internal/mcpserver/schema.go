package mcpserver

import (
	"fmt"
	"strings"

	"veri/internal/core"
	"veri/internal/spec"
)

// MCP 工具 JSON Schema 字段边界与 stdio 帧长上限常量。
const (
	maxInboundJSONRPCLineBytes = 1 << 20 // 1 MiB
	maxQueryRunes              = 400
	maxDomainFilters           = 10
	maxFetchMaxChars           = 100000
	minTimeoutMS               = 500
	maxTimeoutMS               = 120000
)

// SearchInput 定义 web_search 工具的 JSON 参数结构体（与 CLI search 能力对等）。
type SearchInput struct {
	Query        string            `json:"query"`
	Count        int               `json:"count,omitzero"`
	Detail       string            `json:"detail,omitempty"`
	Sites        []string          `json:"sites,omitempty"`
	ExcludeSites []string          `json:"exclude_sites,omitempty"`
	Freshness    string            `json:"freshness,omitempty"`
	Lang         []string          `json:"lang,omitempty"`
	Question     string            `json:"question,omitempty"`
	Providers    []string          `json:"providers,omitempty"`
	Fusion       *bool             `json:"fusion,omitempty"`
	Strict       bool              `json:"strict,omitzero"`
	IncludeExtra bool              `json:"extra,omitzero"`
	TimeoutMS    int               `json:"timeout_ms,omitzero"`
	Options      map[string]string `json:"options,omitempty"`
}

// FetchInput 定义 web_fetch 工具的 JSON 参数结构体（与 CLI fetch 能力对等）。
type FetchInput struct {
	URL       string            `json:"url,omitempty"`
	URLs      []string          `json:"urls,omitempty"`
	Section   string            `json:"section,omitempty"`
	Match     string            `json:"match,omitempty"`
	MaxChars  int               `json:"max_chars,omitzero"`
	Offset    int               `json:"offset,omitzero"`
	Strategy  string            `json:"strategy,omitempty"`
	Providers []string          `json:"providers,omitempty"`
	Fresh     bool              `json:"fresh,omitzero"`
	TimeoutMS int               `json:"timeout_ms,omitzero"`
	Options   map[string]string `json:"options,omitempty"`
}

type searchSchemaDefaults struct {
	defaultCount    int
	maxCount        int
	defaultDetail   string
	defaultStrategy string
}

type fetchSchemaDefaults struct {
	defaultMaxChars int
	defaultStrategy string
}

// buildSearchInputSchema 生成 web_search 的输入 Schema。
// 不在 Schema 属性中写入 JSON Schema "default"，避免 MCP SDK ApplyDefaults 在服务端填死默认值覆盖用户 config.toml 配置；
// 生效默认值改以自然语言写在 description 中供模型感知。
func buildSearchInputSchema(def searchSchemaDefaults, enabledSearchers []string) map[string]any {
	if def.maxCount <= 0 {
		def.maxCount = spec.DefaultMCPMaxCount
	}
	if def.defaultCount <= 0 {
		def.defaultCount = spec.DefaultSearchCount
	}
	effCount := spec.ClampSearchCount(def.defaultCount, def.maxCount)
	effDetail := strings.TrimSpace(def.defaultDetail)
	if effDetail == "" {
		effDetail = string(core.DetailSummary)
	}
	effStrategy := strings.TrimSpace(def.defaultStrategy)
	if effStrategy == "" {
		effStrategy = string(core.StrategyAuto)
	}

	allSearchProviders := spec.ProvidersWithRole(spec.RoleSearch)
	enabledHint := enabledListLabel(enabledSearchers)

	props := map[string]any{
		"query": map[string]any{
			"type":        "string",
			"minLength":   1,
			"maxLength":   maxQueryRunes,
			"description": "Search query (1-400 chars).",
		},
		"count": map[string]any{
			"type":        "integer",
			"minimum":     spec.MinSearchCount,
			"maximum":     spec.MaxSearchCount,
			"description": fmt.Sprintf("Number of results to return (default from config: %d, capped at %d).", effCount, def.maxCount),
		},
		"detail": map[string]any{
			"type":        "string",
			"enum":        []string{string(core.DetailSnippet), string(core.DetailSummary), string(core.DetailContent)},
			"description": fmt.Sprintf("Result text granularity (default from config: %s).", effDetail),
		},
		"sites": map[string]any{
			"type":        "array",
			"maxItems":    maxDomainFilters,
			"items":       map[string]any{"type": "string"},
			"description": "Restrict search to these domains.",
		},
		"exclude_sites": map[string]any{
			"type":        "array",
			"maxItems":    maxDomainFilters,
			"items":       map[string]any{"type": "string"},
			"description": "Exclude these domains from search results.",
		},
		"freshness": map[string]any{
			"type":        "string",
			"description": `Optional time range: "day", "week", "month", "year", "7d", "3m", "1y", or "YYYY-MM-DD..YYYY-MM-DD".`,
		},
		"lang": map[string]any{
			"type":        "array",
			"items":       map[string]any{"type": "string"},
			"description": "ISO 639-1 language codes (e.g. zh, en).",
		},
		"question": map[string]any{
			"type":        "string",
			"description": "Original user question for intent-aware ranking.",
		},
		"fusion": map[string]any{
			"type":        "boolean",
			"description": fmt.Sprintf("Set true to query multiple providers concurrently via RRF, or false to force sequential chain (default strategy from config: %s).", effStrategy),
		},
		"strict": map[string]any{
			"type":        "boolean",
			"description": "Disallow local filter emulation; skip providers lacking native filter support.",
		},
		"extra": map[string]any{
			"type":        "boolean",
			"description": "Include site icon and provider-specific metadata under results[].extra.",
		},
		"timeout_ms": map[string]any{
			"type":        "integer",
			"minimum":     minTimeoutMS,
			"maximum":     maxTimeoutMS,
			"description": "Optional overall search timeout in milliseconds.",
		},
		"options": map[string]any{
			"type":                 "object",
			"additionalProperties": map[string]any{"type": "string"},
			"description": fmt.Sprintf(
				"Optional provider options (e.g. %q, %q, %q, %q, %q, %q, %q, %q, %q, %q).",
				spec.OptExaSearchType, spec.OptExaCategory,
				spec.OptTavilySearchDepth, spec.OptTavilyTopic,
				spec.OptBraveMode, spec.OptBraveThreshold, spec.OptBraveGoggles,
				spec.OptTinyFishDomainType, spec.OptWSAIndustry, spec.OptWSAMode,
			),
		},
		"providers": map[string]any{
			"type":        "array",
			"minItems":    1,
			"items":       map[string]any{"type": "string", "enum": allSearchProviders},
			"description": fmt.Sprintf("Explicitly select search provider(s) in order; a single entry disables fallback (enabled: %s).", enabledHint),
		},
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"query"},
		"properties":           props,
	}
}

// buildFetchInputSchema 生成 web_fetch 的输入 Schema（同样不写入 JSON Schema "default"，避免覆盖用户配置）。
func buildFetchInputSchema(def fetchSchemaDefaults, enabledFetchers []string) map[string]any {
	if def.defaultMaxChars <= 0 {
		def.defaultMaxChars = spec.DefaultMCPFetchMaxChars
	}
	effStrategy := strings.ToLower(strings.TrimSpace(def.defaultStrategy))
	if effStrategy == "" {
		effStrategy = string(core.DefaultFetchStrategy)
	}
	allFetchProviders := spec.ProvidersWithRole(spec.RoleFetch)
	enabledHint := enabledListLabel(enabledFetchers)

	props := map[string]any{
		"url": map[string]any{
			"type":        "string",
			"minLength":   1,
			"description": "Target http/https URL to read (mutually exclusive with urls).",
		},
		"urls": map[string]any{
			"type":        "array",
			"minItems":    1,
			"maxItems":    spec.MaxMCPBatchFetchURLs,
			"items":       map[string]any{"type": "string", "minLength": 1},
			"description": fmt.Sprintf("Batch fetch up to %d http/https URLs concurrently in order (mutually exclusive with url).", spec.MaxMCPBatchFetchURLs),
		},
		"section": map[string]any{
			"type":        "string",
			"description": "Optional heading title to extract only that section subtree (combinable with match).",
		},
		"match": map[string]any{
			"type":        "string",
			"description": "Optional keyword/phrase to extract matching passages or compact code line windows with context (combinable with section).",
		},
		"max_chars": map[string]any{
			"type":        "integer",
			"minimum":     spec.MinMCPFetchMaxChars,
			"maximum":     maxFetchMaxChars,
			"description": fmt.Sprintf("Maximum characters returned in a single call (default from config: %d).", def.defaultMaxChars),
		},
		"offset": map[string]any{
			"type":        "integer",
			"minimum":     0,
			"description": "Character offset for paginated continuation (scoped to section/match when set; default: 0).",
		},
		"strategy": map[string]any{
			"type":        "string",
			"enum":        []string{string(core.FetchAPIFirst), string(core.FetchSmartLocal), string(core.FetchLocalOnly), string(core.FetchAPIOnly)},
			"description": fmt.Sprintf("Optional extraction routing strategy override (default from config: %s; mutually exclusive with providers).", effStrategy),
		},
		"timeout_ms": map[string]any{
			"type":        "integer",
			"minimum":     minTimeoutMS,
			"maximum":     maxTimeoutMS,
			"description": "Optional overall fetch timeout in milliseconds.",
		},
		"fresh": map[string]any{
			"type":        "boolean",
			"description": "Bypass cache and force live page extraction when supported.",
		},
		"options": map[string]any{
			"type":                 "object",
			"additionalProperties": map[string]any{"type": "string"},
			"description": fmt.Sprintf(
				"Optional extractor options (e.g. %q, %q, %q).",
				spec.OptTavilyExtractDepth, spec.OptTinyFishTTL, spec.OptExaMaxAgeHours,
			),
		},
		"providers": map[string]any{
			"type":        "array",
			"minItems":    1,
			"items":       map[string]any{"type": "string", "enum": allFetchProviders},
			"description": fmt.Sprintf("Explicitly select extractor(s) in order; a single entry disables fallback (enabled: %s; mutually exclusive with strategy).", enabledHint),
		},
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           props,
	}
}

func enabledListLabel(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}
