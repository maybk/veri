package buildinfo

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime/debug"
	"strings"
)

const (
	// ConfigAssetPath 是标准构建元信息配置文件在静态资产 FS 中的相对路径。
	ConfigAssetPath = "build/config.yml"
	// DefaultDevVersion 是既无内嵌配置也未注入 -ldflags 时的开发态回退版本标识。
	DefaultDevVersion = "0.0.0-dev"
)

// AppMeta 表示从 build/config.yml 的 info 段落解析出的应用静态元信息。
type AppMeta struct {
	CompanyName string `json:"company_name,omitempty"`
	ProductName string `json:"product_name,omitempty"`
	Description string `json:"description,omitempty"`
	Copyright   string `json:"copyright,omitempty"`
	Version     string `json:"version"`
}

// Info 保存运行期最终生效的版本号、提交摘要与构建时间。
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

var readBuildInfo = debug.ReadBuildInfo

// ParseConfig 无外部 YAML 库依赖解析 build/config.yml 中的 info.* 字段。
func ParseConfig(raw string) (AppMeta, error) {
	var meta AppMeta
	inInfo := false
	for line := range strings.SplitSeq(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inInfo = strings.HasPrefix(trimmed, "info:")
			continue
		}
		if !inInfo {
			continue
		}
		key, val, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		val = stripYAMLValue(val)
		switch strings.TrimSpace(key) {
		case "companyName":
			meta.CompanyName = val
		case "productName":
			meta.ProductName = val
		case "description":
			meta.Description = val
		case "copyright":
			meta.Copyright = val
		case "version":
			meta.Version = val
		}
	}
	if meta.Version == "" {
		return AppMeta{}, errors.New("missing info.version in build/config.yml")
	}
	return meta, nil
}

// ParseVersion 从 build/config.yml 文本中解析 info.version。
func ParseVersion(raw string) (string, error) {
	meta, err := ParseConfig(raw)
	if err != nil {
		return "", err
	}
	return meta.Version, nil
}

// LoadConfig 从给定的静态资产文件系统中读取并解析 build/config.yml。
func LoadConfig(assets fs.FS) (AppMeta, error) {
	if assets == nil {
		return AppMeta{}, errors.New("nil assets filesystem")
	}
	raw, err := fs.ReadFile(assets, ConfigAssetPath)
	if err != nil {
		return AppMeta{}, fmt.Errorf("read %s: %w", ConfigAssetPath, err)
	}
	return ParseConfig(string(raw))
}

// LoadVersion 从给定的静态资产文件系统中读取 info.version。
func LoadVersion(assets fs.FS) (string, error) {
	meta, err := LoadConfig(assets)
	if err != nil {
		return "", err
	}
	return meta.Version, nil
}

// Resolve 合并链接期注入参数（-ldflags）、内嵌 build/config.yml 版本号与 Go VCS 构建信息。
// 优先级：ldVersion > build/config.yml info.version > DefaultDevVersion。
func Resolve(assets fs.FS, ldVersion, ldCommit, ldBuildDate string) Info {
	ver := strings.TrimSpace(ldVersion)
	if ver == "" {
		if cfgVer, err := LoadVersion(assets); err == nil && cfgVer != "" {
			ver = cfgVer
		} else {
			ver = DefaultDevVersion
		}
	}

	commit := strings.TrimSpace(ldCommit)
	buildDate := strings.TrimSpace(ldBuildDate)
	if (commit == "" || commit == "none" || buildDate == "") && readBuildInfo != nil {
		if bi, ok := readBuildInfo(); ok {
			for _, s := range bi.Settings {
				switch s.Key {
				case "vcs.revision":
					if (commit == "" || commit == "none") && s.Value != "" {
						commit = s.Value
						if len(commit) > 12 {
							commit = commit[:12]
						}
					}
				case "vcs.time":
					if buildDate == "" && s.Value != "" {
						buildDate = s.Value
					}
				}
			}
		}
	}
	if commit == "" {
		commit = "none"
	}
	return Info{
		Version:   ver,
		Commit:    commit,
		BuildDate: buildDate,
	}
}

// FormatLine 将应用名称与构建元数据格式化为单行人类可读版本输出。
func FormatLine(appName string, info Info) string {
	ver := strings.TrimSpace(info.Version)
	if ver == "" {
		ver = DefaultDevVersion
	}
	line := fmt.Sprintf("%s %s", strings.TrimSpace(appName), ver)
	if info.Commit != "" && info.Commit != "none" {
		line += fmt.Sprintf(" (%s)", info.Commit)
	}
	if info.BuildDate != "" {
		line += fmt.Sprintf(" built %s", info.BuildDate)
	}
	return line
}

func stripYAMLValue(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if quote := trimmed[0]; quote == '"' || quote == '\'' {
		if end := strings.IndexByte(trimmed[1:], quote); end >= 0 {
			return trimmed[1 : 1+end]
		}
	}
	if before, _, ok := strings.Cut(trimmed, "#"); ok {
		trimmed = strings.TrimSpace(before)
	}
	return strings.Trim(trimmed, `"'`)
}
