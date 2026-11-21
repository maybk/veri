package mcpserver

import (
	"encoding/json/jsontext"
	"encoding/json/v2"

	"veri/internal/core"
	"veri/internal/spec"
)

// SearchOutput 是 web_search 工具的专用结构化输出信封（与 core.Envelope 搜索形态字段 100% 兼容，不含 page/pages）。
type SearchOutput struct {
	Schema  string          `json:"schema"`
	OK      bool            `json:"ok"`
	Kind    string          `json:"kind"`
	Query   string          `json:"query,omitempty"`
	Results []core.Result   `json:"results,omitempty"`
	Meta    core.Meta       `json:"meta"`
	Error   *core.ErrorInfo `json:"error,omitempty"`
}

// MarshalJSONTo 确保成功检索零命中时稳定输出 `"results": []`。
func (s SearchOutput) MarshalJSONTo(enc *jsontext.Encoder) error {
	if s.OK {
		type okSearchOutput struct {
			Schema  string          `json:"schema"`
			OK      bool            `json:"ok"`
			Kind    string          `json:"kind"`
			Query   string          `json:"query,omitempty"`
			Results []core.Result   `json:"results"`
			Meta    core.Meta       `json:"meta"`
			Error   *core.ErrorInfo `json:"error,omitempty"`
		}
		results := s.Results
		if results == nil {
			results = []core.Result{}
		}
		return json.MarshalEncode(enc, okSearchOutput{
			Schema:  s.Schema,
			OK:      s.OK,
			Kind:    s.Kind,
			Query:   s.Query,
			Results: results,
			Meta:    s.Meta,
			Error:   s.Error,
		})
	}
	type alias SearchOutput
	return json.MarshalEncode(enc, alias(s))
}

// MarshalJSON 兼容通过标准 encoding/json（v1）序列化 structuredContent 的第三方库（如官方 MCP Go SDK）。
func (s SearchOutput) MarshalJSON() ([]byte, error) {
	return json.Marshal(s)
}

// FetchOutput 是 web_fetch 工具的专用结构化输出信封（与 core.Envelope 提取形态字段 100% 兼容，不含 results）。
type FetchOutput struct {
	Schema string          `json:"schema"`
	OK     bool            `json:"ok"`
	Kind   string          `json:"kind"`
	Query  string          `json:"query,omitempty"`
	Page   *core.Page      `json:"page,omitempty"`
	Pages  []core.Page     `json:"pages,omitempty"`
	Meta   core.Meta       `json:"meta"`
	Error  *core.ErrorInfo `json:"error,omitempty"`
}

func toSearchOutput(env *core.Envelope) SearchOutput {
	if env == nil {
		return SearchOutput{
			Schema: spec.SchemaVersion,
			Kind:   spec.KindSearch,
			Meta:   normalizeOutputMeta(core.Meta{}),
		}
	}
	results := env.Results
	if env.OK && results == nil {
		results = []core.Result{}
	}
	if needsProviderNormalization(results) {
		copied := make([]core.Result, len(results))
		for i, r := range results {
			if r.Providers == nil {
				r.Providers = []string{}
			}
			copied[i] = r
		}
		results = copied
	}
	return SearchOutput{
		Schema:  env.Schema,
		OK:      env.OK,
		Kind:    env.Kind,
		Query:   env.Query,
		Results: results,
		Meta:    normalizeOutputMeta(env.Meta),
		Error:   env.Error,
	}
}

func toFetchOutput(env *core.Envelope) FetchOutput {
	if env == nil {
		return FetchOutput{
			Schema: spec.SchemaVersion,
			Kind:   spec.KindFetch,
			Meta:   normalizeOutputMeta(core.Meta{}),
		}
	}
	return FetchOutput{
		Schema: env.Schema,
		OK:     env.OK,
		Kind:   env.Kind,
		Query:  env.Query,
		Page:   env.Page,
		Pages:  env.Pages,
		Meta:   normalizeOutputMeta(env.Meta),
		Error:  env.Error,
	}
}

func needsProviderNormalization(results []core.Result) bool {
	for i := range results {
		if results[i].Providers == nil {
			return true
		}
	}
	return false
}

func normalizeOutputMeta(m core.Meta) core.Meta {
	if m.Providers == nil {
		m.Providers = []core.ProviderReport{}
	}
	if m.Cost == nil {
		m.Cost = []core.Cost{}
	}
	return m
}

// buildSearchOutputSchema 构造 web_search 专用紧凑 JSON Schema 2020-12（通过 $defs 去重并排除提取专属字段）。
func buildSearchOutputSchema() map[string]any {
	defs := sharedOutputDefs()
	defs["Result"] = map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"rank", "title", "url", "providers"},
		"properties": map[string]any{
			"rank":      map[string]any{"type": "integer"},
			"title":     map[string]any{"type": "string"},
			"url":       map[string]any{"type": "string"},
			"site":      map[string]any{"type": "string"},
			"authors":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"icon":      map[string]any{"type": "string"},
			"published": map[string]any{"type": "string"},
			"snippet":   map[string]any{"type": "string"},
			"summary":   map[string]any{"type": "string"},
			"content":   map[string]any{"type": "string"},
			"relevance": map[string]any{"type": "number"},
			"authority": map[string]any{"type": "number"},
			"providers": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"extra":     map[string]any{"type": "object", "additionalProperties": true},
		},
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"schema", "ok", "kind", "meta"},
		"$defs":                defs,
		"properties": map[string]any{
			"schema":  map[string]any{"type": "string"},
			"ok":      map[string]any{"type": "boolean"},
			"kind":    map[string]any{"type": "string"},
			"query":   map[string]any{"type": "string"},
			"results": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Result"}},
			"meta":    map[string]any{"$ref": "#/$defs/Meta"},
			"error":   map[string]any{"$ref": "#/$defs/ErrorInfo"},
		},
	}
}

// buildFetchOutputSchema 构造 web_fetch 专用紧凑 JSON Schema 2020-12（通过 $defs 复用 Page 与公共元数据）。
func buildFetchOutputSchema() map[string]any {
	defs := sharedOutputDefs()
	defs["PageSection"] = map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"heading", "level", "offset", "chars"},
		"properties": map[string]any{
			"heading": map[string]any{"type": "string"},
			"level":   map[string]any{"type": "integer"},
			"offset":  map[string]any{"type": "integer"},
			"chars":   map[string]any{"type": "integer"},
		},
	}
	defs["Page"] = map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"url", "title", "content", "total_chars"},
		"properties": map[string]any{
			"url":         map[string]any{"type": "string"},
			"final_url":   map[string]any{"type": "string"},
			"title":       map[string]any{"type": "string"},
			"author":      map[string]any{"type": "string"},
			"site":        map[string]any{"type": "string"},
			"published":   map[string]any{"type": "string"},
			"content":     map[string]any{"type": "string"},
			"total_chars": map[string]any{"type": "integer"},
			"next_offset": map[string]any{"type": "integer"},
			"outline":     map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/PageSection"}},
			"provider":    map[string]any{"type": "string"},
			"source_type": map[string]any{"type": "string"},
			"error":       map[string]any{"$ref": "#/$defs/ErrorInfo"},
		},
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"schema", "ok", "kind", "meta"},
		"$defs":                defs,
		"properties": map[string]any{
			"schema": map[string]any{"type": "string"},
			"ok":     map[string]any{"type": "boolean"},
			"kind":   map[string]any{"type": "string"},
			"query":  map[string]any{"type": "string"},
			"page":   map[string]any{"$ref": "#/$defs/Page"},
			"pages":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Page"}},
			"meta":   map[string]any{"$ref": "#/$defs/Meta"},
			"error":  map[string]any{"$ref": "#/$defs/ErrorInfo"},
		},
	}
}

func sharedOutputDefs() map[string]any {
	return map[string]any{
		"Cost": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"currency", "micros", "estimated"},
			"properties": map[string]any{
				"currency":  map[string]any{"type": "string"},
				"micros":    map[string]any{"type": "integer"},
				"estimated": map[string]any{"type": "boolean"},
			},
		},
		"ErrorInfo": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"code", "message", "retryable"},
			"properties": map[string]any{
				"code":      map[string]any{"type": "string"},
				"provider":  map[string]any{"type": "string"},
				"message":   map[string]any{"type": "string"},
				"hint":      map[string]any{"type": "string"},
				"retryable": map[string]any{"type": "boolean"},
				"status":    map[string]any{"type": "integer"},
			},
		},
		"ProviderReport": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"name", "status", "results", "latency_ms"},
			"properties": map[string]any{
				"name":       map[string]any{"type": "string"},
				"status":     map[string]any{"type": "string"},
				"results":    map[string]any{"type": "integer"},
				"latency_ms": map[string]any{"type": "integer"},
				"cost":       map[string]any{"$ref": "#/$defs/Cost"},
				"degraded":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"warnings":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"error":      map[string]any{"$ref": "#/$defs/ErrorInfo"},
				"request_id": map[string]any{"type": "string"},
			},
		},
		"Meta": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"required":             []string{"strategy", "providers", "fallback_used", "partial", "truncated", "cost", "duration_ms", "cached"},
			"properties": map[string]any{
				"strategy":      map[string]any{"type": "string"},
				"providers":     map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/ProviderReport"}},
				"fallback_used": map[string]any{"type": "boolean"},
				"partial":       map[string]any{"type": "boolean"},
				"truncated":     map[string]any{"type": "boolean"},
				"notes":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"cost":          map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/Cost"}},
				"duration_ms":   map[string]any{"type": "integer"},
				"cached":        map[string]any{"type": "boolean"},
			},
		},
	}
}
