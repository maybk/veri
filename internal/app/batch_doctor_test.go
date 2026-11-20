package app

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/spec"
)

func newTestAppWithServer(t *testing.T, extraTOML string, handler http.HandlerFunc) (*App, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfgBody := fmt.Sprintf(`
[search]
providers = ["deepseek"]

[fetch]
strategy = "api_first"
providers = ["deepseek"]

[providers.deepseek]
api_key = "sk-appsecret999988887777"
base_url = %q

[providers.local]
enabled = true

[network]
allow_private = true
%s
`, srv.URL, extraTOML)
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadWithEnv(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	application, err := NewWithStderr(loaded, "1.0.0", nil)
	if err != nil {
		t.Fatalf("NewWithStderr: %v", err)
	}
	return application, srv
}

func TestFetchBatchCLIAndMCPPaths(t *testing.T) {
	application, srv := newTestAppWithServer(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/fetch":
			var body struct {
				URL string `json:"url"`
			}
			_ = json.UnmarshalRead(r.Body, &body)
			switch {
			case strings.Contains(body.URL, "/ok1"):
				_, _ = w.Write([]byte(`{"title":"Page One","content":"Content one with secret sk-appsecret999988887777 redacted and sufficient length for quality gate."}`))
			case strings.Contains(body.URL, "/ok2"):
				_, _ = w.Write([]byte(`{"title":"Page Two","content":"Content two with enough text for extraction verification."}`))
			default:
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":{"message":"not found for sk-appsecret999988887777"}}`))
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})

	// 1. 空 URL 列表 -> invalid_argument
	if env, err := application.FetchBatch(context.Background(), []string{" ", ""}, nil); err == nil || env.OK || core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument for empty batch, got env=%+v err=%v", env, err)
	}

	// 2. 超过最大上限 -> invalid_argument
	tooMany := make([]string, spec.MaxMCPBatchFetchURLs+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("https://example.org/p%d", i)
	}
	if _, err := application.FetchBatchMCP(context.Background(), tooMany, nil); err == nil || core.AsError(err).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid_argument when exceeding batch max, got %v", err)
	}

	// 3. 单 URL 自动委托单页 Fetch / FetchMCP（返回 Page 而非 Pages）
	singleEnv, err := application.FetchBatch(context.Background(), []string{"https://example.org/ok1"}, &core.FetchRequest{Providers: []string{spec.ProviderDeepSeek}})
	if err != nil || !singleEnv.OK || singleEnv.Page == nil || len(singleEnv.Pages) != 0 {
		t.Fatalf("expected single-URL batch delegation to Page, got env=%+v err=%v", singleEnv, err)
	}

	singleMCP, err := application.FetchBatchMCP(context.Background(), []string{"https://example.org/ok1"}, &core.FetchRequest{Providers: []string{spec.ProviderDeepSeek}})
	if err != nil || !singleMCP.OK || singleMCP.Page == nil {
		t.Fatalf("expected single-URL MCP batch delegation, got env=%+v err=%v", singleMCP, err)
	}

	// 4. 多 URL 混合部分失败：严格保序、partial=true、报错信息脱敏生效、含空行/去重
	urls := []string{"https://example.org/ok1", "https://example.org/missing", "https://example.org/ok2", "https://example.org/ok1"}
	batchEnv, err := application.FetchBatch(context.Background(), urls, &core.FetchRequest{
		Providers: []string{spec.ProviderDeepSeek},
		MaxChars:  4000,
	})
	if err != nil || !batchEnv.OK || !batchEnv.Meta.Partial || len(batchEnv.Pages) != 3 {
		t.Fatalf("expected 3 deduped ordered pages with partial=true, got env=%+v err=%v", batchEnv, err)
	}
	if batchEnv.Pages[0].Title != "Page One" || batchEnv.Pages[1].Error == nil || batchEnv.Pages[2].Title != "Page Two" {
		t.Fatalf("batch order or error mismatch: %+v", batchEnv.Pages)
	}
	if strings.Contains(batchEnv.Pages[1].Error.Message, "sk-appsecret999988887777") {
		t.Fatalf("secret leaked in batch page error message: %s", batchEnv.Pages[1].Error.Message)
	}

	// 5. MCP 批量抓取复用缓存与预算均摊
	mcpBatch, err := application.FetchBatchMCP(context.Background(), []string{"https://example.org/ok1", "https://example.org/ok2"}, &core.FetchRequest{
		Providers: []string{spec.ProviderDeepSeek},
	})
	if err != nil || !mcpBatch.OK || len(mcpBatch.Pages) != 2 {
		t.Fatalf("MCP batch failed: env=%+v err=%v", mcpBatch, err)
	}

	// 6. 全部 URL 均失败时返回 ok=false 与错误
	allFailEnv, allFailErr := application.FetchBatch(context.Background(), []string{"https://example.org/m1", "https://example.org/m2"}, &core.FetchRequest{
		Providers: []string{spec.ProviderDeepSeek},
	})
	if allFailErr == nil || allFailEnv.OK || len(allFailEnv.Pages) != 2 {
		t.Fatalf("expected all-fail batch error with per-page errors, got env=%+v err=%v", allFailEnv, allFailErr)
	}
	_ = srv
}

func TestRunDoctorResolverStatesAndLiveChecks(t *testing.T) {
	mode := "ok"
	application, _ := newTestAppWithServer(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tools/search" {
			if mode == "fail" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"message":"bad key sk-appsecret999988887777"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev","snippet":"Go lang"}]}`))
		}
	})

	// 1. Fake-IP DNS 识别
	application.lookupNetIP = func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("198.18.0.42")}, nil
	}
	repFake := application.RunDoctor(context.Background(), "zh-CN", "flag", false)
	wantMode := "0600"
	if runtime.GOOS == "windows" {
		wantMode = ""
	}
	if !repFake.FakeIPDetected || repFake.ProbeIP != "198.18.0.42" || repFake.EnabledSearcher != 1 || repFake.ConfigMode != wantMode {
		t.Fatalf("unexpected Fake-IP doctor report: %+v", repFake)
	}
	// allow_private=true 计 1 项 issue
	if repFake.Issues != 1 {
		t.Fatalf("expected 1 issue for allow_private=true, got %d", repFake.Issues)
	}

	// 2. 公网 IP + live=true 成功
	application.lookupNetIP = func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("1.1.1.1")}, nil
	}
	repLiveOK := application.RunDoctor(context.Background(), "en-US", "env", true)
	if repLiveOK.FakeIPDetected || len(repLiveOK.LiveChecks) != 1 || repLiveOK.LiveChecks[0].Status != "ok" {
		t.Fatalf("unexpected live-ok doctor report: %+v", repLiveOK)
	}

	// 3. DNS 解析失败 + live=true 失败（含密钥脱敏）
	mode = "fail"
	application.lookupNetIP = func(_ context.Context, _, _ string) ([]netip.Addr, error) {
		return nil, errors.New("dns failed for sk-appsecret999988887777")
	}
	repFail := application.RunDoctor(context.Background(), "en-US", "env", true)
	if repFail.DNSError == "" || strings.Contains(repFail.DNSError, "sk-appsecret999988887777") {
		t.Fatalf("expected scrubbed DNS error, got %q", repFail.DNSError)
	}
	if len(repFail.LiveChecks) != 1 || repFail.LiveChecks[0].Status != "error" || strings.Contains(repFail.LiveChecks[0].Error, "sk-appsecret999988887777") {
		t.Fatalf("expected scrubbed live error, got %+v", repFail.LiveChecks)
	}
	if repFail.Issues < 3 {
		t.Fatalf("expected issues >= 3 (allow_private + dns + live), got %d", repFail.Issues)
	}

	// 4. selectProbeHost 过滤 localhost（含大小写与 *.localhost）、IP 字面量与禁用项
	if got := selectProbeHost([]ProviderStatus{
		{Name: "a", Enabled: false, Endpoint: "disabled.example.com"},
		{Name: "b", Enabled: true, Endpoint: "127.0.0.1:8080"},
		{Name: "c", Enabled: true, Endpoint: "LOCALHOST:9000"},
		{Name: "c2", Enabled: true, Endpoint: "firecrawl.localhost:3002"},
		{Name: "d", Enabled: true, Endpoint: "api.exa.ai:443"},
	}); got != "api.exa.ai" {
		t.Fatalf("selectProbeHost got %q, want api.exa.ai", got)
	}
	if got := selectProbeHost([]ProviderStatus{{Name: "local", Enabled: true, Endpoint: "-"}}); got != "api.github.com" {
		t.Fatalf("selectProbeHost fallback got %q, want api.github.com", got)
	}
	if application.Config() == nil {
		t.Fatal("App.Config() must not be nil")
	}
}

func TestFirecrawlSelfHostedKeylessStatusInApp(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	body := "[providers.firecrawl]\nbase_url = \"http://127.0.0.1:3002\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadWithEnv(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	application, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatalf("NewWithStderr: %v", err)
	}
	var found *ProviderStatus
	for _, st := range application.ProviderStatuses() {
		if st.Name == spec.ProviderFirecrawl {
			cp := st
			found = &cp
			break
		}
	}
	if found == nil || !found.Enabled || found.Source != spec.CredSourceOptional || !strings.Contains(found.Details, "self_host") {
		t.Fatalf("expected enabled keyless self-hosted firecrawl status, got %+v", found)
	}

	// 禁用自托管 Firecrawl 时同样展示 optional 而非 missing（与 GitHub 保持一致）
	disabledCfgPath := filepath.Join(t.TempDir(), "disabled.toml")
	if err := os.WriteFile(disabledCfgPath, []byte("[providers.firecrawl]\nenabled = false\nbase_url = \"http://127.0.0.1:3002\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	disabledLoaded, err := config.LoadWithEnv(disabledCfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv disabled: %v", err)
	}
	disabledApp, err := NewWithStderr(disabledLoaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatalf("NewWithStderr disabled: %v", err)
	}
	for _, st := range disabledApp.ProviderStatuses() {
		if st.Name == spec.ProviderFirecrawl {
			if st.Enabled || st.Source != spec.CredSourceOptional {
				t.Fatalf("expected disabled self-hosted firecrawl source=optional, got %+v", st)
			}
			break
		}
	}
}

func TestSearchMCPCacheIsolatesIncludeExtraAndNormalizesOptions(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"Code":0,"Message":"","Data":{"SearchHashId":"h1","Items":[{"Title":"Go","ContentType":"answer","ContentText":"body","Url":"https://www.zhihu.com/question/1","VoteUpCount":42}]}}`)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	body := "[search]\nproviders = [\"zhihu\"]\n[providers.zhihu]\nbase_url = \"" + srv.URL + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == spec.EnvZhihuAPIKey {
			return "zh-cache-test-key"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	application, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatalf("NewWithStderr: %v", err)
	}

	// 1. extra=false 与 extra=true 不得共用缓存键，后者必须重新查询并返回 extra
	envNoExtra, err := application.SearchMCP(t.Context(), &core.SearchRequest{Query: "go", Count: 5, IncludeExtra: false})
	if err != nil || !envNoExtra.OK || len(envNoExtra.Results) != 1 || envNoExtra.Results[0].Extra != nil {
		t.Fatalf("unexpected extra=false result: env=%+v err=%v", envNoExtra, err)
	}
	envWithExtra, err := application.SearchMCP(t.Context(), &core.SearchRequest{Query: "go", Count: 5, IncludeExtra: true})
	if err != nil || !envWithExtra.OK || envWithExtra.Meta.Cached || len(envWithExtra.Results) != 1 || envWithExtra.Results[0].Extra == nil {
		t.Fatalf("expected extra=true cache miss with populated Extra, got cached=%v results=%+v err=%v", envWithExtra.Meta.Cached, envWithExtra.Results, err)
	}
	if hits.Load() != 2 {
		t.Fatalf("expected 2 upstream hits across extra=false/true, got %d", hits.Load())
	}

	// 2. Options 键大小写与首尾空白归一化命中同一缓存键
	if _, err := application.SearchMCP(t.Context(), &core.SearchRequest{
		Query:   "go-opt",
		Count:   5,
		Options: map[string]string{"zhihu.scope": "zhihu"},
	}); err != nil {
		t.Fatal(err)
	}
	envNormHit, err := application.SearchMCP(t.Context(), &core.SearchRequest{
		Query:   "go-opt",
		Count:   5,
		Options: map[string]string{" Zhihu.Scope ": " zhihu "},
	})
	if err != nil || !envNormHit.Meta.Cached {
		t.Fatalf("expected normalized Options to hit search cache, cached=%v err=%v", envNormHit != nil && envNormHit.Meta.Cached, err)
	}
}
