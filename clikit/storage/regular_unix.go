//go:build unix

package storage

import (
	"errors"
	"os"
	"syscall"
)

func openRegularNoFollow(path string) (*os.File, os.FileInfo, error) {
	//nolint:gosec // G304: 由调用方传入，经 O_NOFOLLOW、O_NONBLOCK 与 f.Stat 常规文件校验约束
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		if errors.Is(err, syscall.ELOOP) || errors.Is(err, syscall.EMLINK) {
			return nil, nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
		}
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	return f, info, nil
}
