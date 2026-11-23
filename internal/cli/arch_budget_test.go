package cli

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	maxGoSourceLines = 500
	maxGoTestLines   = 800
)

var (
	requiredRootGoFiles = []string{"main.go", "main_test.go"}
	goBudgetDirs        = []string{"clikit", "internal"}
	// disallowedGoFilenamePattern intercepts generic junk-drawer or numbered/split filenames
	// (including OS/arch-suffixed variants such as extra_windows.go, client_extra_test.go, or client_2_linux_amd64.go)
	// while permitting legitimate protocol/version identifiers (e.g. ipv4.go, socks5.go, http2.go, sha256.go).
	disallowedGoFilenamePattern = regexp.MustCompile(
		`(?i)(?:^|[_-])(?:extra|misc)(?:_(?:darwin|linux|windows|unix|other|unsupported|purego))?(?:_(?:amd64|arm64|386|arm))?(?:_test)?\.go$` +
			`|[_-](?:part\d+|split\d*|\d+)(?:_(?:darwin|linux|windows|unix|other|unsupported|purego))?(?:_(?:amd64|arm64|386|arm))?(?:_test)?\.go$`,
	)
)

// TestGoFileLineBudgetAndNaming 校验手写 Go 源码与测试文件的正向白名单行数预算、标准生成文件豁免与职责命名规范。
func TestGoFileLineBudgetAndNaming(t *testing.T) {
	repoRoot := archRepoRoot()
	checkedCount := 0
	seen := make(map[string]bool)

	checkFile := func(path string) bool {
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			t.Fatalf("rel %s: %v", path, err)
		}
		rel := filepath.ToSlash(relPath)
		if seen[rel] {
			return false
		}
		seen[rel] = true

		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		generated, err := isGeneratedGoFile(path, raw)
		if err != nil {
			t.Fatalf("parse header of %s: %v", rel, err)
		}
		if generated {
			return false
		}
		checkedCount++

		base := filepath.Base(path)
		if isDisallowedGoFilename(base) {
			t.Errorf("%s uses disallowed generic or numbered/split filename; name Go files by domain responsibility", rel)
		}

		limit := maxGoSourceLines
		if strings.HasSuffix(base, "_test.go") {
			limit = maxGoTestLines
		}
		if lines := countTextLines(raw); lines > limit {
			t.Errorf(
				"%s has %d lines (limit %d); split by responsibility within the same package "+
					"(or move large test fixtures to testdata/) without stripping comments, compacting lines, or using generic/numbered names",
				rel,
				lines,
				limit,
			)
		}
		return true
	}

	for _, relFile := range requiredRootGoFiles {
		path := filepath.Join(repoRoot, relFile)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat required Go entrypoint %s: %v", relFile, err)
		}
		if info.IsDir() {
			t.Fatalf("expected Go file at %s, got directory", relFile)
		}
		checkFile(path)
	}

	rootEntries, err := os.ReadDir(repoRoot)
	if err != nil {
		t.Fatalf("readdir repo root: %v", err)
	}
	for _, entry := range rootEntries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		checkFile(filepath.Join(repoRoot, entry.Name()))
	}

	for _, relDir := range goBudgetDirs {
		dir := filepath.Join(repoRoot, filepath.FromSlash(relDir))
		dirChecked := 0
		walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, wErr error) error {
			if wErr != nil {
				return wErr
			}
			if d.IsDir() {
				name := d.Name()
				if path != dir && (strings.HasPrefix(name, ".") || name == "testdata") {
					return filepath.SkipDir
				}
				if path != dir {
					if _, statErr := os.Stat(filepath.Join(path, "go.mod")); statErr == nil {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			if checkFile(path) {
				dirChecked++
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk %s: %v", relDir, walkErr)
		}
		if dirChecked == 0 {
			t.Fatalf("expected Go file budget check to inspect at least one Go file under %s", relDir)
		}
	}

	if checkedCount == 0 {
		t.Fatal("expected Go file budget check to inspect at least one Go file")
	}
}

func countTextLines(raw []byte) int {
	if len(raw) == 0 {
		return 0
	}
	trimmed := raw
	if bytes.HasSuffix(trimmed, []byte("\r\n")) {
		trimmed = trimmed[:len(trimmed)-2]
	} else if bytes.HasSuffix(trimmed, []byte("\n")) {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return bytes.Count(trimmed, []byte("\n")) + 1
}

func isGeneratedGoFile(path string, raw []byte) (bool, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, raw, parser.ParseComments|parser.PackageClauseOnly)
	if err != nil {
		return false, err
	}
	return ast.IsGenerated(file), nil
}

func isDisallowedGoFilename(base string) bool {
	return disallowedGoFilenamePattern.MatchString(base)
}

func TestCountTextLines(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int
	}{
		{name: "empty", raw: "", want: 0},
		{name: "only_lf", raw: "\n", want: 1},
		{name: "only_crlf", raw: "\r\n", want: 1},
		{name: "single_no_newline", raw: "package a", want: 1},
		{name: "single_lf", raw: "package a\n", want: 1},
		{name: "single_crlf", raw: "package a\r\n", want: 1},
		{name: "two_lines_trailing_lf", raw: "a\nb\n", want: 2},
		{name: "two_lines_trailing_crlf", raw: "a\r\nb\r\n", want: 2},
		{name: "trailing_blank_line", raw: "a\n\n", want: 2},
		{name: "exact_500_lines_with_trailing_lf", raw: strings.Repeat("x\n", 500), want: 500},
		{name: "exact_800_lines_with_trailing_crlf", raw: strings.Repeat("x\r\n", 800), want: 800},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countTextLines([]byte(tc.raw)); got != tc.want {
				t.Fatalf("countTextLines(%q) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}

func TestIsGeneratedGoFile(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "standard_generated_header",
			src:  "// Code generated by stringer; DO NOT EDIT.\n\npackage sample\n",
			want: true,
		},
		{
			name: "windows_crlf_generated_header",
			src:  "// Code generated by tool; DO NOT EDIT.\r\n\r\npackage sample\r\n",
			want: true,
		},
		{
			name: "normal_handwritten_file",
			src:  "// Package sample handles normal logic.\npackage sample\n",
			want: false,
		},
		{
			name: "non_standard_comment_not_exempt",
			src:  "// generated manually, do not edit\npackage sample\n",
			want: false,
		},
		{
			name: "generated_marker_after_package_clause_not_exempt",
			src:  "package sample\n// Code generated by tool; DO NOT EDIT.\n",
			want: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := isGeneratedGoFile("sample.go", []byte(tc.src))
			if err != nil {
				t.Fatalf("isGeneratedGoFile error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("isGeneratedGoFile() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestIsDisallowedGoFilename(t *testing.T) {
	disallowed := []string{
		"extra.go",
		"extra_test.go",
		"extra_windows.go",
		"extra_unix_test.go",
		"client_extra.go",
		"client_extra_test.go",
		"misc.go",
		"misc_test.go",
		"misc_darwin.go",
		"router_misc_test.go",
		"client_2.go",
		"client-2.go",
		"client_2_linux.go",
		"client_2_windows_amd64.go",
		"router_part1.go",
		"router_part2_test.go",
		"router_part1_windows_test.go",
		"fusion_split.go",
		"fusion_split_test.go",
	}
	for _, name := range disallowed {
		if !isDisallowedGoFilename(name) {
			t.Errorf("expected %q to be disallowed", name)
		}
	}

	allowed := []string{
		"main.go",
		"main_test.go",
		"fusion_test.go",
		"ipv4.go",
		"ipv4_windows.go",
		"ipv6_test.go",
		"socks5.go",
		"http2_client.go",
		"sha256.go",
		"key_protection_other.go",
		"system_windows.go",
		"arch_budget_test.go",
	}
	for _, name := range allowed {
		if isDisallowedGoFilename(name) {
			t.Errorf("expected %q to be allowed", name)
		}
	}
}
