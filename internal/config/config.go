// Package config 负责 veri 的 TOML 配置文件解码、未知键拦截、环境变量合并、密钥来源解析与配置脱敏展示。
//
// 依赖边界：仅依赖标准库、clikit/{storage,credentials,redact}、internal/{core,spec} 与 github.com/BurntSushi/toml。
package config

import (
	"reflect"
	"strings"
	"time"
)

// Config 是 veri 的完整生效配置树。
type Config struct {
	UI        UIConfig        `toml:"ui" json:"ui"`
	Search    SearchConfig    `toml:"search" json:"search"`
	Fetch     FetchConfig     `toml:"fetch" json:"fetch"`
	Cache     CacheConfig     `toml:"cache" json:"cache"`
	MCP       MCPConfig       `toml:"mcp" json:"mcp"`
	Network   NetworkConfig   `toml:"network" json:"network"`
	Providers ProvidersConfig `toml:"providers" json:"providers"`
}

// UIConfig 控制人类终端界面的语言偏好。
type UIConfig struct {
	Lang string `toml:"lang" json:"lang"` // auto | zh | en
}

// SearchConfig 控制检索链、策略、超时与过滤行为。
type SearchConfig struct {
	Providers             []string `toml:"providers" json:"providers"`
	ProvidersGlobal       []string `toml:"providers_global,omitempty" json:"providers_global,omitempty"`
	FusionProviders       []string `toml:"fusion_providers,omitempty" json:"fusion_providers,omitempty"`
	FusionProvidersGlobal []string `toml:"fusion_providers_global,omitempty" json:"fusion_providers_global,omitempty"`
	Strategy              string   `toml:"strategy" json:"strategy"`
	Count                 int      `toml:"count" json:"count"`
	Detail                string   `toml:"detail" json:"detail"`
	Timeout               Duration `toml:"timeout" json:"timeout"`
	FallbackOnEmpty       bool     `toml:"fallback_on_empty" json:"fallback_on_empty"`
	StrictFilters         bool     `toml:"strict_filters" json:"strict_filters"`
	FusionK               int      `toml:"fusion_k" json:"fusion_k"`
	SummaryMaxChars       int      `toml:"summary_max_chars" json:"summary_max_chars"`
	ContentMaxChars       int      `toml:"content_max_chars" json:"content_max_chars"`
	MaxChars              int      `toml:"max_chars" json:"max_chars"`
}

// FetchConfig 控制网页正文提取链、质量闸门与体积上限。
type FetchConfig struct {
	Strategy        string   `toml:"strategy" json:"strategy"`
	Providers       []string `toml:"providers" json:"providers"`
	ProvidersGlobal []string `toml:"providers_global,omitempty" json:"providers_global,omitempty"`
	Timeout         Duration `toml:"timeout" json:"timeout"`
	MaxBytes        ByteSize `toml:"max_bytes" json:"max_bytes"`
	MinContentChars int      `toml:"min_content_chars" json:"min_content_chars"`
	RespectRobots   bool     `toml:"respect_robots" json:"respect_robots"`
	UserAgent       string   `toml:"user_agent" json:"user_agent"`
	MaxChars        int      `toml:"max_chars" json:"max_chars"`
}

// CacheConfig 控制统一缓存默认策略、磁盘缓存参数以及 CLI / MCP 分域覆写。
type CacheConfig struct {
	Enabled   bool           `toml:"enabled" json:"enabled"`
	Dir       string         `toml:"dir" json:"dir"`
	FetchTTL  Duration       `toml:"fetch_ttl" json:"fetch_ttl"`
	SearchTTL Duration       `toml:"search_ttl" json:"search_ttl"`
	MaxBytes  ByteSize       `toml:"max_bytes" json:"max_bytes"`
	CLI       CacheCLIConfig `toml:"cli" json:"cli"`
	MCP       CacheMCPConfig `toml:"mcp" json:"mcp"`
}

// CacheCLIConfig 控制 CLI 磁盘缓存的可选独立覆写（未设置字段自动继承 [cache] 全局策略）。
type CacheCLIConfig struct {
	Enabled   *bool     `toml:"enabled,omitempty" json:"enabled,omitempty"`
	FetchTTL  *Duration `toml:"fetch_ttl,omitempty" json:"fetch_ttl,omitempty"`
	SearchTTL *Duration `toml:"search_ttl,omitempty" json:"search_ttl,omitempty"`
}

// CacheMCPConfig 控制 MCP 进程内内存缓存的可选独立覆写与容量上限（未设置字段自动继承 [cache] 全局策略）。
type CacheMCPConfig struct {
	Enabled    *bool     `toml:"enabled,omitempty" json:"enabled,omitempty"`
	FetchTTL   *Duration `toml:"fetch_ttl,omitempty" json:"fetch_ttl,omitempty"`
	SearchTTL  *Duration `toml:"search_ttl,omitempty" json:"search_ttl,omitempty"`
	MaxEntries int       `toml:"max_entries" json:"max_entries"`
}

// EffectiveCachePolicy 描述 CLI 或 MCP 经父子继承合并后的最终生效缓存策略。
type EffectiveCachePolicy struct {
	Enabled    bool
	FetchTTL   time.Duration
	SearchTTL  time.Duration
	MaxEntries int
}

// EffectiveCLI 计算 CLI 磁盘缓存的最终生效策略（子域显式字段优先，未设置项继承顶层 [cache]）。
func (c CacheConfig) EffectiveCLI() EffectiveCachePolicy {
	p := EffectiveCachePolicy{
		Enabled:   c.Enabled,
		FetchTTL:  c.FetchTTL.Duration,
		SearchTTL: c.SearchTTL.Duration,
	}
	if c.CLI.Enabled != nil {
		p.Enabled = *c.CLI.Enabled
	}
	if c.CLI.FetchTTL != nil {
		p.FetchTTL = c.CLI.FetchTTL.Duration
	}
	if c.CLI.SearchTTL != nil {
		p.SearchTTL = c.CLI.SearchTTL.Duration
	}
	return p
}

// EffectiveMCP 计算 MCP 进程内内存缓存的最终生效策略（子域显式字段优先，未设置项继承顶层 [cache]）。
func (c CacheConfig) EffectiveMCP() EffectiveCachePolicy {
	p := EffectiveCachePolicy{
		Enabled:    c.Enabled,
		FetchTTL:   c.FetchTTL.Duration,
		SearchTTL:  c.SearchTTL.Duration,
		MaxEntries: c.MCP.MaxEntries,
	}
	if c.MCP.Enabled != nil {
		p.Enabled = *c.MCP.Enabled
	}
	if c.MCP.FetchTTL != nil {
		p.FetchTTL = c.MCP.FetchTTL.Duration
	}
	if c.MCP.SearchTTL != nil {
		p.SearchTTL = c.MCP.SearchTTL.Duration
	}
	return p
}

// MCPConfig 控制 MCP Server 工具输出格式与上下文预算。
type MCPConfig struct {
	TextFormat      string `toml:"text_format" json:"text_format"`
	SearchMaxChars  int    `toml:"search_max_chars" json:"search_max_chars"`
	FetchMaxChars   int    `toml:"fetch_max_chars" json:"fetch_max_chars"`
	ContentMaxChars int    `toml:"content_max_chars" json:"content_max_chars"`
	MaxCount        int    `toml:"max_count" json:"max_count"`
}

// NetworkConfig 控制外部代理、TUN Fake-IP 放行网段与内网抓取开关。
type NetworkConfig struct {
	Proxy        string   `toml:"proxy" json:"proxy"`
	FakeIPCIDRs  []string `toml:"fake_ip_cidrs" json:"fake_ip_cidrs"`
	AllowPrivate bool     `toml:"allow_private" json:"allow_private"`
}

// ProvidersConfig 汇总各内置 Provider 的独立配置。
type ProvidersConfig struct {
	DeepSeek   DeepSeekConfig   `toml:"deepseek" json:"deepseek"`
	Keenable   KeenableConfig   `toml:"keenable" json:"keenable"`
	Zhihu      ZhihuConfig      `toml:"zhihu" json:"zhihu"`
	ZhihuWeb   ZhihuWebConfig   `toml:"zhihu_web" json:"zhihu_web"`
	Exa        ExaConfig        `toml:"exa" json:"exa"`
	TinyFish   TinyFishConfig   `toml:"tinyfish" json:"tinyfish"`
	TencentWSA TencentWSAConfig `toml:"tencent_wsa" json:"tencent_wsa"`
	Tavily     TavilyConfig     `toml:"tavily" json:"tavily"`
	Brave      BraveConfig      `toml:"brave" json:"brave"`
	Linkup     LinkupConfig     `toml:"linkup" json:"linkup"`
	Parallel   ParallelConfig   `toml:"parallel" json:"parallel"`
	Firecrawl  FirecrawlConfig  `toml:"firecrawl" json:"firecrawl"`
	GitHub     GitHubConfig     `toml:"github" json:"github"`
	Local      LocalConfig      `toml:"local" json:"local"`
}

// DeepSeekConfig 配置 DeepSeek Search & Fetch API。
type DeepSeekConfig struct {
	Enabled      bool     `toml:"enabled" json:"enabled"`
	APIKey       string   `toml:"api_key" json:"api_key"`
	BaseURL      string   `toml:"base_url" json:"base_url"`
	Timeout      Duration `toml:"timeout" json:"timeout"`
	FetchTimeout Duration `toml:"fetch_timeout" json:"fetch_timeout"`
	Concurrency  int      `toml:"concurrency" json:"concurrency"`
	Weight       float64  `toml:"weight" json:"weight"`
	QPS          float64  `toml:"qps" json:"qps"`
	PriceSearch  string   `toml:"price_search,omitempty" json:"price_search,omitempty"`
	PriceFetch   string   `toml:"price_fetch,omitempty" json:"price_fetch,omitempty"`
}

// ExaConfig 配置 Exa Search & Contents API。
type ExaConfig struct {
	Enabled      bool     `toml:"enabled" json:"enabled"`
	APIKey       string   `toml:"api_key" json:"api_key"`
	BaseURL      string   `toml:"base_url" json:"base_url"`
	SearchType   string   `toml:"search_type" json:"search_type"` // lite | auto | deep-lite | deep
	AllowOver10  bool     `toml:"allow_over_10" json:"allow_over_10"`
	Timeout      Duration `toml:"timeout" json:"timeout"`
	FetchTimeout Duration `toml:"fetch_timeout" json:"fetch_timeout"`
	Concurrency  int      `toml:"concurrency" json:"concurrency"`
	Weight       float64  `toml:"weight" json:"weight"`
	QPS          float64  `toml:"qps" json:"qps"`
}

// TinyFishConfig 配置 TinySearch & TinyFetch API。
type TinyFishConfig struct {
	Enabled      bool     `toml:"enabled" json:"enabled"`
	APIKey       string   `toml:"api_key" json:"api_key"`
	SearchURL    string   `toml:"search_url" json:"search_url"`
	FetchURL     string   `toml:"fetch_url" json:"fetch_url"`
	Timeout      Duration `toml:"timeout" json:"timeout"`
	FetchTimeout Duration `toml:"fetch_timeout" json:"fetch_timeout"`
	Concurrency  int      `toml:"concurrency" json:"concurrency"`
	Weight       float64  `toml:"weight" json:"weight"`
	QPS          float64  `toml:"qps" json:"qps"`
	PriceSearch  string   `toml:"price_search,omitempty" json:"price_search,omitempty"`
	PriceFetch   string   `toml:"price_fetch,omitempty" json:"price_fetch,omitempty"`
}

// TencentWSAConfig 配置腾讯云联网搜索 SearchPro API。
type TencentWSAConfig struct {
	Enabled     bool     `toml:"enabled" json:"enabled"`
	APIKey      string   `toml:"api_key" json:"api_key"`
	Edition     string   `toml:"edition" json:"edition"` // lite | standard | flagship
	BaseURL     string   `toml:"base_url" json:"base_url"`
	Timeout     Duration `toml:"timeout" json:"timeout"`
	Concurrency int      `toml:"concurrency" json:"concurrency"`
	Weight      float64  `toml:"weight" json:"weight"`
	QPS         float64  `toml:"qps" json:"qps"`
	PriceSearch string   `toml:"price_search,omitempty" json:"price_search,omitempty"`
}

// TavilyConfig 配置 Tavily Search & Extract API。
type TavilyConfig struct {
	Enabled      bool     `toml:"enabled" json:"enabled"`
	APIKey       string   `toml:"api_key" json:"api_key"`
	BaseURL      string   `toml:"base_url" json:"base_url"`
	SearchDepth  string   `toml:"search_depth" json:"search_depth"`   // ultra-fast | fast | basic | advanced
	ExtractDepth string   `toml:"extract_depth" json:"extract_depth"` // basic | advanced
	Timeout      Duration `toml:"timeout" json:"timeout"`
	FetchTimeout Duration `toml:"fetch_timeout" json:"fetch_timeout"`
	Concurrency  int      `toml:"concurrency" json:"concurrency"`
	Weight       float64  `toml:"weight" json:"weight"`
	QPS          float64  `toml:"qps" json:"qps"`
	PriceCredit  string   `toml:"price_credit,omitempty" json:"price_credit,omitempty"`
}

// BraveConfig 配置 Brave LLM Context & Web Search API。
type BraveConfig struct {
	Enabled     bool     `toml:"enabled" json:"enabled"`
	APIKey      string   `toml:"api_key" json:"api_key"`
	BaseURL     string   `toml:"base_url" json:"base_url"`
	Mode        string   `toml:"mode" json:"mode"` // auto | context | web
	Timeout     Duration `toml:"timeout" json:"timeout"`
	Concurrency int      `toml:"concurrency" json:"concurrency"`
	Weight      float64  `toml:"weight" json:"weight"`
	QPS         float64  `toml:"qps" json:"qps"`
	PriceSearch string   `toml:"price_search,omitempty" json:"price_search,omitempty"`
}

// KeenableConfig 配置 Keenable Search & Fetch API。
type KeenableConfig struct {
	Enabled      bool     `toml:"enabled" json:"enabled"`
	APIKey       string   `toml:"api_key" json:"api_key"`
	BaseURL      string   `toml:"base_url" json:"base_url"`
	Mode         string   `toml:"mode" json:"mode"` // pro | realtime
	Timeout      Duration `toml:"timeout" json:"timeout"`
	FetchTimeout Duration `toml:"fetch_timeout" json:"fetch_timeout"`
	Concurrency  int      `toml:"concurrency" json:"concurrency"`
	Weight       float64  `toml:"weight" json:"weight"`
	QPS          float64  `toml:"qps" json:"qps"`
	PriceCredit  string   `toml:"price_credit,omitempty" json:"price_credit,omitempty"`
}

// ZhihuConfig 配置知乎开放平台站内搜索 API（与 zhihu_web 共享同一份 api_key / ZHIHU_API_KEY）。
type ZhihuConfig struct {
	Enabled     bool     `toml:"enabled" json:"enabled"`
	APIKey      string   `toml:"api_key" json:"api_key"`
	BaseURL     string   `toml:"base_url" json:"base_url"`
	Timeout     Duration `toml:"timeout" json:"timeout"`
	Concurrency int      `toml:"concurrency" json:"concurrency"`
	Weight      float64  `toml:"weight" json:"weight"`
	QPS         float64  `toml:"qps" json:"qps"`
	PriceSearch string   `toml:"price_search,omitempty" json:"price_search,omitempty"`
}

// ZhihuWebConfig 配置知乎开放平台全网搜索 API（自动共享 providers.zhihu / ZHIHU_API_KEY 凭据）。
type ZhihuWebConfig struct {
	Enabled     bool     `toml:"enabled" json:"enabled"`
	BaseURL     string   `toml:"base_url" json:"base_url"`
	SearchDB    string   `toml:"search_db" json:"search_db"` // all | realtime | static
	Timeout     Duration `toml:"timeout" json:"timeout"`
	Concurrency int      `toml:"concurrency" json:"concurrency"`
	Weight      float64  `toml:"weight" json:"weight"`
	QPS         float64  `toml:"qps" json:"qps"`
	PriceSearch string   `toml:"price_search,omitempty" json:"price_search,omitempty"`
}

// LinkupConfig 配置 Linkup Search & Fetch API。
type LinkupConfig struct {
	Enabled      bool     `toml:"enabled" json:"enabled"`
	APIKey       string   `toml:"api_key" json:"api_key"`
	BaseURL      string   `toml:"base_url" json:"base_url"`
	Depth        string   `toml:"depth" json:"depth"`           // flash | fast | standard | deep
	FetchMode    string   `toml:"fetch_mode" json:"fetch_mode"` // standard | pro
	RenderJS     bool     `toml:"render_js" json:"render_js"`
	Timeout      Duration `toml:"timeout" json:"timeout"`
	FetchTimeout Duration `toml:"fetch_timeout" json:"fetch_timeout"`
	Concurrency  int      `toml:"concurrency" json:"concurrency"`
	Weight       float64  `toml:"weight" json:"weight"`
	QPS          float64  `toml:"qps" json:"qps"`
	PriceSearch  string   `toml:"price_search,omitempty" json:"price_search,omitempty"`
	PriceFetch   string   `toml:"price_fetch,omitempty" json:"price_fetch,omitempty"`
}

// ParallelConfig 配置 Parallel Search & Extract REST v1 API。
type ParallelConfig struct {
	Enabled      bool     `toml:"enabled" json:"enabled"`
	APIKey       string   `toml:"api_key" json:"api_key"`
	BaseURL      string   `toml:"base_url" json:"base_url"`
	Mode         string   `toml:"mode" json:"mode"` // turbo | fast | basic | advanced
	Timeout      Duration `toml:"timeout" json:"timeout"`
	FetchTimeout Duration `toml:"fetch_timeout" json:"fetch_timeout"`
	Concurrency  int      `toml:"concurrency" json:"concurrency"`
	Weight       float64  `toml:"weight" json:"weight"`
	QPS          float64  `toml:"qps" json:"qps"`
	PriceSearch  string   `toml:"price_search,omitempty" json:"price_search,omitempty"`
	PriceFetch   string   `toml:"price_fetch,omitempty" json:"price_fetch,omitempty"`
}

// FirecrawlConfig 配置 Firecrawl v2 Scrape & Search API（支持官方云端与开源自托管）。
type FirecrawlConfig struct {
	Enabled      bool     `toml:"enabled" json:"enabled"`
	APIKey       string   `toml:"api_key" json:"api_key"`
	BaseURL      string   `toml:"base_url" json:"base_url"`
	Proxy        string   `toml:"proxy" json:"proxy"` // auto | basic | enhanced
	MaxPDFPages  int      `toml:"max_pdf_pages" json:"max_pdf_pages"`
	Timeout      Duration `toml:"timeout" json:"timeout"`
	FetchTimeout Duration `toml:"fetch_timeout" json:"fetch_timeout"`
	Concurrency  int      `toml:"concurrency" json:"concurrency"`
	Weight       float64  `toml:"weight" json:"weight"`
	QPS          float64  `toml:"qps" json:"qps"`
	PriceCredit  string   `toml:"price_credit,omitempty" json:"price_credit,omitempty"`
}

// GitHubConfig 配置 GitHub 专用提取器。
type GitHubConfig struct {
	Enabled     bool     `toml:"enabled" json:"enabled"`
	Token       string   `toml:"token" json:"token"`
	APIBaseURL  string   `toml:"api_base_url" json:"api_base_url"`
	RawBaseURL  string   `toml:"raw_base_url" json:"raw_base_url"`
	Timeout     Duration `toml:"timeout" json:"timeout"`
	Concurrency int      `toml:"concurrency" json:"concurrency"`
}

// LocalConfig 配置本地纯 Go 正文提取器。
type LocalConfig struct {
	Enabled     bool     `toml:"enabled" json:"enabled"`
	Timeout     Duration `toml:"timeout" json:"timeout"`
	Concurrency int      `toml:"concurrency" json:"concurrency"`
}

// IsEnabled 按 toml 标签读取指定 Provider 的配置启用开关。
func (p ProvidersConfig) IsEnabled(provider string) bool {
	enabled := false
	_ = walkProviderFields(&p, func(provName, fieldTag string, val reflect.Value) error {
		if provName == provider && fieldTag == "enabled" && val.Kind() == reflect.Bool {
			enabled = val.Bool()
		}
		return nil
	})
	return enabled
}

// CredentialSpec 按 toml 标签读取指定 Provider 的凭据配置字符串（无需手写 switch）。
func (p ProvidersConfig) CredentialSpec(provider string) string {
	return p.CredentialSpecs()[provider]
}

// CredentialSpecs 一次遍历按 toml 标签收集全部 Provider 的凭据配置字符串映射。
func (p ProvidersConfig) CredentialSpecs() map[string]string {
	out := make(map[string]string, reflect.ValueOf(p).NumField())
	_ = walkProviderFields(&p, func(provName, fieldTag string, val reflect.Value) error {
		if isSecretTOMLTag(fieldTag) && val.Kind() == reflect.String {
			out[provName] = val.String()
		}
		return nil
	})
	return out
}

// SearchWeights 按 toml 标签收集全部检索源的 RRF 融合权重映射。
func (p ProvidersConfig) SearchWeights() map[string]float64 {
	out := make(map[string]float64)
	_ = walkProviderFields(&p, func(provName, fieldTag string, val reflect.Value) error {
		if fieldTag == "weight" && val.Kind() == reflect.Float64 {
			out[provName] = val.Float()
		}
		return nil
	})
	return out
}

// Redacted 按 toml 标签遍历遮蔽全部凭据字段、端点 URL 敏感参数与代理凭据，返回脱敏后的 Config 副本（防止新增厂商遗漏脱敏）。
func (c Config) Redacted(creds map[string]Credential) Config {
	cp := c
	cp.Network.Proxy = sanitizeConfigURL(cp.Network.Proxy)
	_ = walkProviderFields(&cp.Providers, func(provName, fieldTag string, val reflect.Value) error {
		if val.Kind() != reflect.String || !val.CanSet() {
			return nil
		}
		switch {
		case isSecretTOMLTag(fieldTag):
			val.SetString(maskProviderView(val.String(), creds[provName]))
		case isURLTOMLTag(fieldTag):
			val.SetString(sanitizeConfigURL(val.String()))
		}
		return nil
	})
	return cp
}

func isSecretTOMLTag(tag string) bool {
	return tag == "api_key" || tag == "token" || tag == "secret" ||
		strings.HasSuffix(tag, "_key") || strings.HasSuffix(tag, "_token")
}

func isURLTOMLTag(tag string) bool {
	return tag == "url" || strings.HasSuffix(tag, "_url")
}

func tomlTagName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("toml"), ",")
	return strings.TrimSpace(name)
}

func walkProviderFields(p *ProvidersConfig, visit func(provName, fieldTag string, val reflect.Value) error) error {
	if p == nil {
		return nil
	}
	for provField, provVal := range reflect.ValueOf(p).Elem().Fields() {
		provTag := tomlTagName(provField)
		if provTag == "" || provVal.Kind() != reflect.Struct {
			continue
		}
		for field, fieldVal := range provVal.Fields() {
			fTag := tomlTagName(field)
			if fTag == "" {
				continue
			}
			if err := visit(provTag, fTag, fieldVal); err != nil {
				return err
			}
		}
	}
	return nil
}
