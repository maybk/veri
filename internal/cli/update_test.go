package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veri/clikit"
	"veri/clikit/i18n"
	"veri/internal/core"
	"veri/internal/update"
)

func buildUpdateTestTarGz(t *testing.T, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{
		Name: "veri",
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write tar content: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

func runUpdateCLIForTest(
	t *testing.T,
	args []string,
	version string,
	locale i18n.Locale,
	opts ...update.Option,
) (int, string, string) {
	t.Helper()
	var outBuf, errBuf bytes.Buffer
	cfgPath := filepath.Join(t.TempDir(), "nonexistent.toml")
	fullArgs := append([]string{"--config", cfgPath, "--ui-lang", string(locale)}, args...)

	rt := &runtimeEnv{
		info:       BuildInfo{Version: version},
		uiLocale:   locale,
		uiSource:   "flag",
		cat:        localeBundle.For(locale),
		stdin:      strings.NewReader(""),
		stdout:     &outBuf,
		stderr:     &errBuf,
		getenv:     func(string) string { return "" },
		updateOpts: opts,
	}
	root := newRootCmd(rt)
	root.SetArgs(fullArgs)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		return clikit.ExitOK, outBuf.String(), errBuf.String()
	}
	wantsJSON := false
	for _, a := range args {
		if a == "--json" {
			wantsJSON = true
		}
	}
	code := emitTerminalError(rt, wantsJSON, wrapCLIError(err))
	return code, outBuf.String(), errBuf.String()
}

func wrapCLIError(err error) *core.Error {
	if se := core.AsError(err); se != nil {
		return se
	}
	return wrapCobraUsageError(err)
}

func TestUpdateCommandCheckApplyAndGuards(t *testing.T) {
	t.Parallel()

	archive := buildUpdateTestTarGz(t, []byte("new-cli-binary-v0.2.0"))
	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	asset := "veri-0.2.0-linux-amd64.tar.gz"

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/latest":
			_, _ = fmt.Fprintf(w, `{"tag_name":"v0.2.0","html_url":"%s/releases/tag/v0.2.0","assets":[{"name":"%s","browser_download_url":"%s/download/v0.2.0/%s","size":%d,"digest":"sha256:%s"}]}`,
				srv.URL, asset, srv.URL, asset, len(archive), digest)
		case "/download/v0.2.0/" + asset:
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	exePath := filepath.Join(t.TempDir(), "veri")
	if err := os.WriteFile(exePath, []byte("old-cli-binary-v0.1.0"), 0o755); err != nil {
		t.Fatalf("write old binary: %v", err)
	}

	commonOpts := []update.Option{
		update.WithHTTPClient(srv.Client()),
		update.WithPlatform("linux", "amd64"),
		update.WithEndpoints(srv.URL+"/api/latest", srv.URL+"/releases/latest", srv.URL+"/download"),
		update.WithURLValidators(
			func(string, string) bool { return true },
			func(string, string, string) bool { return true },
		),
		update.WithInstallDetector(func(string) (update.InstallInfo, error) {
			return update.InstallInfo{Method: update.MethodStandalone, ExecutablePath: exePath}, nil
		}),
	}

	// 1. --check --json 返回结构化 ReleaseInfo
	code, out, errOut := runUpdateCLIForTest(t, []string{"update", "--check", "--json"}, "0.1.0", i18n.LocaleEnUS, commonOpts...)
	if code != clikit.ExitOK || errOut != "" {
		t.Fatalf("update --check --json code=%d stderr=%q stdout=%q", code, errOut, out)
	}
	var info update.ReleaseInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("unmarshal check json: %v\n%s", err, out)
	}
	if !info.HasUpdate || info.LatestVersion != "0.2.0" || !info.CanSelfUpdate {
		t.Fatalf("unexpected check info: %+v", info)
	}

	// 2. --check 与 --force 冲突返回退出码 2
	code, _, errOut = runUpdateCLIForTest(t, []string{"update", "--check", "--force"}, "0.1.0", i18n.LocaleZhCN, commonOpts...)
	if code != clikit.ExitUsage || !strings.Contains(errOut, "--check, --force") {
		t.Fatalf("expected flag conflict exit 2, got code=%d stderr=%q", code, errOut)
	}

	// 3. 正式执行更新并验证二进制替换与英文输出
	code, out, errOut = runUpdateCLIForTest(t, []string{"update"}, "0.1.0", i18n.LocaleEnUS, commonOpts...)
	if code != clikit.ExitOK {
		t.Fatalf("update apply failed code=%d stdout=%q stderr=%q", code, out, errOut)
	}
	if !strings.Contains(out, "v0.1.0 -> v0.2.0") {
		t.Fatalf("unexpected apply output: %q", out)
	}
	gotBin, _ := os.ReadFile(exePath)
	if string(gotBin) != "new-cli-binary-v0.2.0" {
		t.Fatalf("binary content = %q", gotBin)
	}

	// 4. 开发构建 --check 输出不带多余 v 前缀（避免出现 vdev）
	code, out, _ = runUpdateCLIForTest(t, []string{"update", "--check"}, "dev", i18n.LocaleZhCN, commonOpts...)
	if code != clikit.ExitOK || strings.Contains(out, "vdev") || !strings.Contains(out, "v0.2.0") {
		t.Fatalf("unexpected dev check output: code=%d out=%q", code, out)
	}

	// 5. 本地版本高于线上版本时输出目标版本信息
	code, out, _ = runUpdateCLIForTest(t, []string{"update", "--check"}, "0.3.0", i18n.LocaleEnUS, commonOpts...)
	if code != clikit.ExitOK || !strings.Contains(out, "v0.3.0") || !strings.Contains(out, "v0.2.0") {
		t.Fatalf("unexpected older target output: code=%d out=%q", code, out)
	}

	// 6. Homebrew 托管拒绝覆盖并提示 `brew upgrade veri`
	brewOpts := append(commonOpts[:len(commonOpts)-1], update.WithInstallDetector(func(string) (update.InstallInfo, error) {
		return update.InstallInfo{
			Method:         update.MethodHomebrew,
			ExecutablePath: "/opt/homebrew/Cellar/veri/0.1.0/bin/veri",
			ManagedCommand: "brew upgrade veri",
		}, nil
	}))
	code, _, errOut = runUpdateCLIForTest(t, []string{"update"}, "0.1.0", i18n.LocaleZhCN, brewOpts...)
	if code != clikit.ExitBlocked || !strings.Contains(errOut, "brew upgrade veri") {
		t.Fatalf("expected brew guard exit 5, got code=%d stderr=%q", code, errOut)
	}

	// 7. 无已发布 Release（404）时，文本与 --json 均返回更新专属提示且 JSON kind 为 "update"
	emptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer emptySrv.Close()
	noRelOpts := []update.Option{
		update.WithHTTPClient(emptySrv.Client()),
		update.WithPlatform("linux", "amd64"),
		update.WithEndpoints(emptySrv.URL+"/api/latest", emptySrv.URL+"/releases/latest", emptySrv.URL+"/download"),
		update.WithURLValidators(
			func(string, string) bool { return true },
			func(string, string, string) bool { return true },
		),
		update.WithInstallDetector(func(string) (update.InstallInfo, error) {
			return update.InstallInfo{Method: update.MethodStandalone, ExecutablePath: exePath}, nil
		}),
	}
	code, out, errOut = runUpdateCLIForTest(t, []string{"update", "--check", "--json"}, "0.1.0", i18n.LocaleZhCN, noRelOpts...)
	if code != clikit.ExitBlocked || errOut != "" {
		t.Fatalf("expected exit 5 json error on no release, got code=%d err=%q out=%q", code, errOut, out)
	}
	var errEnv core.Envelope
	if err := json.Unmarshal([]byte(out), &errEnv); err != nil {
		t.Fatalf("unmarshal update error envelope: %v\n%s", err, out)
	}
	if errEnv.Kind != "update" || errEnv.Meta.Strategy != "" || errEnv.Error == nil || errEnv.Error.Code != core.CodeNotFound {
		t.Fatalf("unexpected update error envelope: %+v", errEnv)
	}
	if !strings.Contains(errEnv.Error.Hint, "GitHub Releases") || strings.Contains(errEnv.Error.Hint, "URL spelling") {
		t.Fatalf("expected update-specific hint in JSON error, got %q", errEnv.Error.Hint)
	}

	code, _, errOut = runUpdateCLIForTest(t, []string{"update", "--check"}, "0.1.0", i18n.LocaleZhCN, noRelOpts...)
	if code != clikit.ExitBlocked || !strings.Contains(errOut, "GitHub Releases") || strings.Contains(errOut, "URL 拼写") {
		t.Fatalf("expected localized update hint in zh stderr, got code=%d stderr=%q", code, errOut)
	}
}
