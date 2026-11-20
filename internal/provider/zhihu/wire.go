package zhihu

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strconv"
	"strings"

	"veri/internal/textnorm"
)

type statusWireResp struct {
	Code    int    `json:"Code"`
	Message string `json:"Message"`
}

type searchWireResp struct {
	Code    int            `json:"Code"`
	Message string         `json:"Message"`
	Data    searchWireData `json:"Data"`
}

type searchWireData struct {
	SearchHashID string           `json:"SearchHashId,omitempty"`
	Items        []searchWireItem `json:"Items"`
}

type searchWireItem struct {
	Title           string         `json:"Title"`
	ContentType     string         `json:"ContentType,omitempty"`
	ContentText     string         `json:"ContentText"`
	URL             string         `json:"Url"`
	CommentCount    int            `json:"CommentCount,omitempty"`
	VoteUpCount     int            `json:"VoteUpCount,omitempty"`
	AuthorName      string         `json:"AuthorName,omitempty"`
	AuthorBadgeText string         `json:"AuthorBadgeText,omitempty"`
	EditTime        int64          `json:"EditTime,omitempty"`
	CreateTime      int64          `json:"CreateTime,omitempty"`
	AuthorityLevel  jsontext.Value `json:"AuthorityLevel,omitempty"`
	RankingScore    *float64       `json:"RankingScore,omitempty"`
}

// parseAuthorityLevel 解析知乎权威等级（"1"~"4" 字符串或整数）并归一化到 [0, 1]。
func parseAuthorityLevel(raw jsontext.Value) *float64 {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil
	}
	var level int
	if err := json.Unmarshal(raw, &level); err != nil {
		var s string
		if sErr := json.Unmarshal(raw, &s); sErr != nil {
			return nil
		}
		parsed, pErr := strconv.Atoi(strings.TrimSpace(s))
		if pErr != nil {
			return nil
		}
		level = parsed
	}
	if level <= 0 {
		return nil
	}
	return textnorm.ClampUnitFloat(float64(level) / maxAuthorityLevel)
}
