package router

import (
	"fmt"

	"veri/internal/core"
	"veri/internal/spec"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

// applyFieldCapsAndBudget 先剥离输出 URL 的营销追踪参数、裁剪单条 snippet/summary/content 字段上限，再按四级策略执行 O(N) 增量整包字符预算截断。
func applyFieldCapsAndBudget(results []core.Result, summaryMaxRunes, contentMaxRunes, totalMaxChars int) ([]core.Result, bool, []string) {
	if len(results) == 0 {
		return []core.Result{}, false, nil
	}

	out := make([]core.Result, len(results))
	for i := range results {
		out[i] = results[i].Clone()
		out[i].URL = urlnorm.StripTrackingParams(out[i].URL)
		if textnorm.RuneCount(out[i].Snippet) > spec.DefaultSnippetMaxRunes {
			out[i].Snippet = textnorm.TruncateRunes(out[i].Snippet, spec.DefaultSnippetMaxRunes)
		}
		if summaryMaxRunes > 0 && textnorm.RuneCount(out[i].Summary) > summaryMaxRunes {
			out[i].Summary = textnorm.TruncateRunes(out[i].Summary, summaryMaxRunes)
		}
		if contentMaxRunes > 0 && textnorm.RuneCount(out[i].Content) > contentMaxRunes {
			out[i].Content = textnorm.TruncateRunes(out[i].Content, contentMaxRunes)
		}
	}

	currentChars := estimateResultsChars(out)
	if totalMaxChars <= 0 || currentChars <= totalMaxChars {
		return out, false, nil
	}

	firstAffectedRank := 0
	mark := func(idx int) {
		rank := out[idx].Rank
		if firstAffectedRank == 0 || rank < firstAffectedRank {
			firstAffectedRank = rank
		}
	}

	// 第 1 步：从末尾开始逐条去掉 content
	for i := len(out) - 1; i >= 0 && currentChars > totalMaxChars; i-- {
		if out[i].Content != "" {
			before := resultBodyChars(out[i])
			out[i].Content = ""
			currentChars -= before - resultBodyChars(out[i])
			mark(i)
		}
	}

	// 第 2 步：从末尾开始把 summary 缩短到 200 字
	for i := len(out) - 1; i >= 0 && currentChars > totalMaxChars; i-- {
		if textnorm.RuneCount(out[i].Summary) > spec.DefaultSnippetMaxRunes {
			before := resultBodyChars(out[i])
			out[i].Summary = textnorm.TruncateRunes(out[i].Summary, spec.DefaultSnippetMaxRunes)
			currentChars -= before - resultBodyChars(out[i])
			mark(i)
		}
	}

	// 第 3 步：从末尾开始把 summary 替换为更短的 snippet
	for i := len(out) - 1; i >= 0 && currentChars > totalMaxChars; i-- {
		if out[i].Summary != "" && textnorm.RuneCount(out[i].Snippet) < textnorm.RuneCount(out[i].Summary) {
			before := resultBodyChars(out[i])
			out[i].Summary = out[i].Snippet
			currentChars -= before - resultBodyChars(out[i])
			mark(i)
		}
	}

	// 第 4 步：紧预算下先压缩为紧凑 snippet，再从尾部裁剪整条低排名结果（保底至少保留第 1 条），避免输出全空摘要的 URL 骨架。
	for i := len(out) - 1; i >= 0 && currentChars > totalMaxChars; i-- {
		before := resultBodyChars(out[i])
		changed := false
		if out[i].Summary != "" {
			if out[i].Snippet == "" {
				out[i].Snippet = textnorm.TruncateRunes(textnorm.CleanInline(out[i].Summary), compactBudgetSnippetRunes)
			}
			out[i].Summary = ""
			changed = true
		}
		if textnorm.RuneCount(out[i].Snippet) > compactBudgetSnippetRunes {
			out[i].Snippet = textnorm.TruncateRunes(out[i].Snippet, compactBudgetSnippetRunes)
			changed = true
		}
		if changed {
			currentChars -= before - resultBodyChars(out[i])
			mark(i)
		}
	}

	origCount := len(out)
	for len(out) > 1 && currentChars > totalMaxChars {
		last := out[len(out)-1]
		currentChars -= estimateResultChars(last)
		out = out[:len(out)-1]
	}

	if currentChars > totalMaxChars && len(out) == 1 && (out[0].Summary != "" || out[0].Snippet != "") {
		out[0].Summary = ""
		out[0].Snippet = ""
		mark(0)
	}

	if len(out) < origCount {
		note := fmt.Sprintf("Response trimmed to %d of %d results to fit %d-char budget; fetch specific URLs directly for full text.", len(out), origCount, totalMaxChars)
		return out, true, []string{note}
	}
	if firstAffectedRank == 0 {
		firstAffectedRank = 1
	}
	note := fmt.Sprintf("Response truncated to fit %d-char budget starting at result #%d; fetch specific URLs directly for full text.", totalMaxChars, firstAffectedRank)
	return out, true, []string{note}
}

func estimateResultsChars(results []core.Result) int {
	total := 0
	for _, r := range results {
		total += estimateResultChars(r)
	}
	return total
}

func estimateResultChars(r core.Result) int {
	return textnorm.RuneCount(r.Title) + len(r.URL) + textnorm.RuneCount(r.Site) + resultBodyChars(r) + 32
}

func resultBodyChars(r core.Result) int {
	if r.Content != "" {
		return textnorm.RuneCount(r.Content)
	}
	if r.Summary != "" {
		return textnorm.RuneCount(r.Summary)
	}
	return textnorm.RuneCount(r.Snippet)
}
