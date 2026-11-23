package httpx

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestRetryStatusAndBackoffCalculation(t *testing.T) {
	if !ShouldRetryStatus(429) || !ShouldRetryStatus(503) || ShouldRetryStatus(400) || ShouldRetryStatus(402) {
		t.Fatal("unexpected ShouldRetryStatus matrix")
	}

	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	if d := RetryDelay("3", now); d != 3*time.Second {
		t.Fatalf("RetryDelay(3) = %v, want 3s", d)
	}
	if d := RetryDelay("999999999999", now); d != 10*time.Second {
		t.Fatalf("RetryDelay(huge) = %v, want capped 10s", d)
	}
	if d := RetryDelay("999", now); d != maxRetryWait {
		t.Fatalf("RetryDelay(999) = %v, want %v", d, maxRetryWait)
	}
	if d := RetryDelay("", now); d < minRetryBackoff || d >= minRetryBackoff+retryBackoffJitter {
		t.Fatalf("RetryDelay out of jitter range: %v", d)
	}

	httpDate := now.Add(4 * time.Second).Format(http.TimeFormat)
	if d := ParseRetryAfter(httpDate, now); d != 4*time.Second {
		t.Fatalf("ParseRetryAfter HTTP-date = %v, want 4s", d)
	}
	if d := ParseRetryAfter("999999999999", now); d != 86400*time.Second {
		t.Fatalf("ParseRetryAfter(huge) = %v, want capped 86400s", d)
	}
	if ParseRetryAfter("-5", now) != 0 || ParseRetryAfter("", now) != 0 {
		t.Fatal("expected 0 for negative/empty Retry-After")
	}

	if SleepContext(t.Context(), 0) != nil {
		t.Fatal("expected zero SleepContext to return nil")
	}
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := SleepContext(canceledCtx, time.Second); err == nil {
		t.Fatal("expected SleepContext on canceled ctx to fail")
	}
}

func TestGateQPSAndConcurrencyTimeout(t *testing.T) {
	gate := NewGate(1, 50) // 20ms 间隔
	rel1, err := gate.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	rel1()

	rel2, err := gate.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer rel2()

	// 不释放 rel2，验证第二个并发在极短超时下触发排队超时
	shortCtx, shortCancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer shortCancel()
	if _, err := gate.Acquire(shortCtx); err == nil {
		t.Fatal("expected Gate concurrency timeout")
	}
}

func TestGateAcquireDeadlineNoSlotPollutionAndCancelRollback(t *testing.T) {
	// 1. 已知超时不足以等到下一个 QPS 槽位时应快速失败，且不得推进 nextAllowed 污染后续健康请求
	g := NewGate(2, 4) // 250ms 间隔
	rel1, err := g.Acquire(t.Context())
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	rel1()

	shortCtx, cancelShort := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancelShort()
	t0 := time.Now()
	if _, err := g.Acquire(shortCtx); err == nil {
		t.Fatal("expected short-deadline acquire to fail immediately")
	}
	if elapsed := time.Since(t0); elapsed > 120*time.Millisecond {
		t.Fatalf("expected fast-fail before QPS wait, took %v", elapsed)
	}

	// 2. 排队途中被主动取消的尾部请求应回滚预留时间，不把后续正常请求推迟双倍间隔（~475ms）
	cancelCtx, cancelWait := context.WithCancel(t.Context())
	defer cancelWait()
	timer := time.AfterFunc(25*time.Millisecond, cancelWait)
	defer timer.Stop()
	if _, err := g.Acquire(cancelCtx); err == nil {
		t.Fatal("expected canceled acquire to fail")
	}

	okStart := time.Now()
	rel2, err := g.Acquire(t.Context())
	if err != nil {
		t.Fatalf("healthy acquire after failed/canceled calls: %v", err)
	}
	rel2()
	if elapsed := time.Since(okStart); elapsed > 380*time.Millisecond {
		t.Fatalf("nextAllowed was polluted by failed/canceled requests: healthy acquire took %v", elapsed)
	}
}
