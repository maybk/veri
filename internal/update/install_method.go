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

// CanSelfUpdate 返回当前安装来源默认是否允许执行原地自更新。
func (i InstallInfo) CanSelfUpdate() bool {
	return i.Method == MethodStandalone
}

// IsManagedByPackageManager 返回当前二进制是否由外部包管理器托管。
func (i InstallInfo) IsManagedByPackageManager() bool {
	switch i.Method {
	case MethodHomebrew, MethodScoop, MethodWinGet:
		return true
	default:
		return false
	}
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
	switch targetOS {
	case "darwin", "linux":
		if slices.ContainsFunc(candidates, isHomebrewPath) {
			return InstallInfo{
				Method:         MethodHomebrew,
				ExecutablePath: exePath,
				ManagedCommand: "brew upgrade " + spec.AppName,
			}
		}
		if slices.ContainsFunc(candidates, isUnixSystemPath) {
			return InstallInfo{
				Method:         MethodSystem,
				ExecutablePath: exePath,
			}
		}
	case "windows":
		for _, p := range candidates {
			if isScoopPath(p) {
				return InstallInfo{
					Method:         MethodScoop,
					ExecutablePath: exePath,
					ManagedCommand: "scoop update " + spec.AppName,
				}
			}
			if isWinGetPath(p) {
				return InstallInfo{
					Method:         MethodWinGet,
					ExecutablePath: exePath,
					ManagedCommand: "winget upgrade " + spec.RepoOwner + "." + spec.AppDisplayName,
				}
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

func isHomebrewPath(normPath string) bool {
	return strings.HasPrefix(normPath, "/opt/homebrew/") ||
		strings.HasPrefix(normPath, "/usr/local/cellar/") ||
		strings.HasPrefix(normPath, "/home/linuxbrew/") ||
		strings.Contains(normPath, "/.linuxbrew/") ||
		strings.Contains(normPath, "/cellar/"+spec.AppName+"/")
}

func isUnixSystemPath(normPath string) bool {
	for _, prefix := range []string{"/usr/bin/", "/bin/", "/sbin/", "/usr/sbin/", "/snap/", "/nix/store/"} {
		if strings.HasPrefix(normPath, prefix) {
			return true
		}
	}
	return false
}

func isScoopPath(normPath string) bool {
	return strings.Contains(normPath, "/scoop/apps/"+spec.AppName+"/") ||
		strings.Contains(normPath, "/scoop/shims/")
}

func isWinGetPath(normPath string) bool {
	return strings.Contains(normPath, "/microsoft/winget/packages/") ||
		strings.Contains(normPath, "/winget/packages/") ||
		strings.Contains(normPath, "/microsoft/windowsapps/")
}

func isGoBuildTempPath(normPaths []string) bool {
	for _, p := range normPaths {
		if strings.Contains(p, "/go-build") {
			return true
		}
	}
	return false
}
