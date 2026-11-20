package keypool

import (
	"testing"
	"time"
)

func TestPoolRoundRobinCooldownAndEviction(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	p := newWithClock(func() time.Time { return now }, "sk-key1, sk-key2\nsk-key3, sk-key1")

	if p.Size() != 3 {
		t.Fatalf("Size() = %d, want 3 deduplicated keys", p.Size())
	}

	l1, ok := p.Acquire(-1)
	l2, ok2 := p.Acquire(-1)
	l3, ok3 := p.Acquire(-1)
	if !ok || !ok2 || !ok3 || l1.Value == l2.Value || l2.Value == l3.Value {
		t.Fatalf("expected round-robin distinct keys, got %+v %+v %+v", l1, l2, l3)
	}

	p.ReportAuthFailed(l1.ID)
	p.ReportRateLimited(l2.ID, 0)
	p.ReportInsufficientBalance(l3.ID)

	// 并发乱序到达的旧请求成功回调不应提前解除正在生效的限流冷却窗口。
	p.ReportSuccess(l2.ID)
	if _, avail := p.Acquire(-1); avail {
		t.Fatal("expected out-of-order ReportSuccess not to clear active cooldown")
	}

	now = now.Add(31 * time.Second)
	got, avail := p.Acquire(-1)
	if !avail || got.ID != l2.ID {
		t.Fatalf("expected key2 to recover after 31s, got %+v (avail=%v)", got, avail)
	}

	p.ReportRateLimited(l2.ID, 0)
	now = now.Add(45 * time.Second)
	if _, avail := p.Acquire(-1); avail {
		t.Fatal("expected key2 to still be cooling down at 45s on 2nd strike")
	}
	now = now.Add(20 * time.Second)
	got, avail = p.Acquire(-1)
	if !avail || got.ID != l2.ID {
		t.Fatal("expected key2 to recover after 65s")
	}

	p.ReportSuccess(l2.ID)
	p.ReportRateLimited(l2.ID, 0)
	now = now.Add(31 * time.Second)
	if _, avail := p.Acquire(-1); !avail {
		t.Fatal("expected strikes reset after ReportSuccess post-cooldown")
	}
}
