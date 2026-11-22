package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"veri/internal/app"
	"veri/internal/config"
)

func TestRunIOCancelsBlockedReaderImmediately(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	loaded, err := config.LoadWithEnv(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	application, err := app.New(loaded, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	pr, pw := io.Pipe()
	defer func() {
		_ = pw.Close()
		_ = pr.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	var out bytes.Buffer
	go func() {
		done <- RunIO(ctx, application, "1.0.0", pr, &out)
	}()

	time.AfterFunc(30*time.Millisecond, cancel)

	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("expected context.Canceled on interrupt, got %v", runErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunIO hung after context cancellation")
	}
}

func TestRunIOCancelsInFlightToolCallImmediately(t *testing.T) {
	started := make(chan struct{})
	srvHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	defer srvHTTP.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-inflight-key-1234"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded.Config.Providers.DeepSeek.BaseURL = srvHTTP.URL
	application, err := app.NewWithStderr(loaded, "1.0.0", io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	c2sR, c2sW := io.Pipe()
	s2cR, s2cW := io.Pipe()
	defer func() {
		_ = c2sW.Close()
		_ = c2sR.Close()
		_ = s2cW.Close()
		_ = s2cR.Close()
	}()

	srvCtx, cancelSrv := context.WithCancel(t.Context())
	defer cancelSrv()
	srvDone := make(chan error, 1)
	go func() {
		defer func() { _ = s2cW.Close() }()
		srvDone <- RunIO(srvCtx, application, "1.0.0", c2sR, s2cW)
	}()

	clientCtx := t.Context()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	session, err := client.Connect(clientCtx, &mcp.IOTransport{Reader: s2cR, Writer: c2sW}, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	go func() {
		_, _ = session.CallTool(clientCtx, &mcp.CallToolParams{
			Name:      "web_search",
			Arguments: map[string]any{"query": "slow-query", "providers": []string{"deepseek"}, "timeout_ms": 20000},
		})
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight tool call never reached upstream")
	}

	cancelSrv()
	select {
	case runErr := <-srvDone:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("expected context.Canceled when canceling server with in-flight call, got %v", runErr)
		}
	case <-time.After(time.Second):
		t.Fatal("RunIO hung waiting for slow in-flight tool call after SIGINT/context cancel")
	}
}
