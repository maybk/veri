package spec

import "strings"

// 应用身份、配置文件名、内嵌资产路径与安全隔离标签常量。
const (
	AppName                = "veri"
	AppDisplayName         = "Veri"
	RepoOwner              = "maybk"
	RepoName               = "veri"
	RepoSlug               = RepoOwner + "/" + RepoName
	NPMPackage             = "@" + RepoOwner + "/" + RepoName
	SchemaVersion          = "veri/v1"
	ConfigDirName          = "veri"
	ConfigFileName         = "config.toml"
	SecretsFileName        = "secrets.json"
	SecretKeyFileName      = "secret.key"
	SkillFileName          = "SKILL.md"
	SkillAssetPath         = "skills/veri/SKILL.md"
	UntrustedWebTag        = "untrusted_web_content"
	LangAuto               = "auto"
	UserAgentBrowserMode   = "browser"
	DetailDisabledInConfig = "disabled in config"
	CredSourceEnvPrefix    = "env:"
	CredSourceMissing      = "missing"
	CredSourceOptional     = "optional"

	BrowserUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/157.0.0.0 Safari/537.36"
)

// 信封操作类型、输出渲染格式与标准 MCP 工具名称常量。
const (
	KindSearch = "search"
	KindFetch  = "fetch"
	KindAsk    = "ask"
	KindUpdate = "update"

	FormatMarkdown = "md"
	FormatJSON     = "json"
	FormatJSONL    = "jsonl"
	FormatURLs     = "urls"

	TextFormatMarkdown = "markdown"
	TextFormatJSON     = "json"

	ToolWebSearch = "web_search"
	ToolWebFetch  = "web_fetch"
)

// UserAgent 生成携带生效版本号的统一出站标识（如 "veri/0.1.0"）。
func UserAgent(version string) string {
	v := strings.TrimSpace(version)
	if v == "" {
		v = "dev"
	}
	return AppName + "/" + v
}
