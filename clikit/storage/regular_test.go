package storage

import (
	"bytes"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestReadRegularFileBoundedAndNoFollow(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.txt")
	want := []byte("hello-regular-file\n")
	if err := WritePrivateFile(path, want); err != nil {
		t.Fatalf("WritePrivateFile: %v", err)
	}

	t.Run("regular empty and max int64 bounds", func(t *testing.T) {
		got, info, err := ReadRegularFileBoundedWithInfo(path, int64(len(want)))
		if err != nil {
			t.Fatalf("ReadRegularFileBoundedWithInfo: %v", err)
		}
		if !bytes.Equal(got, want) || info == nil || !info.Mode().IsRegular() || info.Size() != int64(len(want)) {
			t.Fatalf("ReadRegularFileBoundedWithInfo = (%q, %v), want %q", got, info, want)
		}

		gotMax, err := ReadRegularFileBounded(path, math.MaxInt64)
		if err != nil || !bytes.Equal(gotMax, want) {
			t.Fatalf("ReadRegularFileBounded(MaxInt64) = (%q, %v)", gotMax, err)
		}

		f, _, err := OpenRegularFileNoFollow(path)
		if err != nil {
			t.Fatalf("OpenRegularFileNoFollow: %v", err)
		}
		gotOpen, err := ReadOpenRegularFileBounded(f, int64(len(want)))
		_ = f.Close()
		if err != nil || !bytes.Equal(gotOpen, want) {
			t.Fatalf("ReadOpenRegularFileBounded = (%q, %v)", gotOpen, err)
		}
		if _, err := ReadOpenRegularFileBounded(nil, 64); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("ReadOpenRegularFileBounded(nil) err = %v, want os.ErrInvalid", err)
		}

		emptyPath := filepath.Join(dir, "empty.txt")
		if err := WritePrivateFile(emptyPath, nil); err != nil {
			t.Fatalf("WritePrivateFile empty: %v", err)
		}
		gotEmpty, emptyInfo, err := ReadRegularFileBoundedWithInfo(emptyPath, 0)
		if err != nil || len(gotEmpty) != 0 || emptyInfo == nil || !emptyInfo.Mode().IsRegular() {
			t.Fatalf("ReadRegularFileBoundedWithInfo(empty, 0) = (%q, %v, %v)", gotEmpty, emptyInfo, err)
		}
	})

	t.Run("negative and oversized bounds", func(t *testing.T) {
		if _, _, err := ReadRegularFileBoundedWithInfo(path, -1); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("negative bound err = %v, want os.ErrInvalid", err)
		}
		if _, _, err := ReadRegularFileBoundedWithInfo(path, 0); !errors.Is(err, ErrFileTooLarge) || !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("zero bound on non-empty file err = %v, want ErrFileTooLarge + os.ErrInvalid", err)
		}
		if _, _, err := ReadRegularFileBoundedWithInfo(path, int64(len(want)-1)); !errors.Is(err, ErrFileTooLarge) || !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("oversized file err = %v, want ErrFileTooLarge + os.ErrInvalid", err)
		}
	})

	t.Run("missing directory and symlinks", func(t *testing.T) {
		missing := filepath.Join(dir, "missing.txt")
		assertOpenRegularErr(t, missing, fs.ErrNotExist)
		if _, err := ReadRegularFileBounded(missing, 64); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("missing file err = %v, want fs.ErrNotExist", err)
		}

		assertOpenRegularErr(t, dir, os.ErrInvalid)
		if _, err := ReadRegularFileBounded(dir, 64); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("directory err = %v, want os.ErrInvalid", err)
		}

		linkPath := filepath.Join(dir, "link.txt")
		if err := os.Symlink(path, linkPath); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symlink creation unavailable on Windows: %v", err)
			}
			t.Fatalf("Symlink: %v", err)
		}
		assertOpenRegularErr(t, linkPath, os.ErrInvalid)
		if _, err := ReadRegularFileBounded(linkPath, 64); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("symlink err = %v, want os.ErrInvalid", err)
		}

		danglingLink := filepath.Join(dir, "dangling.txt")
		if err := os.Symlink(filepath.Join(dir, "absent.txt"), danglingLink); err != nil {
			t.Fatalf("dangling Symlink: %v", err)
		}
		assertOpenRegularErr(t, danglingLink, os.ErrInvalid)
		if _, err := ReadRegularFileBounded(danglingLink, 64); !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("dangling symlink err = %v, want os.ErrInvalid", err)
		}
	})
}

func TestReadRegularFileBoundedConcurrentAtomicReplace(t *testing.T) {
	const (
		readerWorkers = 4
		iterations    = 60
		maxReadBytes  = 4096
		payloadA      = "{\"schemaVersion\":1,\"data\":{\"state\":\"alpha\"}}\n"
		payloadB      = "{\"schemaVersion\":1,\"data\":{\"state\":\"bravo-longer\"}}\n"
	)

	dir := t.TempDir()
	target := filepath.Join(dir, "secrets.json")
	if err := WritePrivateFile(target, []byte(payloadA)); err != nil {
		t.Fatalf("initial WritePrivateFile: %v", err)
	}

	// 验证高频原子替换与并发读取之间不存在 Lstat/open TOCTOU 导致的 os.ErrInvalid 误报或撕裂读。
	var (
		done   atomic.Bool
		reads  atomic.Int64
		writes atomic.Int64
		wg     sync.WaitGroup
	)
	errCh := make(chan error, readerWorkers)
	reportErr := func(err error) {
		select {
		case errCh <- err:
		default:
		}
	}
	stopReaders := sync.OnceFunc(func() {
		done.Store(true)
		wg.Wait()
	})
	defer stopReaders()

	for range readerWorkers {
		wg.Go(func() {
			for !done.Load() {
				raw, info, err := ReadRegularFileBoundedWithInfo(target, maxReadBytes)
				if err != nil {
					if !isTransientWindowsContention(err) {
						reportErr(err)
						return
					}
					yieldWindowsContention()
					continue
				}
				if info == nil || (string(raw) != payloadA && string(raw) != payloadB) {
					reportErr(errors.New("unexpected partial or mismatched payload during concurrent replace"))
					return
				}
				reads.Add(1)
				yieldWindowsContention()
			}
		})
	}

	lastWritten := payloadA
	for i := range iterations {
		body := payloadA
		if i%2 == 1 {
			body = payloadB
		}
		if err := WritePrivateFile(target, []byte(body)); err != nil {
			if isTransientWindowsRenameContention(err) {
				continue
			}
			t.Fatalf("WritePrivateFile iteration %d: %v", i, err)
		}
		lastWritten = body
		writes.Add(1)
	}
	stopReaders()

	select {
	case err := <-errCh:
		t.Fatalf("concurrent ReadRegularFileBoundedWithInfo failed: %v", err)
	default:
	}
	if reads.Load() == 0 || writes.Load() == 0 {
		t.Fatalf("expected concurrent reads and writes to execute, got reads=%d writes=%d", reads.Load(), writes.Load())
	}

	finalRaw, _, err := ReadRegularFileBoundedWithInfo(target, maxReadBytes)
	if err != nil || string(finalRaw) != lastWritten {
		t.Fatalf("final ReadRegularFileBoundedWithInfo = (%q, %v), want %q", string(finalRaw), err, lastWritten)
	}
}

func assertOpenRegularErr(t *testing.T, path string, want error) {
	t.Helper()
	f, _, err := OpenRegularFileNoFollow(path)
	if f != nil {
		_ = f.Close()
	}
	if !errors.Is(err, want) {
		t.Fatalf("OpenRegularFileNoFollow(%q) err = %v, want %v", path, err, want)
	}
}

// isTransientWindowsContention 仅识别 Windows 并发句柄竞争导致的瞬态共享/拒绝访问错误，
// 避免掩盖 os.ErrInvalid、fs.ErrNotExist 或其他真实回归。
func isTransientWindowsContention(err error) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	const (
		winErrSharingViolation syscall.Errno = 32 // ERROR_SHARING_VIOLATION
		winErrLockViolation    syscall.Errno = 33 // ERROR_LOCK_VIOLATION
	)
	return errors.Is(err, winErrSharingViolation) || errors.Is(err, winErrLockViolation)
}

// isTransientWindowsRenameContention 仅放行 Windows 上由 os.Rename（MoveFileExW）触发的句柄占用错误，
// 绝不掩盖临时文件创建或 ACL 收紧（chmod）等真实失败。
func isTransientWindowsRenameContention(err error) bool {
	if !isTransientWindowsContention(err) {
		return false
	}
	linkErr, ok := errors.AsType[*os.LinkError](err)
	return ok && linkErr.Op == "rename"
}

// yieldWindowsContention 在 Windows 上短暂让步，避免高频读循环持续占用目标句柄导致 MoveFileExW 饥饿。
func yieldWindowsContention() {
	if runtime.GOOS == "windows" {
		time.Sleep(2 * time.Millisecond)
	}
}
