package httpx

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const (
	minRetryBackoff      = 500 * time.Millisecond
	retryBackoffJitter   = 1000 * time.Millisecond
	maxRetryWait         = 10 * time.Second
	maxRetryAfterSeconds = 86400
)

// ShouldRetryStatus 判断上游明确返回的 HTTP 状态码是否安全且允许重试。
// 仅当收到明确未计费的 408 / 429 / 500 / 502 / 503 / 504 响应时返回 true；
// 网络读超时或连接中断未拿到状态码时绝不重试，防止重复扣费。
func ShouldRetryStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// ParseRetryAfter 解析 HTTP 响应中的 Retry-After 头（支持整数秒与 HTTP-date，上限 24h 防整数溢出）。
func ParseRetryAfter(val string, now time.Time) time.Duration {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return 0
	}
	if secs, err := strconv.Atoi(trimmed); err == nil {
		if secs <= 0 {
			return 0
		}
		return time.Duration(min(secs, maxRetryAfterSeconds)) * time.Second
	}
	if t, err := http.ParseTime(trimmed); err == nil {
		if d := t.Sub(now); d > 0 {
			return min(d, time.Duration(maxRetryAfterSeconds)*time.Second)
		}
	}
	return 0
}

// RetryDelay 计算单次重试前的安全退避时间（遵循 Retry-After 封顶 10s，缺省采用 500ms～1500ms 随机抖动退避）。
func RetryDelay(retryAfterHeader string, now time.Time) time.Duration {
	if d := ParseRetryAfter(retryAfterHeader, now); d > 0 {
		return min(d, maxRetryWait)
	}
	var b [8]byte
	if _, err := crand.Read(b[:]); err == nil && retryBackoffJitter > 0 {
		jitter := binary.LittleEndian.Uint64(b[:]) % uint64(retryBackoffJitter)
		if jitter <= uint64(maxRetryWait) {
			return minRetryBackoff + time.Duration(jitter)
		}
	}
	return minRetryBackoff
}

// SleepContext 在尊重 ctx 取消或截止时间的前提下休眠指定时长。
func SleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
