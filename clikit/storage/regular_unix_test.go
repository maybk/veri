//go:build unix

package storage

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestUnixOpenRegularRejectsFIFOWithoutBlocking(t *testing.T) {
	fifoPath := filepath.Join(t.TempDir(), "blocked.fifo")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}

	errCh := make(chan error, 1)
	go func() {
		f, _, err := OpenRegularFileNoFollow(fifoPath)
		if f != nil {
			_ = f.Close()
		}
		errCh <- err
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, os.ErrInvalid) {
			t.Fatalf("OpenRegularFileNoFollow(fifo) err = %v, want os.ErrInvalid", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OpenRegularFileNoFollow blocked on FIFO instead of returning immediately")
	}

	if _, err := ReadRegularFileBounded(fifoPath, 64); !errors.Is(err, os.ErrInvalid) {
		t.Fatalf("ReadRegularFileBounded(fifo) err = %v, want os.ErrInvalid", err)
	}
}
