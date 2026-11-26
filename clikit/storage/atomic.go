package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// StripUTF8BOM 去除跨平台复制或编辑可能引入的 UTF-8 BOM 前缀。
func StripUTF8BOM(data []byte) []byte {
	return bytes.TrimPrefix(data, utf8BOM)
}

// WriteFileAtomic 在目标文件同级目录创建临时文件、设置权限、落盘同步后原子重命名替换目标文件。
func WriteFileAtomic(path string, data []byte, dirPerm, filePerm os.FileMode) error {
	return WriteFileAtomicWithHook(path, data, dirPerm, filePerm, nil)
}

// WritePrivateFile 以仅当前用户可读写权限原子写入敏感或私有文件：
// 在临时文件关闭落盘后、原子替换目标前收紧系统文件权限（Unix 0600，Windows NTFS 受保护 DACL）。
func WritePrivateFile(path string, data []byte) error {
	return WriteFileAtomicWithHook(path, data, 0o700, 0o600, RestrictPrivateFile)
}

// WriteFileAtomicWithHook 与 WriteFileAtomic 相同，但在临时文件关闭落盘后、原子重命名之前执行可选钩子
// （例如在 Windows NTFS 上提前收紧 Protected DACL，消除重命名后的权限暴露窗口；钩子报错时清理临时文件且不覆盖原目标）。
func WriteFileAtomicWithHook(path string, data []byte, dirPerm, filePerm os.FileMode, beforeRename func(tmpPath string) error) error {
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
	if beforeRename != nil {
		if err := beforeRename(tmpPath); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
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
	// 仅对常规文件清除可能从外部介质带入的只读属性，绝不跟随目录或重解析点。
	clearReadOnlyIfRegular(dst)
	for range 3 {
		time.Sleep(5 * time.Millisecond)
		if err = os.Rename(src, dst); err == nil {
			return nil
		}
	}
	return err
}
