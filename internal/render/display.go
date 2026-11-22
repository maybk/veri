package render

import (
	"fmt"
	"net/url"
	"strings"

	"veri/internal/core"
)

// displayResultTitle 返回检索结果的展示标题，按 Title -> Site -> URL 主机名 -> URL 依次回退，消除上游空标题导致的残缺渲染。
func displayResultTitle(r core.Result) string {
	if t := strings.TrimSpace(r.Title); t != "" {
		return t
	}
	if s := strings.TrimSpace(r.Site); s != "" {
		return s
	}
	if raw := strings.TrimSpace(r.URL); raw != "" {
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			return u.Host
		}
		return raw
	}
	return "(untitled)"
}

// activeProvidersLabel 保序去重提取成功/空响应的 Provider 名称并用 '+' 连接，防止批量抓取重复拼接同类源名（如 tinyfish+tinyfish）。
func activeProvidersLabel(reports []core.ProviderReport) string {
	seen := make(map[string]struct{}, len(reports))
	var names []string
	for _, rep := range reports {
		if rep.Status != core.ProviderStatusOK && rep.Status != core.ProviderStatusEmpty {
			continue
		}
		if _, dup := seen[rep.Name]; dup {
			continue
		}
		seen[rep.Name] = struct{}{}
		names = append(names, rep.Name)
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, "+")
}

// resultProvidersLabel 在融合检索或多源共识命中时返回保序去重的来源引擎拼接标签（如 "parallel+exa"），单源常规检索返回空。
func resultProvidersLabel(strategy string, providers []string) string {
	if len(providers) == 0 || (strategy != string(core.StrategyFusion) && len(providers) <= 1) {
		return ""
	}
	seen := make(map[string]struct{}, len(providers))
	names := make([]string, 0, len(providers))
	for _, raw := range providers {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	if strategy != string(core.StrategyFusion) && len(names) <= 1 {
		return ""
	}
	return strings.Join(names, "+")
}

// FormatOutlinePreview 将页面大纲的前 limit 条标题格式化为紧凑单行预览，供 CLI 截断提示与 MCP 续读指引共用。
func FormatOutlinePreview(outline []core.PageSection, limit int) string {
	if len(outline) == 0 {
		return ""
	}
	if limit <= 0 || limit > len(outline) {
		limit = len(outline)
	}
	parts := make([]string, 0, limit+1)
	for i := range limit {
		sec := outline[i]
		parts = append(parts, fmt.Sprintf("%s (%d)", sec.Heading, sec.Offset))
	}
	if len(outline) > limit {
		parts = append(parts, fmt.Sprintf("...(+%d)", len(outline)-limit))
	}
	return strings.Join(parts, " | ")
}

const (
	noteSectionMissMarker = "not found; showing page preview"
	noteMatchMissMarker   = "had 0 hits"
)

// ResolveFetchContinueScope 根据响应 notes 判定续读时仍应保留的 section 与 match 作用域。
// 当章节未命中回退至整页预览时，match 并未执行，因此 section 与 match 均须丢弃，避免将整页 offset 套用到失效视图。
func ResolveFetchContinueScope(section, match string, notes []string) (activeSection, activeMatch string) {
	sec := strings.TrimSpace(section)
	mat := strings.TrimSpace(match)
	if sec == "" && mat == "" {
		return "", ""
	}
	for _, n := range notes {
		if strings.Contains(n, noteSectionMissMarker) {
			return "", ""
		}
		if strings.Contains(n, noteMatchMissMarker) {
			mat = ""
		}
	}
	return sec, mat
}

func hasScopedMissNote(notes []string) bool {
	for _, n := range notes {
		if strings.Contains(n, noteSectionMissMarker) || strings.Contains(n, noteMatchMissMarker) {
			return true
		}
	}
	return false
}
