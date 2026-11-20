package mcpserver

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"veri/clikit/buildinfo"
	"veri/internal/app"
	"veri/internal/core"
	"veri/internal/render"
	"veri/internal/spec"
)

// New 构造并注册 web_search 与 web_fetch 工具的 MCP Server 实例。
func New(application *app.App, version string) *mcp.Server {
	if strings.TrimSpace(version) == "" {
		version = buildinfo.DefaultDevVersion
	}
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    spec.AppName,
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: serverInstructions,
		Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{},
		},
	})

	cfg := application.Config().Config
	annotations := &mcp.ToolAnnotations{
		ReadOnlyHint:    true,
		DestructiveHint: new(false),
		IdempotentHint:  true,
		OpenWorldHint:   new(true),
	}

	searchDef := searchSchemaDefaults{
		defaultCount:    cfg.Search.Count,
		maxCount:        cfg.MCP.MaxCount,
		defaultDetail:   cfg.Search.Detail,
		defaultStrategy: cfg.Search.Strategy,
		zhihuScope:      cfg.Providers.Zhihu.Scope,
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        spec.ToolWebSearch,
		Title:       toolWebSearchTitle,
		Description: webSearchDescription,
		Annotations: annotations,
		InputSchema: buildSearchInputSchema(searchDef, application.EnabledSearchers()),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in SearchInput) (*mcp.CallToolResult, core.Envelope, error) {
		return handleWebSearch(ctx, application, cfg.MCP.TextFormat, cfg.Search.Count, cfg.MCP.MaxCount, in)
	})

	fetchDef := fetchSchemaDefaults{
		defaultMaxChars: cfg.MCP.FetchMaxChars,
		defaultStrategy: cfg.Fetch.Strategy,
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        spec.ToolWebFetch,
		Title:       toolWebFetchTitle,
		Description: webFetchDescription,
		Annotations: annotations,
		InputSchema: buildFetchInputSchema(fetchDef, application.EnabledFetchers()),
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in FetchInput) (*mcp.CallToolResult, core.Envelope, error) {
		return handleWebFetch(ctx, application, cfg.MCP.TextFormat, in)
	})

	return srv
}

type nopWriteCloser struct {
	io.Writer
}

func (nopWriteCloser) Close() error { return nil }

// RunStdio 在标准输入输出传输上运行 MCP Server（标准输出仅承载 JSON-RPC）。
// 通过进程内管道桥接 os.Stdin，确保收到 SIGINT / SIGTERM 取消信号时在三端均可立即解除阻塞退出。
func RunStdio(ctx context.Context, application *app.App, version string) error {
	return RunIO(ctx, application, version, os.Stdin, os.Stdout)
}

// RunIO 在指定输入输出流上运行 MCP Server，支持取消信号即时关闭桥接管道并中断在途工具调用。
func RunIO(ctx context.Context, application *app.App, version string, in io.Reader, out io.Writer) error {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}

	pr, pw := io.Pipe()
	defer func() {
		_ = pr.Close()
		_ = pw.Close()
	}()

	stopOnCancel := context.AfterFunc(ctx, func() {
		_ = pr.CloseWithError(ctx.Err())
		_ = pw.CloseWithError(ctx.Err())
	})
	defer stopOnCancel()

	go func() {
		_, err := io.Copy(pw, in)
		_ = pw.CloseWithError(err)
	}()

	srv := New(application, version)
	err := srv.Run(ctx, &mcp.IOTransport{
		Reader:        pr,
		Writer:        nopWriteCloser{Writer: out},
		MaxLineLength: maxInboundJSONRPCLineBytes,
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func handleWebSearch(ctx context.Context, application *app.App, textFormat string, cfgCount, maxCount int, in SearchInput) (res *mcp.CallToolResult, outEnv core.Envelope, retErr error) {
	defer func() {
		if r := recover(); r != nil {
			res, outEnv, retErr = buildRecoveredToolError(application, spec.KindSearch, in.Query, textFormat, r)
		}
	}()

	fresh, fErr := core.ParseFreshness(in.Freshness, time.Now())
	if fErr != nil {
		return buildValidationErrorResult(application, spec.KindSearch, in.Query, textFormat, core.AsError(fErr))
	}

	reqCount := in.Count
	if reqCount <= 0 {
		reqCount = cfgCount
	}
	count := spec.ClampSearchCount(reqCount, maxCount)
	providers := cleanMCPProviders(in.Providers)

	var strategy core.Strategy
	if in.Fusion != nil {
		if *in.Fusion {
			strategy = core.StrategyFusion
		} else {
			strategy = core.StrategyAuto
		}
	}
	var timeout time.Duration
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}

	req := &core.SearchRequest{
		Query:         in.Query,
		Question:      in.Question,
		Count:         count,
		Detail:        core.Detail(strings.TrimSpace(in.Detail)),
		Providers:     providers,
		Strategy:      strategy,
		StrictFilters: in.Strict,
		IncludeExtra:  in.IncludeExtra,
		Timeout:       timeout,
		Options:       in.Options,
		Filters: core.Filters{
			Sites:        in.Sites,
			ExcludeSites: in.ExcludeSites,
			Lang:         in.Lang,
			Freshness:    fresh,
		},
	}

	env, _ := application.SearchMCP(ctx, req)
	if env == nil {
		se := core.NewError(core.CodeInternal, "unexpected nil search envelope")
		env = &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     false,
			Kind:   spec.KindSearch,
			Query:  application.ScrubText(in.Query),
			Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
			Error:  se.ToErrorInfo(),
		}
	}

	text, err := render.FormatMCPSearchText(env, textFormat)
	if err != nil {
		return nil, core.Envelope{}, err
	}

	return buildToolCallResult(env, text)
}

func handleWebFetch(ctx context.Context, application *app.App, textFormat string, in FetchInput) (res *mcp.CallToolResult, outEnv core.Envelope, retErr error) {
	singleURL := strings.TrimSpace(in.URL)
	var batchURLs []string
	for _, u := range in.URLs {
		if trimmed := strings.TrimSpace(u); trimmed != "" {
			batchURLs = append(batchURLs, trimmed)
		}
	}
	primaryLabel := singleURL
	if primaryLabel == "" && len(batchURLs) > 0 {
		primaryLabel = batchURLs[0]
	}

	defer func() {
		if r := recover(); r != nil {
			res, outEnv, retErr = buildRecoveredToolError(application, spec.KindFetch, primaryLabel, textFormat, r)
		}
	}()

	if singleURL != "" && len(batchURLs) > 0 {
		se := core.NewError(core.CodeInvalidArgument, "cannot specify both url and urls").
			WithHint("Provide either `url` for a single page or `urls` for batch extraction.")
		return buildValidationErrorResult(application, spec.KindFetch, primaryLabel, textFormat, se)
	}
	if singleURL == "" && len(batchURLs) == 0 {
		se := core.NewError(core.CodeInvalidArgument, "either url or urls is required").
			WithHint("Provide a target `url` or non-empty `urls` array.")
		return buildValidationErrorResult(application, spec.KindFetch, primaryLabel, textFormat, se)
	}

	providers := cleanMCPProviders(in.Providers)
	var timeout time.Duration
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	var opts map[string]string
	if len(in.Options) > 0 || in.Fresh {
		opts = make(map[string]string, len(in.Options)+1)
		maps.Copy(opts, in.Options)
		if in.Fresh {
			opts[spec.OptFetchFresh] = "true"
		}
	}
	req := &core.FetchRequest{
		URL:       singleURL,
		Section:   strings.TrimSpace(in.Section),
		Match:     strings.TrimSpace(in.Match),
		Providers: providers,
		Strategy:  core.FetchStrategy(strings.TrimSpace(in.Strategy)),
		MaxChars:  in.MaxChars,
		Offset:    in.Offset,
		Timeout:   timeout,
		Options:   opts,
	}

	var env *core.Envelope
	if len(batchURLs) > 0 {
		env, _ = application.FetchBatchMCP(ctx, batchURLs, req)
	} else {
		env, _ = application.FetchMCP(ctx, req)
	}
	if env == nil {
		se := core.NewError(core.CodeInternal, "unexpected nil fetch envelope")
		env = &core.Envelope{
			Schema: spec.SchemaVersion,
			OK:     false,
			Kind:   spec.KindFetch,
			Query:  application.ScrubText(primaryLabel),
			Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
			Error:  se.ToErrorInfo(),
		}
	}

	text, err := render.FormatMCPFetchText(env, textFormat)
	if err != nil {
		return nil, core.Envelope{}, err
	}

	return buildToolCallResult(env, text)
}

func buildValidationErrorResult(application *app.App, kind, query, textFormat string, se *core.Error) (*mcp.CallToolResult, core.Envelope, error) {
	safeErr := application.ScrubCoreError(se)
	meta := core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}}
	if kind == spec.KindSearch {
		meta.Strategy = string(core.StrategyAuto)
	}
	env := core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     false,
		Kind:   kind,
		Query:  application.ScrubText(query),
		Meta:   meta,
		Error:  safeErr.ToErrorInfo(),
	}
	return renderToolErrorEnvelope(&env, textFormat)
}

func renderToolErrorEnvelope(env *core.Envelope, textFormat string) (*mcp.CallToolResult, core.Envelope, error) {
	var txt string
	if env.Kind == spec.KindFetch {
		txt, _ = render.FormatMCPFetchText(env, textFormat)
	} else {
		txt, _ = render.FormatMCPSearchText(env, textFormat)
	}
	return buildToolCallResult(env, txt)
}

func buildToolCallResult(env *core.Envelope, text string) (*mcp.CallToolResult, core.Envelope, error) {
	res := &mcp.CallToolResult{
		IsError: !env.OK,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
	// 当 IsError 为 true 时主动预设 StructuredContent，前瞻兼容 mcp-go-sdk v1.9.0+ 在错误结果时忽略返回值 Out 的行为。
	if !env.OK {
		res.StructuredContent = *env
	}
	return res, *env, nil
}

func cleanMCPProviders(items []string) []string {
	var providers []string
	for _, item := range items {
		if p := strings.TrimSpace(item); p != "" {
			providers = append(providers, p)
		}
	}
	return providers
}

func buildRecoveredToolError(application *app.App, kind, query, textFormat string, recovered any) (*mcp.CallToolResult, core.Envelope, error) {
	safeRecovered := application.ScrubText(fmt.Sprintf("%v", recovered))
	se := core.NewError(core.CodeInternal, fmt.Sprintf("MCP tool handler recovered from panic: %s", safeRecovered))
	env := core.Envelope{
		Schema: spec.SchemaVersion,
		OK:     false,
		Kind:   kind,
		Query:  application.ScrubText(query),
		Meta:   core.Meta{Providers: []core.ProviderReport{}, Cost: []core.Cost{}},
		Error:  se.ToErrorInfo(),
	}
	return renderToolErrorEnvelope(&env, textFormat)
}
