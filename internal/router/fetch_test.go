package router

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"veri/internal/core"
	"veri/internal/spec"
	"veri/internal/textnorm"
)

type spyFetcher struct {
	name    string
	profile core.FetchProfile
	calls   atomic.Int32
	lastReq *core.FetchRequest
	fn      func(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error)
}

func (s *spyFetcher) Name() string                    { return s.name }
func (s *spyFetcher) Capabilities() core.Capabilities { return core.Capabilities{} }
func (s *spyFetcher) FetchProfile() core.FetchProfile { return s.profile }
func (s *spyFetcher) Fetch(ctx context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
	s.calls.Add(1)
	cp := *req
	s.lastReq = &cp
	return s.fn(ctx, req)
}

func TestFetchLocalPinnedAndSplitDNSNeverLeakToCloud(t *testing.T) {
	loc := &spyFetcher{
		name: spec.ProviderLocal,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			return &core.FetchOutcome{
				Page:       &core.Page{URL: req.URL, Title: "Dev", Content: "tiny ok", Provider: spec.ProviderLocal, SourceType: core.SourceGeneric},
				LowQuality: true,
				QualityMsg: textnorm.QualityReasonTooShort,
			}, nil
		},
	}
	cloud := &spyFetcher{
		name: spec.ProviderTinyFish,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			return &core.FetchOutcome{
				Page: &core.Page{URL: req.URL, Content: "cloud body", Provider: spec.ProviderTinyFish},
			}, nil
		},
	}

	r := New(Options{FetchStrategy: core.FetchAPIFirst}, []core.Provider{loc, cloud})

	// 1. 即使全局策略为 api_first 且本地页面极短，localhost / 私网地址也必须钉死 local，绝不外发云端
	env, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "http://localhost:3000/api-doc"})
	if err != nil || !env.OK || env.Page.Provider != spec.ProviderLocal {
		t.Fatalf("expected localhost to succeed on pinned local, got env=%+v err=%v", env, err)
	}
	if cloud.calls.Load() != 0 {
		t.Fatalf("expected 0 cloud calls for localhost, got %d", cloud.calls.Load())
	}

	// 2. 显式要求云端提取私网地址（api_only 或 -p tinyfish）必须被安全策略直接拦截，0 外发
	if _, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "http://192.168.1.20/wiki", Strategy: core.FetchAPIOnly}); core.AsError(err).Code != core.CodeBlockedByPolicy {
		t.Fatalf("expected api_only on private IP -> blocked_by_policy, got %v", err)
	}
	if _, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "http://10.0.0.8/secret", Providers: []string{"tinyfish"}}); core.AsError(err).Code != core.CodeBlockedByPolicy {
		t.Fatalf("expected -p tinyfish on private IP -> blocked_by_policy, got %v", err)
	}
	if cloud.calls.Load() != 0 {
		t.Fatalf("expected 0 cloud calls after blocked private attempts, got %d", cloud.calls.Load())
	}

	// 3. 公网后缀内部域名经 Split-DNS 解析至私网被 SSRF 拦截时，必须立即停止，严禁向公网云端回退
	ssrfLoc := &spyFetcher{
		name: spec.ProviderLocal,
		fn: func(_ context.Context, _ *core.FetchRequest) (*core.FetchOutcome, error) {
			return nil, core.NewError(core.CodeContentUnavailable, "blocked connection to non-public address").
				WithProvider(spec.ProviderLocal).
				WithHintKey(core.HintKeySSRFPrivateBlocked)
		},
	}
	rSSRF := New(Options{FetchStrategy: core.FetchSmartLocal}, []core.Provider{ssrfLoc, cloud})
	if _, err := rSSRF.Fetch(context.Background(), &core.FetchRequest{URL: "https://intranet.company.com/roadmap"}); err == nil {
		t.Fatal("expected Split-DNS SSRF block error")
	}
	if cloud.calls.Load() != 0 {
		t.Fatalf("Split-DNS SSRF block leaked internal URL to cloud (%d calls)", cloud.calls.Load())
	}
}

func TestFetchCloudWallRejectionPDFFastPathAndSmartOrdering(t *testing.T) {
	var order []string
	loc := &spyFetcher{
		name: spec.ProviderLocal,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "local")
			if req.URL == "https://portal.cn/download?id=1" {
				return nil, core.NewError(core.CodeContentUnavailable, `unsupported media type "application/pdf"`).WithProvider(spec.ProviderLocal)
			}
			return &core.FetchOutcome{
				Page:       &core.Page{URL: req.URL, Title: "Stub", Content: "short", Provider: spec.ProviderLocal},
				LowQuality: true,
				QualityMsg: textnorm.QualityReasonTooShort,
			}, nil
		},
	}
	kn := &spyFetcher{
		name: spec.ProviderKeenable,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "keenable")
			// 模拟静态云端源在目标站抓回 HTTP 200 的 Cloudflare 盾页
			return &core.FetchOutcome{
				Page: &core.Page{
					URL:      req.URL,
					Title:    "Just a moment...",
					Content:  "Performing security verification. Enable JavaScript and cookies to continue.",
					Provider: spec.ProviderKeenable,
				},
			}, nil
		},
	}
	ex := &spyFetcher{
		name:    spec.ProviderExa,
		profile: core.FetchProfile{DynamicRender: true},
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "exa")
			if req.URL == "https://juejin.cn/post/cf" {
				return nil, core.NewError(core.CodeUpstreamUnavailable, "busy").WithProvider(spec.ProviderExa)
			}
			return &core.FetchOutcome{
				Page: &core.Page{
					URL:      req.URL,
					Title:    "Real Guide",
					Content:  "# Real Guide\n\nFull rendered dynamic content without challenge wall.",
					Provider: spec.ProviderExa,
				},
			}, nil
		},
	}
	tf := &spyFetcher{
		name:    spec.ProviderTinyFish,
		profile: core.FetchProfile{PDF: true, DynamicRender: true},
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "tinyfish")
			return &core.FetchOutcome{
				Page: &core.Page{
					URL:      req.URL,
					Title:    "Rendered",
					Content:  "# Rendered\n\nClean content extracted via TinyFish.",
					Provider: spec.ProviderTinyFish,
				},
			}, nil
		},
	}

	r := New(Options{FetchStrategy: core.FetchSmartLocal}, []core.Provider{loc, kn, ex, tf})

	// 1. 中文域名常规短页升档：默认中文链 keenable -> exa -> tinyfish；当 keenable 返回盾页时不得假成功，应拦截盾页并继续回退至 exa -> tinyfish（同时置 DynamicRenderHint=true）
	order = nil
	cnEnv, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://juejin.cn/post/cf"})
	if err != nil || !cnEnv.OK || cnEnv.Page.Provider != spec.ProviderTinyFish {
		t.Fatalf("expected cloud wall on keenable to fall through to tinyfish, got env=%+v err=%v", cnEnv, err)
	}
	wantCNOrder := []string{"local", "keenable", "exa", "tinyfish"}
	if len(order) != len(wantCNOrder) {
		t.Fatalf("expected call order %v, got %v", wantCNOrder, order)
	}
	for i := range wantCNOrder {
		if order[i] != wantCNOrder[i] {
			t.Fatalf("call[%d] = %s, want %s (full=%v)", i, order[i], wantCNOrder[i], order)
		}
	}
	if tf.lastReq == nil || !tf.lastReq.DynamicRenderHint {
		t.Fatal("expected DynamicRenderHint=true propagated to TinyFish after wall detection")
	}

	// 2. 海外站点默认全球链为 keenable -> exa -> tinyfish；keenable 遇盾页自动拦截并升档至 exa
	order = nil
	ovEnv, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://docs.example.org/spa"})
	if err != nil || ovEnv.Page.Provider != spec.ProviderExa {
		t.Fatalf("expected overseas wall escalation to succeed on exa, got env=%+v err=%v", ovEnv, err)
	}
	if len(order) != 3 || order[0] != "local" || order[1] != "keenable" || order[2] != "exa" {
		t.Fatalf("expected [local keenable exa] on overseas site, got %v", order)
	}

	// 3. 明确 PDF URL（如 arxiv /pdf/）直接跳过 local 直连 PDF 提取源（本夹具仅 tinyfish 声明 PDF=true）
	order = nil
	pdfEnv, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://arxiv.org/pdf/2503.00001"})
	if err != nil || pdfEnv.Page.Provider != spec.ProviderTinyFish {
		t.Fatalf("expected PDF fast-path to tinyfish, got env=%+v err=%v", pdfEnv, err)
	}
	if len(order) != 1 || order[0] != "tinyfish" {
		t.Fatalf("expected PDF URL to skip local and call [tinyfish], got %v", order)
	}

	// 4. 中文域名无后缀下载链接在运行时由 local 0 字节识别出 application/pdf 后，动态提权 tinyfish 跳过不支持 PDF 的 keenable
	order = nil
	dynPDFEnv, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://portal.cn/download?id=1"})
	if err != nil || dynPDFEnv.Page.Provider != spec.ProviderTinyFish {
		t.Fatalf("expected runtime application/pdf to promote tinyfish, got env=%+v err=%v", dynPDFEnv, err)
	}
	if len(order) != 2 || order[0] != "local" || order[1] != "tinyfish" {
		t.Fatalf("expected [local tinyfish] on runtime PDF detection, got %v", order)
	}
}

func TestGlobalFetchChainExplicitOverrideAndStrictLangSkip(t *testing.T) {
	mkFetcher := func(name string, pdf bool) *spyFetcher {
		return &spyFetcher{
			name:    name,
			profile: core.FetchProfile{PDF: pdf, DynamicRender: true},
			fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
				return &core.FetchOutcome{
					Page: &core.Page{URL: req.URL, Title: name, Content: strings.Repeat("clean body ", 40), Provider: name},
				}, nil
			},
		}
	}

	// 1. 默认配置下海外站点与海外 PDF 优先命中 tavily（全球链 tavily -> parallel -> exa -> tinyfish -> deepseek）
	rDefault := New(Options{FetchStrategy: core.FetchAPIFirst}, []core.Provider{
		mkFetcher(spec.ProviderDeepSeek, false),
		mkFetcher(spec.ProviderTinyFish, true),
		mkFetcher(spec.ProviderTavily, true),
		mkFetcher(spec.ProviderParallel, true),
		mkFetcher(spec.ProviderExa, true),
	})
	if got := rDefault.ActiveFetchChainGlobal(); strings.Join(got, ",") != "tavily,parallel,exa,tinyfish,deepseek" {
		t.Fatalf("unexpected ActiveFetchChainGlobal: %v", got)
	}
	ovEnv, err := rDefault.Fetch(context.Background(), &core.FetchRequest{URL: "https://go.dev/doc/effective_go"})
	if err != nil || ovEnv.Page.Provider != spec.ProviderTavily {
		t.Fatalf("expected tavily first on global URL, got env=%+v err=%v", ovEnv, err)
	}
	pdfEnv, err := rDefault.Fetch(context.Background(), &core.FetchRequest{URL: "https://arxiv.org/pdf/2210.03629"})
	if err != nil || pdfEnv.Page.Provider != spec.ProviderTavily {
		t.Fatalf("expected tavily first on global PDF URL, got env=%+v err=%v", pdfEnv, err)
	}

	// 2. 用户显式配置与默认同序的 FetchProviders（未配 ProvidersGlobal）时，不得静默重排（保持中文链 firecrawl 在 parallel 之前）
	rExplicit := New(Options{
		FetchStrategy:          core.FetchAPIFirst,
		FetchProviders:         spec.DefaultFetchProviders(),
		FetchProvidersExplicit: true,
	}, []core.Provider{
		mkFetcher(spec.ProviderDeepSeek, false),
		mkFetcher(spec.ProviderFirecrawl, true),
		mkFetcher(spec.ProviderParallel, true),
		mkFetcher(spec.ProviderExa, true),
	})
	if got := rExplicit.ActiveFetchChainGlobal(); got != nil {
		t.Fatalf("expected nil ActiveFetchChainGlobal when user explicitly configured single fetch chain, got %v", got)
	}
	expEnv, err := rExplicit.Fetch(context.Background(), &core.FetchRequest{URL: "https://go.dev/doc/effective_go"})
	if err != nil || expEnv.Page.Provider != spec.ProviderFirecrawl {
		t.Fatalf("expected explicit fetch.providers order respected (firecrawl first), got env=%+v err=%v", expEnv, err)
	}

	// 2b. 自定义子集 FetchProvidersGlobal 与 SearchProviders 时，ActiveFetchChainGlobal / ActiveFusionChain 仅包含实际执行源，不误附未列出源
	rSubset := New(Options{
		SearchProviders:      []string{spec.ProviderExa},
		FetchStrategy:        core.FetchAPIFirst,
		FetchProviders:       []string{spec.ProviderDeepSeek, spec.ProviderTavily},
		FetchProvidersGlobal: []string{spec.ProviderExa, spec.ProviderTavily},
	}, []core.Provider{
		&fakeSearcher{name: spec.ProviderDeepSeek, caps: core.Capabilities{MaxCount: 10}},
		&fakeSearcher{name: spec.ProviderExa, caps: core.Capabilities{MaxCount: 10}},
		mkFetcher(spec.ProviderDeepSeek, false),
		mkFetcher(spec.ProviderTinyFish, true),
		mkFetcher(spec.ProviderTavily, true),
		mkFetcher(spec.ProviderExa, true),
	})
	if got := rSubset.ActiveFetchChainGlobal(); strings.Join(got, ",") != "exa,tavily" {
		t.Fatalf("expected ActiveFetchChainGlobal=[exa tavily] without unlisted providers, got %v", got)
	}
	if got := rSubset.ActiveFusionChain(); strings.Join(got, ",") != "exa" {
		t.Fatalf("expected ActiveFusionChain=[exa] without unlisted providers, got %v", got)
	}

	// 3. Lang=false 与 Lang=true 并存时：--strict 跳过 Lang=false；非严格 auto 优先原生 Lang=true，仅显式指定或无原生源时降级为 lang:ignored
	noLang := &fakeSearcher{
		name: spec.ProviderDeepSeek,
		caps: core.Capabilities{MaxCount: 50, Lang: false},
		outcome: &core.SearchOutcome{
			Results: []core.Result{{Rank: 1, Title: "中文文档", URL: "https://docs.python.org/zh-cn/3/"}},
		},
	}
	nativeLang := &fakeSearcher{
		name: spec.ProviderTavily,
		caps: core.Capabilities{MaxCount: 20, Lang: true},
		outcome: &core.SearchOutcome{
			Results: []core.Result{{Rank: 1, Title: "English Docs", URL: "https://docs.python.org/3/"}},
		},
	}
	rSearch := New(Options{SearchProviders: []string{spec.ProviderDeepSeek, spec.ProviderTavily}}, []core.Provider{noLang, nativeLang})
	strictEnv, err := rSearch.Search(context.Background(), &core.SearchRequest{
		Query:         "asyncio TaskGroup",
		StrictFilters: true,
		Filters:       core.Filters{Lang: []string{"en"}},
	})
	if err != nil || len(strictEnv.Results) != 1 || strictEnv.Results[0].URL != "https://docs.python.org/3/" {
		t.Fatalf("expected strict --lang en to skip Lang=false provider and hit tavily, got env=%+v err=%v", strictEnv, err)
	}
	nonStrictAutoEnv, err := rSearch.Search(context.Background(), &core.SearchRequest{
		Query:   "asyncio TaskGroup",
		Filters: core.Filters{Lang: []string{"en"}},
	})
	if err != nil || len(nonStrictAutoEnv.Meta.Providers) != 1 || nonStrictAutoEnv.Meta.Providers[0].Name != spec.ProviderTavily || len(nonStrictAutoEnv.Meta.Providers[0].Degraded) != 0 {
		t.Fatalf("expected non-strict auto --lang en to prefer native Lang=true tavily, got %+v err=%v", nonStrictAutoEnv.Meta.Providers, err)
	}
	rNoLangOnly := New(Options{SearchProviders: []string{spec.ProviderDeepSeek}}, []core.Provider{noLang})
	nonStrictEnv, err := rNoLangOnly.Search(context.Background(), &core.SearchRequest{
		Query:   "asyncio TaskGroup",
		Filters: core.Filters{Lang: []string{"en"}},
	})
	if err != nil || len(nonStrictEnv.Meta.Providers) != 1 || !strings.Contains(strings.Join(nonStrictEnv.Meta.Providers[0].Degraded, ","), "lang:ignored") {
		t.Fatalf("expected non-strict --lang en on Lang=false-only chain to record lang:ignored, got %+v err=%v", nonStrictEnv.Meta.Providers, err)
	}
}

func TestFetchTitleFallbackFromH1OrURL(t *testing.T) {
	f := &spyFetcher{
		name: spec.ProviderExa,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			if strings.HasSuffix(req.URL, ".md") {
				return &core.FetchOutcome{
					Page: &core.Page{
						URL:      req.URL,
						Title:    "",
						Content:  "```sh\n# not a heading\n```\n\n# MCP Specification 2026¶\n\nBody text here.\n",
						Provider: spec.ProviderExa,
					},
				}, nil
			}
			return &core.FetchOutcome{
				Page: &core.Page{
					URL:      req.URL,
					Title:    "",
					Content:  "package context\n\n// Package context defines Context.\n",
					Provider: spec.ProviderExa,
				},
			}, nil
		},
	}
	r := New(Options{FetchStrategy: core.FetchAPIOnly, FetchProviders: []string{spec.ProviderExa}}, []core.Provider{f})

	// 1. 纯 .md 文档跳过围栏内注释，提取真实 # H1；即使 offset>0 也应保留标题
	mdEnv, err := r.Fetch(context.Background(), &core.FetchRequest{
		URL:    "https://modelcontextprotocol.io/specification/2026-07-28.md",
		Offset: 30,
	})
	if err != nil || mdEnv.Page.Title != "MCP Specification 2026" {
		t.Fatalf("expected H1 fallback title 'MCP Specification 2026', got env=%+v err=%v", mdEnv, err)
	}

	// 2. 裸源码无 H1 时回退 URL 路径末段
	goEnv, err := r.Fetch(context.Background(), &core.FetchRequest{
		URL: "https://tip.golang.org/src/context/context.go",
	})
	if err != nil || goEnv.Page.Title != "context.go" {
		t.Fatalf("expected URL basename fallback title 'context.go', got env=%+v err=%v", goEnv, err)
	}
}

func TestFetchNon200ChallengeEscalationAndClean404ShortCircuit(t *testing.T) {
	var order []string
	loc := &spyFetcher{
		name: spec.ProviderLocal,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "local")
			if req.URL == "https://juejin.cn/dead-link" {
				return nil, core.NewError(core.CodeNotFound, "target returned HTTP 404").
					WithProvider(spec.ProviderLocal).
					WithStatus(404)
			}
			return nil, core.NewError(core.CodeBlockedByPolicy, "target returned HTTP 403 Forbidden").
				WithProvider(spec.ProviderLocal).
				WithStatus(403).
				WithHintKey(core.HintKeyChallengeDetected)
		},
	}
	ds := &spyFetcher{
		name: spec.ProviderDeepSeek,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "deepseek")
			return &core.FetchOutcome{Page: &core.Page{URL: req.URL, Content: "ds", Provider: spec.ProviderDeepSeek}}, nil
		},
	}
	tv := &spyFetcher{
		name:    spec.ProviderTavily,
		profile: core.FetchProfile{DynamicRender: true},
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "tavily")
			return &core.FetchOutcome{Page: &core.Page{URL: req.URL, Content: "# Unblocked\n\nRendered via Tavily advanced.", Provider: spec.ProviderTavily}}, nil
		},
	}

	r := New(Options{FetchStrategy: core.FetchSmartLocal}, []core.Provider{loc, ds, tv})

	// 1. 干净 404 直接短路停止，不把死链转发给云 API 烧钱
	order = nil
	if _, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://juejin.cn/dead-link"}); core.AsError(err).Code != core.CodeNotFound {
		t.Fatalf("expected clean 404 -> not_found, got %v", err)
	}
	if len(order) != 1 || order[0] != "local" {
		t.Fatalf("expected clean 404 to short-circuit after local, got %v", order)
	}

	// 2. 非 200 Cloudflare 403 挑战立即触发 DynamicRenderHint=true 并将 tavily 排到静态 deepseek 前面
	order = nil
	env, err := r.Fetch(context.Background(), &core.FetchRequest{URL: "https://juejin.cn/cf-protected"})
	if err != nil || !env.OK || env.Page.Provider != spec.ProviderTavily {
		t.Fatalf("expected 403 challenge escalation to tavily, got env=%+v err=%v", env, err)
	}
	if len(order) != 2 || order[0] != "local" || order[1] != "tavily" {
		t.Fatalf("expected [local tavily] on 403 challenge escalation, got %v", order)
	}
	if tv.lastReq == nil || !tv.lastReq.DynamicRenderHint {
		t.Fatal("expected DynamicRenderHint=true on 403 challenge escalation")
	}

	// 3. 零路由改动的自定义新提取器仅凭 FetchProfile{PDF: true, DynamicRender: true} 即可被能力驱动选路优先调度
	custom := &spyFetcher{
		name:    "custom_browser",
		profile: core.FetchProfile{PDF: true, DynamicRender: true},
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "custom_browser")
			return &core.FetchOutcome{Page: &core.Page{URL: req.URL, Content: "# Custom PDF/JS OK", Provider: "custom_browser"}}, nil
		},
	}
	rCustom := New(Options{FetchStrategy: core.FetchSmartLocal, FetchProviders: []string{spec.ProviderDeepSeek, "custom_browser"}}, []core.Provider{loc, ds, custom})
	order = nil
	cEnv, err := rCustom.Fetch(context.Background(), &core.FetchRequest{URL: "https://arxiv.org/pdf/2504.99999"})
	if err != nil || !cEnv.OK || cEnv.Page.Provider != "custom_browser" || len(order) != 1 || order[0] != "custom_browser" {
		t.Fatalf("expected capability-driven PDF routing to custom_browser without router edits, got order=%v env=%+v err=%v", order, cEnv, err)
	}
}

func TestFetchDefaultAPIFirstGlobalOverrideAndDisabledLocal(t *testing.T) {
	var order []string
	loc := &spyFetcher{
		name: spec.ProviderLocal,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "local")
			return &core.FetchOutcome{Page: &core.Page{URL: req.URL, Content: "local fallback", Provider: spec.ProviderLocal}}, nil
		},
	}
	ds := &spyFetcher{
		name: spec.ProviderDeepSeek,
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "deepseek")
			return &core.FetchOutcome{Page: &core.Page{URL: req.URL, Content: "deepseek cloud", Provider: spec.ProviderDeepSeek}}, nil
		},
	}
	tf := &spyFetcher{
		name:    spec.ProviderTinyFish,
		profile: core.FetchProfile{PDF: true, DynamicRender: true},
		fn: func(_ context.Context, req *core.FetchRequest) (*core.FetchOutcome, error) {
			order = append(order, "tinyfish")
			return &core.FetchOutcome{Page: &core.Page{URL: req.URL, Content: "tinyfish cloud", Provider: spec.ProviderTinyFish}}, nil
		},
	}

	// 1. 默认 Options{} 启用 api_first：直接走云端 API，不触发前置 local 探测
	rDefault := New(Options{}, []core.Provider{loc, ds, tf})
	if got := rDefault.EnabledFetchers(); len(got) != 3 || got[0] != "deepseek" || got[1] != "tinyfish" || got[2] != "local" {
		t.Fatalf("expected EnabledFetchers order [deepseek tinyfish local] under default api_first, got %v", got)
	}
	env, err := rDefault.Fetch(context.Background(), &core.FetchRequest{URL: "https://juejin.cn/post/1"})
	if err != nil || !env.OK || env.Page.Provider != spec.ProviderDeepSeek || loc.calls.Load() != 0 {
		t.Fatalf("expected default api_first to call deepseek without local probe, got env=%+v locCalls=%d err=%v", env, loc.calls.Load(), err)
	}

	// 2. 自定义 FetchProvidersGlobal 覆盖非中文站点选路顺序
	rCustomGlobal := New(Options{
		FetchProviders:       []string{spec.ProviderTinyFish, spec.ProviderDeepSeek},
		FetchProvidersGlobal: []string{spec.ProviderDeepSeek, spec.ProviderTinyFish},
	}, []core.Provider{loc, ds, tf})
	order = nil
	gEnv, err := rCustomGlobal.Fetch(context.Background(), &core.FetchRequest{URL: "https://docs.example.org/guide"})
	if err != nil || gEnv.Page.Provider != spec.ProviderDeepSeek || len(order) != 1 || order[0] != "deepseek" {
		t.Fatalf("expected FetchProvidersGlobal to prioritize deepseek on overseas host, got order=%v env=%+v err=%v", order, gEnv, err)
	}

	// 3. 禁用 local 时（不传入 local 实例），内网地址返回明确 CodeNoProvider 错误且绝不外发云端
	rNoLocal := New(Options{}, []core.Provider{ds})
	ds.calls.Store(0)
	_, pErr := rNoLocal.Fetch(context.Background(), &core.FetchRequest{URL: "http://localhost:8080/health"})
	if se := core.AsError(pErr); se.Code != core.CodeNoProvider || se.MsgKey != "err.local_disabled_private_url" || ds.calls.Load() != 0 {
		t.Fatalf("expected err.local_disabled_private_url and 0 cloud calls, got err=%+v dsCalls=%d", se, ds.calls.Load())
	}
}
