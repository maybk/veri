package core

import (
	"context"
	"net/url"
)

// SiteSupport 声明厂商对站点白名单过滤的原生支持程度。
type SiteSupport string

const (
	// SiteNone 不支持原生站点限定。
	SiteNone SiteSupport = "none"
	// SiteSingle 仅支持限定单个站点。
	SiteSingle SiteSupport = "single"
	// SiteMulti 支持同时限定多个站点。
	SiteMulti SiteSupport = "multi"
)

// TimeSupport 声明厂商对时间窗过滤的原生支持程度。
type TimeSupport string

const (
	// TimeNone 不支持原生时间窗过滤。
	TimeNone TimeSupport = "none"
	// TimeRelative 仅支持相对时间窗。
	TimeRelative TimeSupport = "relative"
	// TimeAbsolute 仅支持绝对日期区间。
	TimeAbsolute TimeSupport = "absolute"
	// TimeAfterOnly 仅支持起始日期下界（after_date，无截止日期上界）。
	TimeAfterOnly TimeSupport = "after_only"
	// TimeBoth 同时支持相对时间窗与绝对日期区间。
	TimeBoth TimeSupport = "both"
)

// ContentCost 声明 detail=content 时获取网页正文的计费特征。
type ContentCost string

const (
	// ContentNone 该检索源不支持在检索响应中直接附带正文。
	ContentNone ContentCost = "none"
	// ContentFree 检索响应附带正文不产生额外费用。
	ContentFree ContentCost = "free"
	// ContentPaid 检索响应附带正文会产生额外计费。
	ContentPaid ContentCost = "paid"
)

// Capabilities 描述单个 Provider 的静态能力边界，供路由层计算匹配与降级计划。
type Capabilities struct {
	MaxCount                     int         // 单次最大返回条数
	CountSteps                   []int       // 仅支持离散条数档位时按升序列出（如 10, 20, 30, 40, 50）
	QueryMaxRunes                int         // 检索词（含拼入操作符后）的最大 Unicode 字符数，0 表示不限
	Sites                        SiteSupport // 站点白名单支持程度
	MaxSites                     int         // Sites=SiteMulti 时的最大域名数，0 表示不限
	ExcludeSites                 int         // 最多原生支持的排除域名数，0 表示不支持
	SiteExcludeMutuallyExclusive bool        // true 表示原生接口不可同时传入包含站点与排除站点
	SiteHostOnly                 bool        // true 表示原生 Sites/ExcludeSites 仅支持纯主机名（不支持 host/path 路径前缀）
	ScopeDomains                 []string    // 非空表示该源仅索引指定域名及其子域（如 zhihu -> ["zhihu.com"]）
	Freshness                    TimeSupport // 时间窗原生支持程度
	Lang                         bool        // 是否原生支持语言过滤
	Content                      ContentCost // detail=content 支持与计费情况
	Question                     bool        // 是否支持传入原始问题辅助重排
}

// Provider 是所有厂商适配器的公共基础接口。
type Provider interface {
	// Name 返回全局唯一的小写蛇形标识（如 "deepseek", "tencent_wsa", "tinyfish"）。
	Name() string
	// Capabilities 返回该实例当前配置下的能力声明。
	Capabilities() Capabilities
}

// Searcher 执行一次原始联网检索（不经过 LLM 二手总结）。
type Searcher interface {
	Provider
	Search(ctx context.Context, req *SearchRequest) (*SearchOutcome, error)
}

// DynamicSearcher 是支持按单次请求选项（如 category 或 mode）动态收紧能力边界的可选检索器扩展接口。
type DynamicSearcher interface {
	Searcher
	CapabilitiesFor(req *SearchRequest) Capabilities
}

// Fetcher 提取单个 URL 的网页正文并转换为干净 Markdown。
type Fetcher interface {
	Provider
	Fetch(ctx context.Context, req *FetchRequest) (*FetchOutcome, error)
}

// SpecialistFetcher 是针对特定站点或路由结构（如 GitHub）的可选专用提取器扩展接口。
type SpecialistFetcher interface {
	Fetcher
	Match(u *url.URL) bool
}

// FetchProfile 声明提取器对特殊文档格式与服务端动态渲染的原生支持特征。
type FetchProfile struct {
	PDF           bool // 原生支持解析并提取 PDF 文档正文
	DynamicRender bool // 支持服务端无头渲染 SPA / JS 页面及越过常规静态反爬挑战
}

// FetchProfiler 是可选的提取器特征声明接口；未实现时默认视为纯静态文本提取器。
type FetchProfiler interface {
	Fetcher
	FetchProfile() FetchProfile
}

// ProfileOfFetcher 查询提取器的特殊格式与渲染支持特征（未实现 FetchProfiler 时返回零值）。
func ProfileOfFetcher(f Fetcher) FetchProfile {
	if fp, ok := f.(FetchProfiler); ok {
		return fp.FetchProfile()
	}
	return FetchProfile{}
}

// Answerer 返回带引用的综合回答。
type Answerer interface {
	Provider
	Ask(ctx context.Context, req *AskRequest) (*AskOutcome, error)
}

// SearchOutcome 是单个 Searcher 执行一次检索后的原始产出。
type SearchOutcome struct {
	Results   []Result
	Cost      *Cost
	RequestID string
	Warnings  []string
}

// FetchOutcome 是单个 Fetcher 执行一次单页提取后的原始产出。
type FetchOutcome struct {
	Page       *Page
	LowQuality bool   // 拿到部分文本但未通过正文质量闸门或命中反爬/软 404 盾页
	QualityMsg string // 未过质量闸门的具体原因（如 "too_short"、"bot_or_js_wall"）
	NoStore    bool   // 上游 HTTP 响应包含 Cache-Control: no-store 等禁止缓存指令
	Cost       *Cost
	RequestID  string
	Warnings   []string
}

// AskRequest 定义综合回答请求参数。
type AskRequest struct {
	Query     string
	Depth     string // quick | deep
	Count     int
	Providers []string
}

// AskOutcome 定义综合回答返回结果。
type AskOutcome struct {
	Text      string
	Citations []Result
	Cost      *Cost
	RequestID string
	Warnings  []string
}
