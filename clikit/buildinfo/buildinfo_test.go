package buildinfo

import (
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"testing"
	"testing/fstest"
)

func TestParseAndLoadConfig(t *testing.T) {
	sample := "# comment\nversion: '1'\ninfo:\n  companyName: \"acme\"\n  productName: \"demo #1\"\n  description: 'Sample CLI'\n  copyright: \"(c) 2026\"\n  version: \"1.2.3\" # inline comment\n"
	meta, err := ParseConfig(sample)
	if err != nil {
		t.Fatalf("ParseConfig failed: %v", err)
	}
	if meta.Version != "1.2.3" || meta.CompanyName != "acme" || meta.ProductName != "demo #1" {
		t.Fatalf("unexpected parsed meta: %+v", meta)
	}

	if _, err := ParseConfig("version: '1'\nother:\n  version: \"1.0.0\"\n"); err == nil {
		t.Fatal("expected error when info.version is missing")
	}

	repoRoot := filepath.Join("..", "..")
	realMeta, err := LoadConfig(os.DirFS(repoRoot))
	if err != nil || realMeta.Version == "" {
		t.Fatalf("LoadConfig from repository root failed: meta=%+v err=%v", realMeta, err)
	}
}

func TestResolveAndFormatLine(t *testing.T) {
	orig := readBuildInfo
	defer func() { readBuildInfo = orig }()

	readBuildInfo = func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{
			Settings: []debug.BuildSetting{
				{Key: "vcs.revision", Value: "abcdef0123456789abcdef"},
				{Key: "vcs.time", Value: "2026-11-17T00:00:00Z"},
			},
		}, true
	}

	memFS := fstest.MapFS{
		ConfigAssetPath: &fstest.MapFile{Data: []byte("info:\n  version: \"0.4.0\"\n")},
	}
	info := Resolve(memFS, "", "none", "")
	if info.Version != "0.4.0" || info.Commit != "abcdef012345" || !strings.HasPrefix(info.BuildDate, "2026-11-17") {
		t.Fatalf("unexpected resolved info: %+v", info)
	}

	override := Resolve(memFS, "0.4.0-pre", "11223344", "2026-12-01T00:00:00Z")
	line := FormatLine("demo", override)
	if line != "demo 0.4.0-pre (11223344) built 2026-12-01T00:00:00Z" {
		t.Fatalf("FormatLine = %q", line)
	}

	fallback := Resolve(nil, "", "", "")
	if fallback.Version != DefaultDevVersion {
		t.Fatalf("expected DefaultDevVersion when assets and ldVersion are empty, got %q", fallback.Version)
	}
}
