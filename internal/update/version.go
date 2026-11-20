package update

import (
	"encoding/hex"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"veri/clikit/redact"
	"veri/internal/core"
	"veri/internal/spec"
)

var (
	releaseTagPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	stableVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
)

// RepositoryURL 返回项目官方 GitHub 仓库主页地址。
func RepositoryURL() string {
	return "https://github.com/" + spec.RepoSlug
}

// ReleaseTagURL 返回官方 GitHub Release 标签页地址；tag 为空时返回发布列表页。
func ReleaseTagURL(tag string) (string, error) {
	trimmed := strings.TrimSpace(tag)
	if trimmed == "" {
		return RepositoryURL() + "/releases", nil
	}
	if !isValidReleaseTag(trimmed) {
		return "", errCheckFailed(core.CodeUpstreamBadResponse, fmt.Sprintf("invalid release tag %q", trimmed), nil)
	}
	return RepositoryURL() + "/releases/tag/" + trimmed, nil
}

// ReleaseDownloadURL 返回官方 GitHub Release 指定标签下某资产的下载地址。
func ReleaseDownloadURL(tag, assetName string) (string, error) {
	trimmedTag := strings.TrimSpace(tag)
	trimmedAsset := strings.TrimSpace(assetName)
	if !isValidReleaseTag(trimmedTag) || !isSafePathToken(trimmedAsset) {
		return "", errCheckFailed(core.CodeUpstreamBadResponse, "invalid release download path", nil)
	}
	return defaultDownloadBaseURL + "/" + trimmedTag + "/" + trimmedAsset, nil
}

// IsValidReleaseURL 严格校验链接是否指向本项目官方 GitHub Release 标签页。
func IsValidReleaseURL(rawURL, tag string) bool {
	if !isValidReleaseTag(tag) {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") {
		return false
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(segments) != 5 {
		return false
	}
	return strings.EqualFold(segments[0], spec.RepoOwner) &&
		strings.EqualFold(segments[1], spec.RepoName) &&
		segments[2] == "releases" &&
		segments[3] == "tag" &&
		segments[4] == tag
}

// IsValidAssetDownloadURL 严格校验资产下载链接是否指向本项目官方 GitHub Release 指定标签与文件名。
func IsValidAssetDownloadURL(rawURL, tag, assetName string) bool {
	if !isValidReleaseTag(tag) || !isSafePathToken(assetName) {
		return false
	}
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Host, "github.com") {
		return false
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	segments := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(segments) != 6 {
		return false
	}
	return strings.EqualFold(segments[0], spec.RepoOwner) &&
		strings.EqualFold(segments[1], spec.RepoName) &&
		segments[2] == "releases" &&
		segments[3] == "download" &&
		segments[4] == tag &&
		segments[5] == assetName
}

// ValidateMirrorPrefix 校验并归一化可选的归档下载镜像前缀（必须为无凭据、无查询串的 HTTPS URL）。
func ValidateMirrorPrefix(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}
	u, err := url.Parse(trimmed)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		safe := redact.SanitizeURL(trimmed)
		return "", core.NewError(core.CodeInvalidArgument, fmt.Sprintf("invalid update mirror prefix %q (must be an https:// URL without credentials or query)", safe)).
			WithMsgKey("err.update_invalid_mirror").
			WithParam("mirror", safe)
	}
	if !strings.HasSuffix(trimmed, "/") {
		trimmed += "/"
	}
	return trimmed, nil
}

// NormalizeReleaseVersion 校验并归一化用户输入的正式版本号（支持可选 v 前缀，如 v0.2.0 或 0.2.0）。
func NormalizeReleaseVersion(raw string) (version, tag string, ok bool) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "v")
	trimmed = strings.TrimPrefix(trimmed, "V")
	if !stableVersionPattern.MatchString(trimmed) {
		return "", "", false
	}
	if _, valid := parseVersionParts(trimmed); !valid {
		return "", "", false
	}
	return trimmed, "v" + trimmed, true
}

// IsSupportedPlatform 判断指定的操作系统与架构组合是否提供官方预编译发布包。
func IsSupportedPlatform(goos, goarch string) bool {
	switch strings.ToLower(strings.TrimSpace(goos)) {
	case "darwin", "linux", "windows":
		return goarch == "amd64" || goarch == "arm64"
	default:
		return false
	}
}

// ExpectedAssetName 返回指定版本、操作系统与架构对应的官方发布归档文件名。
func ExpectedAssetName(version, goos, goarch string) (string, bool) {
	ver := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if ver == "" || !IsSupportedPlatform(goos, goarch) {
		return "", false
	}
	targetOS := strings.ToLower(strings.TrimSpace(goos))
	if targetOS == "windows" {
		return fmt.Sprintf("%s-%s-windows-%s.zip", spec.AppName, ver, goarch), true
	}
	return fmt.Sprintf("%s-%s-%s-%s.tar.gz", spec.AppName, ver, targetOS, goarch), true
}

// BinaryNameForOS 返回指定操作系统下的可执行文件名。
func BinaryNameForOS(goos string) string {
	if strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return spec.AppName + ".exe"
	}
	return spec.AppName
}

// ParseSHA256Digest 解析并校验形如 "sha256:<64位hex>" 或 "<64位hex>" 的摘要字符串。
func ParseSHA256Digest(raw string) (string, bool) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(strings.ToLower(s), "sha256:") {
		s = strings.TrimSpace(s[len("sha256:"):])
	}
	if len(s) != 64 {
		return "", false
	}
	if _, err := hex.DecodeString(s); err != nil {
		return "", false
	}
	return strings.ToLower(s), true
}

// ParseSHA256Sums 从 SHA256SUMS.txt 内容中精确匹配目标资产的 SHA-256 哈希。
func ParseSHA256Sums(content, targetAsset string) (string, bool) {
	if targetAsset == "" {
		return "", false
	}
	for line := range strings.SplitSeq(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		name := strings.TrimPrefix(fields[1], "*")
		name = strings.TrimPrefix(name, "./")
		if name == targetAsset {
			if sum, ok := ParseSHA256Digest(fields[0]); ok {
				return sum, true
			}
		}
	}
	return "", false
}

// IsNewerVersion 判断 latest 语义化版本号是否严格高于 current。
func IsNewerVersion(current, latest string) bool {
	curParts, curOK := parseVersionParts(current)
	latParts, latOK := parseVersionParts(latest)
	if !curOK || !latOK {
		return false
	}
	maxLen := max(len(curParts), len(latParts))
	for i := range maxLen {
		var curVal, latVal int
		if i < len(curParts) {
			curVal = curParts[i]
		}
		if i < len(latParts) {
			latVal = latParts[i]
		}
		if latVal != curVal {
			return latVal > curVal
		}
	}
	// 主版本号相同时，仅当当前为预发布版（带 -）且目标为正式版时视为有更新。
	return hasPrereleaseSuffix(current) && !hasPrereleaseSuffix(latest)
}

// IsSameVersion 判断两个语义化版本号在去前缀后是否一致。
func IsSameVersion(a, b string) bool {
	na := strings.TrimPrefix(strings.TrimSpace(a), "v")
	nb := strings.TrimPrefix(strings.TrimSpace(b), "v")
	return na != "" && na == nb
}

// IsDevVersion 判断当前版本是否属于未正式发版的本地开发构建。
func IsDevVersion(version string) bool {
	v := strings.TrimPrefix(strings.TrimSpace(version), "v")
	if v == "" || strings.EqualFold(v, "dev") || v == "0.0.0" || strings.HasPrefix(v, "0.0.0-") {
		return true
	}
	_, ok := parseVersionParts(v)
	return !ok
}

func isValidReleaseTag(tag string) bool {
	return releaseTagPattern.MatchString(tag)
}

func isSafePathToken(s string) bool {
	return s != "" && !strings.ContainsAny(s, "/?#\\") && s != "." && s != ".."
}

func hasPrereleaseSuffix(v string) bool {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	v, _, _ = strings.Cut(v, "+")
	return strings.Contains(v, "-")
}

func parseVersionParts(v string) ([]int, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if v == "" {
		return nil, false
	}
	if cut := strings.IndexAny(v, "-+"); cut >= 0 {
		v = v[:cut]
	}
	rawParts := strings.Split(v, ".")
	if len(rawParts) == 0 {
		return nil, false
	}
	parts := make([]int, 0, len(rawParts))
	for _, p := range rawParts {
		if p == "" {
			return nil, false
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, false
		}
		parts = append(parts, n)
	}
	return parts, true
}
