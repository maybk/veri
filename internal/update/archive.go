package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"veri/internal/core"
)

// ExtractBinary 从官方发布归档（.tar.gz 或 .zip）根目录精确提取当前平台可执行文件到 destPath。
func ExtractBinary(archivePath, goos, destPath string) error {
	targetName := BinaryNameForOS(goos)
	lower := strings.ToLower(archivePath)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractBinaryFromZip(archivePath, targetName, destPath)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractBinaryFromTarGz(archivePath, targetName, destPath)
	default:
		return core.NewError(core.CodeInternal, fmt.Sprintf("unsupported update archive format: %s", filepath.Base(archivePath))).
			WithMsgKey("err.update_install_failed")
	}
}

func extractBinaryFromZip(zipPath, targetName, destPath string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return core.WrapError(core.CodeInternal, "failed to open update zip archive", err).
			WithMsgKey("err.update_install_failed")
	}
	defer func() { _ = r.Close() }()

	found := false
	for _, f := range r.File {
		if f.FileInfo().IsDir() || f.Mode()&os.ModeSymlink != 0 {
			continue
		}
		if !isArchiveRootFile(f.Name, targetName) {
			continue
		}
		if found {
			return core.NewError(core.CodeInternal, fmt.Sprintf("duplicate %s entry in update archive", targetName)).
				WithMsgKey("err.update_install_failed")
		}
		rc, openErr := f.Open()
		if openErr != nil {
			return core.WrapError(core.CodeInternal, "failed to open binary entry in zip archive", openErr).
				WithMsgKey("err.update_install_failed")
		}
		writeErr := writeLimitedBinary(destPath, rc, 0o700)
		_ = rc.Close()
		if writeErr != nil {
			return writeErr
		}
		found = true
	}

	if !found {
		return core.NewError(core.CodeInternal, fmt.Sprintf("update archive missing root binary %s", targetName)).
			WithMsgKey("err.update_install_failed")
	}
	return nil
}

func extractBinaryFromTarGz(tarGzPath, targetName, destPath string) error {
	f, err := os.Open(tarGzPath) //nolint:gosec // tarGzPath 由当前进程创建于受控临时目录并已通过 SHA-256 校验
	if err != nil {
		return core.WrapError(core.CodeInternal, "failed to open update tar.gz archive", err).
			WithMsgKey("err.update_install_failed")
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return core.WrapError(core.CodeInternal, "failed to read gzip stream of update archive", err).
			WithMsgKey("err.update_install_failed")
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	found := false
	for {
		hdr, nextErr := tr.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return core.WrapError(core.CodeInternal, "failed to read tar entry from update archive", nextErr).
				WithMsgKey("err.update_install_failed")
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if !isArchiveRootFile(hdr.Name, targetName) {
			continue
		}
		if found {
			return core.NewError(core.CodeInternal, fmt.Sprintf("duplicate %s entry in update archive", targetName)).
				WithMsgKey("err.update_install_failed")
		}
		if err := writeLimitedBinary(destPath, tr, 0o700); err != nil {
			return err
		}
		found = true
	}

	if !found {
		return core.NewError(core.CodeInternal, fmt.Sprintf("update archive missing root binary %s", targetName)).
			WithMsgKey("err.update_install_failed")
	}
	return nil
}

// isArchiveRootFile 仅接受位于归档根目录的目标文件名（允许可选前导 "./"），拒绝任何嵌套路径或穿越路径。
func isArchiveRootFile(entryName, targetName string) bool {
	if strings.Contains(entryName, "\\") {
		return false
	}
	cleaned := path.Clean(strings.TrimPrefix(entryName, "./"))
	return cleaned == targetName && !strings.Contains(cleaned, "/")
}

func writeLimitedBinary(destPath string, r io.Reader, mode os.FileMode) error {
	out, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode) //nolint:gosec // destPath 位于受控临时目录
	if err != nil {
		return core.WrapError(core.CodeInternal, "failed to create extracted binary file", err).
			WithMsgKey("err.update_install_failed")
	}
	n, copyErr := io.Copy(out, io.LimitReader(r, maxExtractedBinaryBytes+1))
	syncErr := out.Sync()
	closeErr := out.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		_ = os.Remove(destPath)
		cause := errors.Join(copyErr, syncErr, closeErr)
		return core.WrapError(core.CodeInternal, "failed to write extracted binary", cause).
			WithMsgKey("err.update_install_failed")
	}
	if n == 0 || n > maxExtractedBinaryBytes {
		_ = os.Remove(destPath)
		return core.NewError(core.CodeInternal, fmt.Sprintf("extracted binary size (%d bytes) out of allowed bounds", n)).
			WithMsgKey("err.update_install_failed")
	}
	return nil
}

// CheckWritable 在下载前预检目标二进制所在目录是否具备写权限，避免下载完成后才失败。
func CheckWritable(targetExe string) error {
	dir := filepath.Dir(targetExe)
	probe, err := os.CreateTemp(dir, ".veri-write-check-*")
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return core.WrapError(core.CodeBlockedByPolicy, fmt.Sprintf("permission denied writing to %s", dir), err).
				WithMsgKey("err.update_permission_denied").
				WithParam("path", dir)
		}
		return core.WrapError(core.CodeInternal, fmt.Sprintf("cannot write to executable directory %s", dir), err).
			WithMsgKey("err.update_install_failed")
	}
	probeName := probe.Name()
	_ = probe.Close()
	_ = os.Remove(probeName)
	return nil
}

// ReplaceExecutable 将已校验的新二进制原子替换到 targetExe，替换失败时自动回滚原有备份。
func ReplaceExecutable(targetExe, stagedBinPath, goos string) error {
	targetDir := filepath.Dir(targetExe)
	targetMode := os.FileMode(0o755)
	if info, statErr := os.Stat(targetExe); statErr == nil && info.Mode().Perm() != 0 {
		targetMode = info.Mode().Perm() | 0o100
	}

	tmpPath, err := stageExecutableBeside(targetDir, stagedBinPath, targetMode)
	if err != nil {
		return err
	}
	cleanupTmp := true
	defer func() {
		if cleanupTmp {
			_ = os.Remove(tmpPath)
		}
	}()

	oldPath := targetExe + ".old"
	backedUp := false
	if _, statErr := os.Stat(targetExe); statErr == nil {
		_ = os.Remove(oldPath)
		if err := os.Rename(targetExe, oldPath); err != nil {
			return classifyReplaceError(targetDir, "backup current executable", err)
		}
		backedUp = true
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return classifyReplaceError(targetDir, "stat current executable", statErr)
	}

	if err := os.Rename(tmpPath, targetExe); err != nil {
		if backedUp {
			if restoreErr := os.Rename(oldPath, targetExe); restoreErr != nil {
				return core.WrapError(core.CodeInternal, "failed to replace executable and rollback failed", errors.Join(err, restoreErr)).
					WithMsgKey("err.update_rollback_failed").
					WithParam("backup", oldPath)
			}
		}
		return classifyReplaceError(targetDir, "replace executable", err)
	}
	cleanupTmp = false

	// Windows 正在运行的 .exe 重命名为 .old 后仍被系统锁定，延后至下次更新时清理。
	if backedUp && !strings.EqualFold(goos, "windows") {
		_ = os.Remove(oldPath)
	}
	return nil
}

func stageExecutableBeside(targetDir, stagedBinPath string, mode os.FileMode) (string, error) {
	in, err := os.Open(stagedBinPath) //nolint:gosec // stagedBinPath 位于当前进程创建的受控临时目录
	if err != nil {
		return "", core.WrapError(core.CodeInternal, "failed to open staged binary", err).
			WithMsgKey("err.update_install_failed")
	}
	defer func() { _ = in.Close() }()

	tmpFile, err := os.CreateTemp(targetDir, ".veri-update-*")
	if err != nil {
		return "", classifyReplaceError(targetDir, "create temporary executable", err)
	}
	tmpPath := tmpFile.Name()

	n, copyErr := io.Copy(tmpFile, io.LimitReader(in, maxExtractedBinaryBytes+1))
	syncErr := tmpFile.Sync()
	closeErr := tmpFile.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || n == 0 || n > maxExtractedBinaryBytes {
		_ = os.Remove(tmpPath)
		return "", classifyReplaceError(targetDir, "write temporary executable", errors.Join(copyErr, syncErr, closeErr))
	}
	if err := os.Chmod(tmpPath, mode); err != nil { //nolint:gosec // 可执行二进制需要保留可执行位
		_ = os.Remove(tmpPath)
		return "", classifyReplaceError(targetDir, "chmod temporary executable", err)
	}
	return tmpPath, nil
}

func classifyReplaceError(targetDir, action string, cause error) error {
	if errors.Is(cause, os.ErrPermission) {
		return core.WrapError(core.CodeBlockedByPolicy, fmt.Sprintf("permission denied while trying to %s in %s", action, targetDir), cause).
			WithHint("Check write permissions on the install directory, or install to a user-writable directory.").
			WithI18n("err.update_permission_denied", "hint.update_permission_denied").
			WithParam("path", targetDir)
	}
	return core.WrapError(core.CodeInternal, fmt.Sprintf("failed to %s", action), cause).
		WithMsgKey("err.update_install_failed")
}

// CleanupOldBackup 仅在确认当前可执行文件存在时清理上次更新残留的 .old 备份文件。
func CleanupOldBackup(targetExe string) {
	if strings.TrimSpace(targetExe) == "" {
		return
	}
	if _, err := os.Stat(targetExe); err != nil {
		return
	}
	_ = os.Remove(targetExe + ".old")
}
