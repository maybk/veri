package httpx

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"time"
)

const defaultHappyEyeballsDelay = 300 * time.Millisecond

type dialFunc func(ctx context.Context, network, address string) (net.Conn, error)

type dialResult struct {
	conn net.Conn
	err  error
}

// dialAllowed 对已通过 SSRFGuard 预校验的地址列表执行连接：
// 单地址与单协议族直接零额外开销顺序拨号；双栈地址采用精简 Happy Eyeballs（300ms 延迟后拉起另一协议族竞速），
// 既避免 IPv6 路由黑洞导致的 5s+ 阻塞，又确保零连接与零 goroutine 泄漏。
func (g *SSRFGuard) dialAllowed(ctx context.Context, baseDial dialFunc, network, port string, allowed []netip.Addr) (net.Conn, error) {
	if len(allowed) == 1 {
		return baseDial(ctx, network, net.JoinHostPort(allowed[0].String(), port))
	}

	primaries, fallbacks := partitionByPrimaryFamily(allowed)
	if len(fallbacks) == 0 {
		return dialSerial(ctx, baseDial, network, port, primaries)
	}

	delay := defaultHappyEyeballsDelay
	if g != nil && g.happyEyeballsDelay > 0 {
		delay = g.happyEyeballsDelay
	}
	return dialDualStackParallel(ctx, baseDial, network, port, primaries, fallbacks, delay)
}

func partitionByPrimaryFamily(addrs []netip.Addr) (primaries, fallbacks []netip.Addr) {
	firstIs4 := addrs[0].Is4()
	primaries = make([]netip.Addr, 0, len(addrs))
	for _, a := range addrs {
		if a.Is4() == firstIs4 {
			primaries = append(primaries, a)
		} else {
			fallbacks = append(fallbacks, a)
		}
	}
	return primaries, fallbacks
}

func dialSerial(ctx context.Context, baseDial dialFunc, network, port string, addrs []netip.Addr) (net.Conn, error) {
	var lastErr error
	for _, addr := range addrs {
		if err := ctx.Err(); err != nil {
			if lastErr != nil {
				return nil, lastErr
			}
			return nil, err
		}
		conn, err := baseDial(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	return nil, lastErr
}

func dialDualStackParallel(ctx context.Context, baseDial dialFunc, network, port string, primaries, fallbacks []netip.Addr, delay time.Duration) (net.Conn, error) {
	raceCtx, cancelRace := context.WithCancel(ctx)
	defer cancelRace()

	results := make(chan dialResult, 2)
	var (
		mu      sync.Mutex
		claimed bool
	)

	startLane := func(addrs []netip.Addr) {
		go func() {
			conn, err := dialSerial(raceCtx, baseDial, network, port, addrs)
			if err != nil {
				results <- dialResult{err: err}
				return
			}
			mu.Lock()
			if claimed {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			claimed = true
			mu.Unlock()
			cancelRace()
			results <- dialResult{conn: conn}
		}()
	}

	// 启动首选协议族；若其在 delay 内快速成功或快速失败，则无需额外并发。
	startLane(primaries)

	timer := time.NewTimer(delay)
	defer timer.Stop()

	var firstErr error
	select {
	case res := <-results:
		if res.err == nil {
			return res.conn, nil
		}
		firstErr = res.err
		// 首选协议族已快速失败，直接在当前协程顺序拨备选协议族。
		conn, fbErr := dialSerial(ctx, baseDial, network, port, fallbacks)
		if fbErr == nil {
			return conn, nil
		}
		return nil, firstErr
	case <-timer.C:
		startLane(fallbacks)
	case <-ctx.Done():
		return nil, handleCanceledRace(&mu, &claimed, cancelRace, results, ctx.Err())
	}

	var lastErr error
	for lanesRunning := 2; lanesRunning > 0; {
		select {
		case res := <-results:
			lanesRunning--
			if res.err == nil {
				return res.conn, nil
			}
			if firstErr == nil {
				firstErr = res.err
			}
			lastErr = res.err
		case <-ctx.Done():
			return nil, handleCanceledRace(&mu, &claimed, cancelRace, results, ctx.Err())
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, lastErr
}

// handleCanceledRace 在外层 ctx 取消时原子抢占归属权，确保任何竞速分支已建立的连接都会被立即关闭。
func handleCanceledRace(mu *sync.Mutex, claimed *bool, cancelRace context.CancelFunc, results <-chan dialResult, ctxErr error) error {
	mu.Lock()
	alreadyClaimed := *claimed
	*claimed = true
	mu.Unlock()
	cancelRace()

	if alreadyClaimed {
		for {
			res := <-results
			if res.conn != nil {
				_ = res.conn.Close()
				break
			}
		}
	}
	return ctxErr
}
