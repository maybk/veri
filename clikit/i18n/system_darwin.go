//go:build darwin

package i18n

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// detectPlatformLocaleTags 在 macOS 终端未设置 LANG 等环境变量时，零 CGO 读取系统 AppleLanguages 偏好。
func detectPlatformLocaleTags() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	out, err := exec.CommandContext(ctx, "/usr/bin/defaults", "read", "-g", "AppleLanguages").Output()
	if err != nil {
		return nil
	}
	var tags []string
	for line := range strings.SplitSeq(string(out), "\n") {
		trimmed := strings.Trim(strings.TrimSpace(line), `"(),; `)
		if trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	return tags
}
