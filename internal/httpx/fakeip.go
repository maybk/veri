package httpx

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"veri/internal/spec"
)

const (
	fakeIPAutoCacheTTL = 60 * time.Second
	fakeIPProbeTimeout = 1500 * time.Millisecond
)

var (
	fakeIPv4Benchmark = netip.MustParsePrefix(spec.DefaultFakeIPCIDR)
	fakeIPv6Benchmark = netip.MustParsePrefix(spec.DefaultFakeIPv6CIDR)
	fakeIPv6ULA       = netip.MustParsePrefix("fc00::/7")
	fakeIPv6SingBox   = netip.MustParsePrefix("fc00::/18")
	loonPairedPrefix  = netip.MustParsePrefix("fd27:712::/96")

	// neverAutoFakeIPPrefixes 列出永不纳入自动 Fake-IP 学习的回环、元数据、未指定、多播与保留网段。
	neverAutoFakeIPPrefixes = []netip.Prefix{
		netip.MustParsePrefix("127.0.0.0/16"),
		netip.MustParsePrefix("::1/128"),
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("::/128"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("ff00::/8"),
	}

	// fakeIPIneligibleSuffixes 列出永不享受 Fake-IP 放行的本地与内网保留域名后缀。
	fakeIPIneligibleSuffixes = []string{
		".localhost",
		".local",
		".localdomain",
		".lan",
		".internal",
		".home.arpa",
		".arpa",
		".corp",
		".intranet",
		".private",
	}

	// fakeIPCanaryHosts 为用于纯本地 DNS 指纹校验的独立权威国际公网域名（兼容规则分流代理）。
	fakeIPCanaryHosts = []string{
		"example.com",
		"github.com",
		"cloudflare.com",
	}
)

// NetworkDiagStatus 描述 doctor 网络与透明代理 Fake-IP 诊断状态。
type NetworkDiagStatus string

// 网络诊断状态常量。
const (
	NetworkDiagPublic           NetworkDiagStatus = "public"
	NetworkDiagFakeIPConfigured NetworkDiagStatus = "fake_ip_configured"
	NetworkDiagFakeIPAuto       NetworkDiagStatus = "fake_ip_auto"
	NetworkDiagNonPublicBlocked NetworkDiagStatus = "non_public_blocked"
	NetworkDiagDNSError         NetworkDiagStatus = "dns_error"
)

// NetworkDiagnosis 汇总 doctor 的 DNS 与透明代理 Fake-IP 诊断结果。
type NetworkDiagnosis struct {
	Status              NetworkDiagStatus
	ProbeHost           string
	ProbeIP             string
	DNSError            string
	DetectedFakeIPCIDRs []string
	SuggestedFakeIPCIDR string
}

type canarySample struct {
	host  string
	addrs []netip.Addr
}

// isEligibleForFakeIPHost 判断目标主机名是否具备走 Fake-IP 放行策略的资格。
// IP 字面量、带 Zone 标识、单标签主机及本地/内网保留后缀一律拒绝享受 Fake-IP 放行。
func isEligibleForFakeIPHost(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if h == "" || strings.Contains(h, "%") || !strings.Contains(h, ".") {
		return false
	}
	if _, err := netip.ParseAddr(h); err == nil {
		return false
	}
	for _, suf := range fakeIPIneligibleSuffixes {
		if strings.HasSuffix(h, suf) {
			return false
		}
	}
	return true
}

func isNeverAutoFakeIP(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return true
	}
	u := addr.Unmap()
	for _, pfx := range neverAutoFakeIPPrefixes {
		if pfx.Contains(u) {
			return true
		}
	}
	return false
}

func overlapsNeverAutoFakeIP(pfx netip.Prefix) bool {
	return slices.ContainsFunc(neverAutoFakeIPPrefixes, pfx.Overlaps)
}

// isFakePairedIPv6 判断地址是否为双栈 TUN 代理随 IPv4 Fake-IP 伴生下发的 ULA / RFC 5180 / RFC 3849 AAAA 记录。
func isFakePairedIPv6(addr netip.Addr) bool {
	if !addr.IsValid() || addr.Zone() != "" || !addr.Is6() {
		return false
	}
	return fakeIPv6ULA.Contains(addr) || fakeIPv6Benchmark.Contains(addr) || docNet6.Contains(addr)
}

// isLoonPairedIPv6 判断地址是否为 Loon 针对已通过校验的 IPv4 合成的 fd27:712::/96 伴生 AAAA 记录。
func isLoonPairedIPv6(addr netip.Addr, allowedIPv4 map[netip.Addr]struct{}) bool {
	if !addr.IsValid() || addr.Zone() != "" || !addr.Is6() || !loonPairedPrefix.Contains(addr) {
		return false
	}
	b := addr.As16()
	embedded := netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	_, ok := allowedIPv4[embedded]
	return ok
}

// SuggestFakeIPCIDR 为被拦截的非公网地址推导最小权限的 Fake-IP 推荐网段，供报错提示与 doctor 修复指引使用。
// 对永不应作为 Fake-IP 放行的回环、元数据、链路本地、未指定、多播及保留网段返回空串。
func SuggestFakeIPCIDR(addr netip.Addr) string {
	if !addr.IsValid() || isNeverAutoFakeIP(addr) {
		return ""
	}
	u := addr.Unmap()
	for _, exact := range []netip.Prefix{
		fakeIPv4Benchmark,
		fakeIPv6Benchmark,
		ietfProtoRange,
		docNet1,
		docNet2,
		docNet3,
		docNet6,
		fakeIPv6SingBox,
	} {
		if exact.Contains(u) {
			return exact.String()
		}
	}
	if u.Is4() {
		return netip.PrefixFrom(u, 16).Masked().String()
	}
	return netip.PrefixFrom(u, 48).Masked().String()
}

// IsBlockedIP 判断给定地址在指定静态 Fake-IP 白名单下是否属于被 SSRF 守卫拒绝的非公网地址。
func IsBlockedIP(addr netip.Addr, allowedFakeCIDRs []netip.Prefix) bool {
	return isBlockedIP(addr, allowedFakeCIDRs)
}

// probeCanaryHosts 并发执行纯本地 DNS 解析以采样独立权威公网域名的地址特征（不发起任何 TCP/HTTP 连接）。
func probeCanaryHosts(ctx context.Context, resolver Resolver) []canarySample {
	if resolver == nil {
		return nil
	}
	probeCtx, cancel := context.WithTimeout(ctx, fakeIPProbeTimeout)
	defer cancel()

	samples := make([]canarySample, len(fakeIPCanaryHosts))
	var wg sync.WaitGroup
	for i, host := range fakeIPCanaryHosts {
		wg.Add(1)
		go func(idx int, h string) {
			defer wg.Done()
			addrs, err := resolver.LookupNetIP(probeCtx, "ip", h)
			if err != nil || len(addrs) == 0 {
				return
			}
			samples[idx] = canarySample{host: h, addrs: addrs}
		}(i, host)
	}
	wg.Wait()
	return samples
}

func hasAnyCanarySample(samples []canarySample) bool {
	for _, s := range samples {
		if s.host != "" && len(s.addrs) > 0 {
			return true
		}
	}
	return false
}

// inferAutoFakeIPPrefixes 基于四重安全指纹（非黑名单 + 多独立权威公网域 + 互异 IP + 窄子网收敛且零黑名单重叠）
// 从金丝雀 DNS 样本中推导透明代理使用的动态 Fake-IP 子网。
func inferAutoFakeIPPrefixes(samples []canarySample) []netip.Prefix {
	var out []netip.Prefix
	if v4, ok := inferFamilyFakeIPPrefix(samples, true, []int{16, 15}); ok {
		out = append(out, v4)
	}
	if v6, ok := inferFamilyFakeIPPrefix(samples, false, []int{64, 48, 32, 18}); ok {
		out = append(out, v6)
	}
	return out
}

func inferFamilyFakeIPPrefix(samples []canarySample, wantV4 bool, bitLens []int) (netip.Prefix, bool) {
	type hit struct {
		host string
		ip   netip.Addr
	}
	var hits []hit
	for _, s := range samples {
		if s.host == "" || len(s.addrs) == 0 {
			continue
		}
		seenInHost := make(map[netip.Addr]struct{}, len(s.addrs))
		for _, a := range s.addrs {
			if !a.IsValid() || a.Zone() != "" {
				continue
			}
			u := a.Unmap()
			if u.Is4() != wantV4 {
				continue
			}
			if !isBlockedIP(u, nil) || isNeverAutoFakeIP(u) {
				continue
			}
			if _, dup := seenInHost[u]; dup {
				continue
			}
			seenInHost[u] = struct{}{}
			hits = append(hits, hit{host: s.host, ip: u})
		}
	}
	if len(hits) < 2 {
		return netip.Prefix{}, false
	}

	for _, bits := range bitLens {
		type bucket struct {
			hosts map[string]struct{}
			ips   map[netip.Addr]struct{}
		}
		buckets := make(map[netip.Prefix]*bucket)
		order := make([]netip.Prefix, 0, len(hits))
		for _, h := range hits {
			pfx := netip.PrefixFrom(h.ip, bits).Masked()
			if overlapsNeverAutoFakeIP(pfx) {
				continue
			}
			b, exists := buckets[pfx]
			if !exists {
				b = &bucket{
					hosts: make(map[string]struct{}, 2),
					ips:   make(map[netip.Addr]struct{}, 2),
				}
				buckets[pfx] = b
				order = append(order, pfx)
			}
			b.hosts[h.host] = struct{}{}
			b.ips[h.ip] = struct{}{}
		}
		for _, pfx := range order {
			b := buckets[pfx]
			if len(b.hosts) >= 2 && len(b.ips) >= 2 {
				return pfx, true
			}
		}
	}
	return netip.Prefix{}, false
}

// DiagnoseNetwork 对目标探测域名与权威国际公网金丝雀执行统一 DNS 与透明代理 Fake-IP 诊断，供 veri doctor 使用。
func DiagnoseNetwork(ctx context.Context, resolver Resolver, probeHost string, staticFakeCIDRs []netip.Prefix, autoDetect bool) NetworkDiagnosis {
	diag := NetworkDiagnosis{
		Status:    NetworkDiagPublic,
		ProbeHost: probeHost,
	}
	if resolver == nil {
		return diag
	}
	guard := &SSRFGuard{
		FakeIPCIDRs:      staticFakeCIDRs,
		AutoDetectFakeIP: autoDetect,
		Resolver:         resolver,
	}

	addrs, err := resolver.LookupNetIP(ctx, "ip", probeHost)
	if err != nil {
		diag.Status = NetworkDiagDNSError
		diag.DNSError = err.Error()
		return diag
	}
	if len(addrs) > 0 {
		diag.ProbeIP = addrs[0].String()
	}

	// 1. 优先检查首选 probeHost 在静态白名单下是否已出现 Fake-IP 或非公网阻断。
	res := evaluateResolvedAddrs(probeHost, addrs, staticFakeCIDRs)
	if res.ok && res.usedFakeIP {
		diag.Status = NetworkDiagFakeIPConfigured
		if res.firstFakeIP.IsValid() {
			diag.ProbeIP = res.firstFakeIP.String()
		}
		return diag
	}
	if !res.ok {
		return diagnoseBlockedSample(ctx, guard, probeHost, addrs, res.firstBlocked, staticFakeCIDRs, autoDetect)
	}

	// 2. probeHost 返回公网 IP 时（如规则分流模式下国内端点直连），复核国际公网金丝雀以防漏检海外透明代理。
	samples := probeCanaryHosts(ctx, resolver)
	var firstBlockedSample canarySample
	var firstBlockedAddr netip.Addr
	for _, s := range samples {
		if s.host == "" || len(s.addrs) == 0 {
			continue
		}
		sr := evaluateResolvedAddrs(s.host, s.addrs, staticFakeCIDRs)
		if sr.ok && sr.usedFakeIP {
			diag.Status = NetworkDiagFakeIPConfigured
			diag.ProbeHost = s.host
			if sr.firstFakeIP.IsValid() {
				diag.ProbeIP = sr.firstFakeIP.String()
			} else {
				diag.ProbeIP = s.addrs[0].String()
			}
			return diag
		}
		if !sr.ok && firstBlockedSample.host == "" {
			firstBlockedSample = s
			firstBlockedAddr = sr.firstBlocked
		}
	}
	if firstBlockedSample.host != "" {
		return diagnoseBlockedWithSamples(firstBlockedSample.host, firstBlockedSample.addrs, firstBlockedAddr, staticFakeCIDRs, autoDetect, samples)
	}
	return diag
}

func diagnoseBlockedSample(ctx context.Context, guard *SSRFGuard, host string, addrs []netip.Addr, firstBlocked netip.Addr, staticFakeCIDRs []netip.Prefix, autoDetect bool) NetworkDiagnosis {
	var samples []canarySample
	if autoDetect && isEligibleForFakeIPHost(host) && !isNeverAutoFakeIP(firstBlocked) {
		samples = probeCanaryHosts(ctx, guard.resolver())
	}
	return diagnoseBlockedWithSamples(host, addrs, firstBlocked, staticFakeCIDRs, autoDetect, samples)
}

func diagnoseBlockedWithSamples(host string, addrs []netip.Addr, firstBlocked netip.Addr, staticFakeCIDRs []netip.Prefix, autoDetect bool, samples []canarySample) NetworkDiagnosis {
	probeIP := ""
	if firstBlocked.IsValid() {
		probeIP = firstBlocked.String()
	} else if len(addrs) > 0 {
		probeIP = addrs[0].String()
	}
	diag := NetworkDiagnosis{
		ProbeHost: host,
		ProbeIP:   probeIP,
	}

	if autoDetect && len(samples) > 0 {
		if inferred := inferAutoFakeIPPrefixes(samples); len(inferred) > 0 {
			combined := append(slices.Clone(staticFakeCIDRs), inferred...)
			if recheck := evaluateResolvedAddrs(host, addrs, combined); recheck.ok {
				diag.Status = NetworkDiagFakeIPAuto
				diag.DetectedFakeIPCIDRs = prefixesToStrings(inferred)
				if recheck.firstFakeIP.IsValid() {
					diag.ProbeIP = recheck.firstFakeIP.String()
				}
				return diag
			}
		}
	}

	diag.Status = NetworkDiagNonPublicBlocked
	diag.SuggestedFakeIPCIDR = SuggestFakeIPCIDR(firstBlocked)
	return diag
}

func prefixesToStrings(items []netip.Prefix) []string {
	if len(items) == 0 {
		return nil
	}
	out := make([]string, len(items))
	for i, p := range items {
		out[i] = p.String()
	}
	return out
}
