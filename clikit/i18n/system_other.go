//go:build !darwin && !linux && !windows

package i18n

func detectPlatformLocaleTags() []string {
	return nil
}
