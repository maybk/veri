package app

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
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
		"KEENABLE_API_KEY":    "keen_test_key_123456",
		"ZHIHU_API_KEY":       "zh-test-secret-1234",
		"EXA_API_KEY":         "exa-test-key-1234",
		"TINYFISH_API_KEY":    "sk-test-tinyfish-1234",
		"TENCENT_WSA_API_KEY": "sk-test-wsa-1234",
		"TAVILY_API_KEY":      "tvly-test-key-1234",
		"BRAVE_API_KEY":       "BSA-test-brave-1234567890",
		"LINKUP_API_KEY":      "lk-test-linkup-1234",
		"PARALLEL_API_KEY":    "par-test-parallel-1234",
		"FIRECRAWL_API_KEY":   "fc-test-firecrawl-1234",
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
		spec.ProviderDeepSeek:   "10s",
		spec.ProviderKeenable:   "10s/25s",
		spec.ProviderZhihu:      "10s",
		spec.ProviderExa:        "10s/25s",
		spec.ProviderTinyFish:   "10s/25s",
		spec.ProviderTencentWSA: "10s",
		spec.ProviderTavily:     "10s/25s",
		spec.ProviderBrave:      "10s",
		spec.ProviderLinkup:     "10s/25s",
		spec.ProviderParallel:   "10s/25s",
		spec.ProviderFirecrawl:  "10s/45s",
		spec.ProviderGitHub:     "10s",
		spec.ProviderLocal:      "10s",
	}
	wantDetails := map[string]string{
		spec.ProviderDeepSeek:   "concurrency=4",
		spec.ProviderKeenable:   "mode=pro",
		spec.ProviderZhihu:      "scope=global (global|zhihu)",
		spec.ProviderExa:        "type=auto",
		spec.ProviderTinyFish:   "concurrency=4",
		spec.ProviderTencentWSA: "edition=standard",
		spec.ProviderTavily:     "depth=basic/basic",
		spec.ProviderBrave:      "mode=auto",
		spec.ProviderLinkup:     "depth=fast, fetch=standard",
		spec.ProviderParallel:   "mode=fast",
		spec.ProviderFirecrawl:  "proxy=auto, max_pdf=20",
		spec.ProviderGitHub:     "concurrency=4",
		spec.ProviderLocal:      "min_chars=300, max_bytes=5MB",
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

func TestAppSecretScrubbingAndProviderDisable(t *testing.T) {
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
	application, err := NewWithStderr(loaded, "1.0.0-test", io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	// 1. 验证 scrubError 同时脱敏 Params 与空 Message 下的 Cause
	rawErr := core.WrapError(core.CodeAuthFailed, "", errors.New("rejected key sk-secret99887766")).
		WithParam("detail", "token=sk-secret99887766")
	scrubbed := core.AsError(application.scrubError(rawErr))
	if strings.Contains(scrubbed.Message, "99887766") ||
		strings.Contains(scrubbed.Params["detail"], "99887766") ||
		strings.Contains(scrubbed.Cause.Error(), "99887766") {
		t.Fatalf("scrubError leaked secret across Message/Params/Cause: %+v", scrubbed)
	}

	// 2. 验证 scrubEnvelopeSecrets 同时脱敏信封 Query、Page.URL、Meta.Notes 与 Providers[].Warnings
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

	// 3. 验证 providers.<name>.enabled = false 在已有环境变量密钥时仍可停用该 Provider
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
