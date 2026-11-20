package update

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"veri/internal/core"
	"veri/internal/spec"
)

// InstallMethod 表示当前可执行文件的安装与托管来源。
type InstallMethod string

const (
	// MethodStandalone 表示独立下载或官方一键脚本安装的用户态单二进制，支持安全原地自更新。
	MethodStandalone InstallMethod = "standalone"
	// MethodMise 表示由 mise 托管安装，应通过 `mise upgrade` 升级。
	MethodMise InstallMethod = "mise"
	// MethodNPM 表示由 npm / pnpm / bun 托管安装，应通过对应包管理器升级。
	MethodNPM InstallMethod = "npm"
	// MethodHomebrew 表示由 Homebrew 托管安装，应通过 `brew upgrade` 升级。
	MethodHomebrew InstallMethod = "homebrew"
	// MethodScoop 表示由 Scoop 托管安装，应通过 `scoop update` 升级。
	MethodScoop InstallMethod = "scoop"
	// MethodWinGet 表示由 WinGet 托管安装，应通过 `winget upgrade` 升级。
	MethodWinGet InstallMethod = "winget"
	// MethodSystem 表示位于系统只读或系统包管理器目录，不支持覆盖。
	MethodSystem InstallMethod = "system"
	// MethodDev 表示本地开发构建（如 dev 版本或 go run 临时二进制），默认拒绝自覆盖。
	MethodDev InstallMethod = "dev"
)

// InstallInfo 描述当前二进制的安装来源、真实路径与推荐升级命令。
type InstallInfo struct {
	Method         InstallMethod `json:"install_method"`
	ExecutablePath string        `json:"executable_path,omitempty"`
	ManagedCommand string        `json:"managed_command,omitempty"`
}

// installRule 描述一条有序的安装来源判定规则；新增包管理器只需在 installRules 中追加规则项。
type installRule struct {
	Method  InstallMethod
	Managed bool
	OS      []string
	Match   func(candidates []string) (managedCmd string, ok bool)
}

// installRules 按“从具体容器到通用目录”的优先级排列：
// 1. 跨平台版本管理器（mise，排除 node/pnpm/bun 等运行时目录）；
// 2. 跨平台 JS 包管理器（npm/pnpm/bun，优先于 Homebrew 与系统目录，避免 Homebrew Node 误判）；
// 3. 平台级包管理器（Homebrew / Scoop / WinGet）；
// 4. 只读系统目录。
var installRules = []installRule{
	{
		Method:  MethodMise,
		Managed: true,
		Match:   matchMiseInstall,
	},
	{
		Method:  MethodNPM,
		Managed: true,
		Match:   matchNPMInstall,
	},
	{
		Method:  MethodHomebrew,
		Managed: true,
		OS:      []string{"darwin", "linux"},
		Match:   matchHomebrewInstall,
	},
	{
		Method:  MethodScoop,
		Managed: true,
		OS:      []string{"windows"},
		Match:   matchScoopInstall,
	},
	{
		Method:  MethodWinGet,
		Managed: true,
		OS:      []string{"windows"},
		Match:   matchWinGetInstall,
	},
	{
		Method:  MethodSystem,
		Managed: false,
		OS:      []string{"darwin", "linux"},
		Match:   matchUnixSystemInstall,
	},
}

// miseRuntimeTools 表示 mise 管理的语言运行时而非 veri 本体工具目录。
var miseRuntimeTools = map[string]struct{}{
	"bun":    {},
	"deno":   {},
	"go":     {},
	"node":   {},
	"nodejs": {},
	"pnpm":   {},
	"python": {},
	"rust":   {},
	"yarn":   {},
}

// CanSelfUpdate 返回当前安装来源默认是否允许执行原地自更新。
func (i InstallInfo) CanSelfUpdate() bool {
	return i.Method == MethodStandalone
}

// IsManagedByPackageManager 返回当前二进制是否由外部包管理器托管。
func (i InstallInfo) IsManagedByPackageManager() bool {
	for _, rule := range installRules {
		if rule.Managed && rule.Method == i.Method {
			return true
		}
	}
	return false
}

// DetectCurrentInstall 探测当前正在运行的 veri 进程的安装形态与真实路径。
func DetectCurrentInstall(version string) (InstallInfo, error) {
	rawExe, err := os.Executable()
	if err != nil {
		return InstallInfo{}, core.WrapError(core.CodeInternal, "failed to locate current executable", err).
			WithMsgKey("err.update_install_failed")
	}
	resolved := rawExe
	if r, rErr := filepath.EvalSymlinks(rawExe); rErr == nil && strings.TrimSpace(r) != "" {
		resolved = r
	}
	return DetectInstall(runtime.GOOS, version, rawExe, resolved), nil
}

// DetectInstall 根据目标平台、编译版本号、原始路径及符号链接解析路径判定安装形态。
func DetectInstall(goos, version, rawExePath, resolvedExePath string) InstallInfo {
	targetOS := strings.ToLower(strings.TrimSpace(goos))
	exePath := strings.TrimSpace(resolvedExePath)
	if exePath == "" {
		exePath = strings.TrimSpace(rawExePath)
	}

	candidates := collectNormalizedPaths(rawExePath, resolvedExePath)
	for _, rule := range installRules {
		if len(rule.OS) > 0 && !slices.Contains(rule.OS, targetOS) {
			continue
		}
		if cmd, ok := rule.Match(candidates); ok {
			return InstallInfo{
				Method:         rule.Method,
				ExecutablePath: exePath,
				ManagedCommand: cmd,
			}
		}
	}

	if IsDevVersion(version) || isGoBuildTempPath(candidates) {
		return InstallInfo{
			Method:         MethodDev,
			ExecutablePath: exePath,
		}
	}

	return InstallInfo{
		Method:         MethodStandalone,
		ExecutablePath: exePath,
	}
}

func collectNormalizedPaths(paths ...string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		trimmed := strings.TrimSpace(p)
		if trimmed == "" {
			continue
		}
		// 统一将 Windows 反斜杠和大小写归一化，便于跨平台离线单测与规则匹配。
		norm := strings.ToLower(strings.ReplaceAll(trimmed, "\\", "/"))
		out = append(out, norm)
	}
	return out
}

func matchMiseInstall(candidates []string) (string, bool) {
	// 优先检查真实 installs 目录，避免 raw 指向 shims 但 resolved 落在 mise 托管的 node 运行时里造成误判。
	for _, p := range candidates {
		tool, ok := extractMiseToolDir(p)
		if !ok {
			continue
		}
		if _, isRuntime := miseRuntimeTools[tool]; isRuntime {
			return "", false
		}
		return miseUpgradeCommand(tool), true
	}
	if slices.ContainsFunc(candidates, isNPMPath) {
		return "", false
	}
	for _, p := range candidates {
		if strings.Contains(p, "/mise/shims/") {
			return "mise upgrade " + spec.AppName, true
		}
	}
	return "", false
}

func extractMiseToolDir(normPath string) (string, bool) {
	_, rest, ok := strings.Cut(normPath, "/mise/installs/")
	if !ok {
		return "", false
	}
	tool, _, found := strings.Cut(rest, "/")
	if !found || tool == "" {
		return "", false
	}
	return tool, true
}

func miseUpgradeCommand(tool string) string {
	ownerApp := strings.ToLower(spec.RepoOwner + "-" + spec.AppName)
	switch tool {
	case "npm-" + ownerApp:
		return "mise upgrade npm:" + spec.NPMPackage
	case "github-" + ownerApp:
		return "mise upgrade github:" + spec.RepoSlug
	case "ubi-" + ownerApp:
		return "mise upgrade ubi:" + spec.RepoSlug
	case "aqua-" + ownerApp:
		return "mise upgrade aqua:" + spec.RepoSlug
	default:
		return "mise upgrade " + spec.AppName
	}
}

func matchNPMInstall(candidates []string) (string, bool) {
	if !slices.ContainsFunc(candidates, isNPMPath) {
		return "", false
	}
	for _, p := range candidates {
		if strings.Contains(p, "/.pnpm/") || strings.Contains(p, "/pnpm/global/") || strings.Contains(p, "/pnpm-global/") {
			return "pnpm add -g " + spec.NPMPackage + "@latest", true
		}
		if strings.Contains(p, "/.bun/") || strings.Contains(p, "/bun/install/") {
			return "bun add -g " + spec.NPMPackage + "@latest", true
		}
	}
	return "npm install -g " + spec.NPMPackage + "@latest", true
}

func isNPMPath(normPath string) bool {
	return strings.Contains(normPath, "/node_modules/") ||
		strings.Contains(normPath, "/pnpm/global/") ||
		strings.Contains(normPath, "/.bun/install/")
}

func matchHomebrewInstall(candidates []string) (string, bool) {
	if slices.ContainsFunc(candidates, isHomebrewPath) {
		return "brew upgrade " + spec.AppName, true
	}
	return "", false
}

func isHomebrewPath(normPath string) bool {
	return strings.HasPrefix(normPath, "/opt/homebrew/") ||
		strings.HasPrefix(normPath, "/usr/local/cellar/") ||
		strings.HasPrefix(normPath, "/home/linuxbrew/") ||
		strings.Contains(normPath, "/.linuxbrew/") ||
		strings.Contains(normPath, "/cellar/"+spec.AppName+"/")
}

func matchScoopInstall(candidates []string) (string, bool) {
	if slices.ContainsFunc(candidates, isScoopPath) {
		return "scoop update " + spec.AppName, true
	}
	return "", false
}

func isScoopPath(normPath string) bool {
	return strings.Contains(normPath, "/scoop/apps/"+spec.AppName+"/") ||
		strings.Contains(normPath, "/scoop/shims/")
}

func matchWinGetInstall(candidates []string) (string, bool) {
	if slices.ContainsFunc(candidates, isWinGetPath) {
		return "winget upgrade " + spec.RepoOwner + "." + spec.AppDisplayName, true
	}
	return "", false
}

func isWinGetPath(normPath string) bool {
	return strings.Contains(normPath, "/microsoft/winget/packages/") ||
		strings.Contains(normPath, "/winget/packages/") ||
		strings.Contains(normPath, "/microsoft/windowsapps/")
}

func matchUnixSystemInstall(candidates []string) (string, bool) {
	if slices.ContainsFunc(candidates, isUnixSystemPath) {
		return "", true
	}
	return "", false
}

func isUnixSystemPath(normPath string) bool {
	for _, prefix := range []string{"/usr/bin/", "/bin/", "/sbin/", "/usr/sbin/", "/snap/", "/nix/store/"} {
		if strings.HasPrefix(normPath, prefix) {
			return true
		}
	}
	return false
}

func isGoBuildTempPath(normPaths []string) bool {
	for _, p := range normPaths {
		if strings.Contains(p, "/go-build") {
			return true
		}
	}
	return false
}
