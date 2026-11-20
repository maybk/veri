package httpx

import (
	"context"
	"math"
	"sync"
	"time"

	"veri/internal/core"
	"veri/internal/spec"
)

const maxGateInterval = time.Hour

// Gate 控制单个 Provider 的最大并发请求数与可选 QPS 速率上限。
type Gate struct {
	sem         chan struct{}
	minInterval time.Duration
	mu          sync.Mutex
	nextAllowed time.Time
}

// NewGate 构造并发与速率门控（concurrency <= 0 默认 4；qps <= 0 或非有限数表示不限制 QPS）。
func NewGate(concurrency int, qps float64) *Gate {
	if concurrency <= 0 {
		concurrency = spec.DefaultProviderConcurrency
	}
	var interval time.Duration
	if qps > 0 && !math.IsNaN(qps) && !math.IsInf(qps, 0) {
		secPerReq := 1.0 / qps
		if secPerReq >= maxGateInterval.Seconds() {
			interval = maxGateInterval
		} else {
			interval = time.Duration(secPerReq * float64(time.Second))
		}
	}
	return &Gate{
		sem:         make(chan struct{}, concurrency),
		minInterval: interval,
	}
}

// Acquire 等待获取并发槽位与速率令牌，排队时间计入 ctx 截止时间。
func (g *Gate) Acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case g.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, core.WrapError(core.CodeUpstreamTimeout, "timed out waiting for provider concurrency slot", ctx.Err())
	}

	if g.minInterval > 0 {
		g.mu.Lock()
		now := time.Now()
		var wait time.Duration
		if now.Before(g.nextAllowed) {
			wait = g.nextAllowed.Sub(now)
			g.nextAllowed = g.nextAllowed.Add(g.minInterval)
		} else {
			g.nextAllowed = now.Add(g.minInterval)
		}
		g.mu.Unlock()

		if wait > 0 {
			if err := SleepContext(ctx, wait); err != nil {
				<-g.sem
				return nil, core.WrapError(core.CodeUpstreamTimeout, "timed out waiting for provider QPS limiter", err)
			}
		}
	}
	return func() { <-g.sem }, nil
}
