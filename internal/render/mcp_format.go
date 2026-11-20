package render

import (
	"fmt"
	"strings"

	"veri/internal/core"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

// FormatMCPSearchText 生成 MCP web_search 工具的 content[0].text（固定英文并包裹 <untrusted_web_content>）。
func FormatMCPSearchText(env *core.Envelope, textFormat string) (string, error) {
	if env == nil {
		return "", nil
	}
	if strings.EqualFold(strings.TrimSpace(textFormat), spec.TextFormatJSON) {
		rawJSON, err := MarshalEnvelopeJSON(env)
		if err != nil {
			return "", err
		}
		if env.OK {
			return WrapUntrusted(strings.TrimSuffix(rawJSON, "\n")), nil
		}
		return rawJSON, nil
	}
	if !env.OK && env.Error != nil {
		return formatMCPErrorText(env.Error), nil
	}
	if len(env.Results) == 0 {
		return "No results. Try broader terms or remove filters.", nil
	}

	provLabel := activeProvidersLabel(env.Meta.Providers)
	costStr := core.FormatCosts(env.Meta.Cost)
	cntLabel := resultCountLabel(len(env.Results))

	var inner strings.Builder
	for i, r := range env.Results {
		if i > 0 {
			inner.WriteString("\n\n")
		}
		title := displayResultTitle(r)
		fmt.Fprintf(&inner, "[%d] %s\nURL: %s\n", r.Rank, title, r.URL)
		var meta []string
		if r.Site != "" && r.Site != title {
			meta = append(meta, "Site: "+r.Site)
		}
		if len(r.Authors) > 0 {
			meta = append(meta, "Authors: "+strings.Join(r.Authors, ", "))
		}
		if r.Published != nil {
			meta = append(meta, "Published: "+r.Published.Format("2006-01-02"))
		}
		if len(meta) > 0 {
			inner.WriteString(strings.Join(meta, " | ") + "\n")
		}
		body := r.Content
		if body == "" {
			body = r.Summary
		}
		if body == "" {
			body = r.Snippet
		}
		if body != "" {
			inner.WriteString(body)
		}
	}

	var out strings.Builder
	if costStr != "" {
		fmt.Fprintf(&out, "Results for %q (%s, %s, %s):\n\n", env.Query, provLabel, cntLabel, costStr)
	} else {
		fmt.Fprintf(&out, "Results for %q (%s, %s):\n\n", env.Query, provLabel, cntLabel)
	}
	out.WriteString(WrapUntrusted(inner.String()))
	if len(env.Meta.Notes) > 0 {
		out.WriteString("\n\nNotes: " + strings.Join(env.Meta.Notes, "; "))
	}
	return out.String(), nil
}

// FormatMCPFetchText 生成 MCP web_fetch 工具的 content[0].text（固定英文、全量外部字段包裹不可信标签并附带分页续读指引）。
func FormatMCPFetchText(env *core.Envelope, textFormat string) (string, error) {
	if env == nil {
		return "", nil
	}
	if strings.EqualFold(strings.TrimSpace(textFormat), spec.TextFormatJSON) {
		rawJSON, err := MarshalEnvelopeJSON(env)
		if err != nil {
			return "", err
		}
		if env.OK {
			return WrapUntrusted(strings.TrimSuffix(rawJSON, "\n")), nil
		}
		return rawJSON, nil
	}
	if !env.OK && env.Error != nil {
		return formatMCPErrorText(env.Error), nil
	}
	if len(env.Pages) > 0 {
		return formatMCPBatchFetchText(env), nil
	}
	if env.Page == nil {
		return "No page content available.", nil
	}

	p := env.Page
	targetURL := p.URL
	if p.FinalURL != "" {
		targetURL = p.FinalURL
	}

	var inner strings.Builder
	if p.Title != "" && !hasLeadingDuplicateH1(p.Content, p.Title) {
		fmt.Fprintf(&inner, "Title: %s\n", p.Title)
	}
	fmt.Fprintf(&inner, "URL: %s\n", targetURL)
	if metaLine := formatPageMetadataLine(p); metaLine != "" {
		fmt.Fprintf(&inner, "%s\n", metaLine)
	}
	fmt.Fprintf(&inner, "\n%s", p.Content)

	var out strings.Builder
	costStr := core.FormatCosts(env.Meta.Cost)
	if costStr != "" {
		fmt.Fprintf(&out, "Source: %s (%s, %s)\n\n", p.SourceType, p.Provider, costStr)
	} else {
		fmt.Fprintf(&out, "Source: %s (%s)\n\n", p.SourceType, p.Provider)
	}
	out.WriteString(WrapUntrusted(inner.String()))

	if p.NextOffset != nil {
		shown := textnorm.RuneCount(p.Content)
		fmt.Fprintf(&out, "\n\nContent truncated: %d of %d chars shown. Call web_fetch with offset=%d (or section/match) to continue.", shown, p.TotalChars, *p.NextOffset)
		if outlineHint := formatCompactOutline(p.Outline); outlineHint != "" {
			fmt.Fprintf(&out, "\nOutline: %s", outlineHint)
		}
	}
	if len(env.Meta.Notes) > 0 {
		out.WriteString("\nNotes: " + strings.Join(env.Meta.Notes, "; "))
	}
	return out.String(), nil
}

func formatMCPBatchFetchText(env *core.Envelope) string {
	var out strings.Builder
	provLabel := activeProvidersLabel(env.Meta.Providers)
	costStr := core.FormatCosts(env.Meta.Cost)
	if costStr != "" {
		fmt.Fprintf(&out, "Fetched %d pages (%s, %s):\n", len(env.Pages), provLabel, costStr)
	} else {
		fmt.Fprintf(&out, "Fetched %d pages (%s):\n", len(env.Pages), provLabel)
	}

	for i, p := range env.Pages {
		out.WriteString("\n")
		if p.Error != nil {
			fmt.Fprintf(&out, "[%d] %s — Error [%s]: %s\n", i+1, p.URL, p.Error.Code, p.Error.Message)
			continue
		}
		targetURL := p.URL
		if p.FinalURL != "" {
			targetURL = p.FinalURL
		}
		fmt.Fprintf(&out, "[%d] Source: %s (%s)\n", i+1, p.SourceType, p.Provider)
		var inner strings.Builder
		if p.Title != "" && !hasLeadingDuplicateH1(p.Content, p.Title) {
			fmt.Fprintf(&inner, "Title: %s\n", p.Title)
		}
		fmt.Fprintf(&inner, "URL: %s\n\n%s", targetURL, p.Content)
		out.WriteString(WrapUntrusted(inner.String()))
		if p.NextOffset != nil {
			fmt.Fprintf(&out, "\nTruncated at %d/%d chars (next_offset=%d).", textnorm.RuneCount(p.Content), p.TotalChars, *p.NextOffset)
		}
		out.WriteString("\n")
	}
	if len(env.Meta.Notes) > 0 {
		out.WriteString("\nNotes: " + strings.Join(env.Meta.Notes, "; "))
	}
	return strings.TrimRight(out.String(), "\n")
}

func hasLeadingDuplicateH1(content, title string) bool {
	if content == "" || title == "" {
		return false
	}
	firstLine, _, _ := strings.Cut(strings.TrimSpace(content), "\n")
	firstLine = strings.TrimSpace(firstLine)
	if !strings.HasPrefix(firstLine, "# ") {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(firstLine[2:]), strings.TrimSpace(title))
}

func formatPageMetadataLine(p *core.Page) string {
	var parts []string
	if p.Site != "" {
		parts = append(parts, "Site: "+p.Site)
	}
	if p.Author != "" {
		parts = append(parts, "Author: "+p.Author)
	}
	if p.Published != nil {
		parts = append(parts, "Published: "+p.Published.Format("2006-01-02"))
	}
	return strings.Join(parts, " | ")
}

func formatCompactOutline(outline []core.PageSection) string {
	if len(outline) == 0 {
		return ""
	}
	limit := min(len(outline), 12)
	parts := make([]string, 0, limit)
	for i := range limit {
		sec := outline[i]
		parts = append(parts, fmt.Sprintf("[%d] %s %s", sec.Offset, strings.Repeat("#", max(1, sec.Level)), sec.Heading))
	}
	if len(outline) > limit {
		parts = append(parts, fmt.Sprintf("...(+%d more)", len(outline)-limit))
	}
	return strings.Join(parts, " | ")
}

func formatMCPErrorText(e *core.ErrorInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Error [%s]: %s", e.Code, e.Message)
	if e.Provider != "" {
		fmt.Fprintf(&b, " (provider: %s)", e.Provider)
	}
	if e.Hint != "" {
		fmt.Fprintf(&b, "\nHint: %s", e.Hint)
	}
	return b.String()
}
