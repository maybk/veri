package core

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Detail 表示检索结果的正文返回粒度。
type Detail string

const (
	// DetailSnippet 仅返回短摘要（约 100～200 字）。
	DetailSnippet Detail = "snippet"
	// DetailSummary 返回长摘要（约 600～800 字，缺失时回退 snippet）。
	DetailSummary Detail = "summary"
	// DetailContent 额外返回网页正文内容。
	DetailContent Detail = "content"
)

// ParseDetail 校验并解析检索粒度字符串。
func ParseDetail(raw string) (Detail, error) {
	switch Detail(strings.ToLower(strings.TrimSpace(raw))) {
	case "", DetailSummary:
		return DetailSummary, nil
	case DetailSnippet:
		return DetailSnippet, nil
	case DetailContent:
		return DetailContent, nil
	default:
		return "", NewError(CodeInvalidArgument, fmt.Sprintf("invalid detail %q: must be snippet, summary, or content", raw)).
			WithMsgKey("err.invalid_detail").
			WithParam("field", "detail").WithParam("value", raw)
	}
}

// Strategy 表示检索路由策略。
type Strategy string

const (
	// StrategyAuto 按配置顺序链式回退，首个成功即返回。
	StrategyAuto Strategy = "auto"
	// StrategySingle 仅调用单个显式指定的 Provider，不回退。
	StrategySingle Strategy = "single"
	// StrategyFusion 并发调用多个 Provider 并通过 RRF 融合去重。
	StrategyFusion Strategy = "fusion"
)

// ParseStrategy 校验并解析检索路由策略。
func ParseStrategy(raw string) (Strategy, error) {
	switch Strategy(strings.ToLower(strings.TrimSpace(raw))) {
	case "", StrategyAuto:
		return StrategyAuto, nil
	case StrategySingle:
		return StrategySingle, nil
	case StrategyFusion:
		return StrategyFusion, nil
	default:
		return "", NewError(CodeInvalidArgument, fmt.Sprintf("invalid strategy %q: must be auto, single, or fusion", raw)).
			WithMsgKey("err.invalid_search_strategy").
			WithParam("field", "strategy").WithParam("value", raw)
	}
}

// FetchStrategy 表示单页抓取路由策略。
type FetchStrategy string

const (
	// FetchAPIFirst 默认策略：专用提取器 -> 云端 API 提取器链 -> 本地提取回退兜底。
	FetchAPIFirst FetchStrategy = "api_first"
	// FetchSmartLocal 专用提取器 -> 本地提取（带质量闸门） -> 云端 API 提取器链升档。
	FetchSmartLocal FetchStrategy = "smart_local"
	// FetchLocalOnly 仅使用专用提取器与本地提取，不调用外部付费/配额 API。
	FetchLocalOnly FetchStrategy = "local_only"
	// FetchAPIOnly 仅使用专用提取器与云端 API 提取器链，不发起本地通用直连抓取。
	FetchAPIOnly FetchStrategy = "api_only"

	// DefaultFetchStrategy 是未显式指定抓取策略时的内置默认值。
	DefaultFetchStrategy = FetchAPIFirst
)

// ParseFetchStrategy 校验并解析抓取策略。
func ParseFetchStrategy(raw string) (FetchStrategy, error) {
	switch FetchStrategy(strings.ToLower(strings.TrimSpace(raw))) {
	case "", FetchAPIFirst:
		return FetchAPIFirst, nil
	case FetchSmartLocal:
		return FetchSmartLocal, nil
	case FetchLocalOnly:
		return FetchLocalOnly, nil
	case FetchAPIOnly:
		return FetchAPIOnly, nil
	default:
		return "", NewError(CodeInvalidArgument, fmt.Sprintf("invalid fetch strategy %q: must be api_first, smart_local, local_only, or api_only", raw)).
			WithMsgKey("err.invalid_fetch_strategy").
			WithParam("field", "strategy").WithParam("value", raw)
	}
}

// SearchRequest 是与具体厂商无关的统一检索请求。
type SearchRequest struct {
	Query         string            // 检索词
	Question      string            // 可选：触发检索的原始用户问题，辅助意图理解
	Count         int               // 期望结果条数，范围 1～50，默认 8
	Detail        Detail            // 文本返回粒度：snippet | summary | content
	Filters       Filters           // 统一过滤条件
	Providers     []string          // 显式指定的 Provider 列表；为空时使用路由默认链
	Strategy      Strategy          // auto | single | fusion
	StrictFilters bool              // 为 true 时禁止本地模拟过滤，不支持即跳过或报错
	IncludeExtra  bool              // 为 true 时保留站点 Icon 与 Extra 中的厂商特有扩展元数据
	MaxChars      int               // 整次检索响应字符预算，0 表示使用场景默认值或不限
	Timeout       time.Duration     // 可选单次检索超时覆盖，0 表示使用路由默认超时
	NoCache       bool              // 为 true 时跳过本地缓存读取且不写入缓存（CLI 磁盘缓存与 MCP 内存缓存均生效）
	Options       map[string]string // 厂商专属参数，键格式为 "<provider>.<key>"
}

// Filters 是统一的检索过滤条件。
type Filters struct {
	Sites        []string   // 仅保留这些域名（含子域名）的结果
	ExcludeSites []string   // 排除这些域名（含子域名）的结果
	Lang         []string   // ISO 639-1 内容语言代码列表，如 zh、en
	Freshness    *Freshness // 可选发布时间窗
}

// Result 是归一化后的单条检索结果。
type Result struct {
	Rank      int            `json:"rank" jsonschema:"1-based result rank"`
	Title     string         `json:"title" jsonschema:"Page title"`
	URL       string         `json:"url" jsonschema:"Canonical page URL"`
	Site      string         `json:"site,omitempty" jsonschema:"Source site name or domain"`
	Authors   []string       `json:"authors,omitempty" jsonschema:"Author name(s) when reported by the provider"`
	Icon      string         `json:"icon,omitempty" jsonschema:"Site icon URL"`
	Published *time.Time     `json:"published,omitempty" jsonschema:"Publication timestamp in UTC RFC3339"`
	Snippet   string         `json:"snippet,omitempty" jsonschema:"Short excerpt"`
	Summary   string         `json:"summary,omitempty" jsonschema:"Extended grounded summary"`
	Content   string         `json:"content,omitempty" jsonschema:"Extracted body text when detail=content"`
	Relevance *float64       `json:"relevance,omitempty" jsonschema:"Normalized relevance score in [0,1]"`
	Authority *float64       `json:"authority,omitempty" jsonschema:"Normalized authority score in [0,1]"`
	Providers []string       `json:"providers" jsonschema:"Providers that returned this result"`
	Extra     map[string]any `json:"extra,omitempty" jsonschema:"Provider-specific extension metadata"`
}

// Clone 返回 Result 的深拷贝，避免缓存或预算截断修改共享切片与映射。
func (r Result) Clone() Result {
	out := r
	if len(r.Authors) > 0 {
		out.Authors = slices.Clone(r.Authors)
	}
	if r.Published != nil {
		out.Published = new(r.Published.UTC())
	}
	if r.Relevance != nil {
		out.Relevance = new(*r.Relevance)
	}
	if r.Authority != nil {
		out.Authority = new(*r.Authority)
	}
	if len(r.Providers) > 0 {
		out.Providers = slices.Clone(r.Providers)
	}
	if len(r.Extra) > 0 {
		out.Extra = cloneAnyMap(r.Extra)
	}
	return out
}

func cloneAnyMap(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = cloneAnyVal(v)
	}
	return dst
}

func cloneAnyVal(v any) any {
	switch x := v.(type) {
	case []string:
		return slices.Clone(x)
	case []any:
		cp := make([]any, len(x))
		for i, item := range x {
			cp[i] = cloneAnyVal(item)
		}
		return cp
	case map[string]any:
		return cloneAnyMap(x)
	default:
		return v
	}
}

// FetchRequest 是单页正文提取请求。
type FetchRequest struct {
	URL               string            // 目标网页 URL（仅允许 http / https）
	Providers         []string          // 显式指定的提取器；为空时按 Strategy 选择
	Strategy          FetchStrategy     // api_first | smart_local | local_only | api_only
	MaxChars          int               // 本次返回的最大字符数，0 表示不限
	Offset            int               // 分页续读起始码点偏移量，默认 0
	Section           string            // 可选：按章节标题匹配截取对应章节子树
	Match             string            // 可选：按关键词提取页内高相关段落窗口
	Timeout           time.Duration     // 可选单次抓取超时覆盖，0 表示使用路由默认超时
	Options           map[string]string // 可选按次提取扩展参数（如 fresh、tavily.extract_depth、tinyfish.ttl、exa.max_age_hours）
	DynamicRenderHint bool              // 路由层在前置静态提取命中反爬/JS 盾时置为 true，提示支持分档的云端源启用深度渲染
}

// 页面提取来源类型枚举常量。
const (
	SourceGeneric          = "generic"
	SourceGitHubReadme     = "github_readme"
	SourceGitHubFile       = "github_file"
	SourceGitHubIssue      = "github_issue"
	SourceGitHubPR         = "github_pr"
	SourceGitHubRelease    = "github_release"
	SourceGitHubDiscussion = "github_discussion"
	SourceGitHubCommit     = "github_commit"
)

// PageSection 描述页面 Markdown 章节大纲条目及其起始字符偏移量。
type PageSection struct {
	Heading string `json:"heading" jsonschema:"Section heading text"`
	Level   int    `json:"level" jsonschema:"Markdown heading level (1-4)"`
	Offset  int    `json:"offset" jsonschema:"Character offset of this section in full content"`
	Chars   int    `json:"chars" jsonschema:"Approximate character length of this section"`
}

// Page 是提取后的单页结构化内容。
type Page struct {
	URL        string        `json:"url" jsonschema:"Requested target URL"`
	FinalURL   string        `json:"final_url,omitempty" jsonschema:"Final URL after redirects"`
	Title      string        `json:"title" jsonschema:"Extracted page title"`
	Author     string        `json:"author,omitempty" jsonschema:"Extracted author or byline"`
	Site       string        `json:"site,omitempty" jsonschema:"Source site name"`
	Published  *time.Time    `json:"published,omitempty" jsonschema:"Publication timestamp in UTC"`
	Content    string        `json:"content" jsonschema:"Clean Markdown or text body"`
	TotalChars int           `json:"total_chars" jsonschema:"Total character count before slicing"`
	NextOffset *int          `json:"next_offset,omitempty" jsonschema:"Character offset for next page chunk"`
	Outline    []PageSection `json:"outline,omitempty" jsonschema:"Document heading outline with character offsets"`
	Provider   string        `json:"provider,omitempty" jsonschema:"Extractor provider used"`
	SourceType string        `json:"source_type,omitempty" jsonschema:"Content source classification"`
	Error      *ErrorInfo    `json:"error,omitempty" jsonschema:"Per-page error details when batch fetch partially fails"`
}

// Clone 返回 Page 的深拷贝。
func (p *Page) Clone() *Page {
	if p == nil {
		return nil
	}
	cp := *p
	if p.Published != nil {
		cp.Published = new(p.Published.UTC())
	}
	if p.NextOffset != nil {
		cp.NextOffset = new(*p.NextOffset)
	}
	if len(p.Outline) > 0 {
		cp.Outline = slices.Clone(p.Outline)
	}
	if p.Error != nil {
		cp.Error = new(*p.Error)
	}
	return &cp
}
