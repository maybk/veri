package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIErrorRedactionAndSecretGuards(t *testing.T) {
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "config.toml")
	repoFS := os.DirFS(filepath.Join("..", ".."))

	run := func(envMap map[string]string, args ...string) (int, string, string) {
		var outBuf, errBuf bytes.Buffer
		fullArgs := append([]string{"--config", cfgPath, "--no-color"}, args...)
		code := ExecuteContext(t.Context(), fullArgs, repoFS, BuildInfo{
			Version: "1.0.0",
		}, strings.NewReader(""), &outBuf, &errBuf, func(k string) string {
			return envMap[k]
		})
		return code, outBuf.String(), errBuf.String()
	}

	// 1. search --json 在参数预校验早退时，query 内嵌的 URL userinfo 与敏感参数必须自动脱敏
	code, outJSON, errText := run(nil, "search", "https://u:SearchFlagLeak99@example.com/a?token=tokFlagLeak88", "-t", "5h", "--json")
	if code != 2 {
		t.Fatalf("expected exit code 2 for invalid -t 5h, got %d (out=%s err=%s)", code, outJSON, errText)
	}
	for _, leaked := range []string{"SearchFlagLeak99", "tokFlagLeak88", "u:SearchFlagLeak99"} {
		if strings.Contains(outJSON, leaked) || strings.Contains(errText, leaked) {
			t.Fatalf("search early JSON error leaked %q: out=%s err=%s", leaked, outJSON, errText)
		}
	}

	// 2. 根快捷命令阻断 GitHub OAuth Token (gho_) 等前缀误作搜索词
	if code, _, errOut := run(nil, "gho_accidentalOAuthToken1234567890"); code != 2 || strings.Contains(errOut, "accidentalOAuthToken") {
		t.Fatalf("expected gho_ token blocked without echoing secret, got code=%d err=%s", code, errOut)
	}

	// 3. update --mirror 含凭据与查询参数（含无协议前缀形态）时，CLI 终端与 JSON 报错绝不回显明文凭据
	for _, mirror := range []string{
		"https://user:MirrorLeak77@mirror.example.com/?token=MirrorTok66",
		"user:MirrorLeak77@mirror.example.com/?token=MirrorTok66",
	} {
		code, updOut, updErr := run(nil, "update", "--check", "--mirror", mirror, "--json")
		if code != 2 {
			t.Fatalf("expected exit code 2 for invalid mirror %q, got %d (out=%s err=%s)", mirror, code, updOut, updErr)
		}
		if !strings.Contains(updOut, `"kind": "update"`) {
			t.Fatalf("expected update error envelope kind=update for mirror %q, got: %s", mirror, updOut)
		}
		for _, leaked := range []string{"MirrorLeak77", "MirrorTok66", "user:"} {
			if strings.Contains(updOut, leaked) || strings.Contains(updErr, leaked) {
				t.Fatalf("update mirror error leaked %q for %q: out=%s err=%s", leaked, mirror, updOut, updErr)
			}
		}
	}

	// 4. 无协议前缀的非法 network.proxy 配置报错绝不泄露明文密码
	badProxyCfg := filepath.Join(tmpDir, "bad-proxy.toml")
	if err := os.WriteFile(badProxyCfg, []byte("[network]\nproxy = \"proxyuser:NoSchemeProxyLeak55@proxy.local:8080\"\n"), 0o600); err != nil {
		t.Fatalf("write bad proxy config: %v", err)
	}
	var pOut, pErr bytes.Buffer
	pCode := ExecuteContext(t.Context(), []string{"--config", badProxyCfg, "--no-color", "config", "show"}, repoFS, BuildInfo{Version: "1.0.0"}, strings.NewReader(""), &pOut, &pErr, func(string) string { return "" })
	if pCode != 2 || strings.Contains(pOut.String()+pErr.String(), "NoSchemeProxyLeak55") {
		t.Fatalf("bad proxy error leaked password or wrong exit: code=%d out=%s err=%s", pCode, pOut.String(), pErr.String())
	}
}
