package router

import (
	"slices"
	"sync"
	"time"

	"veri/internal/core"
)

type circuitEntry struct {
	failures      int
	degradedUntil time.Time
}

type healthTracker struct {
	mu      sync.Mutex
	entries map[string]circuitEntry
	nowFn   func() time.Time
}

func newHealthTracker() *healthTracker {
	return &healthTracker{
		entries: make(map[string]circuitEntry),
		nowFn:   time.Now,
	}
}

func (h *healthTracker) recordSuccess(provider string) {
	if h == nil || provider == "" {
		return
	}
	h.mu.Lock()
	delete(h.entries, provider)
	h.mu.Unlock()
}

func (h *healthTracker) recordError(provider string, se *core.Error) {
	if h == nil || provider == "" || se == nil {
		return
	}
	if se.Code != core.CodeUpstreamTimeout && se.Code != core.CodeUpstreamUnavailable {
		return
	}
	now := h.nowFn()
	h.mu.Lock()
	cur := h.entries[provider]
	cur.failures++
	if cur.failures >= defaultCircuitFailureThreshold {
		cur.degradedUntil = now.Add(defaultCircuitCooldown)
	}
	h.entries[provider] = cur
	h.mu.Unlock()
}

func (h *healthTracker) isDegraded(provider string, now time.Time) bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	cur, ok := h.entries[provider]
	h.mu.Unlock()
	return ok && now.Before(cur.degradedUntil)
}

// orderByHealth 将处于连续瞬态故障冷却窗口内的 Provider 稳定后置，仅在同时存在健康与降级候选时重排。
func orderByHealth[T core.Provider](h *healthTracker, chain []T) []T {
	if h == nil || len(chain) <= 1 {
		return chain
	}
	now := h.nowFn()
	hasHealthy := false
	hasDegraded := false
	for _, p := range chain {
		if h.isDegraded(p.Name(), now) {
			hasDegraded = true
		} else {
			hasHealthy = true
		}
	}
	if !hasDegraded || !hasHealthy {
		return chain
	}
	out := slices.Clone(chain)
	slices.SortStableFunc(out, func(a, b T) int {
		da := h.isDegraded(a.Name(), now)
		db := h.isDegraded(b.Name(), now)
		switch {
		case !da && db:
			return -1
		case da && !db:
			return 1
		default:
			return 0
		}
	})
	return out
}
