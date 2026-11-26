//go:build !unix && !windows

package storage

import "os"

func openRegularNoFollow(path string) (*os.File, os.FileInfo, error) {
	linfo, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if linfo.Mode()&os.ModeSymlink != 0 || !linfo.Mode().IsRegular() {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	//nolint:gosec // G304: 非 Unix/Windows 兜底路径，打开后立即复核 f.Stat 与 os.SameFile 身份一致
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || !os.SameFile(linfo, info) {
		_ = f.Close()
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	return f, info, nil
}
