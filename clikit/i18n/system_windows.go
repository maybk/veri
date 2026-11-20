//go:build windows

package i18n

import "golang.org/x/sys/windows"

// detectPlatformLocaleTags 在 Windows 下通过零 CGO 系统调用读取当前用户的界面语言列表。
func detectPlatformLocaleTags() []string {
	langs, err := windows.GetUserPreferredUILanguages(windows.MUI_LANGUAGE_NAME)
	if err != nil {
		return nil
	}
	return langs
}
