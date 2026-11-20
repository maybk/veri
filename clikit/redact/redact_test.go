package redact

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestMaskSanitizeScrubAndLogger(t *testing.T) {
	if got := MaskKey("sk-1234567890abcd"); got != "sk-…abcd" {
		t.Fatalf("MaskKey = %q, want sk-…abcd", got)
	}
	if got := MaskKey("keen_1234567890abcdef"); got != "keen_…cdef" {
		t.Fatalf("MaskKey(keen_) = %q, want keen_…cdef", got)
	}
	if got := MaskKey("fc-1234567890abcdef"); got != "fc-…cdef" {
		t.Fatalf("MaskKey(fc-) = %q, want fc-…cdef", got)
	}
	if got := MaskKey("github_pat_12345"); got != "****" {
		t.Fatalf("MaskKey(short prefixed key) = %q, want **** to avoid over-exposing", got)
	}
	if got := MaskKey("env:MY_API_KEY"); got != "env:MY_API_KEY" {
		t.Fatalf("MaskKey(env) = %q", got)
	}

	// 验证长密钥优先于短前缀密钥遮蔽，且文本内嵌 URL 与 Bearer 均被清洗
	raw := `Get "https://api.example.com/v1?api_key=secret123&X-Amz-Signature=sig999": sk-abc1234567890123 and Authorization: Bearer tok_supersecret9876`
	scrubbed := Scrub(raw, []string{"sk-abc123456", "sk-abc1234567890123"})
	if strings.Contains(scrubbed, "secret123") || strings.Contains(scrubbed, "sig999") ||
		strings.Contains(scrubbed, "4567890123") || strings.Contains(scrubbed, "supersecret9876") {
		t.Fatalf("Scrub leaked sensitive token or URL param: %s", scrubbed)
	}

	var buf bytes.Buffer
	logger := NewLogger(&buf, "info", "json", []string{"sk-topsecret99990000"})
	logger.Info("upstream failed", "err", errors.New(`Post "https://api.example.com/v1?token=tok888": bad key sk-topsecret99990000`))
	out := buf.String()
	if strings.Contains(out, "topsecret9999") || strings.Contains(out, "tok888") {
		t.Fatalf("NewLogger failed to scrub error Any attr or embedded URL: %s", out)
	}

	// 畸形 URL（无法被 url.Parse 解析）兜底剥离内嵌账密并折叠查询串；合法 URL 仅脱敏敏感参数并保留普通参数；短口令不得污染全局英文文本
	malformed := "http://admin:mySecretPass99@bad host:notaport/path?api_key=leakQuery77&q=ok#frag"
	if got := SanitizeURL(malformed); strings.Contains(got, "mySecretPass99") || strings.Contains(got, "admin:") || strings.Contains(got, "leakQuery77") || !strings.Contains(got, "?[REDACTED]#frag") {
		t.Fatalf("SanitizeURL failed to strip malformed URL credentials/query: %q", got)
	}
	if got := SanitizeURL("https://example.com/search?q=golang&api_key=leak88#top"); got != "https://example.com/search?api_key=%5BREDACTED%5D&q=golang#top" {
		t.Fatalf("SanitizeURL should preserve non-sensitive query params on valid URL, got %q", got)
	}
	if got := Scrub("api available", []string{"a", "123"}); got != "api available" {
		t.Fatalf("short secret fragment must not corrupt normal text: %q", got)
	}
}

func TestScrubProxySchemesBasicAuthAndAdditionalPrefixes(t *testing.T) {
	// 1. SOCKS5 / SOCKS5H / FTP 等代理与非 HTTP 协议内嵌账密（含短密码）必须被完整剥离
	proxyErr := `dial proxy socks5://proxyuser:p4ss@127.0.0.1:1080 and SOCKS5H://alice:SuperProxy99@proxy.example:1080/p?token=tokLeak1`
	scrubbedProxy := Scrub(proxyErr, nil)
	for _, leaked := range []string{"proxyuser", "p4ss", "alice", "SuperProxy99", "tokLeak1"} {
		if strings.Contains(scrubbedProxy, leaked) {
			t.Fatalf("Scrub leaked %q in proxy URL text: %s", leaked, scrubbedProxy)
		}
	}
	if !strings.Contains(scrubbedProxy, "socks5://127.0.0.1:1080") {
		t.Fatalf("Scrub corrupted non-secret proxy host: %s", scrubbedProxy)
	}

	// 2. HTTP Basic Auth / Proxy-Authorization Basic 凭据必须完整替换为 [REDACTED]（绝不保留末位 Base64，含短凭据 dTpw）
	basicErr := "Proxy-Authorization: Basic dXNlcjpwYXNzMTIzNDU2 and authorization: basic dTpw"
	scrubbedBasic := Scrub(basicErr, nil)
	if strings.Contains(scrubbedBasic, "dXNlcj") || strings.Contains(scrubbedBasic, "MTIzNDU2") || strings.Contains(scrubbedBasic, "dTpw") {
		t.Fatalf("Scrub leaked Basic auth payload: %s", scrubbedBasic)
	}
	if strings.Count(scrubbedBasic, "[REDACTED]") != 2 {
		t.Fatalf("expected both Basic tokens redacted, got: %s", scrubbedBasic)
	}

	// 3. 正常自然语言查询/提示词中的 basic / bearer 英文词组绝不得被误伤
	for _, safe := range []string{
		"basic authentication tutorial",
		"HTTP basic authorization guide",
		"basic extract configuration",
		"OAuth bearer authentication specification",
	} {
		if got := Scrub(safe, nil); got != safe {
			t.Fatalf("Scrub corrupted normal phrase %q -> %q", safe, got)
		}
	}

	// 4. GitHub OAuth / App 前缀正确遮蔽
	if got := MaskKey("gho_1234567890abcdef"); got != "gho_…cdef" {
		t.Fatalf("MaskKey(gho_) = %q, want gho_…cdef", got)
	}
}

func TestSanitizeURLSchemeLessAndMalformedCredentials(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    string
		noLeaks []string
	}{
		{
			name:    "scheme_less_proxy",
			raw:     "proxyuser:NoSchemeProxyPass99@proxy.local:8080",
			want:    "proxy.local:8080",
			noLeaks: []string{"proxyuser", "NoSchemeProxyPass99"},
		},
		{
			name:    "scheme_less_mirror_with_query",
			raw:     "user:NoSchemeMirPass99@mirror.example.com/?token=NoSchemeMirTok88&q=ok#frag",
			want:    "mirror.example.com/?q=ok&token=%5BREDACTED%5D#frag",
			noLeaks: []string{"user:", "NoSchemeMirPass99", "NoSchemeMirTok88"},
		},
		{
			name:    "scheme_less_ipv6_host",
			raw:     "user:IPv6Pass99@[::1]:8080/p?token=IPv6Tok88",
			want:    "[::1]:8080/p?token=%5BREDACTED%5D",
			noLeaks: []string{"user:", "IPv6Pass99", "IPv6Tok88"},
		},
		{
			name:    "scheme_less_malformed_host",
			raw:     "proxy_user:Pass99@bad host:notaport/p?token=Tok88#frag",
			want:    "bad host:notaport/p?[REDACTED]#frag",
			noLeaks: []string{"proxy_user", "Pass99", "Tok88"},
		},
		{
			name:    "scheme_less_at_in_password",
			raw:     "user:p@ss:word@proxy.local:8080",
			want:    "proxy.local:8080",
			noLeaks: []string{"user:", "p@ss", "word"},
		},
		{
			name:    "protocol_relative_malformed",
			raw:     "//user:ProtoRelPass99@bad host:notaport/p?key=K1",
			want:    "//bad host:notaport/p?[REDACTED]",
			noLeaks: []string{"ProtoRelPass99", "K1"},
		},
		{
			name: "mailto_preserved",
			raw:  "mailto:alice@example.com",
			want: "mailto:alice@example.com",
		},
		{
			name:    "relative_path_with_at_and_secret_query",
			raw:     "/path/@alice/doc?api_key=Secret88&q=ok",
			want:    "/path/@alice/doc?api_key=%5BREDACTED%5D&q=ok",
			noLeaks: []string{"Secret88"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeURL(tc.raw)
			if got != tc.want {
				t.Fatalf("SanitizeURL(%q) = %q, want %q", tc.raw, got, tc.want)
			}
			for _, secret := range tc.noLeaks {
				if strings.Contains(got, secret) {
					t.Fatalf("SanitizeURL(%q) leaked %q in %q", tc.raw, secret, got)
				}
			}
		})
	}
}
