package render

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"

	"veri/internal/core"
	"veri/internal/spec"
)

// ValidateSearchFormat 预校验检索输出格式，避免在发起上游网络请求后才因格式非法失败。
func ValidateSearchFormat(format string) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", spec.FormatMarkdown, spec.TextFormatMarkdown, spec.FormatJSON, spec.FormatJSONL, spec.FormatURLs:
		return nil
	default:
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported search output format %q", format)).
			WithMsgKey("err.invalid_format").
			WithParam("format", format).
			WithParam("valid", "md, json, jsonl, urls")
	}
}

// ValidateFetchFormat 预校验提取输出格式，避免在发起抓取后才因格式非法失败。
func ValidateFetchFormat(format string) error {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", spec.FormatMarkdown, spec.TextFormatMarkdown, spec.FormatJSON, spec.FormatJSONL:
		return nil
	default:
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported fetch output format %q", format)).
			WithMsgKey("err.invalid_format").
			WithParam("format", format).
			WithParam("valid", "md, json, jsonl")
	}
}

// FormatSearch 按 CLI 指定的格式（md / json / jsonl / urls）渲染检索响应。
func FormatSearch(env *core.Envelope, format string, pal Palette) (string, error) {
	if err := ValidateSearchFormat(format); err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", spec.FormatMarkdown, spec.TextFormatMarkdown:
		return formatSearchMarkdown(env, pal), nil
	case spec.FormatJSON:
		return MarshalEnvelopeJSON(env)
	case spec.FormatJSONL:
		return formatSearchJSONL(env.Results)
	default:
		return formatSearchURLs(env.Results), nil
	}
}

// FormatFetch 按 CLI 指定的格式（md / json / jsonl）渲染单页或批量抓取响应。
func FormatFetch(env *core.Envelope, format string) (string, error) {
	if err := ValidateFetchFormat(format); err != nil {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", spec.FormatMarkdown, spec.TextFormatMarkdown:
		if env == nil {
			return "", nil
		}
		if len(env.Pages) > 0 {
			return formatBatchFetchMarkdown(env.Pages), nil
		}
		if env.Page == nil {
			return "", nil
		}
		if strings.HasSuffix(env.Page.Content, "\n") {
			return env.Page.Content, nil
		}
		return env.Page.Content + "\n", nil
	case spec.FormatJSON:
		return MarshalEnvelopeJSON(env)
	default:
		return formatFetchJSONL(env)
	}
}

func formatBatchFetchMarkdown(pages []core.Page) string {
	var b strings.Builder
	total := len(pages)
	for i, p := range pages {
		if i > 0 {
			b.WriteString("\n\n---\n\n")
		}
		heading := p.Title
		if heading == "" {
			heading = p.URL
		}
		fmt.Fprintf(&b, "# [%d/%d] %s\n\nURL: %s\n", i+1, total, heading, p.URL)
		if p.Error != nil {
			fmt.Fprintf(&b, "\nError [%s]: %s\n", p.Error.Code, p.Error.Message)
			continue
		}
		if p.Content != "" {
			b.WriteByte('\n')
			b.WriteString(strings.TrimRight(p.Content, "\n"))
		}
	}
	b.WriteByte('\n')
	return b.String()
}

func formatFetchJSONL(env *core.Envelope) (string, error) {
	if env == nil {
		return "", nil
	}
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if len(env.Pages) > 0 {
		for _, p := range env.Pages {
			if err := json.MarshalEncode(enc, p); err != nil {
				return "", core.WrapError(core.CodeInternal, "failed to encode page JSONL line", err)
			}
		}
		return buf.String(), nil
	}
	if env.Page != nil {
		if err := json.MarshalEncode(enc, env.Page); err != nil {
			return "", core.WrapError(core.CodeInternal, "failed to encode page JSONL line", err)
		}
	}
	return buf.String(), nil
}

// MarshalEnvelopeJSON 将对象序列化为带缩进且不转义 HTML 符号的 UTF-8 JSON 文本。
func MarshalEnvelopeJSON(v any) (string, error) {
	encoded, err := json.Marshal(v, jsontext.WithIndent("  "))
	if err != nil {
		return "", core.WrapError(core.CodeInternal, "failed to encode JSON output", err)
	}
	return string(encoded) + "\n", nil
}

func formatSearchMarkdown(env *core.Envelope, pal Palette) string {
	var b strings.Builder
	provLabel := activeProvidersLabel(env.Meta.Providers)
	costStr := core.FormatCosts(env.Meta.Cost)
	secs := float64(env.Meta.DurationMS) / 1000.0
	cntLabel := resultCountLabel(len(env.Results))

	var header string
	if costStr != "" {
		header = fmt.Sprintf("# %s · %q · %s · %s · %s · %.2fs",
			spec.AppName, env.Query, provLabel, cntLabel, costStr, secs)
	} else {
		header = fmt.Sprintf("# %s · %q · %s · %s · %.2fs",
			spec.AppName, env.Query, provLabel, cntLabel, secs)
	}
	b.WriteString(pal.Bold(header))
	b.WriteString("\n")

	if len(env.Results) == 0 {
		return b.String()
	}

	for _, r := range env.Results {
		b.WriteString("\n")
		title := displayResultTitle(r)
		titleLine := fmt.Sprintf("%d. %s", r.Rank, pal.Bold(title))
		var metaParts []string
		if r.Site != "" && r.Site != title {
			metaParts = append(metaParts, r.Site)
		}
		if len(r.Authors) > 0 {
			metaParts = append(metaParts, strings.Join(r.Authors, ", "))
		}
		if r.Published != nil {
			metaParts = append(metaParts, r.Published.Format("2006-01-02"))
		}
		if len(metaParts) > 0 {
			titleLine += " " + pal.Dim("— "+strings.Join(metaParts, " · "))
		}
		b.WriteString(titleLine)
		b.WriteString("\n   ")
		b.WriteString(pal.Cyan(r.URL))
		b.WriteString("\n")

		body := r.Content
		if body == "" {
			body = r.Summary
		}
		if body == "" {
			body = r.Snippet
		}
		if body != "" {
			for line := range strings.SplitSeq(body, "\n") {
				b.WriteString("   ")
				b.WriteString(line)
				b.WriteString("\n")
			}
		}
	}
	return b.String()
}

func resultCountLabel(n int) string {
	if n == 1 {
		return "1 result"
	}
	return fmt.Sprintf("%d results", n)
}

func formatSearchJSONL(results []core.Result) (string, error) {
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	for _, r := range results {
		if err := json.MarshalEncode(enc, r); err != nil {
			return "", core.WrapError(core.CodeInternal, "failed to encode JSONL line", err)
		}
	}
	return buf.String(), nil
}

func formatSearchURLs(results []core.Result) string {
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range results {
		b.WriteString(r.URL)
		b.WriteByte('\n')
	}
	return b.String()
}
