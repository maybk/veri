package keenable

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
	"time"

	"veri/internal/textnorm"
)

type searchWireReq struct {
	Query            string   `json:"query"`
	Mode             string   `json:"mode,omitempty"`
	Site             string   `json:"site,omitempty"`
	ExcludeDomains   []string `json:"exclude_domains,omitempty"`
	PublishedAfter   string   `json:"published_after,omitempty"`
	PublishedBefore  string   `json:"published_before,omitempty"`
	MaxResults       int      `json:"max_results,omitempty"`
	SnippetMaxLength int      `json:"snippet_max_length,omitempty"`
	DisableCards     bool     `json:"disable_cards"`
}

type searchWireResp struct {
	Results []searchWireItem `json:"results"`
	Usage   *wireUsage       `json:"usage,omitempty"`
}

type searchWireItem struct {
	Title       string `json:"title"`
	URL         string `json:"url"`
	Snippet     string `json:"snippet"`
	PublishedAt string `json:"published_at,omitempty"`
}

type fetchWireResp struct {
	URL         string         `json:"url"`
	Title       string         `json:"title"`
	Content     string         `json:"content"`
	Truncated   bool           `json:"truncated"`
	TotalChars  int            `json:"total_chars"`
	Author      string         `json:"author,omitempty"`
	PublishedAt jsontext.Value `json:"published_at,omitempty"`
	Usage       *wireUsage     `json:"usage,omitempty"`
}

type wireUsage struct {
	Credits *float64 `json:"credits,omitempty"`
	Paid    *bool    `json:"paid,omitempty"`
}

type errorWireResp struct {
	Type      string `json:"type"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable *bool  `json:"retryable,omitempty"`
}

// parseFlexiblePublished 解析 ISO 8601 字符串或旧版 Unix 秒整数/浮点数时间戳。
func parseFlexiblePublished(raw jsontext.Value) *time.Time {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return textnorm.ParseFlexibleTime(s, time.UTC)
	}
	var sec int64
	if err := json.Unmarshal(raw, &sec); err == nil {
		return textnorm.UnixSecondsToTime(sec)
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err == nil && f > 0 && f < 1e11 {
		return textnorm.UnixSecondsToTime(int64(f))
	}
	return nil
}
