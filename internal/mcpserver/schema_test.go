package mcpserver

import (
	"context"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"veri/internal/app"
	"veri/internal/config"
	"veri/internal/core"
	"veri/internal/spec"
)

func TestMCPSchemaDynamicOptionsFiltering(t *testing.T) {
	allSearch := spec.ProvidersWithRole(spec.RoleSearch)
	fullSearchSchema := buildSearchInputSchema(searchSchemaDefaults{zhihuScope: spec.ZhihuScopeGlobal}, allSearch)
	fullSearchOpt := fullSearchSchema["properties"].(map[string]any)["options"].(map[string]any)["description"].(string)
	if !strings.Contains(fullSearchOpt, "omit by default") {
		t.Fatalf("expected search options description to state omit by default, got %q", fullSearchOpt)
	}
	fusionDesc := fullSearchSchema["properties"].(map[string]any)["fusion"].(map[string]any)["description"].(string)
	if !strings.Contains(fusionDesc, "breaking news") || !strings.Contains(fusionDesc, "open research") {
		t.Fatalf("expected fusion description to give clear usage guidance, got %q", fusionDesc)
	}
	if !strings.Contains(serverInstructions, "multi-source") || !strings.Contains(webSearchDescription, "breaking news") {
		t.Fatalf("expected MCP prompts to highlight multi-source RRF fusion and breaking news")
	}
	for _, want := range []string{
		spec.OptExaSearchType,
		spec.OptExaCategory,
		spec.OptTavilySearchDepth,
		spec.OptBraveMode,
		spec.OptTinyFishDomainType,
		spec.OptWSAIndustry,
		spec.OptKeenableMode,
		spec.OptZhihuScope,
		spec.OptLinkupDepth,
		spec.OptParallelMode,
		spec.OptParallelLocation,
		spec.OptFirecrawlCategory,
	} {
		if !strings.Contains(fullSearchOpt, want) {
			t.Fatalf("full search options description missing %q: %q", want, fullSearchOpt)
		}
	}

	emptySearchSchema := buildSearchInputSchema(searchSchemaDefaults{}, nil)
	emptySearchOpt := emptySearchSchema["properties"].(map[string]any)["options"].(map[string]any)["description"].(string)
	if !strings.Contains(emptySearchOpt, "configure a search provider") {
		t.Fatalf("unexpected empty search options description: %q", emptySearchOpt)
	}

	allFetch := spec.ProvidersWithRole(spec.RoleFetch)
	fullFetchSchema := buildFetchInputSchema(fetchSchemaDefaults{}, allFetch)
	fullFetchOpt := fullFetchSchema["properties"].(map[string]any)["options"].(map[string]any)["description"].(string)
	for _, want := range []string{
		spec.OptTavilyExtractDepth,
		spec.OptTinyFishTTL,
		spec.OptExaMaxAgeHours,
		spec.OptLinkupFetchMode,
		spec.OptLinkupRenderJS,
		spec.OptFirecrawlProxy,
		spec.OptFirecrawlMaxPages,
	} {
		if !strings.Contains(fullFetchOpt, want) {
			t.Fatalf("full fetch options description missing %q: %q", want, fullFetchOpt)
		}
	}

	partialFetchSchema := buildFetchInputSchema(fetchSchemaDefaults{}, []string{spec.ProviderExa, spec.ProviderLocal})
	partialFetchOpt := partialFetchSchema["properties"].(map[string]any)["options"].(map[string]any)["description"].(string)
	if !strings.Contains(partialFetchOpt, spec.OptExaMaxAgeHours) || strings.Contains(partialFetchOpt, spec.OptFirecrawlProxy) {
		t.Fatalf("partial fetch options description should include only enabled extractor options, got %q", partialFetchOpt)
	}

	emptyFetchSchema := buildFetchInputSchema(fetchSchemaDefaults{}, nil)
	emptyFetchOpt := emptyFetchSchema["properties"].(map[string]any)["options"].(map[string]any)["description"].(string)
	if !strings.Contains(emptyFetchOpt, "configure an extractor") {
		t.Fatalf("unexpected empty fetch options description: %q", emptyFetchOpt)
	}
}

func TestMCPFetchSectionTruncationHintIntegration(t *testing.T) {
	longSection := "# Guide\n\n## Intro\n\nShort intro.\n\n## Concurrency\n\n### Goroutines\n\n" +
		strings.Repeat("Goroutines share memory by communicating over channels. ", 40) + "\n\n### Channels\n\n" +
		strings.Repeat("Buffered channels synchronize producer and consumer goroutines. ", 40)

	srvHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tools/fetch" {
			payload, _ := json.Marshal(map[string]string{
				"title":   "Guide",
				"content": longSection,
			})
			_, _ = w.Write(payload)
		}
	}))
	defer srvHTTP.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	loaded, err := config.LoadWithEnv(cfgPath, func(k string) string {
		if k == "DEEPSEEK_API_KEY" {
			return "sk-section-test-1234"
		}
		return ""
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
	go func() { _ = mcpSrv.Run(ctx, serverTransport) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "section-test", Version: "1.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "web_fetch",
		Arguments: map[string]any{
			"url":       "https://example.com/guide",
			"section":   "Concurrency",
			"max_chars": 1000,
		},
	})
	if err != nil || res.IsError {
		t.Fatalf("web_fetch section failed: res=%+v err=%v", res, err)
	}
	txt := res.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(txt, `Call web_fetch with section="Concurrency", offset=`) {
		t.Fatalf("expected MCP section truncation hint to preserve section argument, got:\n%s", txt)
	}
	if !strings.Contains(txt, "### Goroutines (") {
		t.Fatalf("expected MCP section outline to include char count, got:\n%s", txt)
	}
}

func TestMCPOutputSchemaCompactIsolationAndSync(t *testing.T) {
	searchSchema := buildSearchOutputSchema()
	fetchSchema := buildFetchOutputSchema()

	searchBytes, err := json.Marshal(searchSchema)
	if err != nil {
		t.Fatalf("marshal search outputSchema: %v", err)
	}
	fetchBytes, err := json.Marshal(fetchSchema)
	if err != nil {
		t.Fatalf("marshal fetch outputSchema: %v", err)
	}
	if len(searchBytes) > 3000 || len(fetchBytes) > 3500 {
		t.Fatalf("outputSchema regressed in size: search=%dB fetch=%dB", len(searchBytes), len(fetchBytes))
	}

	searchProps := searchSchema["properties"].(map[string]any)
	if _, leaked := searchProps["page"]; leaked {
		t.Fatal("web_search outputSchema must not expose page")
	}
	if _, leaked := searchProps["pages"]; leaked {
		t.Fatal("web_search outputSchema must not expose pages")
	}
	fetchProps := fetchSchema["properties"].(map[string]any)
	if _, leaked := fetchProps["results"]; leaked {
		t.Fatal("web_fetch outputSchema must not expose results")
	}
	if ref := fetchProps["page"].(map[string]any)["$ref"]; ref != "#/$defs/Page" {
		t.Fatalf("web_fetch page must reuse #/$defs/Page, got %v", ref)
	}
	if ref := fetchProps["pages"].(map[string]any)["items"].(map[string]any)["$ref"]; ref != "#/$defs/Page" {
		t.Fatalf("web_fetch pages.items must reuse #/$defs/Page, got %v", ref)
	}

	// 反射交叉校验：结构体 JSON 字段集合必须与 OutputSchema properties 100% 同步，防止日后新增字段漂移。
	assertStructPropsSync := func(label string, typ reflect.Type, props map[string]any) {
		t.Helper()
		var want, got []string
		for field := range typ.Fields() {
			name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
			if name != "" && name != "-" {
				want = append(want, name)
			}
		}
		for k := range props {
			got = append(got, k)
		}
		slices.Sort(want)
		slices.Sort(got)
		if !slices.Equal(want, got) {
			t.Fatalf("%s properties out of sync with struct JSON tags: schema=%v struct=%v", label, got, want)
		}
	}
	searchDefs := searchSchema["$defs"].(map[string]any)
	fetchDefs := fetchSchema["$defs"].(map[string]any)
	assertStructPropsSync("SearchOutput", reflect.TypeFor[SearchOutput](), searchProps)
	assertStructPropsSync("FetchOutput", reflect.TypeFor[FetchOutput](), fetchProps)
	assertStructPropsSync("Result", reflect.TypeFor[core.Result](), searchDefs["Result"].(map[string]any)["properties"].(map[string]any))
	assertStructPropsSync("Page", reflect.TypeFor[core.Page](), fetchDefs["Page"].(map[string]any)["properties"].(map[string]any))
	assertStructPropsSync("PageSection", reflect.TypeFor[core.PageSection](), fetchDefs["PageSection"].(map[string]any)["properties"].(map[string]any))
	assertStructPropsSync("Meta", reflect.TypeFor[core.Meta](), searchDefs["Meta"].(map[string]any)["properties"].(map[string]any))
	assertStructPropsSync("ProviderReport", reflect.TypeFor[core.ProviderReport](), searchDefs["ProviderReport"].(map[string]any)["properties"].(map[string]any))
	assertStructPropsSync("Cost", reflect.TypeFor[core.Cost](), searchDefs["Cost"].(map[string]any)["properties"].(map[string]any))
	assertStructPropsSync("ErrorInfo", reflect.TypeFor[core.ErrorInfo](), searchDefs["ErrorInfo"].(map[string]any)["properties"].(map[string]any))

	// 通过官方 MCP SDK 真校验所有边界形态（富字段、零命中、单页、批量含局部错误、失败态）。
	now := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	rel, auth, nextOff := 0.95, 0.9, 1200
	var activeSearchOut SearchOutput
	var activeFetchOut FetchOutput

	srv := mcp.NewServer(&mcp.Implementation{Name: "schema-verify", Version: "1.0"}, nil)
	type emptyIn struct{}
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "test_search",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: searchSchema,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, SearchOutput, error) {
		return buildToolCallResult(activeSearchOut.OK, activeSearchOut, "ok")
	})
	mcp.AddTool(srv, &mcp.Tool{
		Name:         "test_fetch",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: fetchSchema,
	}, func(_ context.Context, _ *mcp.CallToolRequest, _ emptyIn) (*mcp.CallToolResult, FetchOutput, error) {
		return buildToolCallResult(activeFetchOut.OK, activeFetchOut, "ok")
	})

	ct, st := mcp.NewInMemoryTransports()
	ctx := t.Context()
	go func() { _ = srv.Run(ctx, st) }()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "c", Version: "1.0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	richMeta := core.Meta{
		Strategy: "fusion",
		Providers: []core.ProviderReport{{
			Name:      "exa",
			Status:    core.ProviderStatusOK,
			Results:   1,
			LatencyMS: 120,
			Cost:      &core.Cost{Currency: core.CurrencyUSD, Micros: 4000, Estimated: false},
			Degraded:  []string{"lang"},
			Warnings:  []string{"trimmed"},
			RequestID: "req-1",
		}},
		Notes:      []string{"emulated lang"},
		Cost:       []core.Cost{{Currency: core.CurrencyUSD, Micros: 4000, Estimated: false}},
		DurationMS: 125,
	}

	// 1) 富字段搜索结果 + 零命中空结果
	for _, tc := range []SearchOutput{
		toSearchOutput(&core.Envelope{
			Schema: spec.SchemaVersion, OK: true, Kind: spec.KindSearch, Query: "golang",
			Results: []core.Result{{
				Rank: 1, Title: "Go", URL: "https://go.dev", Site: "go.dev", Authors: []string{"Go Team"},
				Icon: "https://go.dev/favicon.ico", Published: &now, Snippet: "s", Summary: "sum", Content: "body",
				Relevance: &rel, Authority: &auth, Providers: []string{"exa"}, Extra: map[string]any{"score": 1},
			}},
			Meta: richMeta,
		}),
		toSearchOutput(&core.Envelope{
			Schema: spec.SchemaVersion, OK: true, Kind: spec.KindSearch, Query: "none",
			Results: nil, Meta: core.Meta{Strategy: "auto"},
		}),
	} {
		activeSearchOut = tc
		res, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "test_search", Arguments: map[string]any{}})
		if callErr != nil || res.IsError || res.StructuredContent == nil {
			t.Fatalf("search output failed SDK schema validation: res=%+v err=%v", res, callErr)
		}
	}

	// 2) 单页（含 outline/next_offset/published）+ 批量多页（含局部 error）
	for _, tc := range []FetchOutput{
		toFetchOutput(&core.Envelope{
			Schema: spec.SchemaVersion, OK: true, Kind: spec.KindFetch, Query: "https://go.dev",
			Page: &core.Page{
				URL: "https://go.dev", FinalURL: "https://go.dev/", Title: "Go", Author: "Go Team",
				Site: "go.dev", Published: &now, Content: "# Go", TotalChars: 4000, NextOffset: &nextOff,
				Outline:    []core.PageSection{{Heading: "Go", Level: 1, Offset: 0, Chars: 4000}},
				Provider:   "exa",
				SourceType: core.SourceGeneric,
			},
			Meta: richMeta,
		}),
		toFetchOutput(&core.Envelope{
			Schema: spec.SchemaVersion, OK: true, Kind: spec.KindFetch, Query: "https://go.dev",
			Pages: []core.Page{
				{URL: "https://go.dev", Title: "Go", Content: "ok", TotalChars: 2, Provider: "exa"},
				{URL: "http://127.0.0.1", Title: "", Content: "", TotalChars: 0, Error: &core.ErrorInfo{Code: core.CodeContentUnavailable, Message: "blocked", Retryable: false}},
			},
			Meta: core.Meta{Strategy: "api_first", Partial: true},
		}),
	} {
		activeFetchOut = tc
		res, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: "test_fetch", Arguments: map[string]any{}})
		if callErr != nil || res.IsError || res.StructuredContent == nil {
			t.Fatalf("fetch output failed SDK schema validation: res=%+v err=%v", res, callErr)
		}
	}
}
