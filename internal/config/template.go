package config

import (
	_ "embed"
	"errors"
	"fmt"
	"os"
	"strings"

	"veri/clikit/storage"
	"veri/internal/core"
)

//go:embed templates/zh-CN.toml
var defaultConfigTemplateZH string

//go:embed templates/en-US.toml
var defaultConfigTemplateEN string

// Template 返回带中文注释的默认 TOML 模板内容。
func Template() string {
	return defaultConfigTemplateZH
}

// TemplateForLocale 按界面语言标识（en* 返回英文注释模板，其余返回中文注释模板）返回内嵌的 TOML 模板内容。
func TemplateForLocale(locale string) string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(locale)), "en") {
		return defaultConfigTemplateEN
	}
	return defaultConfigTemplateZH
}

// WriteTemplate 将默认配置模板以 0600 权限写入目标路径；若文件已存在且 force=false 则拒绝覆盖。
func WriteTemplate(path string, force bool) error {
	return WriteTemplateForLocale(path, force, "zh-CN")
}

// WriteTemplateForLocale 按指定界面语言将内嵌配置模板以 0600 权限原子写入目标路径。
func WriteTemplateForLocale(path string, force bool, locale string) error {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return core.NewError(core.CodeInvalidArgument, fmt.Sprintf("config file already exists at %q (use --force to overwrite)", path)).
				WithHint("Pass `--force` to overwrite the existing file.").
				WithI18n("err.config_exists", "hint.use_force").
				WithParam("path", path)
		} else if !errors.Is(err, os.ErrNotExist) {
			return core.WrapError(core.CodeInternal, "failed to stat config path", err)
		}
	}
	tpl := TemplateForLocale(locale)
	if err := storage.WriteFileAtomic(path, []byte(tpl), 0o700, 0o600); err != nil {
		return core.WrapError(core.CodeInternal, fmt.Sprintf("failed to write config file %q", path), err)
	}
	return nil
}
