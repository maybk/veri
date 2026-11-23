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
	now := h.nowFn()
	h.mu.Lock()
	defer h.mu.Unlock()
	cur := h.entries[provider]
	switch se.Code {
	case core.CodeUpstreamTimeout, core.CodeUpstreamUnavailable:
		cur.failures++
		if cur.failures >= defaultCircuitFailureThreshold {
			until := now.Add(defaultCircuitCooldown)
			if until.After(cur.degradedUntil) {
				cur.degradedUntil = until
			}
		}
	case core.CodeInsufficientBalance, core.CodeAuthFailed, core.CodeNotEnabled:
		// 余额/配额耗尽、鉴权失败或服务未开通仅在 keypool 无备用健康 Key 时才透传至路由层，立即短冷却后置以免连续空耗。
		until := now.Add(defaultAccountFailureCooldown)
		if until.After(cur.degradedUntil) {
			cur.degradedUntil = until
		}
	default:
		return
	}
	h.entries[provider] = cur
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

// orderByHealth 将处于故障或配额/鉴权冷却窗口内的 Provider 稳定后置，仅在同时存在健康与降级候选时重排。
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
