package cli

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"veri/internal/app"
	"veri/internal/core"
	"veri/internal/spec"
)

func TestCacheCLICommandsAndFetchSearchRoundTrip(t *testing.T) {
	var fetchHits atomic.Int32
	var searchHits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/fetch":
			n := fetchHits.Add(1)
			_, _ = fmt.Fprintf(w, `{"title":"CLI Cache Doc","content":"# Intro\n\nFirst block.\n\n## Deep Dive\n\nSecond block rev %d."}`, n)
		case "/v1/tools/search":
			searchHits.Add(1)
			_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev","snippet":"Go lang","summary":"Go summary"}]}`))
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	cacheDir := filepath.Join(tmp, "cache")
	cfgBody := fmt.Sprintf("[providers.deepseek]\nbase_url = %q\n", srv.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}

	baseEnv := map[string]string{
		spec.EnvDeepSeekAPIKey: "sk-cli-e2e-cache-1234",
		spec.EnvCacheDir:       cacheDir,
	}
	getenv := func(extra map[string]string) func(string) string {
		return func(k string) string {
			if v, ok := extra[k]; ok {
				return v
			}
			return baseEnv[k]
		}
	}

	run := func(extraEnv map[string]string, args ...string) (int, string, string) {
		var out, errBuf bytes.Buffer
		fullArgs := append([]string{"--config", cfgPath, "--ui-lang", "en", "--no-color"}, args...)
		code := ExecuteContext(t.Context(), fullArgs, nil, BuildInfo{Version: "0.2.6-test"}, strings.NewReader(""), &out, &errBuf, getenv(extraEnv))
		return code, out.String(), errBuf.String()
	}

	// 1. 首次 fetch 落盘，第二次 --section 命中磁盘缓存并在 stderr 打印轻提示，第三次 --json 返回 cached=true。
	code, out1, err1 := run(nil, "fetch", "https://example.com/doc", "--strategy", "api_only", "--max-chars", "25")
	if code != 0 || fetchHits.Load() != 1 || strings.Contains(err1, "local cache") || out1 == "" {
		t.Fatalf("first fetch unexpected: code=%d hits=%d stderr=%q", code, fetchHits.Load(), err1)
	}

	code, out2, err2 := run(nil, "fetch", "https://example.com/doc", "--strategy", "api_only", "--section", "Deep Dive")
	if code != 0 || fetchHits.Load() != 1 || !strings.Contains(err2, "local cache") || !strings.Contains(out2, "rev 1") {
		t.Fatalf("cached section fetch unexpected: code=%d hits=%d stdout=%q stderr=%q", code, fetchHits.Load(), out2, err2)
	}

	code, outJSON, errJSON := run(nil, "fetch", "https://example.com/doc", "--strategy", "api_only", "--json")
	if code != 0 || fetchHits.Load() != 1 || strings.Contains(errJSON, "local cache") {
		t.Fatalf("cached json fetch unexpected: code=%d hits=%d stderr=%q", code, fetchHits.Load(), errJSON)
	}
	var fetchEnv core.Envelope
	if err := json.Unmarshal([]byte(outJSON), &fetchEnv); err != nil || !fetchEnv.Meta.Cached || len(fetchEnv.Meta.Cost) != 0 {
		t.Fatalf("expected cached json envelope, got env=%+v err=%v", fetchEnv, err)
	}

	// 2. --fresh 强制实时抓取并刷新缓存。
	code, outFresh, _ := run(nil, "fetch", "https://example.com/doc", "--strategy", "api_only", "--fresh")
	if code != 0 || fetchHits.Load() != 2 || !strings.Contains(outFresh, "rev 2") {
		t.Fatalf("fresh fetch unexpected: code=%d hits=%d out=%q", code, fetchHits.Load(), outFresh)
	}

	// 3. 开启 VERI_CACHE_SEARCH_TTL=2m 后，二次搜索命中磁盘缓存，--no-cache 绕过缓存。
	searchEnv := map[string]string{spec.EnvCacheSearchTTL: "2m"}
	if code, _, _ = run(searchEnv, "search", "golang"); code != 0 || searchHits.Load() != 1 {
		t.Fatalf("first search failed: code=%d hits=%d", code, searchHits.Load())
	}
	code, _, errSearchHit := run(searchEnv, "search", "golang")
	if code != 0 || searchHits.Load() != 1 || !strings.Contains(errSearchHit, "local cache") {
		t.Fatalf("cached search unexpected: code=%d hits=%d stderr=%q", code, searchHits.Load(), errSearchHit)
	}
	if code, _, _ = run(searchEnv, "search", "golang", "--no-cache"); code != 0 || searchHits.Load() != 2 {
		t.Fatalf("no-cache search unexpected: code=%d hits=%d", code, searchHits.Load())
	}

	// 4. veri cache info / status / info --json / clear --json。
	code, infoText, _ := run(nil, "cache", "status")
	if code != 0 || !strings.Contains(infoText, cacheDir) || !strings.Contains(infoText, "fetch 1") || !strings.Contains(infoText, "search 1") {
		t.Fatalf("cache status text unexpected: code=%d out=%q", code, infoText)
	}
	code, infoJSON, _ := run(nil, "cache", "info", "--json")
	if code != 0 {
		t.Fatalf("cache info --json failed: code=%d", code)
	}
	var info app.CacheInfoResult
	if err := json.Unmarshal([]byte(infoJSON), &info); err != nil {
		t.Fatalf("unmarshal cache info: %v", err)
	}
	if !info.Enabled || !info.CLI.Enabled || !info.MCP.Enabled || info.Disk.Entries != 2 || info.Disk.Dir != cacheDir {
		t.Fatalf("unexpected cache info JSON: %+v", info)
	}

	code, clearJSON, _ := run(nil, "cache", "clear", "--json")
	if code != 0 {
		t.Fatalf("cache clear --json failed: code=%d", code)
	}
	var cleared app.CacheClearResult
	if err := json.Unmarshal([]byte(clearJSON), &cleared); err != nil || cleared.RemovedEntries != 2 || cleared.FreedBytes <= 0 {
		t.Fatalf("unexpected cache clear JSON: %+v err=%v", cleared, err)
	}

	// 再次清理应提示为空且退出码 0。
	code, clearEmpty, _ := run(nil, "cache", "clear")
	if code != 0 || !strings.Contains(clearEmpty, "empty") {
		t.Fatalf("second cache clear unexpected: code=%d out=%q", code, clearEmpty)
	}

	// 非法缓存环境变量在中文模式下应本地化报错且返回退出码 2。
	var zhOut, zhErr bytes.Buffer
	badCode := ExecuteContext(t.Context(),
		[]string{"--config", cfgPath, "--ui-lang", "zh", "--no-color", "cache", "info"},
		nil, BuildInfo{Version: "0.2.6-test"}, strings.NewReader(""), &zhOut, &zhErr,
		getenv(map[string]string{spec.EnvNoCache: "invalid_bool"}),
	)
	if badCode != 2 || !strings.Contains(zhErr.String(), "环境变量 VERI_NO_CACHE 值无效") {
		t.Fatalf("expected localized invalid env error, code=%d stderr=%q", badCode, zhErr.String())
	}
}
