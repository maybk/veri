// Package httpx 提供统一的安全 HTTP 客户端、SSRF 拨号守卫（兼容 TUN Fake-IP）、体积上限读取与计费安全重试辅助。
//
// 依赖边界：仅依赖标准库、clikit/redact、internal/core 与 internal/keypool。所有上游 API 与本地网页抓取必须经由本包发起，严禁使用 http.DefaultClient。
package httpx

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strings"

	"veri/clikit/redact"
	"veri/internal/core"
	"veri/internal/spec"
)

// Resolver 抽象域名解析器，默认使用 net.DefaultResolver，测试可注入确定性假解析器。
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

var (
	cgnatPrefix    = netip.MustParsePrefix("100.64.0.0/10")
	benchmarkNet   = netip.MustParsePrefix(spec.DefaultFakeIPCIDR)
	ietfProtoRange = netip.MustParsePrefix("192.0.0.0/24")
	docNet1        = netip.MustParsePrefix("192.0.2.0/24")
	docNet2        = netip.MustParsePrefix("198.51.100.0/24")
	docNet3        = netip.MustParsePrefix("203.0.113.0/24")

	discard6Prefix  = netip.MustParsePrefix("100::/64")
	ietf6Prefix     = netip.MustParsePrefix("2001::/23")
	orchidPrefix    = netip.MustParsePrefix("2001:10::/28")
	orchid2Prefix   = netip.MustParsePrefix("2001:20::/28")
	docNet6         = netip.MustParsePrefix("2001:db8::/32")
	sixToFourPrefix = netip.MustParsePrefix("2002::/16")
	nat64WKP        = netip.MustParsePrefix("64:ff9b::/96")
	nat64Local      = netip.MustParsePrefix("64:ff9b:1::/48")
	v4CompatPrefix  = netip.MustParsePrefix("::/96")
	siitPrefix      = netip.MustParsePrefix("::ffff:0:0:0/96")
)

// SSRFGuard 负责对目标 URL 与解析后的 IP 地址执行内网隔离与 Fake-IP 规则判定。
type SSRFGuard struct {
	AllowPrivate bool
	FakeIPCIDRs  []netip.Prefix
	Resolver     Resolver
}

// ParseCIDRs 解析 CIDR 列表配置（如 ["198.18.0.0/15"]）。
func ParseCIDRs(raw []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(raw))
	for _, s := range raw {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		pfx, err := netip.ParsePrefix(trimmed)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", s, err)
		}
		out = append(out, pfx.Masked())
	}
	return out, nil
}

// ValidateFetchURL 校验本地抓取的目标 URL（仅允许 http/https，拒绝 userinfo 与 IPv6 Zone，IP 字面量直接做内网拦截）。
func (g *SSRFGuard) ValidateFetchURL(u *url.URL) error {
	if u == nil {
		return core.NewError(core.CodeInvalidArgument, "empty URL")
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("unsupported URL scheme %q: only http and https are allowed", u.Scheme))
	}
	if u.User != nil {
		return core.NewError(core.CodeInvalidArgument, "URL must not contain user credentials")
	}
	host := u.Hostname()
	if host == "" {
		return core.NewError(core.CodeInvalidArgument, "URL must include a hostname")
	}
	if strings.Contains(host, "%") {
		return core.NewError(core.CodeContentUnavailable, "IPv6 zone identifiers are not allowed in fetch URLs")
	}
	if g != nil && !g.AllowPrivate {
		if ip, err := netip.ParseAddr(host); err == nil {
			if ip.Zone() != "" || isBlockedIP(ip.Unmap(), nil) {
				return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("refusing to fetch non-public IP literal %s", ip)).
					WithHint("Private, loopback, link-local, and Fake-IP literals are blocked for SSRF protection; set network.allow_private = true in config.toml if intentional.").
					WithHintKey("hint.ssrf_private_blocked")
			}
		}
	}
	return nil
}

// CheckHost 在使用外部 HTTP 代理导致无法拦截底层拨号 IP 时，于请求发出前对目标域名执行预解析校验。
func (g *SSRFGuard) CheckHost(ctx context.Context, host string) error {
	if g == nil || g.AllowPrivate {
		return nil
	}
	if strings.Contains(host, "%") {
		return core.NewError(core.CodeContentUnavailable, "blocked scoped IPv6 zone address").
			WithHintKey("hint.ssrf_private_blocked")
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if ip.Zone() != "" || isBlockedIP(ip.Unmap(), nil) {
			return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("blocked non-public IP literal %s", ip)).
				WithHint("Local fetching blocks private, loopback, link-local, and cloud metadata addresses; set network.allow_private = true in config.toml if intentional.").
				WithHintKey("hint.ssrf_private_blocked")
		}
		return nil
	}
	addrs, err := g.resolver().LookupNetIP(ctx, "ip", host)
	if err != nil {
		return core.WrapError(core.CodeContentUnavailable, fmt.Sprintf("failed to resolve host %q", host), err)
	}
	if len(addrs) == 0 {
		return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("host %q resolved to no addresses", host))
	}
	for _, addr := range addrs {
		if addr.Zone() != "" || isBlockedIP(addr.Unmap(), g.FakeIPCIDRs) {
			return core.NewError(core.CodeContentUnavailable, fmt.Sprintf("host %q resolved to blocked non-public address %s", host, addr)).
				WithHint("Local fetching blocks private, loopback, link-local, and cloud metadata addresses; set network.allow_private = true in config.toml if intentional.").
				WithHintKey("hint.ssrf_private_blocked")
		}
	}
	return nil
}

// SafeDialContext 先解析目标主机、逐个验证 IP 安全策略（防 DNS 重绑定 TOCTOU），再直接向通过校验的 IP 发起 TCP 连接。
func (g *SSRFGuard) SafeDialContext(baseDial func(ctx context.Context, network, address string) (net.Conn, error)) func(ctx context.Context, network, address string) (net.Conn, error) {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		if g == nil || g.AllowPrivate {
			return baseDial(ctx, network, address)
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, core.WrapError(core.CodeInvalidArgument, "invalid dial address", err)
		}
		if strings.Contains(host, "%") {
			return nil, core.NewError(core.CodeContentUnavailable, "blocked connection to scoped IPv6 zone address").
				WithHintKey("hint.ssrf_private_blocked")
		}

		// 直接拨号 IP 字面量时严禁放行 Fake-IP 段。
		if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
			if ip.Zone() != "" {
				return nil, core.NewError(core.CodeContentUnavailable, "blocked connection to scoped IPv6 zone address").
					WithHintKey("hint.ssrf_private_blocked")
			}
			unmapped := ip.Unmap()
			if isBlockedIP(unmapped, nil) {
				return nil, core.NewError(core.CodeContentUnavailable, fmt.Sprintf("blocked connection to non-public IP literal %s", unmapped)).
					WithHint("Local fetching blocks private, loopback, link-local, and cloud metadata addresses; set network.allow_private = true in config.toml if intentional.").
					WithHintKey("hint.ssrf_private_blocked")
			}
			return baseDial(ctx, network, net.JoinHostPort(unmapped.String(), port))
		}

		// 域名解析允许命中已配置的 Fake-IP 段（如 Clash/Surge TUN 198.18.0.0/15）。
		addrs, err := g.resolver().LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, core.WrapError(core.CodeContentUnavailable, fmt.Sprintf("DNS lookup failed for %q", host), err)
		}
		if len(addrs) == 0 {
			return nil, core.NewError(core.CodeContentUnavailable, fmt.Sprintf("no IP addresses found for %q", host))
		}

		var allowed []netip.Addr
		for _, addr := range addrs {
			if addr.Zone() != "" {
				return nil, core.NewError(core.CodeContentUnavailable, fmt.Sprintf("blocked connection to %q (%s): scoped IPv6 address", host, addr)).
					WithHintKey("hint.ssrf_private_blocked")
			}
			unmapped := addr.Unmap()
			if isBlockedIP(unmapped, g.FakeIPCIDRs) {
				return nil, core.NewError(core.CodeContentUnavailable, fmt.Sprintf("blocked connection to %q (%s): non-public address", host, unmapped)).
					WithHint("Local fetching blocks private, loopback, link-local, and cloud metadata addresses; set network.allow_private = true in config.toml if intentional.").
					WithHintKey("hint.ssrf_private_blocked")
			}
			allowed = append(allowed, unmapped)
		}

		var lastErr error
		for _, addr := range allowed {
			conn, dialErr := baseDial(ctx, network, net.JoinHostPort(addr.String(), port))
			if dialErr == nil {
				return conn, nil
			}
			lastErr = dialErr
		}
		return nil, core.WrapError(core.CodeUpstreamUnavailable, fmt.Sprintf("failed to connect to %q", host), lastErr)
	}
}

func (g *SSRFGuard) resolver() Resolver {
	if g != nil && g.Resolver != nil {
		return g.Resolver
	}
	return net.DefaultResolver
}

// IsFakeIP 判断某个地址是否命中配置的 TUN Fake-IP 网段（供 doctor 环境诊断使用）。
func IsFakeIP(addr netip.Addr, fakeCIDRs []netip.Prefix) bool {
	if !addr.IsValid() || addr.Zone() != "" {
		return false
	}
	unmapped := addr.Unmap()
	for _, pfx := range fakeCIDRs {
		if pfx.Contains(unmapped) {
			return true
		}
	}
	return false
}

// isBlockedIP 判断 IP 是否属于禁止本地直连抓取的内网、保留或过渡隧道非公网网段。
func isBlockedIP(ip netip.Addr, allowedFakeCIDRs []netip.Prefix) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return true
	}
	ip = ip.Unmap()

	for _, pfx := range allowedFakeCIDRs {
		if pfx.Contains(ip) {
			return false
		}
	}

	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return true
	}

	if ip.Is4() {
		b := ip.As4()
		if b[0] == 0 || b[0] >= 240 {
			return true
		}
		return cgnatPrefix.Contains(ip) ||
			benchmarkNet.Contains(ip) ||
			ietfProtoRange.Contains(ip) ||
			docNet1.Contains(ip) ||
			docNet2.Contains(ip) ||
			docNet3.Contains(ip)
	}

	if discard6Prefix.Contains(ip) ||
		ietf6Prefix.Contains(ip) ||
		orchidPrefix.Contains(ip) ||
		orchid2Prefix.Contains(ip) ||
		docNet6.Contains(ip) ||
		nat64Local.Contains(ip) ||
		v4CompatPrefix.Contains(ip) ||
		siitPrefix.Contains(ip) {
		return true
	}

	b16 := ip.As16()
	if nat64WKP.Contains(ip) {
		v4 := netip.AddrFrom4([4]byte{b16[12], b16[13], b16[14], b16[15]})
		return isBlockedIP(v4, nil)
	}
	if sixToFourPrefix.Contains(ip) {
		v4 := netip.AddrFrom4([4]byte{b16[2], b16[3], b16[4], b16[5]})
		return isBlockedIP(v4, nil)
	}

	return false
}

// ValidateAPIBaseURL 校验厂商 API 的固定 base_url：必须为 https；仅当主机是回环地址时允许 http（供本地网关与 httptest 使用）。
// 错误文本始终先经脱敏清洗，避免误填凭据随报错回显。
func ValidateAPIBaseURL(raw string) (*url.URL, error) {
	safeDisplay := redact.SanitizeURL(raw)
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Hostname() == "" {
		return nil, fmt.Errorf("invalid API base_url %q", safeDisplay)
	}
	if u.User != nil {
		return nil, fmt.Errorf("API base_url %q must not contain credentials", safeDisplay)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "https" {
		return u, nil
	}
	if scheme == "http" && isLoopbackHost(u.Hostname()) {
		return u, nil
	}
	return nil, fmt.Errorf("API base_url %q must use https (http is only allowed for loopback hosts)", safeDisplay)
}

// ResolveAPIBaseURL 归一化并校验厂商 API base_url（为空时回退 fallback，去除尾部斜杠）。
func ResolveAPIBaseURL(raw, fallback string) (string, error) {
	candidate := strings.TrimSpace(raw)
	if candidate == "" {
		candidate = fallback
	}
	if _, err := ValidateAPIBaseURL(candidate); err != nil {
		return "", err
	}
	return strings.TrimRight(candidate, "/"), nil
}

func isLoopbackHost(host string) bool {
	h := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip, err := netip.ParseAddr(h); err == nil && ip.Zone() == "" {
		return ip.Unmap().IsLoopback()
	}
	return false
}
