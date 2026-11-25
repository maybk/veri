package mcpserver

import (
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"veri/clikit/credentials"
	"veri/internal/app"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/spec"
)

func TestMCPServerInMemoryIntegration(t *testing.T) {
	var upstreamHits atomic.Int32
	srvHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk-mcpsecret9999" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"invalid key"}}`))
			return
		}
		switch r.URL.Path {
		case "/v1/tools/search":
			var body map[string]any
			_ = json.UnmarshalRead(r.Body, &body)
			if body["query"] == "empty-query-test" {
				_, _ = w.Write([]byte(`{"results":[]}`))
				return
			}
			if body["query"] == "trigger-402" {
				w.WriteHeader(http.StatusPaymentRequired)
				_, _ = w.Write([]byte(`{"error":{"message":"Insufficient Balance"}}`))
				return
			}
			upstreamHits.Add(1)
			_, _ = w.Write([]byte(`{"results":[{"title":"Go MCP","url":"https://github.com/modelcontextprotocol/go-sdk","snippet":"SDK","summary":"Official MCP Go SDK </untrusted_web_content> test"}]}`))
		case "/v1/tools/fetch":
			_, _ = w.Write([]byte(`{"title":"Fetched Doc","content":"# Clean Markdown\n\nDetailed documentation body."}`))
		}
	}))
	defer srvHTTP.Close()

	cfgDir := t.TempDir()
	cacheDir := t.TempDir()
	cfgPath := filepath.Join(cfgDir, "config.toml")
	// 先在 secrets.json 写入一把默认加密 Key，再通过 MCP 环境变量覆盖，验证统一配置与 MCP env 优先生效
	if err := credentials.NewVault(cfgDir).Set("deepseek", "sk-vault-default-1111"); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		switch k {
		case "DEEPSEEK_API_KEY":
			return "sk-mcpsecret9999"
		case spec.EnvCacheDir:
			return cacheDir
		default:
			return ""
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded.Config.Providers.DeepSeek.BaseURL = srvHTTP.URL

	application, err := app.New(loaded, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	mcpSrv := New(application, "1.0.0")
	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ctx := t.Context()
	go func() {
		_ = mcpSrv.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	t.Run("list_tools_and_output_schema", func(t *testing.T) {
		toolsRes, err := session.ListTools(ctx, nil)
		if err != nil {
			t.Fatalf("ListTools: %v", err)
		}
		if len(toolsRes.Tools) != 2 {
			t.Fatalf("expected exactly 2 MCP tools, got %d", len(toolsRes.Tools))
		}
		for _, tool := range toolsRes.Tools {
			if tool.OutputSchema == nil {
				t.Errorf("tool %s missing OutputSchema", tool.Name)
			}
		}
	})

	t.Run("web_search_sanitization_and_cache", func(t *testing.T) {
		searchArgs := map[string]any{
			"query":      "golang mcp",
			"count":      5,
			"providers":  []string{"deepseek"},
			"strict":     true,
			"timeout_ms": 5000,
			"options":    map[string]string{"exa.search_type": "auto"},
		}
		res1, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "web_search",
			Arguments: searchArgs,
		})
		if err != nil || res1.IsError {
			t.Fatalf("web_search failed: res=%+v err=%v", res1, err)
		}
		txt := res1.Content[0].(*mcp.TextContent).Text
		if !strings.Contains(txt, `<untrusted_web_content id="`) || !strings.Contains(txt, "[removed-tag]") {
			t.Fatalf("expected untrusted tag wrapping & breakout sanitization, got: %s", txt)
		}
		if res1.StructuredContent == nil {
			t.Fatal("expected non-nil StructuredContent")
		}

		resCached, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "web_search",
			Arguments: searchArgs,
		})
		if err != nil || resCached.IsError || upstreamHits.Load() != 1 {
			t.Fatalf("expected second identical web_search to hit in-process cache (hits=%d)", upstreamHits.Load())
		}
	})

	t.Run("web_search_empty_not_error", func(t *testing.T) {
		resEmpty, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "web_search",
			Arguments: map[string]any{"query": "empty-query-test"},
		})
		if err != nil || resEmpty.IsError {
			t.Fatalf("empty search result must NOT set IsError=true: res=%+v err=%v", resEmpty, err)
		}
	})

	t.Run("input_schema_no_defaults_and_dynamic_options", func(t *testing.T) {
		searchSchema := buildSearchInputSchema(searchSchemaDefaults{
			defaultCount:    3,
			maxCount:        20,
			defaultDetail:   string(core.DetailSnippet),
			defaultStrategy: string(core.StrategyAuto),
		}, []string{"deepseek", "zhihu_web", "zhihu"})
		sProps := searchSchema["properties"].(map[string]any)
		if _, hasSingle := sProps["provider"]; hasSingle {
			t.Fatal("web_search schema must not expose redundant singular provider field")
		}
		for k, rawProp := range sProps {
			if _, hasDef := rawProp.(map[string]any)["default"]; hasDef {
				t.Fatalf("web_search property %q must not define JSON Schema default (ApplyDefaults would override user config)", k)
			}
		}
		sOptDesc, _ := sProps["options"].(map[string]any)["description"].(string)
		for _, want := range []string{
			spec.OptZhihuWebSearchDB,
			spec.OptZhihuSort,
		} {
			if !strings.Contains(sOptDesc, want) {
				t.Fatalf("web_search options description missing enabled provider option %q: %q", want, sOptDesc)
			}
		}
		for _, unwanted := range []string{spec.OptKeenableMode, spec.OptLinkupDepth, spec.OptParallelMode, spec.OptFirecrawlCategory} {
			if strings.Contains(sOptDesc, unwanted) {
				t.Fatalf("web_search options description should omit disabled provider option %q: %q", unwanted, sOptDesc)
			}
		}

		customSchema := buildFetchInputSchema(fetchSchemaDefaults{
			defaultMaxChars: 30000,
			defaultStrategy: string(core.FetchLocalOnly),
		}, []string{"local"})
		fProps := customSchema["properties"].(map[string]any)
		if _, hasSingle := fProps["provider"]; hasSingle {
			t.Fatal("web_fetch schema must not expose redundant singular provider field")
		}
		for k, rawProp := range fProps {
			if _, hasDef := rawProp.(map[string]any)["default"]; hasDef {
				t.Fatalf("web_fetch property %q must not define JSON Schema default", k)
			}
		}
		stratDesc, _ := fProps["strategy"].(map[string]any)["description"].(string)
		if !strings.Contains(stratDesc, string(core.FetchLocalOnly)) {
			t.Fatalf("expected strategy description to mention configured default %q, got %q", core.FetchLocalOnly, stratDesc)
		}
		fOptDesc, _ := fProps["options"].(map[string]any)["description"].(string)
		if strings.Contains(fOptDesc, spec.OptFirecrawlProxy) || !strings.Contains(fOptDesc, "no extra keys required") {
			t.Fatalf("expected local-only web_fetch options description to omit disabled cloud options, got %q", fOptDesc)
		}
	})

	t.Run("web_fetch_single_batch_and_conflict", func(t *testing.T) {
		bothRes, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "web_fetch",
			Arguments: map[string]any{"url": "https://example.com/a", "urls": []string{"https://example.com/b"}},
		})
		if err != nil || !bothRes.IsError || !strings.Contains(bothRes.Content[0].(*mcp.TextContent).Text, "cannot specify both url and urls") {
			t.Fatalf("expected url+urls conflict error, got res=%+v err=%v", bothRes, err)
		}

		fetchRes, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "web_fetch",
			Arguments: map[string]any{"url": "https://example.com/doc"},
		})
		if err != nil || fetchRes.IsError {
			t.Fatalf("default api_first web_fetch failed: res=%+v err=%v", fetchRes, err)
		}

		batchRes, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "web_fetch",
			Arguments: map[string]any{"urls": []string{"https://example.com/doc", "https://example.com/doc2"}, "strategy": "api_only"},
		})
		batchTxt := batchRes.Content[0].(*mcp.TextContent).Text
		if err != nil || batchRes.IsError || !strings.Contains(batchTxt, "Fetched 2 pages (deepseek):") {
			t.Fatalf("batch web_fetch failed or duplicated provider label: res=%+v err=%v txt=%s", batchRes, err, batchTxt)
		}
	})

	t.Run("upstream_402_error_envelope_with_hint", func(t *testing.T) {
		resFail, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "web_search",
			Arguments: map[string]any{"query": "trigger-402"},
		})
		if err != nil || !resFail.IsError || resFail.StructuredContent == nil {
			t.Fatalf("expected IsError=true with StructuredContent on 402 failure, got res=%+v err=%v", resFail, err)
		}
		failTxt := resFail.Content[0].(*mcp.TextContent).Text
		if !strings.Contains(failTxt, string(core.CodeInsufficientBalance)) || !strings.Contains(failTxt, "Hint:") {
			t.Fatalf("expected error code and Hint in failure text, got: %s", failTxt)
		}
	})
}

func TestMCPRespectsConfigDefaultsWithoutSchemaOverride(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tools/search" {
			_, _ = w.Write([]byte(`{"results":[
				{"title":"R1","url":"https://a.example/1","snippet":"s1","summary":"long summary 1"},
				{"title":"R2","url":"https://a.example/2","snippet":"s2","summary":"long summary 2"},
				{"title":"R3","url":"https://a.example/3","snippet":"s3","summary":"long summary 3"},
				{"title":"R4","url":"https://a.example/4","snippet":"s4","summary":"long summary 4"},
				{"title":"R5","url":"https://a.example/5","snippet":"s5","summary":"long summary 5"}
			]}`))
		}
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfgBody := fmt.Sprintf(`
[search]
providers = ["deepseek"]
count = 3
detail = "snippet"

[providers.deepseek]
api_key = "sk-test-config-default"
base_url = %q
`, srv.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == "VERI_CACHE_DIR" {
			return cacheDir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := app.New(loaded, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	mcpSrv := New(application, "1.0.0")
	t1, t2 := mcp.NewInMemoryTransports()
	ctx := t.Context()
	go func() { _ = mcpSrv.Run(ctx, t1) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "cfg-test", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	t.Run("respects_config_count_and_detail", func(t *testing.T) {
		// 不传 count 和 detail 时，必须采用 config.toml 中的 count=3 与 detail=snippet，绝不被 Schema default 覆盖成 8/summary
		res, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "web_search",
			Arguments: map[string]any{"query": "test defaults"},
		})
		if err != nil || res.IsError {
			t.Fatalf("web_search failed: res=%+v err=%v", res, err)
		}
		txt := res.Content[0].(*mcp.TextContent).Text
		if !strings.Contains(txt, "3 results") || strings.Contains(txt, "long summary") || !strings.Contains(txt, "s1") {
			t.Fatalf("expected config count=3 and detail=snippet respected, got:\n%s", txt)
		}
	})

	t.Run("validation_errors_redact_credentials", func(t *testing.T) {
		resNoFusion, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "web_search",
			Arguments: map[string]any{
				"query":     "https://u:SearchEarlyPass99@example.com/a?token=tokLeak88",
				"fusion":    false,
				"extra":     true,
				"freshness": "invalid-range",
			},
		})
		if err != nil || !resNoFusion.IsError {
			t.Fatalf("expected invalid freshness error, got res=%+v err=%v", resNoFusion, err)
		}
		scRaw, _ := json.Marshal(resNoFusion.StructuredContent)
		if strings.Contains(string(scRaw), "SearchEarlyPass99") || strings.Contains(string(scRaw), "tokLeak88") {
			t.Fatalf("web_search validation error leaked credentials in StructuredContent: %s", scRaw)
		}

		resBothSecret, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name: "web_fetch",
			Arguments: map[string]any{
				"url":  "https://u:FetchEarlyPass77@example.com/a?api_key=keyLeak66",
				"urls": []string{"https://example.com/b"},
			},
		})
		if err != nil || !resBothSecret.IsError {
			t.Fatalf("expected url+urls conflict error, got res=%+v err=%v", resBothSecret, err)
		}
		scFetchRaw, _ := json.Marshal(resBothSecret.StructuredContent)
		if strings.Contains(string(scFetchRaw), "FetchEarlyPass77") || strings.Contains(string(scFetchRaw), "keyLeak66") {
			t.Fatalf("web_fetch validation error leaked credentials in StructuredContent: %s", scFetchRaw)
		}

		resEmptyFetch, err := session.CallTool(ctx, &mcp.CallToolParams{
			Name:      "web_fetch",
			Arguments: map[string]any{},
		})
		if err != nil || !resEmptyFetch.IsError || !strings.Contains(resEmptyFetch.Content[0].(*mcp.TextContent).Text, "either url or urls is required") {
			t.Fatalf("expected missing url/urls error, got res=%+v err=%v", resEmptyFetch, err)
		}
	})

	t.Run("panic_recovery_redacts_secrets", func(t *testing.T) {
		panicPayload := "boom sk-test-config-default socks5://u:p@127.0.0.1:1080 Authorization: Basic dXNlcjpwYXNzMTIz"
		recRes, recOut, recErr := buildFetchErrorResult(
			application,
			"https://u:PanicQueryPass55@example.org",
			spec.TextFormatMarkdown,
			recoveredCoreError(application, panicPayload),
		)
		recTxt := recRes.Content[0].(*mcp.TextContent).Text
		if recErr != nil || !recRes.IsError || recOut.OK || !strings.Contains(recTxt, "boom") {
			t.Fatalf("unexpected recovered fetch error result: res=%+v out=%+v err=%v", recRes, recOut, recErr)
		}
		for _, leaked := range []string{"PanicQueryPass55", "sk-test-config-default", "u:p@", "dXNlcjpwYXNzMTIz"} {
			if strings.Contains(recTxt, leaked) || strings.Contains(recOut.Query, leaked) || strings.Contains(recOut.Error.Message, leaked) {
				t.Fatalf("recovered fetch error leaked %q: txt=%s out=%+v", leaked, recTxt, recOut)
			}
		}

		recResS, _, _ := buildSearchErrorResult(nil, "q", spec.TextFormatMarkdown, recoveredCoreError(nil, "boom2"))
		if !recResS.IsError || !strings.Contains(recResS.Content[0].(*mcp.TextContent).Text, "boom2") {
			t.Fatalf("unexpected recovered search error result: %+v", recResS)
		}
	})

	t.Run("handler_provider_validation_and_role_guards", func(t *testing.T) {
		// 即使直接调用 handler 绕过 JSON Schema，空白项、非法或错角色 provider 仍返回 invalid_argument
		for _, badProvs := range [][]string{{"local"}, {"   "}} {
			badSearchRes, badSearchOut, err := handleWebSearch(ctx, application, spec.TextFormatMarkdown, 5, 20, SearchInput{
				Query:     "go",
				Providers: badProvs,
			})
			if err != nil || !badSearchRes.IsError || badSearchOut.Error == nil || badSearchOut.Error.Code != core.CodeInvalidArgument {
				t.Fatalf("expected invalid_argument for search providers %v, got res=%+v out=%+v err=%v", badProvs, badSearchRes, badSearchOut, err)
			}
		}

		badFetchRes, badFetchOut, err := handleWebFetch(ctx, application, spec.TextFormatMarkdown, FetchInput{
			URL:       "https://example.com",
			Providers: []string{"zhihu"},
		})
		if err != nil ||
			!badFetchRes.IsError ||
			badFetchOut.Error == nil ||
			badFetchOut.Error.Code != core.CodeInvalidArgument ||
			badFetchOut.Meta.Strategy != string(core.DefaultFetchStrategy) {
			t.Fatalf("expected invalid_argument with default fetch strategy for role-mismatched fetch provider, got res=%+v out=%+v err=%v", badFetchRes, badFetchOut, err)
		}

		// 若调用方绕过 Schema 传入超过 mcp.max_count 的 count，运行期裁顶并在 meta.notes 中给出透明提示
		cappedRes, cappedOut, err := handleWebSearch(ctx, application, spec.TextFormatMarkdown, 3, 2, SearchInput{
			Query: "test defaults",
			Count: 5,
		})
		if err != nil || cappedRes.IsError || !cappedOut.OK || len(cappedOut.Results) != 2 || !slices.Contains(cappedOut.Meta.Notes, "requested count 5 capped at 2") {
			t.Fatalf("expected runtime count cap note when bypassing schema, got res=%+v out=%+v err=%v", cappedRes, cappedOut, err)
		}
	})
}

func TestMCPFusionStrictBackfillAndQuotaRotation(t *testing.T) {
	var zhihuCalls, zhihuWebCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/tools/search":
			_, _ = w.Write([]byte(`{"results":[{"title":"DS Go","url":"https://go.dev/ds","summary":"ds summary"}]}`))
		case "/v1/search":
			_, _ = w.Write([]byte(`{"results":[{"title":"Keen Go","url":"https://go.dev/keen","snippet":"keen summary"}]}`))
		case "/api/v1/content/zhihu_search":
			zhihuCalls.Add(1)
			_, _ = w.Write([]byte(`{"Code":30002,"Message":"quota exceeded"}`))
		case "/api/v1/content/global_search":
			zhihuWebCalls.Add(1)
			_, _ = w.Write([]byte(`{"status":0,"data":{"items":[{"title":"ZhihuWeb Go","url":"https://go.dev/zhihu-web","content":"zhihu web summary"}]}}`))
		case "/SearchPro":
			_, _ = w.Write([]byte(`{"Response":{"RequestId":"wsa-1","Pages":["{\"title\":\"WSA Go\",\"url\":\"https://go.dev/wsa\",\"passage\":\"wsa summary\",\"site\":\"Go\"}"]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfgBody := fmt.Sprintf(`
[providers.deepseek]
api_key = "sk-ds-test"
base_url = %q

[providers.keenable]
api_key = "sk-keen-test"
base_url = %q

[providers.zhihu]
api_key = "sk-zhihu-test"
base_url = %q

[providers.zhihu_web]
base_url = %q

[providers.tencent_wsa]
api_key = "sk-wsa-test"
base_url = %q
`, srv.URL, srv.URL, srv.URL, srv.URL, srv.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == "VERI_CACHE_DIR" {
			return cacheDir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := app.New(loaded, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	// auto 全网优先、单站垫底；-F 按默认顺序取前 3
	wantOrder := []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA, spec.ProviderZhihuWeb, spec.ProviderZhihu}
	if got := application.EnabledSearchers(); !slices.Equal(got, wantOrder) {
		t.Fatalf("unexpected default EnabledSearchers order: %v", got)
	}
	wantFusion := []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderZhihu}
	if got := application.RunDoctor(t.Context(), "en-US", "default", false).FusionChain; !slices.Equal(got, wantFusion) {
		t.Fatalf("unexpected default FusionChain order: %v", got)
	}

	ctx := t.Context()

	t.Run("strict_sites_backfills_tencent_wsa", func(t *testing.T) {
		resStrict, outStrict, err := handleWebSearch(ctx, application, spec.TextFormatMarkdown, 5, 20, SearchInput{
			Query:  "Go 并发",
			Sites:  []string{"go.dev"},
			Fusion: new(true),
			Strict: true,
		})
		if err != nil || resStrict.IsError || !outStrict.OK || zhihuCalls.Load() != 0 || zhihuWebCalls.Load() != 0 {
			t.Fatalf("expected MCP strict fusion to backfill tencent_wsa without calling zhihu/zhihu_web, got res=%+v out=%+v zhihu=%d zhihuWeb=%d err=%v",
				resStrict, outStrict, zhihuCalls.Load(), zhihuWebCalls.Load(), err)
		}
		var strictProvs []string
		for _, p := range outStrict.Meta.Providers {
			strictProvs = append(strictProvs, p.Name)
		}
		if !slices.Equal(strictProvs, []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA}) {
			t.Fatalf("expected strict MCP fusion providers [deepseek keenable tencent_wsa], got %v", strictProvs)
		}
	})

	t.Run("nonstrict_sites_skips_zhihu_and_backfills_tencent_wsa", func(t *testing.T) {
		resSite, outSite, err := handleWebSearch(ctx, application, spec.TextFormatMarkdown, 5, 20, SearchInput{
			Query:  "Go 并发",
			Sites:  []string{"go.dev"},
			Fusion: new(true),
		})
		if err != nil || resSite.IsError || !outSite.OK || zhihuCalls.Load() != 0 || zhihuWebCalls.Load() != 0 {
			t.Fatalf("expected non-strict -s go.dev to skip zhihu and backfill tencent_wsa, got res=%+v out=%+v zhihu=%d zhihuWeb=%d err=%v",
				resSite, outSite, zhihuCalls.Load(), zhihuWebCalls.Load(), err)
		}
		var provs []string
		for _, p := range outSite.Meta.Providers {
			provs = append(provs, p.Name)
		}
		if !slices.Equal(provs, []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA}) {
			t.Fatalf("expected non-strict -s go.dev providers [deepseek keenable tencent_wsa], got %v", provs)
		}
	})

	t.Run("strict_lang_no_match_returns_hint", func(t *testing.T) {
		resNoMatch, outNoMatch, err := handleWebSearch(ctx, application, spec.TextFormatMarkdown, 5, 20, SearchInput{
			Query:  "Go 并发",
			Lang:   []string{"zh"},
			Fusion: new(true),
			Strict: true,
		})
		if err != nil ||
			!resNoMatch.IsError ||
			outNoMatch.Error == nil ||
			outNoMatch.Error.Code != core.CodeNoProvider ||
			!strings.Contains(outNoMatch.Error.Hint, "strict") {
			t.Fatalf("expected no_provider with strict hint in MCP, got res=%+v out=%+v err=%v", resNoMatch, outNoMatch, err)
		}
	})

	t.Run("quota_exhausted_provider_rotates_in_long_lived_mcp", func(t *testing.T) {
		resQ1, outQ1, err := handleWebSearch(ctx, application, spec.TextFormatMarkdown, 5, 20, SearchInput{
			Query:  "中文融合一",
			Fusion: new(true),
		})
		if err != nil || resQ1.IsError || !outQ1.OK || !outQ1.Meta.Partial || zhihuCalls.Load() != 1 {
			t.Fatalf("expected first MCP fusion to hit zhihu once with partial success, got out=%+v zhihuCalls=%d err=%v", outQ1, zhihuCalls.Load(), err)
		}
		resQ2, outQ2, err := handleWebSearch(ctx, application, spec.TextFormatMarkdown, 5, 20, SearchInput{
			Query:  "中文融合二",
			Fusion: new(true),
		})
		if err != nil || resQ2.IsError || !outQ2.OK || outQ2.Meta.Partial || zhihuCalls.Load() != 1 {
			t.Fatalf("expected second MCP fusion to rotate tencent_wsa without re-hitting quota-exhausted zhihu, got out=%+v zhihuCalls=%d err=%v",
				outQ2, zhihuCalls.Load(), err)
		}
		var rotatedProvs []string
		for _, p := range outQ2.Meta.Providers {
			rotatedProvs = append(rotatedProvs, p.Name)
		}
		if !slices.Equal(rotatedProvs, []string{spec.ProviderDeepSeek, spec.ProviderKeenable, spec.ProviderTencentWSA}) {
			t.Fatalf("expected rotated MCP fusion providers [deepseek keenable tencent_wsa], got %v", rotatedProvs)
		}
	})
}

func TestMCPSearchNoCacheBypass(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tools/search" {
			n := hits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"results":[{"title":"Rev %d","url":"https://example.com/%d","snippet":"s","summary":"sum %d"}]}`, n, n, n)
		}
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	cfgBody := fmt.Sprintf("[search]\nproviders = [\"deepseek\"]\n[providers.deepseek]\napi_key = \"sk-mcp-nocache-test\"\nbase_url = %q\n", srv.URL)
	if err := os.WriteFile(cfgPath, []byte(cfgBody), 0o600); err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == "VERI_CACHE_DIR" {
			return cacheDir
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := app.New(loaded, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	mcpSrv := New(application, "1.0.0")
	t1, t2 := mcp.NewInMemoryTransports()
	ctx := t.Context()
	go func() { _ = mcpSrv.Run(ctx, t1) }()

	session, err := mcp.NewClient(&mcp.Implementation{Name: "nocache-test", Version: "1.0"}, nil).Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	// 1. 首次搜索写入 5m 进程内缓存，第二次命中缓存不回源。
	r1, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: map[string]any{"query": "live breaking"}})
	if err != nil || r1.IsError || hits.Load() != 1 {
		t.Fatalf("first web_search failed: res=%+v hits=%d err=%v", r1, hits.Load(), err)
	}
	r2, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: map[string]any{"query": "live breaking"}})
	if err != nil || r2.IsError || hits.Load() != 1 {
		t.Fatalf("expected cached web_search hit: res=%+v hits=%d err=%v", r2, hits.Load(), err)
	}

	// 2. 传 no_cache=true 绕过读写缓存直接回源拿最新 Rev 2；后续常规请求仍读原缓存 Rev 1（不污染原槽位）。
	rLive, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": "live breaking", "no_cache": true},
	})
	if err != nil || rLive.IsError || hits.Load() != 2 {
		t.Fatalf("expected no_cache=true to bypass MCP search cache: res=%+v hits=%d err=%v", rLive, hits.Load(), err)
	}
	if txt := rLive.Content[0].(*mcp.TextContent).Text; !strings.Contains(txt, "Rev 2") {
		t.Fatalf("expected fresh Rev 2 from no_cache=true, got:\n%s", txt)
	}
	rAfter, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "web_search", Arguments: map[string]any{"query": "live breaking"}})
	if err != nil || rAfter.IsError || hits.Load() != 2 {
		t.Fatalf("expected cache entry preserved after no_cache call: hits=%d err=%v", hits.Load(), err)
	}
	if txt := rAfter.Content[0].(*mcp.TextContent).Text; !strings.Contains(txt, "Rev 1") {
		t.Fatalf("expected preserved Rev 1 from cache after no_cache call, got:\n%s", txt)
	}
}
