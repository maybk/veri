package app

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/spec"
)

func TestDiskCacheAtomicityConcurrencySelfHealAndSafeClear(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	dc := newDiskCache(dir, 1<<20, nil)
	dc.now = func() time.Time { return now }

	key := buildFetchCacheKey("https://example.com/doc", core.FetchAPIFirst, nil, nil)
	sample := &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     true,
		Kind:   spec.KindFetch,
		Page: &core.Page{
			URL:        "https://example.com/doc",
			Title:      "Doc",
			Content:    "Hello disk cache body",
			TotalChars: 21,
			Provider:   spec.ProviderDeepSeek,
		},
		Meta: core.Meta{
			Strategy:  string(core.FetchAPIFirst),
			Providers: []core.ProviderReport{{Name: spec.ProviderDeepSeek, Status: core.ProviderStatusOK, Results: 1}},
			Cost:      []core.Cost{{Currency: "USD", Micros: 1000}},
		},
	}

	// 1. 并发多 goroutine 同时读写同一键不得出现竞态或撕裂 JSON。
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Go(func() {
			if i%2 == 0 {
				dc.Set(diskCacheKindFetch, key, sample, 10*time.Minute)
			} else {
				if got, ok := dc.Get(diskCacheKindFetch, key); ok && (got.Page == nil || got.Page.Content != sample.Page.Content) {
					t.Errorf("corrupted concurrent read: %+v", got)
				}
			}
		})
	}
	wg.Wait()

	got, ok := dc.Get(diskCacheKindFetch, key)
	if !ok || got.Page == nil || got.Page.Content != sample.Page.Content {
		t.Fatalf("expected hit after concurrent writes, got=(%+v, %v)", got, ok)
	}

	// 2. 过期条目读取时静默删除并按 miss 处理。
	now = now.Add(11 * time.Minute)
	if _, ok := dc.Get(diskCacheKindFetch, key); ok {
		t.Fatal("expected expired entry to miss")
	}
	entryPath, _ := dc.entryPath(diskCacheKindFetch, key)
	if _, err := os.Stat(entryPath); !os.IsNotExist(err) {
		t.Fatalf("expected expired file self-removed, stat err=%v", err)
	}

	// 3. 损坏文件、旧版本记录、解压炸弹均静默自愈。
	if err := os.MkdirAll(filepath.Dir(entryPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, []byte("not-gzip-garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := dc.Get(diskCacheKindFetch, key); ok {
		t.Fatal("expected corrupt entry to miss")
	}
	if _, err := os.Stat(entryPath); !os.IsNotExist(err) {
		t.Fatalf("expected corrupt file self-removed, stat err=%v", err)
	}

	// 构造超过 16 MiB 解压上限的 gzip 炸弹验证防膨胀自愈。
	var bombBuf bytes.Buffer
	gz := gzip.NewWriter(&bombBuf)
	chunk := bytes.Repeat([]byte("a"), 1<<20)
	for range 17 {
		if _, err := gz.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entryPath, bombBuf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := dc.Get(diskCacheKindFetch, key); ok {
		t.Fatal("expected oversized gzip bomb entry to miss")
	}
	if _, err := os.Stat(entryPath); !os.IsNotExist(err) {
		t.Fatalf("expected oversized bomb file self-removed, stat err=%v", err)
	}

	// 4. 安全清理严禁误删非 veri 缓存文件与子目录。
	dc.Set(diskCacheKindFetch, key, sample, 10*time.Minute)
	unrelatedFile := filepath.Join(dc.versionDir(), "important-user-notes.txt")
	unrelatedRoot := filepath.Join(dir, "keep-me.txt")
	if err := os.WriteFile(unrelatedFile, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelatedRoot, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	clearRes, err := ClearDiskCache(dir)
	if err != nil {
		t.Fatalf("ClearDiskCache: %v", err)
	}
	if clearRes.RemovedEntries != 1 || clearRes.FreedBytes <= 0 {
		t.Fatalf("unexpected clear result: %+v", clearRes)
	}
	if _, err := os.Stat(unrelatedFile); err != nil {
		t.Fatalf("ClearDiskCache must not remove unrelated file in v1/: %v", err)
	}
	if _, err := os.Stat(unrelatedRoot); err != nil {
		t.Fatalf("ClearDiskCache must not remove unrelated file in root: %v", err)
	}

	// 5. 符号链接不得被 Get 读取或被 ClearDiskCache 跟随/删除。
	symTarget := filepath.Join(dir, "secret-target.txt")
	if err := os.WriteFile(symTarget, []byte("do-not-touch"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(symTarget, entryPath); err == nil {
		if _, ok := dc.Get(diskCacheKindFetch, key); ok {
			t.Fatal("diskCache.Get must ignore symlink entry")
		}
		if _, err := ClearDiskCache(dir); err != nil {
			t.Fatalf("ClearDiskCache with symlink: %v", err)
		}
		if _, err := os.Lstat(entryPath); err != nil {
			t.Fatalf("expected symlink left untouched by Get/ClearDiskCache, err=%v", err)
		}
		_ = os.Remove(entryPath)
	}
}

func TestDiskCacheLRUEvictionByMaxBytes(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	dc := newDiskCache(dir, 1<<20, nil)
	dc.now = func() time.Time { return now }

	makeEnv := func(tag string) *core.Envelope {
		return &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     true,
			Kind:   spec.KindFetch,
			Page: &core.Page{
				URL:        "https://example.com/" + tag,
				Title:      tag,
				Content:    strings.Repeat(tag+"-payload-body-text-", 60),
				TotalChars: 1200,
				Provider:   spec.ProviderDeepSeek,
			},
		}
	}

	k1 := buildFetchCacheKey("https://example.com/1", core.FetchAPIFirst, nil, nil)
	k2 := buildFetchCacheKey("https://example.com/2", core.FetchAPIFirst, nil, nil)
	k3 := buildFetchCacheKey("https://example.com/3", core.FetchAPIFirst, nil, nil)

	dc.Set(diskCacheKindFetch, k1, makeEnv("p1"), time.Hour)
	p1Path, _ := dc.entryPath(diskCacheKindFetch, k1)
	fi, err := os.Stat(p1Path)
	if err != nil {
		t.Fatal(err)
	}
	// 容量精确设为仅可容纳 2 条压缩后记录，第 3 条写入必触发按 mtime 淘汰最旧条目。
	dc.maxBytes = fi.Size()*2 + 20

	now = now.Add(time.Minute)
	dc.Set(diskCacheKindFetch, k2, makeEnv("p2"), time.Hour)
	// 访问 k1 刷新其 mtime，使 k2 成为最久未访问条目。
	now = now.Add(time.Minute)
	if _, ok := dc.Get(diskCacheKindFetch, k1); !ok {
		t.Fatal("expected k1 hit before eviction")
	}
	now = now.Add(time.Minute)
	dc.Set(diskCacheKindFetch, k3, makeEnv("p3"), time.Hour)

	if _, ok := dc.Get(diskCacheKindFetch, k1); !ok {
		t.Fatal("expected recently accessed k1 to survive LRU eviction")
	}
	if _, ok := dc.Get(diskCacheKindFetch, k3); !ok {
		t.Fatal("expected newly written k3 to survive eviction")
	}
	if _, ok := dc.Get(diskCacheKindFetch, k2); ok {
		t.Fatal("expected oldest untouched k2 to be evicted under max_bytes limit")
	}
}

func TestAppCLIDiskCacheFetchSlicingFreshSearchAndSecurity(t *testing.T) {
	var fetchHits atomic.Int32
	var searchHits atomic.Int32
	pageBody := "# Overview\n\nIntro section paragraph with enough content.\n\n## Details\n\nDetailed section body with specific keyword AlphaBetaGamma and trailing lines."

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/fetch":
			n := fetchHits.Add(1)
			_, _ = fmt.Fprintf(w, `{"title":"Long Guide","content":%q}`, fmt.Sprintf("%s (rev %d)", pageBody, n))
		case "/v1/tools/search":
			searchHits.Add(1)
			_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev","snippet":"Go lang","summary":"Go summary"}]}`))
		}
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	cfgPath := filepath.Join(tmp, "config.toml")
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		switch k {
		case spec.EnvDeepSeekAPIKey:
			return "sk-cli-cache-secret-1234"
		case spec.EnvCacheDir:
			return cacheDir
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	loaded.Config.Providers.DeepSeek.BaseURL = srv.URL
	loaded.Config.Fetch.MaxChars = 60 // 即使配置了全局截断，磁盘也必须存整页！

	app1, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatalf("NewWithStderr: %v", err)
	}

	// 1. 首次 CLI fetch 走网络并落盘完整正文；按 max_chars 截断返回。
	env1, err := app1.Fetch(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/guide",
		Strategy: core.FetchAPIOnly,
		MaxChars: 50,
	})
	if err != nil || !env1.OK || env1.Meta.Cached || env1.Page == nil || env1.Page.NextOffset == nil || fetchHits.Load() != 1 {
		t.Fatalf("unexpected first CLI fetch: ok=%v cached=%v hits=%d err=%v", env1 != nil && env1.OK, env1 != nil && env1.Meta.Cached, fetchHits.Load(), err)
	}

	// 2. 模拟新 CLI 进程（新建 App 实例）：--section、--match、--offset 全部命中磁盘缓存零网络调用！
	app2, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	secEnv, err := app2.Fetch(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/guide",
		Strategy: core.FetchAPIOnly,
		Section:  "Details",
		MaxChars: 400,
	})
	if err != nil || !secEnv.OK || !secEnv.Meta.Cached || fetchHits.Load() != 1 || !strings.Contains(secEnv.Page.Content, "AlphaBetaGamma") {
		t.Fatalf("expected --section disk cache hit with full section content: cached=%v hits=%d content=%q err=%v",
			secEnv != nil && secEnv.Meta.Cached, fetchHits.Load(), secEnv.Page.Content, err)
	}

	offEnv, err := app2.Fetch(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/guide",
		Strategy: core.FetchAPIOnly,
		Offset:   *env1.Page.NextOffset,
		MaxChars: 400,
	})
	if err != nil || !offEnv.OK || !offEnv.Meta.Cached || fetchHits.Load() != 1 || !strings.Contains(offEnv.Page.Content, "(rev 1)") {
		t.Fatalf("expected --offset continuation from disk cache: cached=%v hits=%d content=%q err=%v",
			offEnv != nil && offEnv.Meta.Cached, fetchHits.Load(), offEnv.Page.Content, err)
	}

	// 3. 参数校验前置：即使该 URL 已在磁盘缓存中，非法负 offset 仍必须报错而非返回缓存！
	if _, badErr := app2.Fetch(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/guide",
		Strategy: core.FetchAPIOnly,
		Offset:   -1,
	}); badErr == nil || core.AsError(badErr).Code != core.CodeInvalidArgument {
		t.Fatalf("expected invalid negative offset to fail before cache hit, got %v", badErr)
	}

	// 4. --fresh 绕过读缓存并用最新抓取结果刷新磁盘。
	freshEnv, err := app2.Fetch(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/guide",
		Strategy: core.FetchAPIOnly,
		MaxChars: 400,
		Options:  map[string]string{spec.OptFetchFresh: "true"},
	})
	if err != nil || !freshEnv.OK || freshEnv.Meta.Cached || fetchHits.Load() != 2 || !strings.Contains(freshEnv.Page.Content, "(rev 2)") {
		t.Fatalf("expected --fresh to bypass and refresh disk cache: cached=%v hits=%d err=%v",
			freshEnv != nil && freshEnv.Meta.Cached, fetchHits.Load(), err)
	}
	afterFresh, err := app2.Fetch(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/guide",
		Strategy: core.FetchAPIOnly,
		MaxChars: 400,
	})
	if err != nil || !afterFresh.Meta.Cached || fetchHits.Load() != 2 || !strings.Contains(afterFresh.Page.Content, "(rev 2)") {
		t.Fatalf("expected subsequent fetch to read refreshed rev 2 from disk cache: cached=%v hits=%d content=%q",
			afterFresh != nil && afterFresh.Meta.Cached, fetchHits.Load(), afterFresh.Page.Content)
	}

	// 5. CLI search 默认 search_ttl=0s 不缓存；显式开启 cache.cli.search_ttl=2m 后命中磁盘，--no-cache 跳过读写。
	if _, err := app2.Search(t.Context(), &core.SearchRequest{Query: "golang"}); err != nil {
		t.Fatal(err)
	}
	if _, err := app2.Search(t.Context(), &core.SearchRequest{Query: "golang"}); err != nil {
		t.Fatal(err)
	}
	if searchHits.Load() != 2 {
		t.Fatalf("expected default CLI search (search_ttl=0s) not to cache, hits=%d", searchHits.Load())
	}

	searchTTL := config.Duration{Duration: 2 * time.Minute}
	loaded.Config.Cache.CLI.SearchTTL = &searchTTL
	app3, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := app3.Search(t.Context(), &core.SearchRequest{Query: "rust"})
	if err != nil || s1.Meta.Cached || searchHits.Load() != 3 {
		t.Fatalf("expected first cached-mode CLI search miss, cached=%v hits=%d err=%v", s1 != nil && s1.Meta.Cached, searchHits.Load(), err)
	}
	s2, err := app3.Search(t.Context(), &core.SearchRequest{Query: "rust"})
	if err != nil || !s2.Meta.Cached || len(s2.Meta.Cost) != 0 || s2.Meta.DurationMS != 0 ||
		(len(s2.Meta.Providers) > 0 && s2.Meta.Providers[0].LatencyMS != 0) || searchHits.Load() != 3 {
		t.Fatalf("expected second CLI search disk cache hit with zero cost/latency, cached=%v meta=%+v hits=%d err=%v",
			s2 != nil && s2.Meta.Cached, s2.Meta, searchHits.Load(), err)
	}
	sNoCache, err := app3.Search(t.Context(), &core.SearchRequest{Query: "rust", NoCache: true})
	if err != nil || sNoCache.Meta.Cached || searchHits.Load() != 4 {
		t.Fatalf("expected --no-cache search to bypass disk cache, cached=%v hits=%d err=%v", sNoCache != nil && sNoCache.Meta.Cached, searchHits.Load(), err)
	}

	// 6. InspectCache 统计准确包含 1 fetch + 1 search 条目。
	info, err := app3.CacheInfo()
	if err != nil {
		t.Fatalf("CacheInfo: %v", err)
	}
	if info.Disk.Entries != 2 || info.Disk.FetchEntries != 1 || info.Disk.SearchEntries != 1 || info.Disk.SizeBytes <= 0 {
		t.Fatalf("unexpected CacheInfo disk stats: %+v", info.Disk)
	}
}

func TestNoStoreAndMCPDecoupledTTLsAndTokenIsolation(t *testing.T) {
	var noStoreHits atomic.Int32
	var searchHits atomic.Int32
	var fetchHits atomic.Int32

	noStoreSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		noStoreHits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "private, no-store")
		_, _ = io.WriteString(w, `<!doctype html><html><head><title>Secret</title></head><body><article>`+
			`<h1>Private Doc</h1><p>`+strings.Repeat("Sensitive non-storable documentation content body. ", 12)+`</p>`+
			`</article></body></html>`)
	}))
	defer noStoreSrv.Close()

	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/search":
			searchHits.Add(1)
			_, _ = w.Write([]byte(`{"results":[{"title":"Live","url":"https://go.dev","snippet":"s","summary":"sum"}]}`))
		case "/v1/tools/fetch":
			fetchHits.Add(1)
			_, _ = w.Write([]byte(`{"title":"Doc","content":"Paragraph one.\n\nParagraph two for pagination."}`))
		}
	}))
	defer apiSrv.Close()

	tmp := t.TempDir()
	cacheDir := filepath.Join(tmp, "cache")
	cfgPath := filepath.Join(tmp, "config.toml")
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		switch k {
		case spec.EnvDeepSeekAPIKey:
			return "sk-mcp-decoupled-1234"
		case spec.EnvCacheDir:
			return cacheDir
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded.Config.Network.AllowPrivate = true
	loaded.Config.Providers.DeepSeek.BaseURL = apiSrv.URL
	// MCP 单独关闭 search_ttl（0s），同时继承 15m fetch_ttl：验证两者彻底解耦！
	zero := config.Duration{Duration: 0}
	loaded.Config.Cache.MCP.SearchTTL = &zero

	application, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Cache-Control: no-store 响应不得写入 MCP 内存缓存（两次均命中上游）。
	if _, err := application.FetchMCP(t.Context(), &core.FetchRequest{URL: noStoreSrv.URL, Strategy: core.FetchLocalOnly}); err != nil {
		t.Fatal(err)
	}
	if env2, err := application.FetchMCP(t.Context(), &core.FetchRequest{URL: noStoreSrv.URL, Strategy: core.FetchLocalOnly}); err != nil || env2.Meta.Cached || noStoreHits.Load() != 2 {
		t.Fatalf("expected no-store page never cached in MCP RAM: cached=%v hits=%d err=%v", env2 != nil && env2.Meta.Cached, noStoreHits.Load(), err)
	}

	// 2. MCP search_ttl=0s 关闭搜索缓存，但 fetch_ttl=15m 翻页缓存仍正常工作！
	if _, err := application.SearchMCP(t.Context(), &core.SearchRequest{Query: "live-query"}); err != nil {
		t.Fatal(err)
	}
	if _, err := application.SearchMCP(t.Context(), &core.SearchRequest{Query: "live-query"}); err != nil {
		t.Fatal(err)
	}
	if searchHits.Load() != 2 {
		t.Fatalf("expected MCP search_ttl=0s to disable search cache, hits=%d", searchHits.Load())
	}

	p1, err := application.FetchMCP(t.Context(), &core.FetchRequest{URL: "https://example.com/decoupled", Strategy: core.FetchAPIOnly, MaxChars: 15})
	if err != nil || p1.Page.NextOffset == nil {
		t.Fatalf("expected MCP paginated first chunk, err=%v", err)
	}
	p2, err := application.FetchMCP(t.Context(), &core.FetchRequest{URL: "https://example.com/decoupled", Strategy: core.FetchAPIOnly, Offset: *p1.Page.NextOffset, MaxChars: 100})
	if err != nil || !p2.Meta.Cached || fetchHits.Load() != 1 {
		t.Fatalf("expected MCP fetch cache still active when search_ttl=0s: cached=%v hits=%d err=%v", p2 != nil && p2.Meta.Cached, fetchHits.Load(), err)
	}

	// 3. 切换 GITHUB_TOKEN 必须改变 fetch 缓存键，防止跨身份误命中私有仓库缓存。
	loadedNoGH, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == spec.EnvCacheDir {
			return cacheDir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	loadedWithGH, err := config.LoadWithEnv(cfgPath, func(k string) string {
		switch k {
		case spec.EnvCacheDir:
			return cacheDir
		case spec.EnvGitHubToken:
			return "ghp_privatetoken123456"
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	appAnon, err := NewWithStderr(loadedNoGH, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	appAuthed, err := NewWithStderr(loadedWithGH, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	kAnon := appAnon.fetchCacheKey("https://github.com/org/private-repo", core.FetchAPIFirst, nil, nil)
	kAuthed := appAuthed.fetchCacheKey("https://github.com/org/private-repo", core.FetchAPIFirst, nil, nil)
	if kAnon == kAuthed {
		t.Fatal("expected GitHub token presence to isolate fetch cache key")
	}

	// 4. low_quality 与 partial 信封绝不可入缓存。
	lowQEnv := &core.Envelope{
		OK:   true,
		Page: &core.Page{URL: "https://example.com/lowq", Content: "partial"},
		Meta: core.Meta{Providers: []core.ProviderReport{{Name: spec.ProviderLocal, Status: core.ProviderStatusLowQuality}}},
	}
	if isCacheableFetchEnvelope(lowQEnv, true) {
		t.Fatal("low_quality fallback envelope must not be cacheable")
	}
	privateRedirectEnv := &core.Envelope{
		OK:   true,
		Page: &core.Page{URL: "https://example.com/redir", FinalURL: "http://127.0.0.1:8080/internal", Content: "secret"},
		Meta: core.Meta{Providers: []core.ProviderReport{{Name: spec.ProviderLocal, Status: core.ProviderStatusOK}}},
	}
	if isCacheableFetchEnvelope(privateRedirectEnv, true) {
		t.Fatal("private redirect target must not be disk-cacheable")
	}
	partialSearch := &core.Envelope{
		OK:      true,
		Results: []core.Result{{Title: "x", URL: "https://example.com"}},
		Meta:    core.Meta{Partial: true},
	}
	if isCacheableSearchEnvelope(partialSearch) {
		t.Fatal("partial fusion search envelope must not be cacheable")
	}
}

func TestMCPAndCLIDiskCacheCooperation(t *testing.T) {
	var upstreamHits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tools/fetch" {
			n := upstreamHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"title":"Shared Page %d","content":"# Shared Heading\n\n## Alpha\n\nBody version %d with sufficient text for quality gate."}`, n, n)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "config.toml")
	cacheDir := filepath.Join(tmp, "cache")
	cfgBody := fmt.Sprintf(`
[fetch]
strategy = "api_only"
providers = ["deepseek"]

[cache]
fetch_ttl = "15m"

[providers.deepseek]
api_key = "sk-coop-test-secret"
base_url = %q
`, srv.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	loadApp := func() *App {
		t.Helper()
		loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
			if k == spec.EnvCacheDir {
				return cacheDir
			}
			return ""
		})
		if err != nil {
			t.Fatalf("LoadWithEnv: %v", err)
		}
		a, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
		if err != nil {
			t.Fatalf("NewWithStderr: %v", err)
		}
		return a
	}

	// 1. CLI 先抓取并落盘，全新的 MCP App 实例首次 FetchMCP 直接命中 L2 磁盘缓存（0 次上游请求、cached=true、duration_ms=0）
	cliApp := loadApp()
	firstCLI, err := cliApp.Fetch(t.Context(), &core.FetchRequest{URL: "https://example.com/shared-doc"})
	if err != nil || !firstCLI.OK || firstCLI.Meta.Cached || upstreamHits.Load() != 1 {
		t.Fatalf("CLI cold fetch failed: env=%+v hits=%d err=%v", firstCLI, upstreamHits.Load(), err)
	}

	mcpApp := loadApp()
	hitFromDisk, err := mcpApp.FetchMCP(t.Context(), &core.FetchRequest{URL: "https://example.com/shared-doc", Section: "Alpha"})
	if err != nil || !hitFromDisk.OK || !hitFromDisk.Meta.Cached || hitFromDisk.Meta.DurationMS != 0 || upstreamHits.Load() != 1 {
		t.Fatalf("expected MCP to hit CLI disk cache with duration_ms=0: meta=%+v hits=%d err=%v", hitFromDisk.Meta, upstreamHits.Load(), err)
	}
	if !strings.Contains(hitFromDisk.Page.Content, "Body version 1") {
		t.Fatalf("unexpected sliced content from L2 disk cache: %q", hitFromDisk.Page.Content)
	}

	// 2. MCP 抓取新页面写入 L1+L2 后，新 CLI 实例与重启后的 MCP 实例均可零成本命中
	mcpCold, err := mcpApp.FetchMCP(t.Context(), &core.FetchRequest{URL: "https://example.com/mcp-origin"})
	if err != nil || !mcpCold.OK || mcpCold.Meta.Cached || upstreamHits.Load() != 2 {
		t.Fatalf("MCP cold fetch failed: env=%+v hits=%d err=%v", mcpCold, upstreamHits.Load(), err)
	}
	cliApp2 := loadApp()
	cliHit, err := cliApp2.Fetch(t.Context(), &core.FetchRequest{URL: "https://example.com/mcp-origin"})
	if err != nil || !cliHit.OK || !cliHit.Meta.Cached || cliHit.Meta.DurationMS != 0 || upstreamHits.Load() != 2 {
		t.Fatalf("expected CLI to hit page cached by MCP: meta=%+v hits=%d err=%v", cliHit.Meta, upstreamHits.Load(), err)
	}

	// 3. fresh=true 同时绕过 L1 与 L2 并刷新磁盘，供后续 CLI 拿到最新版本
	mcpFresh, err := mcpApp.FetchMCP(t.Context(), &core.FetchRequest{
		URL:     "https://example.com/shared-doc",
		Options: map[string]string{spec.OptFetchFresh: "true"},
	})
	if err != nil || !mcpFresh.OK || mcpFresh.Meta.Cached || upstreamHits.Load() != 3 || !strings.Contains(mcpFresh.Page.Content, "Body version 3") {
		t.Fatalf("expected MCP fresh=true to bypass L1+L2 and refresh: content=%q hits=%d err=%v", mcpFresh.Page.Content, upstreamHits.Load(), err)
	}
	cliRefreshed, err := loadApp().Fetch(t.Context(), &core.FetchRequest{URL: "https://example.com/shared-doc"})
	if err != nil || !cliRefreshed.Meta.Cached || !strings.Contains(cliRefreshed.Page.Content, "Body version 3") || upstreamHits.Load() != 3 {
		t.Fatalf("expected refreshed L2 disk entry visible to CLI: content=%q cached=%v hits=%d err=%v", cliRefreshed.Page.Content, cliRefreshed.Meta.Cached, upstreamHits.Load(), err)
	}

	// 4. GetWithMaxAge 超龄未命中但保留 CLI 磁盘文件不误删
	dc := newDiskCache(cacheDir, 1<<20, nil)
	now := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	dc.now = func() time.Time { return now }
	k := buildFetchCacheKey("https://example.com/age-test", core.FetchAPIOnly, nil, nil)
	dc.Set(diskCacheKindFetch, k, firstCLI, 15*time.Minute)
	dc.now = func() time.Time { return now.Add(6 * time.Minute) }
	if _, ok := dc.GetWithMaxAge(diskCacheKindFetch, k, 5*time.Minute); ok {
		t.Fatal("expected GetWithMaxAge(5m) to miss 6m-old entry")
	}
	if _, ok := dc.Get(diskCacheKindFetch, k); !ok {
		t.Fatal("expected GetWithMaxAge miss NOT to delete entry still valid for 15m TTL")
	}
}

func TestDiskCachePruneThrottleAndStrictQuota(t *testing.T) {
	dc := newDiskCache(t.TempDir(), 1<<20, nil)
	dc.maxEntries = 4
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	dc.now = func() time.Time { return now }

	sample := func(i int) *core.Envelope {
		return &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     true,
			Kind:   spec.KindFetch,
			Query:  fmt.Sprintf("https://example.com/p/%d", i),
			Page: &core.Page{
				URL:        fmt.Sprintf("https://example.com/p/%d", i),
				Title:      "Sample",
				Content:    strings.Repeat("Clean content line.\n", 20),
				TotalChars: 400,
				Provider:   spec.ProviderLocal,
				SourceType: core.SourceGeneric,
			},
		}
	}

	// 同进程首次写盘必扫描 1 次；后续在配额与时间窗内的第 2～4 次写盘跳过目录扫描。
	for i := range 4 {
		k := buildFetchCacheKey(fmt.Sprintf("https://example.com/p/%d", i), core.FetchSmartLocal, nil, nil)
		dc.Set(diskCacheKindFetch, k, sample(i), 15*time.Minute)
	}
	if dc.pruneScans != 1 {
		t.Fatalf("expected 1 directory scan within quota window, got %d", dc.pruneScans)
	}

	// 第 5 次写盘预估条目触达上限 maxEntries=4，必须立刻扫描并淘汰最旧条目。
	k5 := buildFetchCacheKey("https://example.com/p/5", core.FetchSmartLocal, nil, nil)
	dc.Set(diskCacheKindFetch, k5, sample(5), 15*time.Minute)
	if dc.pruneScans != 2 {
		t.Fatalf("expected quota boundary to trigger immediate prune scan (want 2), got %d", dc.pruneScans)
	}
	if st := statDiskCache(dc.rootDir, now); st.Entries > 4 {
		t.Fatalf("expected strict entry quota <= 4 after prune, got %d", st.Entries)
	}

	// 超过扫描间隔后再次写盘会触发周期扫描。
	now = now.Add(diskCachePruneInterval + time.Second)
	dc.Set(diskCacheKindFetch, k5, sample(5), 15*time.Minute)
	if dc.pruneScans != 3 {
		t.Fatalf("expected interval expiry to trigger prune scan (want 3), got %d", dc.pruneScans)
	}
}

func BenchmarkDiskCacheGetSet(b *testing.B) {
	dc := newDiskCache(b.TempDir(), spec.DefaultCacheMaxBytes, nil)
	env := &core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     true,
		Kind:   spec.KindFetch,
		Query:  "https://docs.example.com/guide",
		Page: &core.Page{
			URL:        "https://docs.example.com/guide",
			Title:      "Guide",
			Content:    strings.Repeat("# Section\n\nConcise technical documentation paragraph.\n\n", 120),
			TotalChars: 6000,
			Provider:   spec.ProviderLocal,
			SourceType: core.SourceGeneric,
		},
	}
	key := buildFetchCacheKey(env.Query, core.FetchSmartLocal, nil, nil)
	dc.Set(diskCacheKindFetch, key, env, 15*time.Minute)

	b.Run("GetHit", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, ok := dc.GetWithMaxAge(diskCacheKindFetch, key, 15*time.Minute); !ok {
				b.Fatal("expected disk cache hit")
			}
		}
	})

	b.Run("SetWithinQuota", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			dc.Set(diskCacheKindFetch, key, env, 15*time.Minute)
		}
	})
}
