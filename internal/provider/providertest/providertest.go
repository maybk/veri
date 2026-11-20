// Package providertest 提供各 Provider 契约测试共用的客户端构造、脱敏样本加载与不变量断言。
package providertest

import (
	"encoding/json/jsontext"
	"os"
	"strings"
	"testing"

	"veri/internal/core"
	"veri/internal/httpx"
	"veri/internal/textnorm"
	"veri/internal/urlnorm"
)

// NewTestAPIClient 构造连接本地 httptest.Server 的测试 API 客户端。
func NewTestAPIClient(t *testing.T) *httpx.Client {
	t.Helper()
	c, err := httpx.NewAPIClient(httpx.Options{})
	if err != nil {
		t.Fatalf("NewAPIClient: %v", err)
	}
	return c
}

// NewTestFetchClient 构造允许连接本地回环测试服务器的抓取客户端。
func NewTestFetchClient(t *testing.T) *httpx.Client {
	t.Helper()
	c, err := httpx.NewSafeFetchClient(httpx.Options{AllowPrivate: true})
	if err != nil {
		t.Fatalf("NewSafeFetchClient: %v", err)
	}
	return c
}

// LoadFixture 读取当前包 testdata/ 下的标准格式脱敏样本文件，并对 .json 样本执行严格语法校验。
func LoadFixture(t *testing.T, filename string) []byte {
	t.Helper()
	root, err := os.OpenRoot("testdata")
	if err != nil {
		t.Fatalf("OpenRoot(testdata): %v", err)
	}
	defer func() { _ = root.Close() }()
	raw, err := root.ReadFile(filename)
	if err != nil {
		t.Fatalf("LoadFixture(%q): %v", filename, err)
	}
	if strings.HasSuffix(filename, ".json") && !jsontext.Value(raw).IsValid() {
		t.Fatalf("LoadFixture(%q): invalid RFC 8259 JSON", filename)
	}
	return raw
}

// AssertSearchOutcomeInvariants 校验任意 Searcher 产出均严格遵守归一化契约不变量。
func AssertSearchOutcomeInvariants(t *testing.T, providerName string, out *core.SearchOutcome) {
	t.Helper()
	if out == nil {
		t.Fatal("SearchOutcome must not be nil")
	}
	seen := make(map[string]struct{})
	for i, r := range out.Results {
		if r.Rank != i+1 {
			t.Errorf("result[%d].Rank = %d, want %d", i, r.Rank, i+1)
		}
		if _, ok := urlnorm.IsSafeHTTPURL(r.URL); !ok {
			t.Errorf("result[%d] has unsafe URL %q", i, r.URL)
		}
		key := urlnorm.Key(r.URL)
		if _, dup := seen[key]; dup {
			t.Errorf("duplicate normalized URL %q in %s results", key, providerName)
		}
		seen[key] = struct{}{}
		if len(r.Providers) != 1 || r.Providers[0] != providerName {
			t.Errorf("result[%d].Providers = %v, want [%s]", i, r.Providers, providerName)
		}
		if r.Relevance != nil && (*r.Relevance < 0 || *r.Relevance > 1) {
			t.Errorf("result[%d].Relevance = %f out of [0,1]", i, *r.Relevance)
		}
		if r.Authority != nil && (*r.Authority < 0 || *r.Authority > 1) {
			t.Errorf("result[%d].Authority = %f out of [0,1]", i, *r.Authority)
		}
		if r.Published != nil && r.Published.Location().String() != "UTC" {
			t.Errorf("result[%d].Published must be UTC, got %s", i, r.Published.Location())
		}
		if strings.ContainsRune(r.Title, '\u200b') || strings.ContainsRune(r.Summary, '\u200b') {
			t.Errorf("result[%d] still contains zero-width character", i)
		}
	}
}

// AssertFetchOutcomeInvariants 校验任意 Fetcher 产出均严格遵守归一化契约不变量。
func AssertFetchOutcomeInvariants(t *testing.T, providerName string, out *core.FetchOutcome) {
	t.Helper()
	if out == nil || out.Page == nil {
		t.Fatal("FetchOutcome and Page must not be nil")
	}
	if _, ok := urlnorm.IsSafeHTTPURL(out.Page.URL); !ok {
		t.Errorf("Page.URL %q is not a safe HTTP(S) URL", out.Page.URL)
	}
	if out.Page.Provider != providerName {
		t.Errorf("Page.Provider = %q, want %q", out.Page.Provider, providerName)
	}
	if strings.TrimSpace(out.Page.Content) == "" {
		t.Error("Page.Content must not be empty")
	}
	if out.Page.TotalChars != textnorm.RuneCount(out.Page.Content) {
		t.Errorf("Page.TotalChars = %d, want %d", out.Page.TotalChars, textnorm.RuneCount(out.Page.Content))
	}
	if out.Page.Published != nil && out.Page.Published.Location().String() != "UTC" {
		t.Errorf("Page.Published must be UTC, got %s", out.Page.Published.Location())
	}
}
