//go:build linux

package i18n

import (
	"os"
	"strings"
)

// detectPlatformLocaleTags 在 Linux 无环境变量时读取 /etc/locale.conf。
func detectPlatformLocaleTags() []string {
	data, err := os.ReadFile("/etc/locale.conf")
	if err != nil {
		return nil
	}
	values := make(map[string]string)
	for line := range strings.SplitSeq(string(data), "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(val), `"'`)
		}
	}
	var out []string
	for _, k := range []string{"LC_ALL", "LC_MESSAGES", "LANGUAGE", "LANG"} {
		if v := values[k]; v != "" && !isNeutralOrCPosix(v) {
			out = append(out, v)
		}
	}
	return out
}
