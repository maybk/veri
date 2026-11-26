package storage

import (
	"errors"
	"fmt"
	"io"
	"math"
	"os"
)

// ErrFileTooLarge 表示常规文件体积超过调用方允许的最大字节上限。
var ErrFileTooLarge = errors.New("file exceeds size limit")

// OpenRegularFileNoFollow 以不跟随符号链接与重解析点的方式原子只读打开常规文件，
// 拒绝目录、命名管道、符号链接与重解析点，并返回与句柄同源的文件元信息（无 Lstat/open 竞态窗口）。
func OpenRegularFileNoFollow(path string) (*os.File, os.FileInfo, error) {
	return openRegularNoFollow(path)
}

// ReadRegularFileBounded 防符号链接/重解析点打开并读取不超过 maxBytes 的常规文件，拒绝目录、管道与超大文件。
func ReadRegularFileBounded(path string, maxBytes int64) ([]byte, error) {
	data, _, err := ReadRegularFileBoundedWithInfo(path, maxBytes)
	return data, err
}

// ReadRegularFileBoundedWithInfo 与 ReadRegularFileBounded 相同，在同一次句柄读取中返回文件元信息
// 并在返回前显式关闭句柄，避免二次 Stat 的 TOCTOU 窗口及 Windows 后续原子替换时的句柄占用。
func ReadRegularFileBoundedWithInfo(path string, maxBytes int64) ([]byte, os.FileInfo, error) {
	if maxBytes < 0 {
		return nil, nil, &os.PathError{Op: "open", Path: path, Err: os.ErrInvalid}
	}
	f, info, err := OpenRegularFileNoFollow(path)
	if err != nil {
		return nil, nil, err
	}
	closed := false
	defer func() {
		if !closed {
			_ = f.Close()
		}
	}()

	raw, err := readOpenRegularBoundedWithInfo(f, info, maxBytes)
	if err != nil {
		return nil, nil, &os.PathError{Op: "read", Path: path, Err: err}
	}
	closed = true
	if err := f.Close(); err != nil {
		clear(raw)
		return nil, nil, err
	}
	return raw, info, nil
}

// ReadOpenRegularFileBounded 从已打开句柄读取不超过 maxBytes 的常规文件。
func ReadOpenRegularFileBounded(f *os.File, maxBytes int64) ([]byte, error) {
	if f == nil || maxBytes < 0 {
		return nil, os.ErrInvalid
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return readOpenRegularBoundedWithInfo(f, info, maxBytes)
}

func readOpenRegularBoundedWithInfo(f *os.File, info os.FileInfo, maxBytes int64) ([]byte, error) {
	if maxBytes < 0 || !info.Mode().IsRegular() {
		return nil, os.ErrInvalid
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%w: %w", os.ErrInvalid, ErrFileTooLarge)
	}
	readLimit := maxBytes
	if readLimit < math.MaxInt64 {
		readLimit++
	}
	raw, err := io.ReadAll(io.LimitReader(f, readLimit))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		clear(raw)
		return nil, fmt.Errorf("%w: %w", os.ErrInvalid, ErrFileTooLarge)
	}
	return raw, nil
}
