package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
