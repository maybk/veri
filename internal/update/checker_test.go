package update

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"veri/internal/core"
)

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
