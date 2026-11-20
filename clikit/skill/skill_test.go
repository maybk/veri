package skill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
)

func TestResolveAndInstallSkill(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	work := filepath.Join(t.TempDir(), "work")

	cases := []struct {
		opts InstallOptions
		want string
	}{
		{
			opts: InstallOptions{HomeDir: home},
			want: filepath.Join(home, ".agents", "skills", "demo", "SKILL.md"),
		},
		{
			opts: InstallOptions{Project: true, WorkDir: work},
			want: filepath.Join(work, ".agents", "skills", "demo", "SKILL.md"),
		},
		{
			opts: InstallOptions{Dir: filepath.Join(work, "custom")},
			want: filepath.Join(work, "custom", "demo", "SKILL.md"),
		},
		{
			opts: InstallOptions{Dir: filepath.Join(work, "custom", "demo")},
			want: filepath.Join(work, "custom", "demo", "SKILL.md"),
		},
		{
			opts: InstallOptions{Dir: filepath.Join(work, "custom", "SKILL.md")},
			want: filepath.Join(work, "custom", "SKILL.md"),
		},
	}
	for _, tc := range cases {
		got, err := ResolveInstallPath("demo", tc.opts)
		if err != nil || got != tc.want {
			t.Fatalf("ResolveInstallPath(%+v) = (%q, %v), want %q", tc.opts, got, err, tc.want)
		}
	}

	memFS := fstest.MapFS{
		"skills/demo/SKILL.md": &fstest.MapFile{Data: []byte("---\nname: demo\n---\n# Demo Skill\n")},
	}
	dest, err := Install(memFS, "skills/demo/SKILL.md", "demo", InstallOptions{HomeDir: home})
	if err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	raw, err := os.ReadFile(dest)
	if err != nil || string(raw) != "---\nname: demo\n---\n# Demo Skill\n" {
		t.Fatalf("installed SKILL.md mismatch: %q err=%v", string(raw), err)
	}
	if dupDest, err := Install(memFS, "skills/demo/SKILL.md", "demo", InstallOptions{HomeDir: home}); !errors.Is(err, ErrAlreadyExists) || dupDest != dest {
		t.Fatalf("expected ErrAlreadyExists with dest=%q, got (%q, %v)", dest, dupDest, err)
	}
	if _, err := ResolveInstallPath("../evil", InstallOptions{HomeDir: home}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for path traversal skillName, got %v", err)
	}
	if _, err := Install(memFS, "skills/demo/SKILL.md", "demo", InstallOptions{Dir: "bad\x00dir"}); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("expected ErrInvalidPath for NUL byte in dir, got %v", err)
	}
	if _, err := Read(nil, "skills/demo/SKILL.md"); err == nil {
		t.Fatal("expected nil assets error")
	}
	if _, err := Install(memFS, "skills/demo/SKILL.md", "demo", InstallOptions{HomeDir: home, Force: true}); err != nil {
		t.Fatalf("Install with Force failed: %v", err)
	}
}
