//go:build live

package providertest_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/provider/brave"
	"veri/internal/provider/deepseek"
	"veri/internal/provider/exa"
	"veri/internal/provider/firecrawl"
	"veri/internal/provider/keenable"
	"veri/internal/provider/linkup"
	"veri/internal/provider/tavily"
	"veri/internal/provider/tencentwsa"
	"veri/internal/provider/tinyfish"
	"veri/internal/provider/zhihu"
	"veri/internal/spec"
)

func firstCredentialEnv(provider string) string {
	for _, name := range spec.ProviderCredentialEnvVars(provider) {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v
		}
	}
	return ""
}

// TestLiveProviders 仅在显式传入 -tags live（如 `task test:live`）时运行，对已配置环境变量密钥的厂商发起 1 次最小真实请求。
func TestLiveProviders(t *testing.T) {
	cli, err := httpx.NewAPIClient(httpx.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req := &core.SearchRequest{Query: "Model Context Protocol Go SDK", Count: 2, Detail: core.DetailSummary}

	t.Run("DeepSeek", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderDeepSeek)
		if k == "" {
			t.Skip("DEEPSEEK_API_KEY not set")
		}
		p, _, err := deepseek.New(deepseek.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, req)
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live search failed: out=%+v err=%v", out, err)
		}
	})

	t.Run("Exa", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderExa)
		if k == "" {
			t.Skip("EXA_API_KEY not set")
		}
		p, _, err := exa.New(exa.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, req)
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live search failed: out=%+v err=%v", out, err)
		}
	})

	t.Run("TinyFish", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderTinyFish)
		if k == "" {
			t.Skip("TINYFISH_API_KEY not set")
		}
		p, _, err := tinyfish.New(tinyfish.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, req)
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live search failed: out=%+v err=%v", out, err)
		}
	})

	t.Run("TencentWSA", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderTencentWSA)
		if k == "" {
			t.Skip("TENCENT_WSA_API_KEY not set")
		}
		p, _, err := tencentwsa.New(tencentwsa.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, req)
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live search failed: out=%+v err=%v", out, err)
		}
	})

	t.Run("Tavily", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderTavily)
		if k == "" {
			t.Skip("TAVILY_API_KEY not set")
		}
		p, _, err := tavily.New(tavily.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, req)
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live search failed: out=%+v err=%v", out, err)
		}
	})

	t.Run("Brave", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderBrave)
		if k == "" {
			t.Skip("BRAVE_API_KEY not set")
		}
		p, _, err := brave.New(brave.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, req)
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live search failed: out=%+v err=%v", out, err)
		}
	})

	t.Run("Keenable", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderKeenable)
		if k == "" {
			t.Skip("KEENABLE_API_KEY not set")
		}
		p, _, err := keenable.New(keenable.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, req)
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live search failed: out=%+v err=%v", out, err)
		}
	})

	t.Run("Zhihu", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderZhihu)
		if k == "" {
			t.Skip("ZHIHU_API_KEY not set")
		}
		p, _, err := zhihu.New(zhihu.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, &core.SearchRequest{Query: "Go 语言 MCP SDK", Count: 2, Detail: core.DetailSummary})
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live global search failed: out=%+v err=%v", out, err)
		}
		outCommunity, err := p.Search(ctx, &core.SearchRequest{
			Query:   "Go 并发编程",
			Count:   2,
			Detail:  core.DetailSummary,
			Options: map[string]string{spec.OptZhihuScope: spec.ZhihuScopeZhihu},
		})
		if err != nil || len(outCommunity.Results) == 0 {
			t.Fatalf("live zhihu community search failed: out=%+v err=%v", outCommunity, err)
		}
	})

	t.Run("Linkup", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderLinkup)
		if k == "" {
			t.Skip("LINKUP_API_KEY not set")
		}
		p, _, err := linkup.New(linkup.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, req)
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live search failed: out=%+v err=%v", out, err)
		}
	})

	t.Run("Firecrawl", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderFirecrawl)
		if k == "" {
			t.Skip("FIRECRAWL_API_KEY not set")
		}
		p, _, err := firecrawl.New(firecrawl.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		out, err := p.Search(ctx, req)
		if err != nil || len(out.Results) == 0 {
			t.Fatalf("live search failed: out=%+v err=%v", out, err)
		}
	})
}

// TestLiveFetchNotFoundContracts 验证各云端提取源在真实 404 目标页面上的响应均稳定映射为 core.CodeNotFound。
func TestLiveFetchNotFoundContracts(t *testing.T) {
	cli, err := httpx.NewAPIClient(httpx.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	req := &core.FetchRequest{URL: "https://example.com/nonexistent-veri-live-404-contract"}

	assertNotFound := func(t *testing.T, err error) {
		t.Helper()
		if err == nil {
			t.Fatal("expected not_found error, got nil")
		}
		if se := core.AsError(err); se.Code != core.CodeNotFound {
			t.Fatalf("expected CodeNotFound, got %+v", se)
		}
	}

	t.Run("TinyFish", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderTinyFish)
		if k == "" {
			t.Skip("TINYFISH_API_KEY not set")
		}
		p, _, err := tinyfish.New(tinyfish.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Fetch(ctx, req)
		assertNotFound(t, err)
	})

	t.Run("Tavily", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderTavily)
		if k == "" {
			t.Skip("TAVILY_API_KEY not set")
		}
		p, _, err := tavily.New(tavily.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Fetch(ctx, req)
		assertNotFound(t, err)
	})

	t.Run("Exa", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderExa)
		if k == "" {
			t.Skip("EXA_API_KEY not set")
		}
		p, _, err := exa.New(exa.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Fetch(ctx, req)
		assertNotFound(t, err)
	})

	t.Run("DeepSeek", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderDeepSeek)
		if k == "" {
			t.Skip("DEEPSEEK_API_KEY not set")
		}
		p, _, err := deepseek.New(deepseek.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Fetch(ctx, req)
		assertNotFound(t, err)
	})

	t.Run("Keenable", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderKeenable)
		if k == "" {
			t.Skip("KEENABLE_API_KEY not set")
		}
		p, _, err := keenable.New(keenable.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Fetch(ctx, req)
		assertNotFound(t, err)
	})

	t.Run("Linkup", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderLinkup)
		if k == "" {
			t.Skip("LINKUP_API_KEY not set")
		}
		p, _, err := linkup.New(linkup.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Fetch(ctx, req)
		assertNotFound(t, err)
	})

	t.Run("Firecrawl", func(t *testing.T) {
		k := firstCredentialEnv(spec.ProviderFirecrawl)
		if k == "" {
			t.Skip("FIRECRAWL_API_KEY not set")
		}
		p, _, err := firecrawl.New(firecrawl.Options{Keys: []string{k}}, cli)
		if err != nil {
			t.Fatal(err)
		}
		_, err = p.Fetch(ctx, req)
		assertNotFound(t, err)
	})
}
