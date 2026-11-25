package spec

import (
	"slices"
	"testing"
)

func TestProviderRegistryOptionsAndDefaults(t *testing.T) {
	if got := UserAgent(""); got != "veri/dev" {
		t.Fatalf("UserAgent(\"\") = %q, want veri/dev", got)
	}
	if got := UserAgent(" 1.2.0 "); got != "veri/1.2.0" {
		t.Fatalf("UserAgent(\"1.2.0\") = %q", got)
	}

	if p1, ok := NormalizeProviderName(" Tencent-WSA "); !ok || p1 != ProviderTencentWSA {
		t.Fatalf("NormalizeProviderName(tencent-wsa) = %q, %t", p1, ok)
	}
	if p2, ok := NormalizeProviderName("tencentwsa"); !ok || p2 != ProviderTencentWSA {
		t.Fatalf("NormalizeProviderName(tencentwsa) = %q, %t", p2, ok)
	}
	if p3, ok := NormalizeProviderName("zhihu-web"); !ok || p3 != ProviderZhihuWeb {
		t.Fatalf("NormalizeProviderName(zhihu-web) = %q, %t", p3, ok)
	}
	if _, ok := NormalizeProviderName("unknown"); ok {
		t.Fatal("unknown provider must not normalize")
	}
	if _, ok := NormalizeCredentialProvider(ProviderLocal); ok {
		t.Fatal("local provider must not accept credentials")
	}
	if owner, ok := NormalizeCredentialProvider(ProviderZhihuWeb); !ok || owner != ProviderZhihu {
		t.Fatalf("NormalizeCredentialProvider(zhihu_web) = (%q, %t), want (zhihu, true)", owner, ok)
	}
	if slices.Contains(CredentialProviders(), ProviderZhihuWeb) {
		t.Fatal("shared credential provider zhihu_web must not appear as separate slot in CredentialProviders")
	}
	if info, ok := LookupProvider(ProviderZhihuWeb); !ok || info.PackageDir() != ProviderZhihu || info.CredentialOwner() != ProviderZhihu || info.OwnsCredential() {
		t.Fatalf("unexpected ProviderInfo for zhihu_web: %+v", info)
	}
	if got, bad, ok := NormalizeProvidersForRole([]string{"Tencent-WSA", "exa", "tencentwsa", "EXA"}, RoleSearch); !ok || bad != "" || !slices.Equal(got, []string{ProviderTencentWSA, ProviderExa}) {
		t.Fatalf("NormalizeProvidersForRole valid = (%v, %q, %t)", got, bad, ok)
	}
	if _, bad, ok := NormalizeProvidersForRole([]string{"exa", "  "}, RoleSearch); ok || bad != "" {
		t.Fatalf("NormalizeProvidersForRole blank must fail with empty bad, got (%q, %t)", bad, ok)
	}
	if _, bad, ok := NormalizeProvidersForRole([]string{"zhihu"}, RoleFetch); ok || bad != "zhihu" {
		t.Fatalf("NormalizeProvidersForRole role mismatch = (%q, %t)", bad, ok)
	}
	if ref := DefaultEnvRef(ProviderDeepSeek); ref != "env:DEEPSEEK_API_KEY" {
		t.Fatalf("DefaultEnvRef = %q", ref)
	}
	if DefaultEnvRef(ProviderLocal) != "" {
		t.Fatal("expected empty DefaultEnvRef for local provider")
	}
	if got := ProviderCredentialEnvVars(ProviderBrave); !slices.Equal(got, []string{EnvBraveAPIKey, EnvBraveSearchAliasKey}) {
		t.Fatalf("ProviderCredentialEnvVars(brave) = %v", got)
	}

	if !IsFreshFetchOption(map[string]string{OptFetchFresh: " YES "}) || IsFreshFetchOption(nil) {
		t.Fatal("unexpected IsFreshFetchOption behavior")
	}
	if ClampSearchCount(0, 20) != DefaultSearchCount || ClampSearchCount(99, 20) != 20 {
		t.Fatal("unexpected ClampSearchCount behavior")
	}
	if !slices.Equal(DefaultFakeIPCIDRs(), []string{DefaultFakeIPCIDR, DefaultFakeIPv6CIDR}) {
		t.Fatalf("unexpected default Fake-IP CIDRs: %v", DefaultFakeIPCIDRs())
	}
}

// TestProviderTableIntegrity 校验登记表唯一性、角色完整性以及默认路由链与角色声明的一致性。
func TestProviderTableIntegrity(t *testing.T) {
	seen := make(map[string]string)
	for _, p := range Providers() {
		if p.Roles == 0 {
			t.Errorf("provider %s declares no role", p.Name)
		}
		for _, key := range append([]string{p.Name}, p.Aliases...) {
			if owner, dup := seen[key]; dup {
				t.Errorf("identifier %q of %s collides with %s", key, p.Name, owner)
			}
			seen[key] = p.Name
		}
	}

	if got := (RoleSearch | RoleFetch).String(); got != "search,fetch" {
		t.Fatalf("Role.String() = %q", got)
	}
	if RoleSearch.Has(0) {
		t.Fatal("empty role must never match")
	}

	assertChain := func(chain string, names []string, role Role) {
		t.Helper()
		dup := make(map[string]struct{}, len(names))
		for _, name := range names {
			if _, exists := dup[name]; exists {
				t.Errorf("%s contains duplicate provider %q", chain, name)
			}
			dup[name] = struct{}{}
			p, ok := LookupProvider(name)
			if !ok || !p.Roles.Has(role) {
				t.Errorf("%s contains %q without role %s", chain, name, role)
			}
		}
	}
	assertChain("DefaultSearchProviders", DefaultSearchProviders(), RoleSearch)
	assertChain("DefaultSearchProvidersGlobal", DefaultSearchProvidersGlobal(), RoleSearch)
	assertChain("DefaultFetchProviders", DefaultFetchProviders(), RoleFetch)
	assertChain("DefaultFetchProvidersGlobal", DefaultFetchProvidersGlobal(), RoleFetch)

	allSearchers := ProvidersWithRole(RoleSearch)
	if !slices.Equal(slices.Sorted(slices.Values(DefaultSearchProviders())), slices.Sorted(slices.Values(allSearchers))) {
		t.Fatal("DefaultSearchProviders must cover exactly all RoleSearch providers")
	}
	if !slices.Equal(slices.Sorted(slices.Values(DefaultSearchProviders())), slices.Sorted(slices.Values(DefaultSearchProvidersGlobal()))) {
		t.Fatal("DefaultSearchProvidersGlobal must reorder exactly the same providers as DefaultSearchProviders")
	}
	var cloudFetchers []string
	for _, name := range ProvidersWithRole(RoleFetch) {
		if name != ProviderLocal && name != ProviderGitHub {
			cloudFetchers = append(cloudFetchers, name)
		}
	}
	if !slices.Equal(slices.Sorted(slices.Values(DefaultFetchProviders())), slices.Sorted(slices.Values(cloudFetchers))) {
		t.Fatal("DefaultFetchProviders must cover exactly all cloud RoleFetch providers")
	}
	if !slices.Equal(slices.Sorted(slices.Values(DefaultFetchProviders())), slices.Sorted(slices.Values(DefaultFetchProvidersGlobal()))) {
		t.Fatal("DefaultFetchProvidersGlobal must reorder exactly the same providers as DefaultFetchProviders")
	}
	if slices.Contains(DefaultFetchProviders(), ProviderLocal) || slices.Contains(DefaultFetchProviders(), ProviderGitHub) {
		t.Fatal("default cloud fetch chain must not include local or specialist extractors")
	}
}
