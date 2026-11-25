package storage

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolveConfigFilePrecedence(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	defPath, err := ResolveConfigFile("demoapp", "config.toml", "", "DEMO_CONFIG", func(string) string { return "" })
	if err != nil {
		t.Fatalf("ResolveConfigFile default failed: %v", err)
	}
	wantDef := filepath.Join(tmpHome, ".config", "demoapp", "config.toml")
	if defPath != wantDef {
		t.Fatalf("default path = %q, want %q", defPath, wantDef)
	}

	xdgDir := filepath.Join(tmpHome, "xdg")
	xdgPath, err := ResolveConfigFile("demoapp", "config.toml", "", "DEMO_CONFIG", func(k string) string {
		if k == "XDG_CONFIG_HOME" {
			return xdgDir
		}
		return ""
	})
	if err != nil || xdgPath != filepath.Join(xdgDir, "demoapp", "config.toml") {
		t.Fatalf("XDG path = (%q, %v)", xdgPath, err)
	}

	envPath, err := ResolveConfigFile("demoapp", "config.toml", "", "DEMO_CONFIG", func(k string) string {
		if k == "DEMO_CONFIG" {
			return "~/custom.toml"
		}
		return ""
	})
	if err != nil || envPath != filepath.Join(tmpHome, "custom.toml") {
		t.Fatalf("env path = (%q, %v)", envPath, err)
	}

	expPath, err := ResolveConfigFile("demoapp", "config.toml", filepath.Join(tmpHome, "flag.toml"), "DEMO_CONFIG", func(string) string {
		return "~/ignored.toml"
	})
	if err != nil || expPath != filepath.Join(tmpHome, "flag.toml") {
		t.Fatalf("explicit path = (%q, %v)", expPath, err)
	}
}

func TestWriteFileAtomicAndPermissions(t *testing.T) {
	target := filepath.Join(t.TempDir(), "nested", "secrets.json")
	if err := WriteFileAtomic(target, []byte("hello\n"), 0o700, 0o600); err != nil {
		t.Fatalf("WriteFileAtomic failed: %v", err)
	}
	raw, err := os.ReadFile(target)
	if err != nil || string(raw) != "hello\n" {
		t.Fatalf("ReadFile = (%q, %v)", string(raw), err)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if HasBroadPermissions(info.Mode()) {
		t.Fatalf("expected 0600 file not to have broad permissions, got %v", info.Mode())
	}
	if runtime.GOOS != "windows" && !HasBroadPermissions(0o644) {
		t.Fatal("expected 0644 to report broad permissions on Unix")
	}

	// 校验 WriteFileAtomicWithHook 在重命名前调用钩子；钩子失败时不得覆盖已有文件且自动清理临时文件。
	var seenTmp string
	hookErr := os.ErrPermission
	if err := WriteFileAtomicWithHook(target, []byte("updated\n"), 0o700, 0o600, func(tmpPath string) error {
		seenTmp = tmpPath
		if tmpPath == target {
			t.Fatal("beforeRename hook must run on temp path before rename")
		}
		return hookErr
	}); !errors.Is(err, hookErr) {
		t.Fatalf("expected hook error %v, got %v", hookErr, err)
	}
	if _, statErr := os.Stat(seenTmp); !os.IsNotExist(statErr) {
		t.Fatalf("expected temp file %q cleaned up on hook error, stat err=%v", seenTmp, statErr)
	}
	if afterFail, err := os.ReadFile(target); err != nil || string(afterFail) != "hello\n" {
		t.Fatalf("original file must remain untouched on hook error: (%q, %v)", string(afterFail), err)
	}

	hookRan := false
	if err := WriteFileAtomicWithHook(target, []byte("updated\n"), 0o700, 0o600, func(tmpPath string) error {
		hookRan = true
		if _, err := os.Stat(tmpPath); err != nil {
			return err
		}
		return nil
	}); err != nil || !hookRan {
		t.Fatalf("WriteFileAtomicWithHook success path failed: err=%v hookRan=%v", err, hookRan)
	}
	if afterOK, err := os.ReadFile(target); err != nil || string(afterOK) != "updated\n" {
		t.Fatalf("ReadFile after hook = (%q, %v)", string(afterOK), err)
	}
}

func TestResolveCacheDirPrecedence(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	defDir, err := ResolveCacheDir("demoapp", "", "DEMO_CACHE_DIR", func(string) string { return "" })
	if err != nil {
		t.Fatalf("ResolveCacheDir default failed: %v", err)
	}
	wantDef := filepath.Join(tmpHome, ".cache", "demoapp")
	if defDir != wantDef {
		t.Fatalf("default cache dir = %q, want %q", defDir, wantDef)
	}

	xdgDir := filepath.Join(tmpHome, "xdg-cache")
	gotXDG, err := ResolveCacheDir("demoapp", "", "DEMO_CACHE_DIR", func(k string) string {
		if k == "XDG_CACHE_HOME" {
			return xdgDir
		}
		return ""
	})
	if err != nil || gotXDG != filepath.Join(xdgDir, "demoapp") {
		t.Fatalf("XDG cache dir = (%q, %v)", gotXDG, err)
	}

	gotEnv, err := ResolveCacheDir("demoapp", "", "DEMO_CACHE_DIR", func(k string) string {
		switch k {
		case "DEMO_CACHE_DIR":
			return "~/env-cache"
		case "XDG_CACHE_HOME":
			return xdgDir
		default:
			return ""
		}
	})
	if err != nil || gotEnv != filepath.Join(tmpHome, "env-cache") {
		t.Fatalf("env cache dir = (%q, %v)", gotEnv, err)
	}

	gotExplicit, err := ResolveCacheDir("demoapp", "~/explicit-cache", "DEMO_CACHE_DIR", func(string) string {
		return "~/ignored-cache"
	})
	if err != nil || gotExplicit != filepath.Join(tmpHome, "explicit-cache") {
		t.Fatalf("explicit cache dir = (%q, %v)", gotExplicit, err)
	}
}
