package router

import (
	"fmt"
	"testing"

	"veri/internal/core"
)

func BenchmarkFuseRRF(b *testing.B) {
	providers := []string{"deepseek", "keenable", "exa", "parallel"}
	branches := make([]branchResult, len(providers))
	for pIdx, name := range providers {
		items := make([]core.Result, 10)
		for i := range items {
			// 制造部分跨源重合 URL 与近似内容，覆盖精确去重、指纹聚簇与排序热路径。
			docID := i
			if i >= 4 {
				docID = pIdx*10 + i
			}
			rel := 0.95 - float64(i)*0.05
			items[i] = core.Result{
				Title:     fmt.Sprintf("Go 1.27 Runtime Optimization Guide #%d", docID),
				URL:       fmt.Sprintf("https://docs.example.com/articles/go-runtime-%d?utm_source=%s", docID, name),
				Site:      "docs.example.com",
				Snippet:   fmt.Sprintf("Detailed benchmark analysis of Go 1.27 scheduler, GC pacing, and memory allocation in article %d.", docID),
				Summary:   fmt.Sprintf("Summary of Go 1.27 runtime optimization techniques and production tuning notes for article %d.", docID),
				Relevance: &rel,
			}
		}
		branches[pIdx] = branchResult{
			chainIdx: pIdx,
			name:     name,
			results:  items,
		}
	}

	weightFn := func(string) float64 { return 1.0 }
	b.ReportAllocs()
	for b.Loop() {
		out, _ := fuseRRF(branches, 60, weightFn, 10)
		if len(out) == 0 {
			b.Fatal("expected non-empty fused results")
		}
	}
}
