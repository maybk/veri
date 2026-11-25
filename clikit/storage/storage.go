package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ExpandHome 将路径开头的 "~"、"~/..." 或 "~\..." 跨平台展开为当前用户主目录并规范化分隔符。
func ExpandHome(path string) (string, error) {
	if path == "~" || strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine user home directory: %w", err)
		}
		if path == "~" {
			return filepath.Clean(home), nil
		}
		return filepath.Join(home, path[2:]), nil
	}
	return filepath.Clean(path), nil
}

// ResolveConfigFile 按三端（macOS / Windows / Linux）统一优先级解析应用配置文件绝对路径：
//  1. explicitPath（如命令行 --config）
//  2. envVarName 指定的环境变量（非空时）
//  3. $XDG_CONFIG_HOME/<appDirName>/<fileName>（显式设置 XDG_CONFIG_HOME 时）
//  4. ~/.config/<appDirName>/<fileName>
func ResolveConfigFile(appDirName, fileName, explicitPath, envVarName string, getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if p := strings.TrimSpace(explicitPath); p != "" {
		return ExpandHome(p)
	}
	if envVarName != "" {
		if p := strings.TrimSpace(getenv(envVarName)); p != "" {
			return ExpandHome(p)
		}
	}
	if xdg := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); xdg != "" {
		expanded, err := ExpandHome(xdg)
		if err != nil {
			return "", err
		}
		return filepath.Join(expanded, appDirName, fileName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine user home directory: %w", err)
	}
	return filepath.Join(home, ".config", appDirName, fileName), nil
}

// ResolveConfigDir 返回应用配置与凭据文件所在的目录路径（默认 ~/.config/<appDirName>）。
func ResolveConfigDir(appDirName, fileName, explicitConfigPath, envVarName string, getenv func(string) string) (string, error) {
	cfgPath, err := ResolveConfigFile(appDirName, fileName, explicitConfigPath, envVarName, getenv)
	if err != nil {
		return "", err
	}
	return filepath.Dir(cfgPath), nil
}

// ResolveCacheDir 按三端（macOS / Windows / Linux）统一优先级解析应用缓存目录绝对路径：
//  1. explicitDir（显式指定目录，如已完成合并的配置/参数路径）
//  2. envVarName 指定的环境变量（非空时）
//  3. $XDG_CACHE_HOME/<appDirName>（显式设置 XDG_CACHE_HOME 时）
//  4. ~/.cache/<appDirName>（Windows 为 %USERPROFILE%\.cache\<appDirName>）
func ResolveCacheDir(appDirName, explicitDir, envVarName string, getenv func(string) string) (string, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	if p := strings.TrimSpace(explicitDir); p != "" {
		return ExpandHome(p)
	}
	if envVarName != "" {
		if p := strings.TrimSpace(getenv(envVarName)); p != "" {
			return ExpandHome(p)
		}
	}
	if xdg := strings.TrimSpace(getenv("XDG_CACHE_HOME")); xdg != "" {
		expanded, err := ExpandHome(xdg)
		if err != nil {
			return "", err
		}
		return filepath.Join(expanded, appDirName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine user home directory: %w", err)
	}
	return filepath.Join(home, ".cache", appDirName), nil
}

// WriteFileAtomic 在目标文件同级目录创建临时文件、设置权限、落盘同步后原子重命名替换目标文件。
func WriteFileAtomic(path string, data []byte, dirPerm, filePerm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	if err := tmp.Chmod(filePerm); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := renameWithRetry(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func renameWithRetry(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil || runtime.GOOS != "windows" {
		return err
	}
	// 从外部介质复制过来的目标文件在 Windows 上可能带有只读属性，清除后再重试原子替换。
	if info, statErr := os.Stat(dst); statErr == nil && !info.IsDir() && (info.Mode()&0o200) == 0 {
		_ = os.Chmod(dst, 0o600)
	}
	for range 3 {
		time.Sleep(5 * time.Millisecond)
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
	}
	return err
}

// HasBroadPermissions 判断类 Unix 系统下敏感文件是否向组或其他用户暴露了读写执行权限（宽于 0600）。
func HasBroadPermissions(mode os.FileMode) bool {
	return runtime.GOOS != "windows" && (mode.Perm()&0o077) != 0
}
