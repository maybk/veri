// Package keypool 实现同一厂商多把 API Key 的轮询分配、按状态退避冷却与鉴权失效隔离。
//
// 依赖边界：仅依赖 Go 标准库。日志与外部观测仅暴露掩码标识，不暴露原始明文。
package keypool

import (
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultBaseCooldown    = 30 * time.Second
	maxRateLimitCooldown   = 10 * time.Minute
	authFailCooldown       = 30 * time.Minute
	balanceExhaustCooldown = 1 * time.Hour
)

// Lease 表示单次请求借用的一把可用密钥租约。
type Lease struct {
	ID    int
	Value string
}

// ExhaustReason 说明 Key 池当前无可用 Key 的主导原因。
type ExhaustReason string

const (
	// ExhaustEmpty 未配置任何 Key。
	ExhaustEmpty ExhaustReason = "empty"
	// ExhaustAuthFailed 所有配置的 Key 均因鉴权失败处于冷却隔离中。
	ExhaustAuthFailed ExhaustReason = "auth_failed"
	// ExhaustBalance Key 因余额或额度耗尽处于冷却中。
	ExhaustBalance ExhaustReason = "insufficient_balance"
	// ExhaustRateLimited Key 因触发频率限制处于冷却中。
	ExhaustRateLimited ExhaustReason = "rate_limited"
)

type slot struct {
	value         string
	cooldownUntil time.Time
	rateStrikes   int
	lastReason    ExhaustReason
}

// Pool 管理单个 Provider 的一组 API Key。
type Pool struct {
	mu     sync.Mutex
	slots  []slot
	cursor atomic.Uint64
	nowFn  func() time.Time
}

// New 从密钥切片创建 Key 池（自动拆分逗号或换行分隔项并去重，严格按配置主备顺序起始轮询与故障切换）。
func New(keys ...string) *Pool {
	return newWithClock(time.Now, keys...)
}

func newWithClock(nowFn func() time.Time, keys ...string) *Pool {
	seen := make(map[string]struct{})
	var slots []slot
	for _, raw := range keys {
		for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == '\n' || r == '\r'
		}) {
			val := strings.TrimSpace(part)
			if val == "" {
				continue
			}
			if _, dup := seen[val]; dup {
				continue
			}
			seen[val] = struct{}{}
			slots = append(slots, slot{value: val})
		}
	}
	return &Pool{
		slots: slots,
		nowFn: nowFn,
	}
}

// Size 返回池中配置的去重后 Key 总数。
func (p *Pool) Size() int {
	if p == nil {
		return 0
	}
	return len(p.slots)
}

// AllValues 返回全部配置的密钥切片副本（仅供内部脱敏器注册使用）。
func (p *Pool) AllValues() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]string, len(p.slots))
	for i, s := range p.slots {
		out[i] = s.value
	}
	return out
}

// Acquire 按轮询顺序获取一把当前不在冷却期的可用 Key；可传入 excludeID 跳过刚刚失败的 Key。
func (p *Pool) Acquire(excludeID int) (Lease, bool) {
	if p == nil || len(p.slots) == 0 {
		return Lease{}, false
	}
	now := p.nowFn()
	n := len(p.slots)
	rem := (p.cursor.Add(1) - 1) % uint64(n)
	start := 0
	if rem <= uint64(math.MaxInt) {
		start = int(rem)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// 第一轮优先跳过 excludeID；若仅剩 excludeID 可用且 excludeID 未进入冷却，则不返回它以免原地死循环。
	for offset := range n {
		idx := (start + offset) % n
		if idx == excludeID {
			continue
		}
		s := &p.slots[idx]
		if now.Before(s.cooldownUntil) {
			continue
		}
		return Lease{ID: idx, Value: s.value}, true
	}
	return Lease{}, false
}

// DiagnoseExhaustion 返回当前池内无可用 Key 时的最准确原因分类。
func (p *Pool) DiagnoseExhaustion() ExhaustReason {
	if p == nil || len(p.slots) == 0 {
		return ExhaustEmpty
	}
	now := p.nowFn()
	p.mu.Lock()
	defer p.mu.Unlock()

	hasBalance := false
	hasRate := false
	hasAuth := false
	for _, s := range p.slots {
		if now.Before(s.cooldownUntil) {
			switch s.lastReason {
			case ExhaustBalance:
				hasBalance = true
			case ExhaustRateLimited:
				hasRate = true
			case ExhaustAuthFailed:
				hasAuth = true
			}
		}
	}
	if hasRate {
		return ExhaustRateLimited
	}
	if hasBalance {
		return ExhaustBalance
	}
	if hasAuth {
		return ExhaustAuthFailed
	}
	return ExhaustRateLimited
}

// ReportSuccess 标记该 Key 请求成功，清零连续限流计数（若该槽位已被并发请求打入冷却期则保留冷却）。
func (p *Pool) ReportSuccess(id int) {
	if p == nil {
		return
	}
	now := p.nowFn()
	p.mu.Lock()
	defer p.mu.Unlock()
	if id >= 0 && id < len(p.slots) {
		if now.Before(p.slots[id].cooldownUntil) {
			return
		}
		p.slots[id].rateStrikes = 0
		p.slots[id].cooldownUntil = time.Time{}
		p.slots[id].lastReason = ""
	}
}

// ReportRateLimited 对触发 429 限流的 Key 施加冷却（优先遵循 retryAfter，否则 30s 指数退避，封顶 10m）。
func (p *Pool) ReportRateLimited(id int, retryAfter time.Duration) {
	if p == nil {
		return
	}
	now := p.nowFn()
	p.mu.Lock()
	defer p.mu.Unlock()
	if id < 0 || id >= len(p.slots) {
		return
	}
	s := &p.slots[id]
	s.rateStrikes++
	wait := retryAfter
	if wait <= 0 {
		shift := min(s.rateStrikes-1, 5)
		wait = min(defaultBaseCooldown*time.Duration(1<<shift), maxRateLimitCooldown)
	} else if wait > maxRateLimitCooldown {
		wait = maxRateLimitCooldown
	}
	until := now.Add(wait)
	if until.After(s.cooldownUntil) {
		s.cooldownUntil = until
		s.lastReason = ExhaustRateLimited
	}
}

// ReportInsufficientBalance 对余额/配额耗尽的 Key 冷却 1 小时。
func (p *Pool) ReportInsufficientBalance(id int) {
	if p == nil {
		return
	}
	until := p.nowFn().Add(balanceExhaustCooldown)
	p.mu.Lock()
	defer p.mu.Unlock()
	if id >= 0 && id < len(p.slots) && until.After(p.slots[id].cooldownUntil) {
		p.slots[id].cooldownUntil = until
		p.slots[id].lastReason = ExhaustBalance
	}
}

// ReportAuthFailed 对鉴权失败（401）的 Key 施加 30 分钟长冷却（兼顾快速剔除与长驻 MCP 网关偶发误报后的自愈能力）。
func (p *Pool) ReportAuthFailed(id int) {
	if p == nil {
		return
	}
	until := p.nowFn().Add(authFailCooldown)
	p.mu.Lock()
	defer p.mu.Unlock()
	if id >= 0 && id < len(p.slots) {
		if until.After(p.slots[id].cooldownUntil) {
			p.slots[id].cooldownUntil = until
		}
		p.slots[id].lastReason = ExhaustAuthFailed
	}
}
