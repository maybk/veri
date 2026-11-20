package skill

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"veri/clikit/storage"
)

// InstallOptions 配置 SKILL.md 的本地安装位置与覆盖策略。
// 默认写入开放标准的 .agents/skills/<name>/SKILL.md 目录；多宿主统一管理推荐由外部 npx skills 接管。
type InstallOptions struct {
	Dir     string // 显式自定义目录或 SKILL.md 文件路径（非空时优先于默认 .agents/skills）
	Project bool   // true 安装到工作目录 .agents/skills，false 安装到用户主目录 ~/.agents/skills
	Force   bool   // true 允许覆盖已有 SKILL.md
	HomeDir string // 可选覆盖用户主目录（供测试注入）
	WorkDir string // 可选覆盖工作目录（供测试注入）
}

// ErrAlreadyExists 表示目标路径已存在 SKILL.md 且未启用 Force 覆盖选项。
var ErrAlreadyExists = errors.New("skill file already exists")

// ErrInvalidPath 表示传入了非法的技能名称或目标路径。
var ErrInvalidPath = errors.New("invalid skill name or install path")

// Read 从给定的静态资产文件系统（fs.FS）读取指定路径的 SKILL.md 文本。
func Read(assets fs.FS, assetPath string) (string, error) {
	if assets == nil {
		return "", errors.New("skill assets filesystem is not configured")
	}
	raw, err := fs.ReadFile(assets, strings.TrimPrefix(strings.TrimSpace(assetPath), "/"))
	if err != nil {
		return "", fmt.Errorf("read embedded skill %q: %w", assetPath, err)
	}
	return string(raw), nil
}

// ResolveInstallPath 解析跨平台（macOS / Windows / Linux）目标 SKILL.md 文件绝对路径。
func ResolveInstallPath(skillName string, opts InstallOptions) (string, error) {
	dirName := strings.TrimSpace(skillName)
	if dirName == "" || dirName == "." || dirName == ".." ||
		strings.ContainsAny(dirName, "/\\\x00") ||
		filepath.IsAbs(dirName) ||
		filepath.VolumeName(dirName) != "" ||
		filepath.Base(dirName) != dirName {
		return "", fmt.Errorf("%w: invalid skill name %q", ErrInvalidPath, skillName)
	}

	if custom := strings.TrimSpace(opts.Dir); custom != "" {
		if strings.ContainsRune(custom, '\x00') {
			return "", fmt.Errorf("%w: directory contains NUL byte", ErrInvalidPath)
		}
		expanded, err := storage.ExpandHome(custom)
		if err != nil {
			return "", fmt.Errorf("%w: %w", ErrInvalidPath, err)
		}
		cleaned := filepath.Clean(expanded)
		if strings.EqualFold(filepath.Base(cleaned), "SKILL.md") {
			return cleaned, nil
		}
		if strings.EqualFold(filepath.Base(cleaned), dirName) {
			return filepath.Join(cleaned, "SKILL.md"), nil
		}
		return filepath.Join(cleaned, dirName, "SKILL.md"), nil
	}

	var base string
	if opts.Project {
		wd := strings.TrimSpace(opts.WorkDir)
		if wd == "" {
			var err error
			wd, err = os.Getwd()
			if err != nil {
				return "", fmt.Errorf("resolve working directory: %w", err)
			}
		}
		base = wd
	} else {
		home := strings.TrimSpace(opts.HomeDir)
		if home == "" {
			var err error
			home, err = os.UserHomeDir()
			if err != nil {
				return "", fmt.Errorf("resolve user home directory: %w", err)
			}
		}
		base = home
	}

	return filepath.Join(base, ".agents", "skills", dirName, "SKILL.md"), nil
}

// Install 从 assets 读取 assetPath 对应的 SKILL.md 并原子写入解析后的目标路径。
// 若目标文件已存在且 Force=false，返回已解析的目标路径与包装了 ErrAlreadyExists 的错误。
func Install(assets fs.FS, assetPath, skillName string, opts InstallOptions) (string, error) {
	content, err := Read(assets, assetPath)
	if err != nil {
		return "", err
	}
	dest, err := ResolveInstallPath(skillName, opts)
	if err != nil {
		return "", err
	}
	if !opts.Force {
		if _, statErr := os.Stat(dest); statErr == nil {
			return dest, fmt.Errorf("%w at %q (use --force to overwrite)", ErrAlreadyExists, dest)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", fmt.Errorf("stat %q: %w", dest, statErr)
		}
	}
	if err := storage.WriteFileAtomic(dest, []byte(content), 0o755, 0o644); err != nil {
		return "", fmt.Errorf("write skill file %q: %w", dest, err)
	}
	return dest, nil
}
