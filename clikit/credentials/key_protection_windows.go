//go:build windows

package credentials

import (
	"errors"
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

func sealKeyBytes(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > math.MaxUint32 {
		return nil, errors.New("master key exceeds DPAPI size limit")
	}
	inBlob := windows.DataBlob{
		Size: uint32(len(raw)), //nolint:gosec // G115: 上方已校验 len(raw) <= math.MaxUint32
		Data: &raw[0],
	}
	var outBlob windows.DataBlob
	if err := windows.CryptProtectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &outBlob); err != nil {
		return nil, err
	}
	defer func() {
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data))) //nolint:gosec // G103: 释放 Win32 DPAPI 分配的输出缓冲区
	}()

	out := make([]byte, outBlob.Size)
	copy(out, unsafe.Slice(outBlob.Data, outBlob.Size)) //nolint:gosec // G103: 读取 Win32 DPAPI 输出缓冲区
	return out, nil
}

func unsealKeyBytes(sealed []byte) ([]byte, error) {
	if len(sealed) == 0 {
		return nil, nil
	}
	if len(sealed) > math.MaxUint32 {
		return nil, errors.New("sealed master key exceeds DPAPI size limit")
	}
	inBlob := windows.DataBlob{
		Size: uint32(len(sealed)), //nolint:gosec // G115: 上方已校验 len(sealed) <= math.MaxUint32
		Data: &sealed[0],
	}
	var outBlob windows.DataBlob
	if err := windows.CryptUnprotectData(&inBlob, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &outBlob); err != nil {
		return nil, err
	}
	defer func() {
		clear(unsafe.Slice(outBlob.Data, outBlob.Size))                        //nolint:gosec // G103: 归零 Win32 DPAPI 解密后的明文缓冲区
		_, _ = windows.LocalFree(windows.Handle(unsafe.Pointer(outBlob.Data))) //nolint:gosec // G103: 释放 Win32 DPAPI 分配的输出缓冲区
	}()

	out := make([]byte, outBlob.Size)
	copy(out, unsafe.Slice(outBlob.Data, outBlob.Size)) //nolint:gosec // G103: 读取 Win32 DPAPI 输出缓冲区
	return out, nil
}
