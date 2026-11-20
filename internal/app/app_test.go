package app

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
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

func TestProviderRegistryMatchesCoreCatalog(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	loaded, err := config.LoadWithEnv(cfgPath, func(string) string { return "" })
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	application, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatalf("NewWithStderr: %v", err)
	}

	statuses := application.ProviderStatuses()
	expected := spec.AllProviders()
	if len(statuses) != len(expected) {
		t.Fatalf("registered provider count (%d) != spec.AllProviders() (%d)", len(statuses), len(expected))
	}
	seen := make(map[string]bool, len(statuses))
	for _, st := range statuses {
		if seen[st.Name] {
			t.Errorf("duplicate provider registration %q", st.Name)
		}
		seen[st.Name] = true
		if !slices.Contains(expected, st.Name) {
			t.Errorf("provider %q registered in app but missing from spec.AllProviders()", st.Name)
		}
		if st.Role == "" {
			t.Errorf("provider %q has empty Role declaration", st.Name)
		}
		if strings.Contains(st.Details, "·") {
			t.Errorf("provider %q details must not contain middle dot: %q", st.Name, st.Details)
		}
		if st.Enabled && st.Timeout == "" {
			t.Errorf("enabled provider %q missing Timeout", st.Name)
		}
		if !st.Enabled && st.Timeout != "" {
			t.Errorf("disabled provider %q should omit Timeout, got %q", st.Name, st.Timeout)
		}
	}
}

func TestProviderStatusTimeoutAndDetailsWhenAllEnabled(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	env := map[string]string{
		"DEEPSEEK_API_KEY":    "sk-test-deepseek-1234",
		"EXA_API_KEY":         "exa-test-key-1234",
		"TINYFISH_API_KEY":    "sk-test-tinyfish-1234",
		"TENCENT_WSA_API_KEY": "sk-test-wsa-1234",
		"TAVILY_API_KEY":      "tvly-test-key-1234",
		"BRAVE_API_KEY":       "BSA-test-brave-1234567890",
	}
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("LoadWithEnv: %v", err)
	}
	application, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatalf("NewWithStderr: %v", err)
	}

	wantTimeout := map[string]string{
		spec.ProviderDeepSeek:   "10s/25s",
		spec.ProviderExa:        "10s/25s",
		spec.ProviderTinyFish:   "10s/25s",
		spec.ProviderTencentWSA: "10s",
		spec.ProviderTavily:     "10s/25s",
		spec.ProviderBrave:      "10s",
		spec.ProviderGitHub:     "10s",
		spec.ProviderLocal:      "10s",
	}
	wantDetails := map[string]string{
		spec.ProviderDeepSeek:   "concurrency=4",
		spec.ProviderExa:        "type=auto",
		spec.ProviderTinyFish:   "concurrency=4",
		spec.ProviderTencentWSA: "edition=standard",
		spec.ProviderTavily:     "depth=basic/basic",
		spec.ProviderBrave:      "mode=auto",
		spec.ProviderGitHub:     "concurrency=4",
		spec.ProviderLocal:      "min_runes=300, max_bytes=5MB",
	}
	for _, st := range application.ProviderStatuses() {
		if !st.Enabled {
			t.Errorf("expected provider %q enabled", st.Name)
		}
		if st.Timeout != wantTimeout[st.Name] {
			t.Errorf("provider %q Timeout = %q, want %q", st.Name, st.Timeout, wantTimeout[st.Name])
		}
		if st.Details != wantDetails[st.Name] {
			t.Errorf("provider %q Details = %q, want %q", st.Name, st.Details, wantDetails[st.Name])
		}
	}

	timeoutCases := []struct {
		search time.Duration
		fetch  time.Duration
		want   string
	}{
		{10 * time.Second, 25 * time.Second, "10s/25s"},
		{10 * time.Second, 10 * time.Second, "10s"},
		{10 * time.Second, 0, "10s"},
		{0, 25 * time.Second, "25s"},
		{0, 0, "-"},
	}
	for _, tc := range timeoutCases {
		if got := formatProviderTimeout(tc.search, tc.fetch); got != tc.want {
			t.Errorf("formatProviderTimeout(%v, %v) = %q, want %q", tc.search, tc.fetch, got, tc.want)
		}
	}
}

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
			env, sErr := application.SearchMCP(context.Background(), &core.SearchRequest{Query: "golang", Count: 3})
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
	cachedEnv, err := application.SearchMCP(context.Background(), &core.SearchRequest{Query: "golang", Count: 3})
	if err != nil || !cachedEnv.Meta.Cached || len(cachedEnv.Meta.Cost) != 0 || searchHits.Load() != 1 {
		t.Fatalf("expected cache hit with zero cost, got cached=%v cost=%v hits=%d", cachedEnv.Meta.Cached, cachedEnv.Meta.Cost, searchHits.Load())
	}
	cachedEnv.Results[0].Title = "MutatedByCaller"
	cachedEnv2, err := application.SearchMCP(context.Background(), &core.SearchRequest{Query: "golang", Count: 3})
	if err != nil || cachedEnv2.Results[0].Title != "Go" {
		t.Fatalf("expected deep-copied cached envelope, got title=%q", cachedEnv2.Results[0].Title)
	}

	// 验证单个短超时调用者取消时不会中断同键 flightGroup 的其他并发调用者
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancelShort()
	var shortErr, fullErr error
	var fullEnv *core.Envelope
	var cwg sync.WaitGroup
	cwg.Go(func() {
		_, shortErr = application.SearchMCP(shortCtx, &core.SearchRequest{Query: "isolated-cancel", Count: 3})
	})
	cwg.Go(func() {
		fullEnv, fullErr = application.SearchMCP(context.Background(), &core.SearchRequest{Query: "isolated-cancel", Count: 3})
	})
	cwg.Wait()
	if shortErr == nil || fullErr != nil || fullEnv == nil || !fullEnv.OK {
		t.Fatalf("expected short caller canceled while full caller succeeded: shortErr=%v fullErr=%v fullEnv=%+v", shortErr, fullErr, fullEnv)
	}

	// 2. 验证 FetchMCP 分页续读直接命中完整正文缓存，不发起第二次请求
	p1, err := application.FetchMCP(context.Background(), &core.FetchRequest{
		URL:      "https://example.com/article",
		Strategy: core.FetchAPIOnly,
		MaxChars: 10,
	})
	if err != nil || p1.Page.NextOffset == nil {
		t.Fatalf("expected paginated first chunk, got %+v err=%v", p1, err)
	}
	p2, err := application.FetchMCP(context.Background(), &core.FetchRequest{
		URL:      "https://example.com/article",
		Strategy: core.FetchAPIOnly,
		Offset:   *p1.Page.NextOffset,
		MaxChars: 100,
	})
	if err != nil || !p2.Meta.Cached || fetchHits.Load() != 1 {
		t.Fatalf("expected paginated continuation from cache with 1 upstream hit, got cached=%v hits=%d err=%v", p2.Meta.Cached, fetchHits.Load(), err)
	}

	// 验证 fresh=true && Offset==0 绕过旧缓存重新拉取并刷新缓存，且后续分页（含携带 fresh=true）直接命中新缓存不重复请求
	pFresh1, err := application.FetchMCP(context.Background(), &core.FetchRequest{
		URL:      "https://example.com/article",
		Strategy: core.FetchAPIOnly,
		MaxChars: 10,
		Options:  map[string]string{spec.OptFetchFresh: "true"},
	})
	if err != nil || pFresh1.Meta.Cached || fetchHits.Load() != 2 || pFresh1.Page.NextOffset == nil {
		t.Fatalf("expected fresh=true at offset=0 to bypass cache (hits=2), got cached=%v hits=%d err=%v", pFresh1.Meta.Cached, fetchHits.Load(), err)
	}
	pFresh2, err := application.FetchMCP(context.Background(), &core.FetchRequest{
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

func TestFetchMCPUnboundedCacheScrubAndFlightRecovery(t *testing.T) {
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

	chunk1, err := application.FetchMCP(context.Background(), &core.FetchRequest{
		URL:      "https://example.com/long",
		Strategy: core.FetchAPIOnly,
		MaxChars: 25,
	})
	if err != nil || chunk1.Page == nil || chunk1.Page.NextOffset == nil {
		t.Fatalf("expected NextOffset even when config fetch.max_chars=20, got %+v err=%v", chunk1, err)
	}
	chunk2, err := application.FetchMCP(context.Background(), &core.FetchRequest{
		URL:      "https://example.com/long",
		Strategy: core.FetchAPIOnly,
		Offset:   *chunk1.Page.NextOffset,
		MaxChars: 200,
	})
	if err != nil || !strings.Contains(chunk2.Page.Content, "exceeds CLI fetch.max_chars") {
		t.Fatalf("expected full remainder from cache, got %+v err=%v", chunk2, err)
	}

	// 验证 scrubError 同时脱敏 Params 与空 Message 下的 Cause
	rawErr := core.WrapError(core.CodeAuthFailed, "", errors.New("rejected key sk-secret99887766")).
		WithParam("detail", "token=sk-secret99887766")
	scrubbed := core.AsError(application.scrubError(rawErr))
	if strings.Contains(scrubbed.Message, "99887766") ||
		strings.Contains(scrubbed.Params["detail"], "99887766") ||
		strings.Contains(scrubbed.Cause.Error(), "99887766") {
		t.Fatalf("scrubError leaked secret across Message/Params/Cause: %+v", scrubbed)
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

	// 验证 scrubEnvelopeSecrets 同时脱敏信封 Query、Page.URL、Meta.Notes 与 Providers[].Warnings
	leakEnv := &core.Envelope{
		Query: "https://u:pw@example.com/a?token=sk-secret99887766",
		Page:  &core.Page{URL: "https://example.com/a?api_key=sk-secret99887766"},
		Meta: core.Meta{
			Notes: []string{"upstream note leaked sk-secret99887766 via socks5://u:pw@127.0.0.1:1080"},
			Providers: []core.ProviderReport{{
				Name:     spec.ProviderDeepSeek,
				Warnings: []string{"warn Authorization: Basic dXNlcjpwYXNzMTIz and sk-secret99887766"},
			}},
		},
	}
	application.scrubEnvelopeSecrets(leakEnv)
	if strings.Contains(leakEnv.Query, "99887766") || strings.Contains(leakEnv.Query, "u:pw") || strings.Contains(leakEnv.Page.URL, "99887766") {
		t.Fatalf("scrubEnvelopeSecrets leaked URL credentials: %+v %+v", leakEnv, leakEnv.Page)
	}
	if strings.Contains(leakEnv.Meta.Notes[0], "99887766") || strings.Contains(leakEnv.Meta.Notes[0], "u:pw") {
		t.Fatalf("scrubEnvelopeSecrets leaked Meta.Notes secret: %q", leakEnv.Meta.Notes[0])
	}
	if strings.Contains(leakEnv.Meta.Providers[0].Warnings[0], "99887766") || strings.Contains(leakEnv.Meta.Providers[0].Warnings[0], "dXNlcjpwYXNzMTIz") {
		t.Fatalf("scrubEnvelopeSecrets leaked provider warning secret: %q", leakEnv.Meta.Providers[0].Warnings[0])
	}

	// 验证 providers.<name>.enabled = false 在已有环境变量密钥时仍可停用该 Provider
	loaded.Config.Providers.DeepSeek.Enabled = false
	loaded.Config.Providers.Local.Enabled = false
	appDisabled, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatalf("NewWithStderr disabled providers: %v", err)
	}
	if len(appDisabled.EnabledSearchers()) != 0 {
		t.Fatalf("expected 0 searchers when deepseek.enabled=false, got %v", appDisabled.EnabledSearchers())
	}
	if slices.Contains(appDisabled.EnabledFetchers(), spec.ProviderLocal) || slices.Contains(appDisabled.EnabledFetchers(), spec.ProviderDeepSeek) {
		t.Fatalf("expected local and deepseek disabled in EnabledFetchers, got %v", appDisabled.EnabledFetchers())
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
