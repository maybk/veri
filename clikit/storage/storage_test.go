package storage

import (
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
}
