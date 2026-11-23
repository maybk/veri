package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/spec"
)

func TestAppMCPCacheAndSingleflightCoalescing(t *testing.T) {
	var searchHits atomic.Int32
	var fetchHits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tools/search":
			searchHits.Add(1)
			time.Sleep(40 * time.Millisecond)
			_, _ = w.Write([]byte(`{"results":[{"title":"Go","url":"https://go.dev","snippet":"Go lang","summary":"Go summary"}]}`))
		case "/v1/tools/fetch":
			fetchHits.Add(1)
			_, _ = w.Write([]byte(`{"title":"Long Page","content":"Line 1.\n\nLine 2 continuation body text."}`))
		}
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-secret123456"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded.Config.Providers.DeepSeek.BaseURL = srv.URL

	application, err := New(loaded, "1.0.0-test")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// 1. 并发发起 5 个完全相同的 SearchMCP 请求 -> flightGroup + 缓存必须保证上游只收到 1 次计费请求！
	var wg sync.WaitGroup
	for range 5 {
		wg.Go(func() {
			env, sErr := application.SearchMCP(t.Context(), &core.SearchRequest{Query: "golang", Count: 3})
			if sErr != nil || !env.OK || len(env.Results) != 1 {
				t.Errorf("SearchMCP failed: env=%+v err=%v", env, sErr)
			}
		})
	}
	wg.Wait()
	if searchHits.Load() != 1 {
		t.Fatalf("expected flightGroup to coalesce 5 concurrent searches into 1 upstream call, got %d", searchHits.Load())
	}

	// 再次调用应命中进程内缓存，Cached=true 且 Cost 为空（不重复计费）
	cachedEnv, err := application.SearchMCP(t.Context(), &core.SearchRequest{Query: "golang", Count: 3})
	if err != nil || !cachedEnv.Meta.Cached || len(cachedEnv.Meta.Cost) != 0 || searchHits.Load() != 1 {
		t.Fatalf("expected cache hit with zero cost, got cached=%v cost=%v hits=%d", cachedEnv.Meta.Cached, cachedEnv.Meta.Cost, searchHits.Load())
	}
	cachedEnv.Results[0].Title = "MutatedByCaller"
	cachedEnv2, err := application.SearchMCP(t.Context(), &core.SearchRequest{Query: "golang", Count: 3})
	if err != nil || cachedEnv2.Results[0].Title != "Go" {
		t.Fatalf("expected deep-copied cached envelope, got title=%q", cachedEnv2.Results[0].Title)
	}

	// 验证单个短超时调用者取消时不会中断同键 flightGroup 的其他并发调用者
	shortCtx, cancelShort := context.WithTimeout(t.Context(), 5*time.Millisecond)
	defer cancelShort()
	var shortErr, fullErr error
	var fullEnv *core.Envelope
	var cwg sync.WaitGroup
	cwg.Go(func() {
		_, shortErr = application.SearchMCP(shortCtx, &core.SearchRequest{Query: "isolated-cancel", Count: 3})
	})
	cwg.Go(func() {
		fullEnv, fullErr = application.SearchMCP(t.Context(), &core.SearchRequest{Query: "isolated-cancel", Count: 3})
	})
	cwg.Wait()
	if shortErr == nil || fullErr != nil || fullEnv == nil || !fullEnv.OK {
		t.Fatalf("expected short caller canceled while full caller succeeded: shortErr=%v fullErr=%v fullEnv=%+v", shortErr, fullErr, fullEnv)
	}

	// 2. 验证 FetchMCP 分页续读直接命中完整正文缓存，不发起第二次请求
	p1, err := application.FetchMCP(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/article",
		Strategy: core.FetchAPIOnly,
		MaxChars: 10,
	})
	if err != nil || p1.Page.NextOffset == nil {
		t.Fatalf("expected paginated first chunk, got %+v err=%v", p1, err)
	}
	p2, err := application.FetchMCP(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/article",
		Strategy: core.FetchAPIOnly,
		Offset:   *p1.Page.NextOffset,
		MaxChars: 100,
	})
	if err != nil || !p2.Meta.Cached || fetchHits.Load() != 1 {
		t.Fatalf("expected paginated continuation from cache with 1 upstream hit, got cached=%v hits=%d err=%v", p2.Meta.Cached, fetchHits.Load(), err)
	}

	// 验证 fresh=true && Offset==0 绕过旧缓存重新拉取并刷新缓存，且后续分页（含携带 fresh=true）直接命中新缓存不重复请求
	pFresh1, err := application.FetchMCP(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/article",
		Strategy: core.FetchAPIOnly,
		MaxChars: 10,
		Options:  map[string]string{spec.OptFetchFresh: "true"},
	})
	if err != nil || pFresh1.Meta.Cached || fetchHits.Load() != 2 || pFresh1.Page.NextOffset == nil {
		t.Fatalf("expected fresh=true at offset=0 to bypass cache (hits=2), got cached=%v hits=%d err=%v", pFresh1.Meta.Cached, fetchHits.Load(), err)
	}
	pFresh2, err := application.FetchMCP(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/article",
		Strategy: core.FetchAPIOnly,
		Offset:   *pFresh1.Page.NextOffset,
		MaxChars: 100,
		Options:  map[string]string{spec.OptFetchFresh: "true"},
	})
	if err != nil || !pFresh2.Meta.Cached || fetchHits.Load() != 2 {
		t.Fatalf("expected fresh pagination at offset>0 to reuse refreshed cache (hits=2), got cached=%v hits=%d err=%v", pFresh2.Meta.Cached, fetchHits.Load(), err)
	}
}

func TestFetchMCPUnboundedCacheAndFlightRecovery(t *testing.T) {
	fullBody := "First paragraph text.\n\nSecond paragraph text that exceeds CLI fetch.max_chars."
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"title":"Full Doc","content":` + `"` + strings.ReplaceAll(fullBody, "\n", `\n`) + `"` + `}`))
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-secret99887766"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded.Config.Providers.DeepSeek.BaseURL = srv.URL
	// 即使配置了非零 CLI fetch.max_chars，FetchMCP 也必须缓存未截断的完整正文以支持后续分页
	loaded.Config.Fetch.MaxChars = 20

	application, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	chunk1, err := application.FetchMCP(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/long",
		Strategy: core.FetchAPIOnly,
		MaxChars: 25,
	})
	if err != nil || chunk1.Page == nil || chunk1.Page.NextOffset == nil {
		t.Fatalf("expected NextOffset even when config fetch.max_chars=20, got %+v err=%v", chunk1, err)
	}
	chunk2, err := application.FetchMCP(t.Context(), &core.FetchRequest{
		URL:      "https://example.com/long",
		Strategy: core.FetchAPIOnly,
		Offset:   *chunk1.Page.NextOffset,
		MaxChars: 200,
	})
	if err != nil || !strings.Contains(chunk2.Page.Content, "exceeds CLI fetch.max_chars") {
		t.Fatalf("expected full remainder from cache, got %+v err=%v", chunk2, err)
	}

	// 验证 flightGroup 在全部等待者取消时立即取消底层共享 ctx，且自动恢复内部 panic
	synctest.Test(t, func(t *testing.T) {
		var fg flightGroup[int]
		canceledCh := make(chan struct{})
		cCtx, cancelCaller := context.WithCancel(t.Context())
		defer cancelCaller()

		go func() {
			time.Sleep(15 * time.Millisecond)
			cancelCaller()
		}()
		_, completed, _ := fg.Do(cCtx, "cancel-key", func(sharedCtx context.Context) (int, error) {
			<-sharedCtx.Done()
			close(canceledCh)
			return 0, sharedCtx.Err()
		})
		if completed {
			t.Fatal("expected canceled caller to report completed=false")
		}
		synctest.Wait()
		select {
		case <-canceledCh:
		default:
			t.Fatal("flightGroup failed to cancel sharedCtx when sole waiter canceled")
		}

		_, pCompleted, pErr := fg.Do(t.Context(), "panic-key", func(context.Context) (int, error) {
			panic("synthetic boom")
		})
		if !pCompleted || core.AsError(pErr).Code != core.CodeInternal {
			t.Fatalf("expected flightGroup to recover panic into CodeInternal, got completed=%v err=%v", pCompleted, pErr)
		}
	})

	// 验证 buildFetchCacheKey 区分不同 #fragment 行区间与厂商参数，同时忽略 fresh 以便刷新同一规范缓存槽
	k1 := buildFetchCacheKey("https://github.com/a/b/blob/main/x.go#L1-L5", core.FetchSmartLocal, nil, nil)
	k2 := buildFetchCacheKey("https://github.com/a/b/blob/main/x.go#L10-L20", core.FetchSmartLocal, nil, nil)
	if k1 == k2 {
		t.Fatal("buildFetchCacheKey must preserve URL fragment so GitHub line slices do not collide")
	}
	kFresh := buildFetchCacheKey("https://github.com/a/b/blob/main/x.go#L1-L5", core.FetchSmartLocal, nil, map[string]string{"fresh": "true"})
	kAdv := buildFetchCacheKey("https://github.com/a/b/blob/main/x.go#L1-L5", core.FetchSmartLocal, nil, map[string]string{spec.OptTavilyExtractDepth: "advanced"})
	if kFresh != k1 || kAdv == k1 {
		t.Fatalf("unexpected fetch cache key behavior: k1=%s kFresh=%s kAdv=%s", k1, kFresh, kAdv)
	}
	lru := newBoundedTTLLRU[string](4, 10, time.Minute, func(s string) int { return len(s) })
	lru.Set("doc", "short")
	lru.Set("doc", "oversized-body-exceeding-limit")
	if _, ok := lru.Get("doc"); ok {
		t.Fatal("expected oversize Set to evict stale entry under same key")
	}
}

func TestTTLLRUExpiryWithSynctest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		cache := newTTLLRU[string](4, 5*time.Minute)
		cache.Set("k1", "v1")
		if got, ok := cache.Get("k1"); !ok || got != "v1" {
			t.Fatalf("expected fresh cache hit, got (%q, %v)", got, ok)
		}

		time.Sleep(5*time.Minute + time.Second)
		if _, ok := cache.Get("k1"); ok {
			t.Fatal("expected entry to expire after TTL")
		}
	})
}

func TestFetchMCPViewNotesRedactionLiveAndCached(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><html><head><title>Doc</title></head><body><article>`+
			`<h1>Overview</h1><p>`+strings.Repeat("Clean reference documentation content for local extraction. ", 12)+`</p>`+
			`</article></body></html>`)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-ViewNotesSecret99887766"
		}
		return ""
	})
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	loaded.Config.Network.AllowPrivate = true
	loaded.Config.Fetch.Strategy = string(core.FetchLocalOnly)

	application, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatalf("NewWithStderr: %v", err)
	}

	// 1. 首次实时抓取未命中 match：viewNotes 必须脱敏 URL 凭据、查询参数与已加载 API Key
	envLive, err := application.FetchMCP(t.Context(), &core.FetchRequest{
		URL:      srv.URL,
		Strategy: core.FetchLocalOnly,
		Match:    "https://u:VnMatchPass99@example.com/?token=VnMatchTok88 sk-ViewNotesSecret99887766",
	})
	if err != nil || !envLive.OK || envLive.Meta.Cached || len(envLive.Meta.Notes) == 0 {
		t.Fatalf("unexpected live FetchMCP result: ok=%v cached=%v notes=%v err=%v", envLive.OK, envLive.Meta.Cached, envLive.Meta.Notes, err)
	}
	liveNotes := strings.Join(envLive.Meta.Notes, "\n")
	for _, leaked := range []string{"VnMatchPass99", "VnMatchTok88", "ViewNotesSecret99887766"} {
		if strings.Contains(liveNotes, leaked) {
			t.Fatalf("live FetchMCP viewNotes leaked %q: %s", leaked, liveNotes)
		}
	}

	// 2. 二次缓存命中未命中 section：缓存切片路径 viewNotes 同样必须脱敏
	envCached, err := application.FetchMCP(t.Context(), &core.FetchRequest{
		URL:      srv.URL,
		Strategy: core.FetchLocalOnly,
		Section:  "https://u:VnSecPass99@example.com/?api_key=VnSecKey88 sk-ViewNotesSecret99887766",
	})
	if err != nil || !envCached.OK || !envCached.Meta.Cached || len(envCached.Meta.Notes) == 0 {
		t.Fatalf("unexpected cached FetchMCP result: ok=%v cached=%v notes=%v err=%v", envCached.OK, envCached.Meta.Cached, envCached.Meta.Notes, err)
	}
	cachedNotes := strings.Join(envCached.Meta.Notes, "\n")
	for _, leaked := range []string{"VnSecPass99", "VnSecKey88", "ViewNotesSecret99887766"} {
		if strings.Contains(cachedNotes, leaked) {
			t.Fatalf("cached FetchMCP viewNotes leaked %q: %s", leaked, cachedNotes)
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

	// 3. 未指定 Providers（走自动/分链路由）与显式传入 search.providers 必须隔离缓存键，避免英文分链或融合池误命中缓存
	cfg := config.Default().Search
	kAuto := buildSearchCacheKey(&core.SearchRequest{Query: "golang context"}, cfg, 24000)
	kExplicit := buildSearchCacheKey(&core.SearchRequest{Query: "golang context", Providers: cfg.Providers}, cfg, 24000)
	if kAuto == kExplicit {
		t.Fatal("auto-routed search cache key must not collide with explicit search.providers cache key")
	}

	// 4. 大小写/空白扩展参数均准确触发 fresh 缓存旁路
	for _, opts := range []map[string]string{
		{" Fresh ": " TRUE "},
		{" Exa.Max_Age_Hours ": " 0 "},
		{" TINYFISH_TTL ": "0"},
	} {
		if !isFreshFetchBypass(opts) {
			t.Fatalf("expected isFreshFetchBypass=true for %+v", opts)
		}
	}
}
