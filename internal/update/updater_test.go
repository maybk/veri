package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veri/internal/core"
)

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func makeArchiveBytes(t *testing.T, goos string, binContent []byte) []byte {
	t.Helper()
	dir := t.TempDir()
	if goos == "windows" {
		p := filepath.Join(dir, "pkg.zip")
		buildTestZip(t, p, map[string][]byte{"veri.exe": binContent})
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read zip: %v", err)
		}
		return raw
	}
	p := filepath.Join(dir, "pkg.tar.gz")
	buildTestTarGz(t, p, map[string][]byte{"veri": binContent})
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read tar.gz: %v", err)
	}
	return raw
}

func TestUpdaterCheckAndApplyViaAPI(t *testing.T) {
	t.Parallel()

	archiveBytes := makeArchiveBytes(t, "linux", []byte("veri-v0.2.0-binary"))
	assetName := "veri-0.2.0-linux-amd64.tar.gz"
	digest := sha256Hex(archiveBytes)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/latest":
			payload := githubReleasePayload{
				TagName: "v0.2.0",
				HTMLURL: srv.URL + "/releases/tag/v0.2.0",
				Assets: []githubReleaseAsset{
					{
						Name:               assetName,
						BrowserDownloadURL: srv.URL + "/download/v0.2.0/" + assetName,
						Size:               int64(len(archiveBytes)),
						Digest:             "sha256:" + digest,
					},
				},
			}
			_ = json.MarshalWrite(w, payload)
		case "/download/v0.2.0/" + assetName:
			_, _ = w.Write(archiveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	exeDir := t.TempDir()
	exePath := filepath.Join(exeDir, "veri")
	if err := os.WriteFile(exePath, []byte("veri-v0.1.0-binary"), 0o755); err != nil {
		t.Fatalf("write initial exe: %v", err)
	}

	u, err := New("0.1.0",
		WithHTTPClient(srv.Client()),
		WithPlatform("linux", "amd64"),
		WithEndpoints(srv.URL+"/api/latest", srv.URL+"/releases/latest", srv.URL+"/download"),
		WithURLValidators(
			func(rawURL, tag string) bool { return strings.HasSuffix(rawURL, "/releases/tag/"+tag) },
			func(rawURL, tag, name string) bool { return strings.HasSuffix(rawURL, "/download/"+tag+"/"+name) },
		),
		WithInstallDetector(func(string) (InstallInfo, error) {
			return InstallInfo{Method: MethodStandalone, ExecutablePath: exePath}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	var stages []string
	res, err := u.Apply(context.Background(), ApplyOptions{
		OnProgress: func(p Progress) {
			if len(stages) == 0 || stages[len(stages)-1] != p.Stage {
				stages = append(stages, p.Stage)
			}
		},
	})
	if err != nil {
		t.Fatalf("Apply() failed: %v", err)
	}
	if !res.Updated || res.AlreadyLatest || res.TargetVersion != "0.2.0" {
		t.Fatalf("unexpected ApplyResult: %+v", res)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "veri-v0.2.0-binary" {
		t.Fatalf("updated binary = %q", got)
	}
	if len(stages) < 4 {
		t.Fatalf("expected checking/downloading/verifying/installing stages, got %v", stages)
	}
}

func TestUpdaterFallbackToRedirectOnAPIRateLimit(t *testing.T) {
	t.Parallel()

	archiveBytes := makeArchiveBytes(t, "darwin", []byte("veri-v0.3.0-darwin"))
	assetName := "veri-0.3.0-darwin-arm64.tar.gz"
	digest := sha256Hex(archiveBytes)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/latest":
			w.WriteHeader(http.StatusForbidden)
		case "/releases/latest":
			http.Redirect(w, r, srv.URL+"/releases/tag/v0.3.0", http.StatusFound)
		case "/releases/tag/v0.3.0":
			w.WriteHeader(http.StatusOK)
		case "/download/v0.3.0/SHA256SUMS.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, assetName)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	u, err := New("0.2.0",
		WithHTTPClient(srv.Client()),
		WithPlatform("darwin", "arm64"),
		WithEndpoints(srv.URL+"/api/latest", srv.URL+"/releases/latest", srv.URL+"/download"),
		WithURLValidators(
			func(rawURL, tag string) bool { return strings.HasSuffix(rawURL, "/releases/tag/"+tag) },
			func(rawURL, tag, name string) bool { return strings.HasSuffix(rawURL, "/download/"+tag+"/"+name) },
		),
		WithInstallDetector(func(string) (InstallInfo, error) {
			return InstallInfo{Method: MethodStandalone, ExecutablePath: "/tmp/veri"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	info, err := u.Check(context.Background(), "")
	if err != nil {
		t.Fatalf("Check() fallback failed: %v", err)
	}
	if !info.HasUpdate || info.LatestVersion != "0.3.0" || info.AssetSHA256 != digest {
		t.Fatalf("unexpected ReleaseInfo: %+v", info)
	}
}

func TestUpdaterChecksumMismatchFailsClosed(t *testing.T) {
	t.Parallel()

	archiveBytes := makeArchiveBytes(t, "linux", []byte("tampered-binary"))
	assetName := "veri-0.2.0-linux-amd64.tar.gz"
	wrongDigest := strings.Repeat("a", 64)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/download/v0.2.0/SHA256SUMS.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", wrongDigest, assetName)
		case "/download/v0.2.0/" + assetName:
			_, _ = w.Write(archiveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	exePath := filepath.Join(t.TempDir(), "veri")
	_ = os.WriteFile(exePath, []byte("original-binary"), 0o755)

	u, err := New("0.1.0",
		WithHTTPClient(srv.Client()),
		WithPlatform("linux", "amd64"),
		WithEndpoints(srv.URL+"/api/latest", srv.URL+"/releases/latest", srv.URL+"/download"),
		WithURLValidators(
			func(string, string) bool { return true },
			func(string, string, string) bool { return true },
		),
		WithInstallDetector(func(string) (InstallInfo, error) {
			return InstallInfo{Method: MethodStandalone, ExecutablePath: exePath}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	_, err = u.Apply(context.Background(), ApplyOptions{TargetVersion: "0.2.0"})
	if se, ok := errors.AsType[*core.Error](err); !ok || se.MsgKey != "err.update_checksum_mismatch" {
		t.Fatalf("expected checksum mismatch error, got: %v", err)
	}
	got, _ := os.ReadFile(exePath)
	if string(got) != "original-binary" {
		t.Fatalf("original binary must remain untouched, got %q", got)
	}
}

func TestUpdaterGuardsAndMirrorFallback(t *testing.T) {
	t.Parallel()

	// 1. Homebrew 托管拒绝覆盖但允许 --check
	uBrew, err := New("0.1.0", WithInstallDetector(func(string) (InstallInfo, error) {
		return InstallInfo{
			Method:         MethodHomebrew,
			ExecutablePath: "/opt/homebrew/Cellar/veri/0.1.0/bin/veri",
			ManagedCommand: "brew upgrade veri",
		}, nil
	}))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	_, err = uBrew.Apply(context.Background(), ApplyOptions{Force: true})
	if se, ok := errors.AsType[*core.Error](err); !ok || se.MsgKey != "err.update_managed_install" || se.Params["command"] != "brew upgrade veri" {
		t.Fatalf("expected managed install error, got %v", err)
	}

	// 2. 开发版默认拒绝覆盖
	uDev, err := New("dev", WithInstallDetector(func(string) (InstallInfo, error) {
		return InstallInfo{Method: MethodDev, ExecutablePath: "/tmp/veri"}, nil
	}))
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	_, err = uDev.Apply(context.Background(), ApplyOptions{})
	if se, ok := errors.AsType[*core.Error](err); !ok || se.MsgKey != "err.update_dev_build" {
		t.Fatalf("expected dev build guard error, got %v", err)
	}

	// 3. 镜像 503 时自动回退官方直连并成功校验替换
	archiveBytes := makeArchiveBytes(t, "linux", []byte("veri-mirror-fallback"))
	assetName := "veri-0.2.0-linux-amd64.tar.gz"
	digest := sha256Hex(archiveBytes)

	mirrorHits := 0
	directHits := 0
	tlsSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/mirror/"):
			mirrorHits++
			w.WriteHeader(http.StatusServiceUnavailable)
		case r.URL.Path == "/download/v0.2.0/SHA256SUMS.txt":
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, assetName)
		case r.URL.Path == "/download/v0.2.0/"+assetName:
			directHits++
			_, _ = w.Write(archiveBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	defer tlsSrv.Close()

	exePath := filepath.Join(t.TempDir(), "veri")
	_ = os.WriteFile(exePath, []byte("old"), 0o755)

	uMirror, err := New("0.1.0",
		WithHTTPClient(tlsSrv.Client()),
		WithPlatform("linux", "amd64"),
		WithMirrorPrefix(tlsSrv.URL+"/mirror/"),
		WithEndpoints(tlsSrv.URL+"/api/latest", tlsSrv.URL+"/releases/latest", tlsSrv.URL+"/download"),
		WithURLValidators(
			func(string, string) bool { return true },
			func(string, string, string) bool { return true },
		),
		WithInstallDetector(func(string) (InstallInfo, error) {
			return InstallInfo{Method: MethodStandalone, ExecutablePath: exePath}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() with mirror failed: %v", err)
	}
	res, err := uMirror.Apply(context.Background(), ApplyOptions{TargetVersion: "0.2.0"})
	if err != nil {
		t.Fatalf("Apply() mirror fallback failed: %v", err)
	}
	if !res.Updated || mirrorHits != 1 || directHits != 1 {
		t.Fatalf("expected mirror fallback (mirrorHits=%d, directHits=%d), res=%+v", mirrorHits, directHits, res)
	}
}

func TestUpdaterEdgeCasesAlreadyLatestNoReleaseAndWindowsZip(t *testing.T) {
	t.Parallel()

	// 1. 尚无发布版本（API 503 + Redirect 落到 /releases）精准报 err.update_no_release
	var noRelSrv *httptest.Server
	noRelSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/latest":
			w.WriteHeader(http.StatusForbidden)
		case "/releases/latest":
			http.Redirect(w, r, noRelSrv.URL+"/releases", http.StatusFound)
		case "/releases":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer noRelSrv.Close()

	uNoRel, err := New("0.1.0",
		WithHTTPClient(noRelSrv.Client()),
		WithPlatform("linux", "amd64"),
		WithEndpoints(noRelSrv.URL+"/api/latest", noRelSrv.URL+"/releases/latest", noRelSrv.URL+"/download"),
		WithURLValidators(
			func(string, string) bool { return true },
			func(string, string, string) bool { return true },
		),
		WithInstallDetector(func(string) (InstallInfo, error) {
			return InstallInfo{Method: MethodStandalone, ExecutablePath: "/tmp/veri"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}
	_, err = uNoRel.Check(context.Background(), "")
	if se, ok := errors.AsType[*core.Error](err); !ok || se.MsgKey != "err.update_no_release" {
		t.Fatalf("expected err.update_no_release, got %v", err)
	}

	// 2. 指定非法版本与不存在版本
	_, err = uNoRel.Check(context.Background(), "not-a-semver")
	if se, ok := errors.AsType[*core.Error](err); !ok || se.MsgKey != "err.update_invalid_version" {
		t.Fatalf("expected err.update_invalid_version, got %v", err)
	}
	_, err = uNoRel.Check(context.Background(), "9.9.9")
	if se, ok := errors.AsType[*core.Error](err); !ok || se.MsgKey != "err.update_version_not_found" {
		t.Fatalf("expected err.update_version_not_found, got %v", err)
	}

	// 3. 已在最新版时幂等跳过（零归档下载）+ Windows .zip 端到端升级
	winZip := makeArchiveBytes(t, "windows", []byte("veri-v0.2.0-win"))
	winAsset := "veri-0.2.0-windows-amd64.zip"
	winDigest := sha256Hex(winZip)
	downloads := 0

	var winSrv *httptest.Server
	winSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/latest":
			payload := githubReleasePayload{
				TagName: "v0.2.0",
				HTMLURL: winSrv.URL + "/releases/tag/v0.2.0",
				Assets: []githubReleaseAsset{
					{
						Name:               winAsset,
						BrowserDownloadURL: winSrv.URL + "/download/v0.2.0/" + winAsset,
						Size:               int64(len(winZip)),
					},
					{
						Name:               sha256SumsAssetName,
						BrowserDownloadURL: winSrv.URL + "/download/v0.2.0/" + sha256SumsAssetName,
					},
				},
			}
			_ = json.MarshalWrite(w, payload)
		case "/download/v0.2.0/" + sha256SumsAssetName:
			_, _ = fmt.Fprintf(w, "%s  %s\n", winDigest, winAsset)
		case "/download/v0.2.0/" + winAsset:
			downloads++
			_, _ = w.Write(winZip)
		default:
			http.NotFound(w, r)
		}
	}))
	defer winSrv.Close()

	winExe := filepath.Join(t.TempDir(), "veri.exe")
	_ = os.WriteFile(winExe, []byte("veri-v0.1.0-win"), 0o755)

	newWinUpdater := func(ver string) *Updater {
		t.Helper()
		u, uErr := New(ver,
			WithHTTPClient(winSrv.Client()),
			WithPlatform("windows", "amd64"),
			WithEndpoints(winSrv.URL+"/api/latest", winSrv.URL+"/releases/latest", winSrv.URL+"/download"),
			WithURLValidators(
				func(rawURL, tag string) bool { return strings.HasSuffix(rawURL, "/releases/tag/"+tag) },
				func(rawURL, tag, name string) bool { return strings.HasSuffix(rawURL, "/download/"+tag+"/"+name) },
			),
			WithInstallDetector(func(string) (InstallInfo, error) {
				return InstallInfo{Method: MethodStandalone, ExecutablePath: winExe}, nil
			}),
		)
		if uErr != nil {
			t.Fatalf("New(%s) failed: %v", ver, uErr)
		}
		return u
	}

	// 同版本应跳过下载
	sameRes, err := newWinUpdater("0.2.0").Apply(context.Background(), ApplyOptions{})
	if err != nil || !sameRes.AlreadyLatest || sameRes.Updated || downloads != 0 {
		t.Fatalf("expected idempotent skip with 0 downloads, res=%+v downloads=%d err=%v", sameRes, downloads, err)
	}

	// 0.1.0 -> 0.2.0 通过 SHA256SUMS.txt 回退获取哈希并替换 Windows .exe（保留 .old）
	upRes, err := newWinUpdater("0.1.0").Apply(context.Background(), ApplyOptions{})
	if err != nil || !upRes.Updated || downloads != 1 {
		t.Fatalf("expected windows zip update success, res=%+v downloads=%d err=%v", upRes, downloads, err)
	}
	got, _ := os.ReadFile(winExe)
	if string(got) != "veri-v0.2.0-win" {
		t.Fatalf("windows exe content = %q", got)
	}
	if _, err := os.Stat(winExe + ".old"); err != nil {
		t.Fatalf("expected windows .old backup to exist: %v", err)
	}
}

func TestProgressAndResultOmitZeroJSON(t *testing.T) {
	t.Parallel()

	pRaw, err := json.Marshal(Progress{Stage: StageChecking})
	if err != nil {
		t.Fatalf("marshal Progress: %v", err)
	}
	if string(pRaw) != `{"stage":"checking"}` {
		t.Fatalf("expected zero numeric progress fields to be omitted under json/v2 omitzero, got %s", pRaw)
	}

	rRaw, err := json.Marshal(ApplyResult{
		CurrentVersion: "0.1.0",
		TargetVersion:  "0.1.0",
		Tag:            "v0.1.0",
		AlreadyLatest:  true,
		InstallMethod:  MethodStandalone,
	})
	if err != nil {
		t.Fatalf("marshal ApplyResult: %v", err)
	}
	if strings.Contains(string(rRaw), "downloaded_bytes") {
		t.Fatalf("expected zero downloaded_bytes to be omitted, got %s", rRaw)
	}
}

type errHTTPDoer struct{ err error }

func (d errHTTPDoer) Do(*http.Request) (*http.Response, error) { return nil, d.err }

func TestUpdaterRedactsProxyCredentialsInTransportErrors(t *testing.T) {
	t.Parallel()

	u, err := New("0.1.0",
		WithProxyURL("socks5://proxyadmin:UpdateProxySecret99@127.0.0.1:1080"),
		WithHTTPClient(errHTTPDoer{
			err: errors.New("proxyconnect tcp: socks5://proxyadmin:UpdateProxySecret99@127.0.0.1:1080 Proxy-Authorization: Basic dXNlcjpwYXNzMTIz"),
		}),
		WithInstallDetector(func(string) (InstallInfo, error) {
			return InstallInfo{Method: MethodStandalone, ExecutablePath: "/tmp/veri"}, nil
		}),
	)
	if err != nil {
		t.Fatalf("New() failed: %v", err)
	}

	_, checkErr := u.Check(t.Context(), "")
	if checkErr == nil {
		t.Fatal("expected Check() transport error")
	}
	se := core.AsError(checkErr)
	combined := se.Error()
	if se.Cause != nil {
		combined += " | " + se.Cause.Error()
	}
	for _, leaked := range []string{"UpdateProxySecret99", "proxyadmin:", "dXNlcjpwYXNzMTIz"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("update error leaked %q: %s", leaked, combined)
		}
	}
}
