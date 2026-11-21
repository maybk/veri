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
// 若调用方截止时间早于可发车时刻，则立即失败且不推进后续正常请求的速率窗口；排队途中被取消的尾部预留会自动回滚。
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
		target := now
		if now.Before(g.nextAllowed) {
			target = g.nextAllowed
		}
		wait := target.Sub(now)
		if dl, hasDL := ctx.Deadline(); hasDL && target.After(dl) {
			g.mu.Unlock()
			<-g.sem
			return nil, core.WrapError(core.CodeUpstreamTimeout, "context deadline exceeded before provider QPS slot", context.DeadlineExceeded)
		}
		reservedNext := target.Add(g.minInterval)
		g.nextAllowed = reservedNext
		g.mu.Unlock()

		if wait > 0 {
			if err := SleepContext(ctx, wait); err != nil {
				g.mu.Lock()
				if g.nextAllowed.Equal(reservedNext) {
					g.nextAllowed = target
				}
				g.mu.Unlock()
				<-g.sem
				return nil, core.WrapError(core.CodeUpstreamTimeout, "timed out waiting for provider QPS limiter", err)
			}
		}
	}
	return func() { <-g.sem }, nil
}
