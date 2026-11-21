package spec

import (
	"slices"
	"strings"
)

// 内置检索与正文提取 Provider 唯一标识常量。
const (
	ProviderDeepSeek   = "deepseek"
	ProviderKeenable   = "keenable"
	ProviderZhihu      = "zhihu"
	ProviderExa        = "exa"
	ProviderTinyFish   = "tinyfish"
	ProviderTencentWSA = "tencent_wsa"
	ProviderTavily     = "tavily"
	ProviderBrave      = "brave"
	ProviderLinkup     = "linkup"
	ProviderParallel   = "parallel"
	ProviderFirecrawl  = "firecrawl"
	ProviderGitHub     = "github"
	ProviderLocal      = "local"
)

// 上游 HTTP 认证请求头与前缀常量。
const (
	HeaderAuthorization = "Authorization"
	HeaderAPIKey        = "X-API-Key"
	HeaderExaAPIKey     = "x-api-key"
	HeaderBraveToken    = "X-Subscription-Token"
	AuthBearerPrefix    = "Bearer "
)

// 内置默认上游 API 基础端点常量。
const (
	DefaultDeepSeekBaseURL   = "https://api.deepseek.com"
	DefaultKeenableBaseURL   = "https://api.keenable.ai"
	DefaultZhihuBaseURL      = "https://developer.zhihu.com"
	DefaultExaBaseURL        = "https://api.exa.ai"
	DefaultTinyFishSearchURL = "https://api.search.tinyfish.ai"
	DefaultTinyFishFetchURL  = "https://api.fetch.tinyfish.ai"
	DefaultTencentWSABaseURL = "https://api.wsa.cloud.tencent.com"
	DefaultTavilyBaseURL     = "https://api.tavily.com"
	DefaultBraveBaseURL      = "https://api.search.brave.com"
	DefaultLinkupBaseURL     = "https://api.linkup.so"
	DefaultParallelBaseURL   = "https://api.parallel.ai"
	DefaultFirecrawlBaseURL  = "https://api.firecrawl.dev"
	DefaultGitHubAPIBaseURL  = "https://api.github.com"
	DefaultGitHubRawBaseURL  = "https://raw.githubusercontent.com"
)

// 环境变量名常量（行为覆盖与各检索/提取源凭据）。
const (
	EnvConfig         = "VERI_CONFIG"
	EnvLang           = "VERI_LANG"
	EnvProviders      = "VERI_PROVIDERS"
	EnvFetchProviders = "VERI_FETCH_PROVIDERS"
	EnvFetchStrategy  = "VERI_FETCH_STRATEGY"
	EnvTimeout        = "VERI_TIMEOUT"
	EnvLogLevel       = "VERI_LOG_LEVEL"
	EnvLogFormat      = "VERI_LOG_FORMAT"
	EnvUpdateMirror   = "VERI_UPDATE_MIRROR"
	EnvNoColor        = "NO_COLOR"

	EnvDeepSeekAPIKey      = "DEEPSEEK_API_KEY"
	EnvKeenableAPIKey      = "KEENABLE_API_KEY"
	EnvZhihuAPIKey         = "ZHIHU_API_KEY"
	EnvZhihuSecretAlias    = "ZHIHU_ACCESS_SECRET"
	EnvExaAPIKey           = "EXA_API_KEY"
	EnvTinyFishAPIKey      = "TINYFISH_API_KEY"
	EnvTencentWSAAPIKey    = "TENCENT_WSA_API_KEY"
	EnvTencentWSAAliasKey  = "TENCENTCLOUD_WSA_APIKEY"
	EnvTavilyAPIKey        = "TAVILY_API_KEY"
	EnvBraveAPIKey         = "BRAVE_API_KEY"
	EnvBraveSearchAliasKey = "BRAVE_SEARCH_API_KEY"
	EnvLinkupAPIKey        = "LINKUP_API_KEY"
	EnvParallelAPIKey      = "PARALLEL_API_KEY"
	EnvFirecrawlAPIKey     = "FIRECRAWL_API_KEY"
	EnvGitHubToken         = "GITHUB_TOKEN"
	EnvGHTokenAlias        = "GH_TOKEN"
)

// Role 是 Provider 承担的能力角色位集合。
type Role uint8

const (
	// RoleSearch 表示实现 core.Searcher。
	RoleSearch Role = 1 << iota
	// RoleFetch 表示实现 core.Fetcher。
	RoleFetch
)

// Has 判断角色集合是否包含指定角色。
func (r Role) Has(want Role) bool { return want != 0 && r&want == want }

// String 返回逗号分隔的角色名称（如 "search,fetch"）。
func (r Role) String() string {
	var parts []string
	if r.Has(RoleSearch) {
		parts = append(parts, "search")
	}
	if r.Has(RoleFetch) {
		parts = append(parts, "fetch")
	}
	return strings.Join(parts, ",")
}

// ProviderInfo 是内置 Provider 的静态登记信息。
type ProviderInfo struct {
	Name    string   // 全局唯一的小写蛇形标识
	Roles   Role     // 承担的能力角色
	EnvVars []string // 凭据环境变量（首项为主变量，其余为兼容别名）；为空表示无需凭据
	Aliases []string // 可被识别的名称别名
}

// NeedsCredential 判断该 Provider 是否支持配置密钥或访问令牌。
func (p ProviderInfo) NeedsCredential() bool { return len(p.EnvVars) > 0 }

// Providers 返回全部内置 Provider 的登记表副本（新增 Provider 只需在此追加一行）。
func Providers() []ProviderInfo {
	return []ProviderInfo{
		{Name: ProviderDeepSeek, Roles: RoleSearch | RoleFetch, EnvVars: []string{EnvDeepSeekAPIKey}},
		{Name: ProviderKeenable, Roles: RoleSearch | RoleFetch, EnvVars: []string{EnvKeenableAPIKey}},
		{Name: ProviderZhihu, Roles: RoleSearch, EnvVars: []string{EnvZhihuAPIKey, EnvZhihuSecretAlias}},
		{Name: ProviderExa, Roles: RoleSearch | RoleFetch, EnvVars: []string{EnvExaAPIKey}},
		{Name: ProviderTinyFish, Roles: RoleSearch | RoleFetch, EnvVars: []string{EnvTinyFishAPIKey}},
		{Name: ProviderTencentWSA, Roles: RoleSearch, EnvVars: []string{EnvTencentWSAAPIKey, EnvTencentWSAAliasKey}, Aliases: []string{"tencentwsa", "tencent-wsa"}},
		{Name: ProviderTavily, Roles: RoleSearch | RoleFetch, EnvVars: []string{EnvTavilyAPIKey}},
		{Name: ProviderBrave, Roles: RoleSearch, EnvVars: []string{EnvBraveAPIKey, EnvBraveSearchAliasKey}},
		{Name: ProviderLinkup, Roles: RoleSearch | RoleFetch, EnvVars: []string{EnvLinkupAPIKey}},
		{Name: ProviderParallel, Roles: RoleSearch | RoleFetch, EnvVars: []string{EnvParallelAPIKey}},
		{Name: ProviderFirecrawl, Roles: RoleSearch | RoleFetch, EnvVars: []string{EnvFirecrawlAPIKey}},
		{Name: ProviderGitHub, Roles: RoleFetch, EnvVars: []string{EnvGitHubToken, EnvGHTokenAlias}},
		{Name: ProviderLocal, Roles: RoleFetch},
	}
}

// LookupProvider 按标识或别名（大小写与首尾空白不敏感）查找登记信息。
func LookupProvider(raw string) (ProviderInfo, bool) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return ProviderInfo{}, false
	}
	for _, p := range Providers() {
		if v == p.Name || slices.Contains(p.Aliases, v) {
			return p, true
		}
	}
	return ProviderInfo{}, false
}

// AllProviders 返回全部内置检索与提取适配器名称列表。
func AllProviders() []string {
	return providerNames(func(ProviderInfo) bool { return true })
}

// ProvidersWithRole 按登记顺序返回承担指定角色的 Provider 名称列表。
func ProvidersWithRole(role Role) []string {
	return providerNames(func(p ProviderInfo) bool { return p.Roles.Has(role) })
}

// CredentialProviders 返回支持配置密钥或访问令牌的内置适配器名称列表。
func CredentialProviders() []string {
	return providerNames(ProviderInfo.NeedsCredential)
}

func providerNames(keep func(ProviderInfo) bool) []string {
	var out []string
	for _, p := range Providers() {
		if keep(p) {
			out = append(out, p.Name)
		}
	}
	return out
}

// ProviderCredentialEnvVars 返回指定 Provider 支持的环境变量名列表（首项为主变量，其余为兼容别名）。
func ProviderCredentialEnvVars(provider string) []string {
	if p, ok := LookupProvider(provider); ok {
		return p.EnvVars
	}
	return nil
}

// NormalizeProviderName 规整内置适配器名称（兼容登记表中的别名）。
func NormalizeProviderName(raw string) (string, bool) {
	p, ok := LookupProvider(raw)
	return p.Name, ok
}

// NormalizeCredentialProvider 规整并校验支持配置密钥的适配器标识。
func NormalizeCredentialProvider(raw string) (string, bool) {
	p, ok := LookupProvider(raw)
	if !ok || !p.NeedsCredential() {
		return "", false
	}
	return p.Name, true
}

// DefaultEnvRef 返回指定 Provider 在配置文件中的默认环境变量引用表达式（如 "env:DEEPSEEK_API_KEY"）。
func DefaultEnvRef(provider string) string {
	vars := ProviderCredentialEnvVars(provider)
	if len(vars) == 0 {
		return ""
	}
	return "env:" + vars[0]
}

// DefaultSearchProviders 返回内置默认检索路由链的独立副本。
func DefaultSearchProviders() []string {
	return []string{
		ProviderDeepSeek,
		ProviderExa,
		ProviderZhihu,
		ProviderTinyFish,
		ProviderTencentWSA,
		ProviderTavily,
		ProviderBrave,
		ProviderLinkup,
		ProviderParallel,
		ProviderKeenable,
		ProviderFirecrawl,
	}
}

// DefaultFetchProviders 返回内置默认云端网页提取路由链的独立副本。
func DefaultFetchProviders() []string {
	return []string{
		ProviderDeepSeek,
		ProviderTinyFish,
		ProviderTavily,
		ProviderExa,
		ProviderLinkup,
		ProviderParallel,
		ProviderKeenable,
		ProviderFirecrawl,
	}
}

// DefaultFetchProvidersGlobal 返回非中文站点使用的默认云端提取顺序（仅在用户未显式配置 fetch.providers 与 fetch.providers_global 时生效）。
func DefaultFetchProvidersGlobal() []string {
	return []string{
		ProviderExa,
		ProviderTavily,
		ProviderLinkup,
		ProviderParallel,
		ProviderTinyFish,
		ProviderKeenable,
		ProviderFirecrawl,
		ProviderDeepSeek,
	}
}
