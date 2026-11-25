package httpx

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"veri/internal/core"
	"veri/internal/spec"
)

func testDummyDial(dialed *string) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(_ context.Context, _, addr string) (net.Conn, error) {
		if dialed != nil {
			*dialed = addr
		}
		c1, c2 := net.Pipe()
		_ = c2.Close()
		return c1, nil
	}
}

func TestSSRFGuardDualStackFakePairedLoonPairedAndHostEligibility(t *testing.T) {
	staticCIDRs, err := ParseCIDRs(spec.DefaultFakeIPCIDRs())
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}

	resolver := staticResolver{
		// 标准 Clash/Mihomo 双栈：IPv4 RFC2544 + IPv6 ULA 伴生
		"mihomo-dual.example": {
			netip.MustParseAddr("198.18.0.10"),
			netip.MustParseAddr("fdfe:dcba:9876::10"),
		},
		// 旧版 sing-box 双栈：IPv4 RFC2544 + fc00::/18 伴生
		"singbox-ula.example": {
			netip.MustParseAddr("198.18.0.11"),
			netip.MustParseAddr("fc00::11"),
		},
		// Throne 双栈：IPv4 RFC2544 + 2001:db8::/32 伴生（规避 TUN fc00::/7 绕过）
		"throne-dual.example": {
			netip.MustParseAddr("198.18.0.12"),
			netip.MustParseAddr("2001:db8::12"),
		},
		// 纯 IPv6 RFC 5180 Benchmark（sing-box v1.14+ 默认）
		"singbox-v6only.example": {
			netip.MustParseAddr("2001:2::22"),
		},
		// Loon 合成伴生 AAAA（末尾 4 字节 == 93.184.216.34）
		"loon-paired.example": {
			netip.MustParseAddr("93.184.216.34"),
			netip.MustParseAddr("fd27:712::5db8:d822"),
		},
		// 攻击：未配对的裸 ULA / 未配对的 Loon AAAA
		"bare-ula.example": {
			netip.MustParseAddr("fdfe:dcba:9876::99"),
		},
		"loon-unpaired.example": {
			netip.MustParseAddr("93.184.216.34"),
			netip.MustParseAddr("fd27:712::0a00:0001"),
		},
		// 攻击：DNS 混合投毒（Fake-IP 混真实回环/内网/链路本地）
		"poison-loopback.example": {
			netip.MustParseAddr("198.18.0.10"),
			netip.MustParseAddr("127.0.0.1"),
		},
		"poison-linklocal.example": {
			netip.MustParseAddr("198.18.0.10"),
			netip.MustParseAddr("fe80::1"),
		},
		// 攻击：无资格的本地/内网保留主机名即使被代理分配了 Fake-IP 也必须硬拦截
		"metadata.google.internal": {netip.MustParseAddr("198.18.0.9")},
		"printer.local":            {netip.MustParseAddr("198.18.0.12")},
		"redis":                    {netip.MustParseAddr("198.18.0.13")},
		"secret.private":           {netip.MustParseAddr("198.18.0.14")},
	}

	guard := &SSRFGuard{
		FakeIPCIDRs:      staticCIDRs,
		AutoDetectFakeIP: false,
		Resolver:         resolver,
	}

	var dialed string
	dial := guard.SafeDialContext(testDummyDial(&dialed))

	for _, tc := range []struct {
		target   string
		wantDial string
	}{
		{"mihomo-dual.example:443", "198.18.0.10:443"},
		{"singbox-ula.example:443", "198.18.0.11:443"},
		{"throne-dual.example:443", "198.18.0.12:443"},
		{"singbox-v6only.example:443", "[2001:2::22]:443"},
		{"loon-paired.example:443", "93.184.216.34:443"},
	} {
		dialed = ""
		conn, dErr := dial(t.Context(), "tcp", tc.target)
		if dErr != nil {
			t.Fatalf("expected %s to succeed, got %v", tc.target, dErr)
		}
		_ = conn.Close()
		if dialed != tc.wantDial {
			t.Fatalf("%s dialed %q, want %q (must never dial skipped paired ULA)", tc.target, dialed, tc.wantDial)
		}
	}

	// 无资格主机名、IP 字面量与回环/链路本地投毒必须带 HintKeySSRFPrivateBlocked（严禁建议加入 fake_ip_cidrs）
	for _, blocked := range []string{
		"metadata.google.internal:80",
		"printer.local:80",
		"redis:6379",
		"secret.private:443",
		"[2001:2::22]:443",
		"127.255.0.1:443",
		"poison-loopback.example:443",
		"poison-linklocal.example:443",
	} {
		conn, dErr := dial(t.Context(), "tcp", blocked)
		if dErr == nil {
			_ = conn.Close()
			t.Fatalf("expected %s to be blocked", blocked)
		}
		se, ok := errors.AsType[*core.Error](dErr)
		if !ok || se.HintKey != core.HintKeySSRFPrivateBlocked {
			t.Fatalf("expected %s HintKey=%q, got %+v", blocked, core.HintKeySSRFPrivateBlocked, se)
		}
	}

	// 普通域名解析到可配置非公网 IP 必须硬拦截且带 HintKeySSRFHostNonPublic + 最小权限 cidr 建议
	for _, blocked := range []string{
		"bare-ula.example:443",
		"loon-unpaired.example:443",
	} {
		conn, dErr := dial(t.Context(), "tcp", blocked)
		if dErr == nil {
			_ = conn.Close()
			t.Fatalf("expected %s to be blocked", blocked)
		}
		se, ok := errors.AsType[*core.Error](dErr)
		if !ok || se.HintKey != core.HintKeySSRFHostNonPublic || se.Params["cidr"] == "" || !strings.Contains(se.Hint, "network.fake_ip_cidrs") {
			t.Fatalf("expected %s HintKey=%q with fake_ip_cidrs hint, got %+v", blocked, core.HintKeySSRFHostNonPublic, se)
		}
	}
}

func TestSSRFGuardAutoDetectTransparentProxyAndSecurityInvariants(t *testing.T) {
	staticCIDRs, _ := ParseCIDRs(spec.DefaultFakeIPCIDRs())

	t.Run("windows_transparent_proxy_127_255_auto_detected_and_scoped", func(t *testing.T) {
		resolver := staticResolver{
			"example.com":        {netip.MustParseAddr("127.255.0.1")},
			"github.com":         {netip.MustParseAddr("127.255.0.2")},
			"cloudflare.com":     {netip.MustParseAddr("127.255.0.3")},
			"go.dev":             {netip.MustParseAddr("127.255.0.9")},
			"intranet.corp.com":  {netip.MustParseAddr("10.0.1.5")},
			"loopback.evil.com":  {netip.MustParseAddr("127.0.0.1")},
			"metadata.evil.com":  {netip.MustParseAddr("169.254.169.254")},
			"svc.internal":       {netip.MustParseAddr("127.255.0.20")},
			"local.deepseek.com": {netip.MustParseAddr("116.128.128.1")}, // Clash Rule 模式国内直连
		}
		guard := &SSRFGuard{
			FakeIPCIDRs:      staticCIDRs,
			AutoDetectFakeIP: true,
			Resolver:         resolver,
		}
		var dialed string
		dial := guard.SafeDialContext(testDummyDial(&dialed))

		// 1. 透明代理分配的 127.255.0.9 与国内直连公网 IP 均放行
		for _, okHost := range []string{"go.dev:443", "local.deepseek.com:443"} {
			conn, err := dial(t.Context(), "tcp", okHost)
			if err != nil {
				t.Fatalf("expected %s allowed under transparent proxy, got %v", okHost, err)
			}
			_ = conn.Close()
		}

		// 2. 真实内网、127.0.0.1、云元数据、IP 字面量与 .internal 全部继续拦截！
		for _, blocked := range []string{
			"intranet.corp.com:443",
			"loopback.evil.com:80",
			"metadata.evil.com:80",
			"svc.internal:80",
			"127.255.0.9:443",
		} {
			conn, err := dial(t.Context(), "tcp", blocked)
			if err == nil {
				_ = conn.Close()
				t.Fatalf("expected %s to remain blocked even when 127.255.0.0/16 auto-detected", blocked)
			}
		}

		// 3. 显式代理预解析 CheckHost 与直连路径一致
		if err := guard.CheckHost(t.Context(), "go.dev"); err != nil {
			t.Fatalf("expected CheckHost(go.dev) allowed under auto-detected proxy, got %v", err)
		}
		if err := guard.CheckHost(t.Context(), "intranet.corp.com"); err == nil {
			t.Fatal("expected CheckHost(intranet.corp.com) blocked")
		}
	})

	t.Run("cgnat_transparent_proxy_auto_detected", func(t *testing.T) {
		resolver := staticResolver{
			"example.com":    {netip.MustParseAddr("100.64.0.1")},
			"github.com":     {netip.MustParseAddr("100.64.0.2")},
			"cloudflare.com": {netip.MustParseAddr("100.64.0.3")},
			"docs.go.dev":    {netip.MustParseAddr("100.64.0.88")},
		}
		guard := &SSRFGuard{FakeIPCIDRs: staticCIDRs, AutoDetectFakeIP: true, Resolver: resolver}
		conn, err := guard.SafeDialContext(testDummyDial(nil))(t.Context(), "tcp", "docs.go.dev:443")
		if err != nil {
			t.Fatalf("expected CGNAT fake-IP pool auto-detected, got %v", err)
		}
		_ = conn.Close()
	})

	t.Run("no_proxy_public_network_blocks_127_255_and_split_dns", func(t *testing.T) {
		resolver := staticResolver{
			"example.com":       {netip.MustParseAddr("93.184.216.34")},
			"github.com":        {netip.MustParseAddr("140.82.121.4")},
			"cloudflare.com":    {netip.MustParseAddr("104.16.132.229")},
			"evil-loop.example": {netip.MustParseAddr("127.255.0.1")},
			"wiki.company.com":  {netip.MustParseAddr("10.0.1.50")},
		}
		guard := &SSRFGuard{FakeIPCIDRs: staticCIDRs, AutoDetectFakeIP: true, Resolver: resolver}
		dial := guard.SafeDialContext(testDummyDial(nil))
		for _, target := range []string{"evil-loop.example:80", "wiki.company.com:443"} {
			conn, err := dial(t.Context(), "tcp", target)
			if err == nil {
				_ = conn.Close()
				t.Fatalf("expected %s blocked on normal public network", target)
			}
		}
	})

	t.Run("single_ip_dns_hijack_and_blacklist_rejected", func(t *testing.T) {
		for name, hijackIP := range map[string]string{
			"captive_portal_single_ip": "10.0.0.1",
			"metadata_hijack":          "169.254.169.254",
			"loopback_127_0_0_x":       "127.0.0.2",
		} {
			t.Run(name, func(t *testing.T) {
				resolver := staticResolver{
					"example.com":    {netip.MustParseAddr(hijackIP)},
					"github.com":     {netip.MustParseAddr(hijackIP)},
					"cloudflare.com": {netip.MustParseAddr(hijackIP)},
					"target.example": {netip.MustParseAddr(hijackIP)},
				}
				guard := &SSRFGuard{FakeIPCIDRs: staticCIDRs, AutoDetectFakeIP: true, Resolver: resolver}
				conn, err := guard.SafeDialContext(testDummyDial(nil))(t.Context(), "tcp", "target.example:443")
				if err == nil {
					_ = conn.Close()
					t.Fatalf("expected single-IP/blacklisted hijack %s to be rejected", hijackIP)
				}
			})
		}

		// 即使给不同域名返回不同 127.0.0.x 或 169.254.x.y，黑名单段也永不自动学习
		blacklistDistinct := staticResolver{
			"example.com":    {netip.MustParseAddr("127.0.0.2"), netip.MustParseAddr("169.254.1.2")},
			"github.com":     {netip.MustParseAddr("127.0.0.3"), netip.MustParseAddr("169.254.1.3")},
			"cloudflare.com": {netip.MustParseAddr("127.0.0.4"), netip.MustParseAddr("169.254.1.4")},
			"target.example": {netip.MustParseAddr("127.0.0.9")},
			"meta.example":   {netip.MustParseAddr("169.254.169.254")},
		}
		guard := &SSRFGuard{FakeIPCIDRs: staticCIDRs, AutoDetectFakeIP: true, Resolver: blacklistDistinct}
		dial := guard.SafeDialContext(testDummyDial(nil))
		for _, target := range []string{"target.example:80", "meta.example:80"} {
			conn, err := dial(t.Context(), "tcp", target)
			if err == nil {
				_ = conn.Close()
				t.Fatalf("expected blacklisted range target %s rejected", target)
			}
		}
	})

	t.Run("auto_detect_disabled_stays_strict_static", func(t *testing.T) {
		resolver := staticResolver{
			"example.com":    {netip.MustParseAddr("127.255.0.1")},
			"github.com":     {netip.MustParseAddr("127.255.0.2")},
			"cloudflare.com": {netip.MustParseAddr("127.255.0.3")},
			"go.dev":         {netip.MustParseAddr("127.255.0.9")},
		}
		guard := &SSRFGuard{FakeIPCIDRs: staticCIDRs, AutoDetectFakeIP: false, Resolver: resolver}
		conn, err := guard.SafeDialContext(testDummyDial(nil))(t.Context(), "tcp", "go.dev:443")
		if err == nil {
			_ = conn.Close()
			t.Fatal("expected 127.255.0.9 blocked when AutoDetectFakeIP=false")
		}
		if se := core.AsError(err); se.HintKey != core.HintKeySSRFHostNonPublic || se.Params["cidr"] != "127.255.0.0/16" {
			t.Fatalf("expected HintKeySSRFHostNonPublic with cidr=127.255.0.0/16, got %+v", se)
		}
	})
}

func TestDiagnoseNetworkFiveStatesAndRuleMode(t *testing.T) {
	staticCIDRs, _ := ParseCIDRs(spec.DefaultFakeIPCIDRs())

	// 1. 纯公网
	pubRes := staticResolver{
		"api.deepseek.com": {netip.MustParseAddr("116.128.128.1")},
		"example.com":      {netip.MustParseAddr("93.184.216.34")},
		"github.com":       {netip.MustParseAddr("140.82.121.4")},
		"cloudflare.com":   {netip.MustParseAddr("104.16.132.229")},
	}
	if d := DiagnoseNetwork(t.Context(), pubRes, "api.deepseek.com", staticCIDRs, true); d.Status != NetworkDiagPublic || d.ProbeIP != "116.128.128.1" {
		t.Fatalf("expected public diagnosis, got %+v", d)
	}

	// 2. Clash Rule 分流模式 + 静态 198.18.0.0/15（国内直连真 IP，海外走 Fake-IP）
	ruleStaticRes := staticResolver{
		"api.deepseek.com": {netip.MustParseAddr("116.128.128.1")},
		"example.com":      {netip.MustParseAddr("198.18.0.4"), netip.MustParseAddr("fdfe:dcba:9876::4")},
		"github.com":       {netip.MustParseAddr("198.18.0.5")},
	}
	if d := DiagnoseNetwork(t.Context(), ruleStaticRes, "api.deepseek.com", staticCIDRs, true); d.Status != NetworkDiagFakeIPConfigured || d.ProbeIP != "198.18.0.4" {
		t.Fatalf("expected rule-mode configured fake-ip diagnosis, got %+v", d)
	}

	// 3. Windows 透明代理 127.255.0.0/16 自动识别
	winAutoRes := staticResolver{
		"api.deepseek.com": {netip.MustParseAddr("127.255.0.1")},
		"example.com":      {netip.MustParseAddr("127.255.0.2")},
		"github.com":       {netip.MustParseAddr("127.255.0.3")},
		"cloudflare.com":   {netip.MustParseAddr("127.255.0.4")},
	}
	dAuto := DiagnoseNetwork(t.Context(), winAutoRes, "api.deepseek.com", staticCIDRs, true)
	if dAuto.Status != NetworkDiagFakeIPAuto || !slices.Equal(dAuto.DetectedFakeIPCIDRs, []string{"127.255.0.0/16"}) {
		t.Fatalf("expected fake_ip_auto with 127.255.0.0/16, got %+v", dAuto)
	}

	// 4. 关闭自动探测时 127.255.0.1 必须被报 non_public_blocked 并给建议 CIDR
	dBlocked := DiagnoseNetwork(t.Context(), winAutoRes, "api.deepseek.com", staticCIDRs, false)
	if dBlocked.Status != NetworkDiagNonPublicBlocked || dBlocked.SuggestedFakeIPCIDR != "127.255.0.0/16" {
		t.Fatalf("expected non_public_blocked with suggested 127.255.0.0/16, got %+v", dBlocked)
	}

	// 5. DNS 错误
	dErr := DiagnoseNetwork(t.Context(), staticResolver{}, "api.deepseek.com", staticCIDRs, true)
	if dErr.Status != NetworkDiagDNSError || dErr.DNSError == "" {
		t.Fatalf("expected dns_error, got %+v", dErr)
	}
}

func TestSafeFetchClientWithAutoDetectThroughLoopbackProxy(t *testing.T) {
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok:" + r.URL.Host))
	}))
	defer proxySrv.Close()

	resolver := staticResolver{
		"example.com":    {netip.MustParseAddr("127.255.0.1")},
		"github.com":     {netip.MustParseAddr("127.255.0.2")},
		"cloudflare.com": {netip.MustParseAddr("127.255.0.3")},
		"go.dev":         {netip.MustParseAddr("127.255.0.8")},
	}
	client, err := NewSafeFetchClient(Options{
		ProxyURL:         proxySrv.URL,
		AutoDetectFakeIP: true,
		Resolver:         resolver,
	})
	if err != nil {
		t.Fatalf("NewSafeFetchClient: %v", err)
	}
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://go.dev/doc", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected proxied request under auto-detected transparent proxy to succeed, got %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := ReadBodyLimited(resp, 1024)
	if err != nil || string(body) != "ok:go.dev" {
		t.Fatalf("unexpected proxied body=%q err=%v", string(body), err)
	}
}

func TestEcosystemProxiesProxyLaneV2rayNAnyproxyAndSuggestCIDR(t *testing.T) {
	staticCIDRs, _ := ParseCIDRs(spec.DefaultFakeIPCIDRs())

	t.Run("proxylane_dual_stack_ipv6_first_auto_and_manual", func(t *testing.T) {
		resolver := staticResolver{
			"example.com":    {netip.MustParseAddr("fdff::7fff:1"), netip.MustParseAddr("127.255.0.1")},
			"github.com":     {netip.MustParseAddr("fdff::7fff:2"), netip.MustParseAddr("127.255.0.2")},
			"cloudflare.com": {netip.MustParseAddr("fdff::7fff:3"), netip.MustParseAddr("127.255.0.3")},
			"go.dev":         {netip.MustParseAddr("fdff::7fff:9"), netip.MustParseAddr("127.255.0.9")},
		}
		// 1. 自动模式同时识别 127.255.0.0/16 与 fdff::/64
		autoGuard := &SSRFGuard{FakeIPCIDRs: staticCIDRs, AutoDetectFakeIP: true, Resolver: resolver}
		conn, err := autoGuard.SafeDialContext(testDummyDial(nil))(t.Context(), "tcp", "go.dev:443")
		if err != nil {
			t.Fatalf("expected ProxyLane dual-stack auto-detect to succeed, got %v", err)
		}
		_ = conn.Close()

		// 2. 手动仅配置 127.255.0.0/16 且关闭自动探测时，伴生 fdff::7fff:9 应被剥离并直拨 127.255.0.9
		manualCIDRs, _ := ParseCIDRs(append(spec.DefaultFakeIPCIDRs(), "127.255.0.0/16"))
		manualGuard := &SSRFGuard{FakeIPCIDRs: manualCIDRs, AutoDetectFakeIP: false, Resolver: resolver}
		var dialed string
		conn, err = manualGuard.SafeDialContext(testDummyDial(&dialed))(t.Context(), "tcp", "go.dev:443")
		if err != nil {
			t.Fatalf("expected ProxyLane manual IPv4 CIDR to strip paired ULA, got %v", err)
		}
		_ = conn.Close()
		if dialed != "127.255.0.9:443" {
			t.Fatalf("expected manual ProxyLane dial to 127.255.0.9:443, got %q", dialed)
		}
	})

	t.Run("v2rayn_11_slash_8_with_fc00_ipv6_auto_detected", func(t *testing.T) {
		resolver := staticResolver{
			"example.com":    {netip.MustParseAddr("11.0.0.1"), netip.MustParseAddr("fc00::1")},
			"github.com":     {netip.MustParseAddr("11.0.0.2"), netip.MustParseAddr("fc00::2")},
			"cloudflare.com": {netip.MustParseAddr("11.0.0.3"), netip.MustParseAddr("fc00::3")},
			"docs.go.dev":    {netip.MustParseAddr("11.0.0.9"), netip.MustParseAddr("fc00::9")},
		}
		guard := &SSRFGuard{FakeIPCIDRs: staticCIDRs, AutoDetectFakeIP: true, Resolver: resolver}
		conn, err := guard.SafeDialContext(testDummyDial(nil))(t.Context(), "tcp", "docs.go.dev:443")
		if err != nil {
			t.Fatalf("expected v2rayN 11.0.0.0/8 + fc00::/18 auto-detect to succeed, got %v", err)
		}
		_ = conn.Close()
	})

	t.Run("anyproxy_static_blackhole_sentinel_not_auto_learned_suggests_slash24", func(t *testing.T) {
		resolver := staticResolver{
			"example.com":    {netip.MustParseAddr("192.0.0.0")},
			"github.com":     {netip.MustParseAddr("192.0.0.0")},
			"cloudflare.com": {netip.MustParseAddr("192.0.0.0")},
			"blocked.test":   {netip.MustParseAddr("192.0.0.0")},
		}
		guard := &SSRFGuard{FakeIPCIDRs: staticCIDRs, AutoDetectFakeIP: true, Resolver: resolver}
		conn, err := guard.SafeDialContext(testDummyDial(nil))(t.Context(), "tcp", "blocked.test:443")
		if err == nil {
			_ = conn.Close()
			t.Fatal("expected single static blackhole sentinel 192.0.0.0 not to be auto-learned")
		}
		se := core.AsError(err)
		if se.HintKey != core.HintKeySSRFHostNonPublic || se.Params["cidr"] != "192.0.0.0/24" {
			t.Fatalf("expected HintKeySSRFHostNonPublic with cidr=192.0.0.0/24, got %+v", se)
		}
	})

	t.Run("suggest_fake_ip_cidr_exact_rfc_and_blacklist_empty", func(t *testing.T) {
		for ip, want := range map[string]string{
			"198.18.0.5":      "198.18.0.0/15",
			"2001:2::5":       "2001:2::/48",
			"2001:db8::5":     "2001:db8::/32",
			"192.0.0.0":       "192.0.0.0/24",
			"192.0.2.10":      "192.0.2.0/24",
			"198.51.100.10":   "198.51.100.0/24",
			"203.0.113.10":    "203.0.113.0/24",
			"127.255.0.1":     "127.255.0.0/16",
			"fc00::1234":      "fc00::/18",
			"fdff::7fff:1":    "fdff::/48",
			"127.0.0.1":       "",
			"169.254.169.254": "",
			"0.0.0.0":         "",
			"::1":             "",
			"fe80::1":         "",
			"240.0.0.1":       "",
		} {
			got := SuggestFakeIPCIDR(netip.MustParseAddr(ip))
			if got != want {
				t.Fatalf("SuggestFakeIPCIDR(%s) = %q, want %q", ip, got, want)
			}
		}
	})

	t.Run("canceled_ctx_does_not_poison_auto_detect_cache", func(t *testing.T) {
		ctxAware := &ctxAwareResolver{
			records: staticResolver{
				"example.com":    {netip.MustParseAddr("127.255.0.1")},
				"github.com":     {netip.MustParseAddr("127.255.0.2")},
				"cloudflare.com": {netip.MustParseAddr("127.255.0.3")},
				"go.dev":         {netip.MustParseAddr("127.255.0.9")},
			},
		}
		guard := &SSRFGuard{FakeIPCIDRs: staticCIDRs, AutoDetectFakeIP: true, Resolver: ctxAware}
		canceledCtx, cancel := context.WithCancel(t.Context())
		cancel()

		if got := guard.detectAutoFakeIPCIDRs(canceledCtx); len(got) != 0 {
			t.Fatalf("expected nil auto prefixes on canceled ctx, got %v", got)
		}
		// 后续健康上下文不得受先前取消请求负缓存污染
		conn, err := guard.SafeDialContext(testDummyDial(nil))(t.Context(), "tcp", "go.dev:443")
		if err != nil {
			t.Fatalf("expected healthy call to auto-detect after prior canceled ctx, got %v", err)
		}
		_ = conn.Close()
	})
}

type ctxAwareResolver struct {
	records staticResolver
}

func (r *ctxAwareResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return r.records.LookupNetIP(ctx, network, host)
}
