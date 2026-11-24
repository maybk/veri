package core

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"

	"veri/internal/spec"
)

// Envelope 是 CLI --json 与 MCP structuredContent 共用的顶层输出信封。
type Envelope struct {
	Schema  string     `json:"schema" jsonschema:"Envelope schema identifier"`
	OK      bool       `json:"ok" jsonschema:"True when the operation succeeded"`
	Kind    string     `json:"kind" jsonschema:"Operation kind (search or fetch)"`
	Query   string     `json:"query,omitempty" jsonschema:"Executed search query"`
	Results []Result   `json:"results,omitempty" jsonschema:"Normalized search results"`
	Page    *Page      `json:"page,omitempty" jsonschema:"Extracted single-page payload"`
	Pages   []Page     `json:"pages,omitempty" jsonschema:"Extracted multi-page payload for batch fetch"`
	Meta    Meta       `json:"meta" jsonschema:"Execution telemetry, provider reports, and cost"`
	Error   *ErrorInfo `json:"error,omitempty" jsonschema:"Structured error details when ok=false"`

	// NoCacheStore 标识上游响应包含 Cache-Control: no-store 等禁止缓存指令（仅进程内流转，不输出到 JSON）。
	NoCacheStore bool `json:"-"`
}

// MarshalJSONTo 使用 Go 1.27 encoding/json/v2 流式写出信封，确保成功检索零命中时稳定输出 `"results": []`。
func (e Envelope) MarshalJSONTo(enc *jsontext.Encoder) error {
	if e.Kind == spec.KindSearch && e.OK {
		type searchEnvelope struct {
			Schema  string     `json:"schema"`
			OK      bool       `json:"ok"`
			Kind    string     `json:"kind"`
			Query   string     `json:"query,omitempty"`
			Results []Result   `json:"results"`
			Meta    Meta       `json:"meta"`
			Error   *ErrorInfo `json:"error,omitempty"`
		}
		results := e.Results
		if results == nil {
			results = []Result{}
		}
		return json.MarshalEncode(enc, searchEnvelope{
			Schema:  e.Schema,
			OK:      e.OK,
			Kind:    e.Kind,
			Query:   e.Query,
			Results: results,
			Meta:    e.Meta,
			Error:   e.Error,
		})
	}
	type alias Envelope
	return json.MarshalEncode(enc, alias(e))
}

// MarshalJSON 兼容第三方库（如官方 MCP Go SDK）通过标准 encoding/json（v1）接口序列化 structuredContent。
func (e Envelope) MarshalJSON() ([]byte, error) {
	return json.Marshal(e)
}

// Clone 返回 Envelope 及其所有嵌套切片与指针的完整深拷贝，确保并发缓存读写零共享状态。
func (e *Envelope) Clone() *Envelope {
	if e == nil {
		return nil
	}
	cp := *e
	if e.Results != nil {
		cp.Results = make([]Result, len(e.Results))
		for i, r := range e.Results {
			cp.Results[i] = r.Clone()
		}
	}
	cp.Page = e.Page.Clone()
	if e.Pages != nil {
		cp.Pages = make([]Page, len(e.Pages))
		for i := range e.Pages {
			if pCopy := e.Pages[i].Clone(); pCopy != nil {
				cp.Pages[i] = *pCopy
			}
		}
	}
	cp.Meta = e.Meta.Clone()
	if e.Error != nil {
		cp.Error = new(*e.Error)
	}
	return &cp
}

// Meta 记录路由执行策略、各参与 Provider 报告、降级/截断状态与总成本耗时。
type Meta struct {
	Strategy     string           `json:"strategy" jsonschema:"Routing strategy applied"`
	Providers    []ProviderReport `json:"providers" jsonschema:"Per-provider execution trace"`
	FallbackUsed bool             `json:"fallback_used" jsonschema:"True when fallback to secondary provider occurred"`
	Partial      bool             `json:"partial" jsonschema:"True when some fusion branches failed or timed out"`
	Truncated    bool             `json:"truncated" jsonschema:"True when output was truncated to fit character budget"`
	Notes        []string         `json:"notes,omitempty" jsonschema:"Diagnostic degradation or truncation notes"`
	Cost         []Cost           `json:"cost" jsonschema:"Aggregated cost per currency"`
	DurationMS   int64            `json:"duration_ms" jsonschema:"Total execution duration in milliseconds"`
	Cached       bool             `json:"cached" jsonschema:"True when served from cache"`
}

// Clone 返回 Meta 的完整深拷贝。
func (m Meta) Clone() Meta {
	out := m
	if m.Providers != nil {
		out.Providers = make([]ProviderReport, len(m.Providers))
		for i, p := range m.Providers {
			out.Providers[i] = p.Clone()
		}
	}
	if len(m.Notes) > 0 {
		out.Notes = slices.Clone(m.Notes)
	}
	if m.Cost != nil {
		out.Cost = slices.Clone(m.Cost)
	}
	return out
}

// Provider status 常量枚举。
const (
	ProviderStatusOK         = "ok"
	ProviderStatusEmpty      = "empty"
	ProviderStatusLowQuality = "low_quality"
	ProviderStatusError      = "error"
	ProviderStatusSkipped    = "skipped"
	ProviderStatusTimeout    = "timeout"
)

// ProviderReport 记录单个参与或被跳过的 Provider 执行明细。
type ProviderReport struct {
	Name      string     `json:"name" jsonschema:"Provider identifier"`
	Status    string     `json:"status" jsonschema:"Execution status"`
	Results   int        `json:"results" jsonschema:"Returned item count"`
	LatencyMS int64      `json:"latency_ms" jsonschema:"Provider call latency in milliseconds"`
	Cost      *Cost      `json:"cost,omitempty" jsonschema:"Provider call cost"`
	Degraded  []string   `json:"degraded,omitempty" jsonschema:"Emulated or downgraded filter capabilities"`
	Warnings  []string   `json:"warnings,omitempty" jsonschema:"Non-fatal provider warnings"`
	Error     *ErrorInfo `json:"error,omitempty" jsonschema:"Provider error details"`
	RequestID string     `json:"request_id,omitempty" jsonschema:"Upstream request trace ID"`
}

// Clone 返回 ProviderReport 的完整深拷贝。
func (p ProviderReport) Clone() ProviderReport {
	out := p
	out.Cost = CloneCost(p.Cost)
	if len(p.Degraded) > 0 {
		out.Degraded = slices.Clone(p.Degraded)
	}
	if len(p.Warnings) > 0 {
		out.Warnings = slices.Clone(p.Warnings)
	}
	if p.Error != nil {
		out.Error = new(*p.Error)
	}
	return out
}

// ErrorInfo 是输出信封中的机器可读错误信息（固定英文）。
type ErrorInfo struct {
	Code      Code   `json:"code" jsonschema:"Stable machine-readable error code"`
	Provider  string `json:"provider,omitempty" jsonschema:"Originating provider"`
	Message   string `json:"message" jsonschema:"Error summary"`
	Hint      string `json:"hint,omitempty" jsonschema:"Actionable remediation hint"`
	Retryable bool   `json:"retryable" jsonschema:"Whether retrying may succeed"`
	Status    int    `json:"status,omitzero" jsonschema:"Upstream HTTP status code"`
}
