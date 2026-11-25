//go:build !windows

package credentials

import (
	"os"

	"veri/clikit/storage"
)

// restrictFilePermissions 确保类 Unix 系统上的凭据文件权限收紧至 0600。
func restrictFilePermissions(path string) error {
	return os.Chmod(path, 0o600)
}

// HasBroadPermissions 检查指定凭据文件是否向当前用户以外的主体暴露了读写执行权限。
func HasBroadPermissions(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	return storage.HasBroadPermissions(info.Mode())
}
