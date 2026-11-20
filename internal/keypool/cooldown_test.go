package keypool

import (
	"testing"
	"time"
)

func TestKeyPoolEdgeCasesAndDiagnose(t *testing.T) {
	var nilPool *Pool
	if nilPool.Size() != 0 || nilPool.AllValues() != nil || nilPool.DiagnoseExhaustion() != ExhaustEmpty {
		t.Fatal("unexpected nil pool behavior")
	}
	nilPool.ReportSuccess(0)
	nilPool.ReportRateLimited(0, time.Second)
	nilPool.ReportInsufficientBalance(0)
	nilPool.ReportAuthFailed(0)

	emptyPool := New(" , \n ")
	if emptyPool.DiagnoseExhaustion() != ExhaustEmpty {
		t.Fatalf("expected ExhaustEmpty, got %s", emptyPool.DiagnoseExhaustion())
	}

	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	p := newWithClock(func() time.Time { return now }, "k1,k2")
	if len(p.AllValues()) != 2 {
		t.Fatalf("expected 2 values, got %v", p.AllValues())
	}

	// 测试显式超长 Retry-After 封顶 10m
	p.ReportRateLimited(0, 30*time.Minute)
	p.ReportInsufficientBalance(1)
	if got := p.DiagnoseExhaustion(); got != ExhaustRateLimited {
		t.Fatalf("expected ExhaustRateLimited when one slot is rate-limited, got %s", got)
	}

	// 推进 11 分钟：k1 恢复，k2 仍在 1h 余额冷却中；若再把 k1 剔除，应报告 ExhaustBalance
	now = now.Add(11 * time.Minute)
	p.ReportAuthFailed(0)
	if got := p.DiagnoseExhaustion(); got != ExhaustBalance {
		t.Fatalf("expected ExhaustBalance, got %s", got)
	}

	// 把 k2 也剔除，应报告 ExhaustAuthFailed
	p.ReportAuthFailed(1)
	if got := p.DiagnoseExhaustion(); got != ExhaustAuthFailed {
		t.Fatalf("expected ExhaustAuthFailed, got %s", got)
	}
}
