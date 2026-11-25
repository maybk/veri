package httpx

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"veri/internal/spec"
)

type closeTrackingConn struct {
	net.Conn
	closed atomic.Bool
}

func (c *closeTrackingConn) Close() error {
	c.closed.Store(true)
	return c.Conn.Close()
}

func newTrackedPipe() (*closeTrackingConn, net.Conn) {
	c1, c2 := net.Pipe()
	return &closeTrackingConn{Conn: c1}, c2
}

func TestSafeDialHappyEyeballsDualStack(t *testing.T) {
	t.Run("blackholed_ipv6_falls_back_to_ipv4_after_delay", func(t *testing.T) {
		v6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")
		v4 := netip.MustParseAddr("93.184.216.34")
		guard := &SSRFGuard{
			Resolver:           staticResolver{"dual.example": {v6, v4}},
			happyEyeballsDelay: 25 * time.Millisecond,
		}

		var v6Canceled atomic.Bool
		var dialed string
		baseDial := func(ctx context.Context, _, addr string) (net.Conn, error) {
			if addr == "["+v6.String()+"]:443" {
				<-ctx.Done()
				v6Canceled.Store(true)
				return nil, ctx.Err()
			}
			dialed = addr
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		}

		start := time.Now()
		conn, err := guard.SafeDialContext(baseDial)(t.Context(), "tcp", "dual.example:443")
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("expected IPv4 fallback to succeed, got %v", err)
		}
		_ = conn.Close()
		if dialed != "93.184.216.34:443" {
			t.Fatalf("dialed = %q, want 93.184.216.34:443", dialed)
		}
		if elapsed > 2*time.Second {
			t.Fatalf("expected fast Happy Eyeballs fallback, took %v", elapsed)
		}
		deadline := time.Now().Add(500 * time.Millisecond)
		for !v6Canceled.Load() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !v6Canceled.Load() {
			t.Fatal("expected blackholed IPv6 lane to be canceled after IPv4 won")
		}
	})

	t.Run("fast_primary_failure_switches_without_waiting_delay", func(t *testing.T) {
		v6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")
		v4 := netip.MustParseAddr("93.184.216.34")
		guard := &SSRFGuard{
			Resolver:           staticResolver{"fastfail.example": {v6, v4}},
			happyEyeballsDelay: 2 * time.Second,
		}

		baseDial := func(_ context.Context, _, addr string) (net.Conn, error) {
			if addr == "["+v6.String()+"]:443" {
				return nil, errors.New("network unreachable")
			}
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		}

		start := time.Now()
		conn, err := guard.SafeDialContext(baseDial)(t.Context(), "tcp", "fastfail.example:443")
		if err != nil {
			t.Fatalf("expected immediate IPv4 fallback, got %v", err)
		}
		_ = conn.Close()
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Fatalf("fast failure should not wait for happyEyeballsDelay, took %v", elapsed)
		}
	})

	t.Run("fast_primary_success_skips_fallback_lane", func(t *testing.T) {
		v6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")
		v4 := netip.MustParseAddr("93.184.216.34")
		guard := &SSRFGuard{
			Resolver:           staticResolver{"fastok.example": {v6, v4}},
			happyEyeballsDelay: 200 * time.Millisecond,
		}

		var v4Called atomic.Bool
		baseDial := func(_ context.Context, _, addr string) (net.Conn, error) {
			if addr == "93.184.216.34:443" {
				v4Called.Store(true)
			}
			c1, c2 := net.Pipe()
			_ = c2.Close()
			return c1, nil
		}

		conn, err := guard.SafeDialContext(baseDial)(t.Context(), "tcp", "fastok.example:443")
		if err != nil {
			t.Fatalf("expected primary success, got %v", err)
		}
		_ = conn.Close()
		if v4Called.Load() {
			t.Fatal("fallback lane should not be dialed when primary succeeds immediately")
		}
	})

	t.Run("simultaneous_winners_close_loser_conn", func(t *testing.T) {
		v6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")
		v4 := netip.MustParseAddr("93.184.216.34")
		guard := &SSRFGuard{
			Resolver:           staticResolver{"both.example": {v6, v4}},
			happyEyeballsDelay: 15 * time.Millisecond,
		}

		v6Conn, v6Peer := newTrackedPipe()
		defer func() { _ = v6Peer.Close() }()
		v4Conn, v4Peer := newTrackedPipe()
		defer func() { _ = v4Peer.Close() }()

		release := make(chan struct{})
		var started atomic.Int32
		baseDial := func(_ context.Context, _, addr string) (net.Conn, error) {
			if started.Add(1) == 2 {
				close(release)
			}
			<-release
			if addr == "["+v6.String()+"]:443" {
				return v6Conn, nil
			}
			return v4Conn, nil
		}

		winner, err := guard.SafeDialContext(baseDial)(t.Context(), "tcp", "both.example:443")
		if err != nil {
			t.Fatalf("expected one lane to win, got %v", err)
		}

		loser := v4Conn
		if winner == v4Conn {
			loser = v6Conn
		}
		deadline := time.Now().Add(500 * time.Millisecond)
		for !loser.closed.Load() && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		if !loser.closed.Load() {
			t.Fatal("expected losing concurrent dial connection to be closed immediately")
		}
		_ = winner.Close()
	})

	t.Run("canceled_context_aborts_both_lanes_cleanly", func(t *testing.T) {
		v6 := netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946")
		v4 := netip.MustParseAddr("93.184.216.34")
		guard := &SSRFGuard{
			Resolver:           staticResolver{"cancel.example": {v6, v4}},
			happyEyeballsDelay: 10 * time.Millisecond,
		}

		ctx, cancel := context.WithCancel(t.Context())
		var started atomic.Int32
		baseDial := func(dctx context.Context, _, _ string) (net.Conn, error) {
			if started.Add(1) == 2 {
				cancel()
			}
			<-dctx.Done()
			return nil, dctx.Err()
		}

		conn, err := guard.SafeDialContext(baseDial)(ctx, "tcp", "cancel.example:443")
		if err == nil {
			_ = conn.Close()
			t.Fatal("expected canceled dial to fail")
		}
	})
}

func BenchmarkEvaluateResolvedAddrs(b *testing.B) {
	staticCIDRs, err := ParseCIDRs(spec.DefaultFakeIPCIDRs())
	if err != nil {
		b.Fatalf("ParseCIDRs: %v", err)
	}
	dualFake := []netip.Addr{
		netip.MustParseAddr("198.18.0.10"),
		netip.MustParseAddr("fdfe:dcba:9876::10"),
	}
	publicAddrs := []netip.Addr{
		netip.MustParseAddr("2606:2800:220:1:248:1893:25c8:1946"),
		netip.MustParseAddr("93.184.216.34"),
	}

	b.ReportAllocs()
	for b.Loop() {
		if r := evaluateResolvedAddrs("docs.example.com", dualFake, staticCIDRs); !r.ok {
			b.Fatal("expected dual-stack fake-IP allowed")
		}
		if r := evaluateResolvedAddrs("example.com", publicAddrs, staticCIDRs); !r.ok {
			b.Fatal("expected public dual-stack allowed")
		}
	}
}
