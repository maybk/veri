package firecrawl

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"

	"veri/internal/textnorm"
)

type scrapeWireReq struct {
	URL             string   `json:"url"`
	Formats         []string `json:"formats"`
	OnlyMainContent bool     `json:"onlyMainContent"`
	Proxy           string   `json:"proxy,omitempty"`
	MaxAge          *int     `json:"maxAge,omitempty"`
	Parsers         []any    `json:"parsers,omitempty"`
	Timeout         int      `json:"timeout,omitempty"`
}

type pdfParserSpec struct {
	Type     string `json:"type"`
	Mode     string `json:"mode"`
	MaxPages int    `json:"maxPages"`
}

type scrapeWireResp struct {
	Success bool            `json:"success"`
	Error   string          `json:"error,omitempty"`
	Code    string          `json:"code,omitempty"`
	Data    *scrapeWireData `json:"data,omitempty"`
}

type scrapeWireData struct {
	Markdown string          `json:"markdown"`
	Metadata *scrapeWireMeta `json:"metadata,omitempty"`
}

type scrapeWireMeta struct {
	Title      jsontext.Value `json:"title,omitempty"`
	URL        string         `json:"url,omitempty"`
	StatusCode int            `json:"statusCode,omitempty"`
	NumPages   int            `json:"numPages,omitempty"`
	TotalPages int            `json:"totalPages,omitempty"`
	Error      string         `json:"error,omitempty"`
}

type searchWireReq struct {
	Query          string   `json:"query"`
	Limit          int      `json:"limit"`
	Sources        []string `json:"sources,omitempty"`
	Categories     []string `json:"categories,omitempty"`
	IncludeDomains []string `json:"includeDomains,omitempty"`
	ExcludeDomains []string `json:"excludeDomains,omitempty"`
	TBS            string   `json:"tbs,omitempty"`
}

type searchWireResp struct {
	Success     bool            `json:"success"`
	Error       string          `json:"error,omitempty"`
	Code        string          `json:"code,omitempty"`
	Warning     string          `json:"warning,omitempty"`
	CreditsUsed int             `json:"creditsUsed,omitempty"`
	Data        *searchWireData `json:"data,omitempty"`
}

type searchWireData struct {
	Web  []searchWireItem `json:"web,omitempty"`
	News []searchWireItem `json:"news,omitempty"`
}

type searchWireItem struct {
	URL         string `json:"url"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Category    string `json:"category,omitempty"`
}

type statusWireResp struct {
	Success *bool  `json:"success,omitempty"`
	Error   string `json:"error,omitempty"`
	Code    string `json:"code,omitempty"`
}

type errorWireResp struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
	Code    string `json:"code"`
}

// firstFlexString 解析 Firecrawl 元数据中的多态字段（string 或 []string），返回首个非空清洗文本。
func firstFlexString(raw jsontext.Value) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return textnorm.CleanInline(s)
	}
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, item := range list {
			if c := textnorm.CleanInline(item); c != "" {
				return c
			}
		}
	}
	return ""
}
