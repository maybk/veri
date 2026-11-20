package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func buildTestTarGz(t *testing.T, dest string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatalf("create tar.gz: %v", err)
	}
	defer func() { _ = f.Close() }()

	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for name, data := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o755,
			Size: int64(len(data)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("write tar body: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
}

func buildTestZip(t *testing.T, dest string, files map[string][]byte) {
	t.Helper()
	f, err := os.Create(dest)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer func() { _ = f.Close() }()

	zw := zip.NewWriter(f)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
}

func TestExtractBinarySecurityAndFormats(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// 1. 正常 tar.gz 提取根目录 veri，并忽略子目录同名文件
	tarPath := filepath.Join(dir, "veri-0.2.0-linux-amd64.tar.gz")
	buildTestTarGz(t, tarPath, map[string][]byte{
		"sub/veri": []byte("nested-fake"),
		"./veri":   []byte("real-linux-bin"),
		"README":   []byte("readme"),
	})
	outBin := filepath.Join(dir, "out-linux")
	if err := ExtractBinary(tarPath, "linux", outBin); err != nil {
		t.Fatalf("ExtractBinary tar.gz failed: %v", err)
	}
	got, err := os.ReadFile(outBin)
	if err != nil || string(got) != "real-linux-bin" {
		t.Fatalf("extracted tar.gz = %q, err=%v", got, err)
	}

	// 2. 拒绝仅包含嵌套路径或路径穿越的归档
	badTar := filepath.Join(dir, "bad.tar.gz")
	buildTestTarGz(t, badTar, map[string][]byte{
		"../veri":  []byte("traversal"),
		"sub/veri": []byte("nested"),
	})
	if err := ExtractBinary(badTar, "linux", filepath.Join(dir, "bad-out")); err == nil {
		t.Fatal("expected error when archive has no root binary")
	}

	// 3. 正常 zip 提取根目录 veri.exe
	zipPath := filepath.Join(dir, "veri-0.2.0-windows-amd64.zip")
	buildTestZip(t, zipPath, map[string][]byte{
		"nested/veri.exe": []byte("nested-exe"),
		"veri.exe":        []byte("real-win-exe"),
	})
	outWin := filepath.Join(dir, "out-win.exe")
	if err := ExtractBinary(zipPath, "windows", outWin); err != nil {
		t.Fatalf("ExtractBinary zip failed: %v", err)
	}
	gotWin, err := os.ReadFile(outWin)
	if err != nil || string(gotWin) != "real-win-exe" {
		t.Fatalf("extracted zip = %q, err=%v", gotWin, err)
	}

	// 4. 拒绝包含重复根目标条目的归档
	dupZip := filepath.Join(dir, "dup.zip")
	f, err := os.Create(dupZip)
	if err != nil {
		t.Fatalf("create dup zip: %v", err)
	}
	zw := zip.NewWriter(f)
	for _, name := range []string{"veri.exe", "./veri.exe"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte("payload"))
	}
	_ = zw.Close()
	_ = f.Close()
	if err := ExtractBinary(dupZip, "windows", filepath.Join(dir, "dup-out.exe")); err == nil {
		t.Fatal("expected error for duplicate binary entries")
	}
}

func TestReplaceExecutableUnixAndWindowsBackup(t *testing.T) {
	t.Parallel()

	// Unix 替换完成后应立即清理 .old 备份
	unixDir := t.TempDir()
	targetUnix := filepath.Join(unixDir, "veri")
	stagedUnix := filepath.Join(unixDir, "staged")
	if err := os.WriteFile(targetUnix, []byte("old-binary"), 0o755); err != nil {
		t.Fatalf("write old binary: %v", err)
	}
	if err := os.WriteFile(stagedUnix, []byte("new-binary"), 0o700); err != nil {
		t.Fatalf("write staged binary: %v", err)
	}
	if err := ReplaceExecutable(targetUnix, stagedUnix, "linux"); err != nil {
		t.Fatalf("ReplaceExecutable(linux) failed: %v", err)
	}
	got, _ := os.ReadFile(targetUnix)
	if string(got) != "new-binary" {
		t.Fatalf("replaced content = %q", got)
	}
	if _, err := os.Stat(targetUnix + ".old"); !os.IsNotExist(err) {
		t.Fatalf("expected unix .old backup to be removed, stat err=%v", err)
	}

	// Windows 替换完成后保留 .old 备份，并由 CleanupOldBackup 在下次清理
	winDir := t.TempDir()
	targetWin := filepath.Join(winDir, "veri.exe")
	stagedWin := filepath.Join(winDir, "staged.exe")
	if err := os.WriteFile(targetWin, []byte("old-exe"), 0o755); err != nil {
		t.Fatalf("write old exe: %v", err)
	}
	if err := os.WriteFile(stagedWin, []byte("new-exe"), 0o700); err != nil {
		t.Fatalf("write staged exe: %v", err)
	}
	if err := ReplaceExecutable(targetWin, stagedWin, "windows"); err != nil {
		t.Fatalf("ReplaceExecutable(windows) failed: %v", err)
	}
	gotWin, _ := os.ReadFile(targetWin)
	if string(gotWin) != "new-exe" {
		t.Fatalf("replaced windows content = %q", gotWin)
	}
	if _, err := os.Stat(targetWin + ".old"); err != nil {
		t.Fatalf("expected windows .old backup to remain until next cleanup: %v", err)
	}
	CleanupOldBackup(targetWin)
	if _, err := os.Stat(targetWin + ".old"); !os.IsNotExist(err) {
		t.Fatalf("expected CleanupOldBackup to remove .old, stat err=%v", err)
	}
}
