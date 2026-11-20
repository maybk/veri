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
	if _, ok := NormalizeProviderName("unknown"); ok {
		t.Fatal("unknown provider must not normalize")
	}
	if _, ok := NormalizeCredentialProvider(ProviderLocal); ok {
		t.Fatal("local provider must not accept credentials")
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
	if len(DefaultFakeIPCIDRs()) != 1 {
		t.Fatal("unexpected default Fake-IP CIDRs")
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

	assertSubset := func(chain string, names []string, role Role) {
		t.Helper()
		for _, name := range names {
			p, ok := LookupProvider(name)
			if !ok || !p.Roles.Has(role) {
				t.Errorf("%s contains %q without role %s", chain, name, role)
			}
		}
	}
	assertSubset("DefaultSearchProviders", DefaultSearchProviders(), RoleSearch)
	assertSubset("DefaultFetchProviders", DefaultFetchProviders(), RoleFetch)
	assertSubset("DefaultFetchProvidersGlobal", DefaultFetchProvidersGlobal(), RoleFetch)

	if !slices.Equal(slices.Sorted(slices.Values(DefaultFetchProviders())), slices.Sorted(slices.Values(DefaultFetchProvidersGlobal()))) {
		t.Fatal("DefaultFetchProvidersGlobal must reorder exactly the same providers as DefaultFetchProviders")
	}
	if slices.Contains(DefaultFetchProviders(), ProviderLocal) || slices.Contains(DefaultFetchProviders(), ProviderGitHub) {
		t.Fatal("default cloud fetch chain must not include local or specialist extractors")
	}
}
