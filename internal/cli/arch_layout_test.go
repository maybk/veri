package cli

import (
	"bytes"
	"encoding/json/jsontext"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veri/internal/spec"
)

// TestRepositoryLayoutAndDocsCompliance 校验纯静态资产目录、RFC 8259 JSON 语法、Provider 包结构与单层 README 局部索引。
func TestRepositoryLayoutAndDocsCompliance(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))
	ignored := loadGitIgnoreRules(repoRoot)
	forbiddenDocRefs := []string{
		"docs/" + "design",
		"docs/" + "providers",
	}

	checkPureAssetDirectories(t, repoRoot, []string{"build", "skills"})
	checkAllJSONFilesValid(t, repoRoot, ignored)
	checkProviderPackageLayouts(t, repoRoot)
	checkDirectoryREADMEsAndDocsSelfContainment(t, repoRoot, ignored, forbiddenDocRefs)
}

type gitIgnoreRules struct {
	rootAnchored  map[string]bool
	anyLevelNames map[string]bool
}

// loadGitIgnoreRules 解析根级 .gitignore 中的目录忽略规则，区分根锚定路径与任意层级目录名。
func loadGitIgnoreRules(repoRoot string) gitIgnoreRules {
	rules := gitIgnoreRules{
		rootAnchored:  make(map[string]bool),
		anyLevelNames: make(map[string]bool),
	}
	raw, err := os.ReadFile(filepath.Join(repoRoot, ".gitignore"))
	if err != nil {
		return rules
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		rawRule := strings.TrimSpace(line)
		if rawRule == "" || strings.HasPrefix(rawRule, "#") || strings.HasPrefix(rawRule, "!") {
			continue
		}
		anchored := strings.HasPrefix(rawRule, "/")
		trimmed := filepath.ToSlash(strings.Trim(rawRule, "/"))
		if trimmed == "" || strings.ContainsAny(trimmed, "*?[") {
			continue
		}
		if anchored || strings.Contains(trimmed, "/") {
			rules.rootAnchored[trimmed] = true
		} else {
			rules.anyLevelNames[trimmed] = true
		}
	}
	return rules
}

// shouldSkipRepoWalkDir 判断全仓扫描时是否应跳过隐藏目录、.gitignore 忽略目录或独立子模块目录。
func shouldSkipRepoWalkDir(repoRoot, path string, d fs.DirEntry, ignored gitIgnoreRules) bool {
	if path == repoRoot {
		return false
	}
	name := d.Name()
	if ignored.anyLevelNames[name] {
		return true
	}
	if strings.HasPrefix(name, ".") && name != ".github" {
		return true
	}
	if rel, err := filepath.Rel(repoRoot, path); err == nil {
		if ignored.rootAnchored[filepath.ToSlash(rel)] {
			return true
		}
	}
	if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
		return true
	}
	return false
}

func checkPureAssetDirectories(t *testing.T, repoRoot string, dirs []string) {
	t.Helper()
	for _, dir := range dirs {
		target := filepath.Join(repoRoot, dir)
		err := filepath.WalkDir(target, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil || d.IsDir() {
				return walkErr
			}
			if strings.HasSuffix(d.Name(), ".go") {
				rel, _ := filepath.Rel(repoRoot, path)
				t.Errorf("asset/config directory %s must not contain Go source file %s", dir, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk asset dir %s: %v", dir, err)
		}
	}
}

func checkAllJSONFilesValid(t *testing.T, repoRoot string, ignored gitIgnoreRules) {
	t.Helper()
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			if shouldSkipRepoWalkDir(repoRoot, path, d, ignored) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(d.Name()), ".json") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(repoRoot, path)
		if !jsontext.Value(raw).IsValid() {
			t.Errorf("%s is not valid RFC 8259 JSON (comments or syntax errors are forbidden in .json files)", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk json files: %v", err)
	}
}

type providerASTSpec struct {
	hasSearch            bool
	hasFetch             bool
	hasAsk               bool
	hasWire              bool
	hasProviderNameConst bool
	methodFiles          map[string]string
	wireStructFiles      map[string]bool
}

func inspectProviderPackageAST(t *testing.T, pkgDir string) providerASTSpec {
	t.Helper()
	astSpec := providerASTSpec{
		methodFiles:     make(map[string]string),
		wireStructFiles: make(map[string]bool),
	}
	entries, err := os.ReadDir(pkgDir)
	if err != nil {
		t.Fatalf("readdir %s: %v", pkgDir, err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		fname := e.Name()
		if e.IsDir() || !strings.HasSuffix(fname, ".go") || strings.HasSuffix(fname, "_test.go") {
			continue
		}
		fileAST, pErr := parser.ParseFile(fset, filepath.Join(pkgDir, fname), nil, 0)
		if pErr != nil {
			t.Fatalf("parse %s/%s: %v", pkgDir, fname, pErr)
		}
		for _, decl := range fileAST.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) == 0 {
					continue
				}
				recvType := d.Recv.List[0].Type
				if star, ok := recvType.(*ast.StarExpr); ok {
					recvType = star.X
				}
				if ident, ok := recvType.(*ast.Ident); ok && ident.Name == "Provider" {
					astSpec.methodFiles[d.Name.Name] = fname
				}
			case *ast.GenDecl:
				if fname == "defaults.go" && d.Tok == token.CONST {
					for _, s := range d.Specs {
						vs, ok := s.(*ast.ValueSpec)
						if !ok {
							continue
						}
						for _, n := range vs.Names {
							if n.Name == "ProviderName" {
								astSpec.hasProviderNameConst = true
							}
						}
					}
				}
			}
		}
		ast.Inspect(fileAST, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				if field.Tag != nil && (strings.Contains(field.Tag.Value, "json:") || strings.Contains(field.Tag.Value, "xml:")) {
					astSpec.wireStructFiles[fname] = true
				}
			}
			return true
		})
	}
	astSpec.hasSearch = astSpec.methodFiles["Search"] != ""
	astSpec.hasFetch = astSpec.methodFiles["Fetch"] != ""
	astSpec.hasAsk = astSpec.methodFiles["Ask"] != ""
	astSpec.hasWire = len(astSpec.wireStructFiles) > 0
	return astSpec
}

type providerLayoutSpec struct {
	dir       string
	hasSearch bool
	hasFetch  bool
	hasAsk    bool
	hasWire   bool
	inferred  bool
}

func assertCapabilitySpec(t *testing.T, providerDir, basePath, methodName, fileName string, wantCap, actualCap bool, actualFile string) {
	t.Helper()
	if actualCap != wantCap {
		t.Errorf("provider %s (*Provider).%s implementation=%t mismatches declared spec=%t", providerDir, methodName, actualCap, wantCap)
	}
	_, statErr := os.Stat(filepath.Join(basePath, fileName))
	fileExists := statErr == nil
	if wantCap != fileExists {
		t.Errorf("provider %s spec %s=%t mismatches %s existence=%t", providerDir, methodName, wantCap, fileName, fileExists)
		return
	}
	if wantCap && actualFile != fileName {
		t.Errorf("provider %s must define (*Provider).%s in %s, found in %s", providerDir, methodName, fileName, actualFile)
	}
}

func checkProviderPackageLayouts(t *testing.T, repoRoot string) {
	t.Helper()
	// 从 spec.Providers() 单一登记表推导内置 Provider 的预期目录与能力实现契约；
	// 未登记的新增目录自动回退至 AST 静态推导检验。
	regProviders := spec.Providers()
	knownProviders := make([]providerLayoutSpec, 0, len(regProviders))
	for _, info := range regProviders {
		knownProviders = append(knownProviders, providerLayoutSpec{
			dir:       strings.ReplaceAll(info.Name, "_", ""),
			hasSearch: info.Roles.Has(spec.RoleSearch),
			hasFetch:  info.Roles.Has(spec.RoleFetch),
			hasWire:   info.Name != spec.ProviderLocal,
		})
	}

	providersRoot := filepath.Join(repoRoot, "internal", "provider")
	dirEntries, err := os.ReadDir(providersRoot)
	if err != nil {
		t.Fatalf("readdir internal/provider: %v", err)
	}

	knownByDir := make(map[string]bool, len(knownProviders))
	targets := make([]providerLayoutSpec, 0, len(knownProviders))
	for _, kp := range knownProviders {
		knownByDir[kp.dir] = true
		targets = append(targets, kp)
	}

	discoveredDirs := 0
	for _, entry := range dirEntries {
		name := entry.Name()
		if !entry.IsDir() || strings.HasPrefix(name, ".") || name == "providertest" {
			continue
		}
		discoveredDirs++
		if !knownByDir[name] {
			inferred := inspectProviderPackageAST(t, filepath.Join(providersRoot, name))
			targets = append(targets, providerLayoutSpec{
				dir:       name,
				hasSearch: inferred.hasSearch,
				hasFetch:  inferred.hasFetch,
				hasAsk:    inferred.hasAsk,
				hasWire:   inferred.hasWire,
				inferred:  true,
			})
		}
	}

	for _, target := range targets {
		name := target.dir
		base := filepath.Join(providersRoot, name)

		prefix := "provider/" + name
		if target.inferred {
			prefix += "[inferred:"
		} else {
			prefix += "["
		}
		subName := fmt.Sprintf("%ssearch=%t,fetch=%t,wire=%t]", prefix, target.hasSearch, target.hasFetch, target.hasWire)
		if target.hasAsk {
			subName = fmt.Sprintf("%ssearch=%t,fetch=%t,ask=%t,wire=%t]", prefix, target.hasSearch, target.hasFetch, target.hasAsk, target.hasWire)
		}

		t.Run(subName, func(t *testing.T) {
			if info, statErr := os.Stat(base); statErr != nil || !info.IsDir() {
				t.Fatalf("declared provider directory %s missing: %v", name, statErr)
			}
			actual := inspectProviderPackageAST(t, base)

			for _, f := range []string{"doc.go", "provider.go", "defaults.go", "errors.go", "provider_test.go", "README.md"} {
				if _, err := os.Stat(filepath.Join(base, f)); err != nil {
					t.Errorf("provider %s missing required file %s", name, f)
				}
			}
			if !actual.hasSearch && !actual.hasFetch && !actual.hasAsk {
				t.Errorf("provider %s must implement at least one of Search, Fetch, or Ask", name)
			}
			if actual.methodFiles["Name"] != "provider.go" {
				t.Errorf("provider %s must define (*Provider).Name in provider.go (got %q)", name, actual.methodFiles["Name"])
			}
			if !actual.hasProviderNameConst {
				t.Errorf("provider %s must declare exported ProviderName constant in defaults.go", name)
			}

			assertCapabilitySpec(t, name, base, "Search", "search.go", target.hasSearch, actual.hasSearch, actual.methodFiles["Search"])
			assertCapabilitySpec(t, name, base, "Fetch", "fetch.go", target.hasFetch, actual.hasFetch, actual.methodFiles["Fetch"])
			assertCapabilitySpec(t, name, base, "Ask", "ask.go", target.hasAsk, actual.hasAsk, actual.methodFiles["Ask"])

			_, wireStatErr := os.Stat(filepath.Join(base, "wire.go"))
			wireExists := wireStatErr == nil
			if actual.hasWire != target.hasWire || target.hasWire != wireExists {
				t.Errorf("provider %s wire spec=%t (actualDTO=%t, wire.go=%t) mismatch", name, target.hasWire, actual.hasWire, wireExists)
			}
			for wf := range actual.wireStructFiles {
				if wf != "wire.go" {
					t.Errorf("provider %s defines serialized wire struct in %s; move all wire DTOs to wire.go", name, wf)
				}
			}

			entries, err := os.ReadDir(filepath.Join(base, "testdata"))
			if err != nil {
				t.Errorf("provider %s missing testdata/ directory: %v", name, err)
				return
			}
			fixtureCount := 0
			for _, e := range entries {
				if e.IsDir() || e.Name() == "README.md" || strings.HasPrefix(e.Name(), ".") {
					continue
				}
				fixtureCount++
			}
			if fixtureCount < 3 {
				t.Errorf("provider %s must have at least 3 fixture files in testdata/ (got %d)", name, fixtureCount)
			}
		})
	}

	if expected := len(spec.AllProviders()); discoveredDirs != expected {
		t.Errorf("discovered %d provider packages under internal/provider, want %d matching spec.AllProviders()", discoveredDirs, expected)
	}
}

func discoverIndexedDirs(t *testing.T, repoRoot string, ignored gitIgnoreRules) []string {
	t.Helper()
	indexedDirs := []string{".github/workflows", "skills"}
	recursiveRoots := []string{"build", "clikit", "docs/guide", "internal", "scripts"}

	for _, root := range recursiveRoots {
		absRoot := filepath.Join(repoRoot, filepath.FromSlash(root))
		err := filepath.WalkDir(absRoot, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !d.IsDir() {
				return nil
			}
			if shouldSkipRepoWalkDir(repoRoot, path, d, ignored) {
				return filepath.SkipDir
			}
			rel, rErr := filepath.Rel(repoRoot, path)
			if rErr == nil {
				indexedDirs = append(indexedDirs, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk indexed dir root %s: %v", root, err)
		}
	}
	return indexedDirs
}

func checkDirectoryREADMEsAndDocsSelfContainment(t *testing.T, repoRoot string, ignored gitIgnoreRules, forbiddenDocRefs []string) {
	t.Helper()
	indexedDirs := discoverIndexedDirs(t, repoRoot, ignored)

	for _, relDir := range indexedDirs {
		absDir := filepath.Join(repoRoot, filepath.FromSlash(relDir))
		readmePath := filepath.Join(absDir, "README.md")
		raw, err := os.ReadFile(readmePath)
		if err != nil || len(bytes.TrimSpace(raw)) == 0 {
			t.Errorf("directory %s missing non-empty README.md", relDir)
			continue
		}
		text := string(raw)

		entries, err := os.ReadDir(absDir)
		if err != nil {
			t.Fatalf("readdir %s: %v", relDir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if name == "README.md" || strings.HasPrefix(name, ".") {
				continue
			}
			if e.IsDir() && shouldSkipRepoWalkDir(repoRoot, filepath.Join(absDir, name), e, ignored) {
				continue
			}
			if !e.IsDir() && (strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "test_")) {
				if strings.Contains(text, "- `"+name+"`") {
					t.Errorf("%s/README.md should not list test file %s in directory structure", relDir, name)
				}
				continue
			}
			expectedBullet := "- `" + name + "`"
			if e.IsDir() {
				expectedBullet = "- `" + name + "/`"
			}
			if !strings.Contains(text, expectedBullet) {
				t.Errorf("%s/README.md missing single-layer index bullet %s", relDir, expectedBullet)
			}
			if e.IsDir() {
				withoutDirToken := strings.ReplaceAll(text, "`"+name+"/`", "")
				if strings.Contains(withoutDirToken, "`"+name+"/") {
					t.Errorf("%s/README.md expands nested path inside subdirectory %s/", relDir, name)
				}
			}
		}
	}

	docRoots := []string{"README.md", "README.en.md", "AGENTS.md", "CHANGELOG.md", ".github", "build", "clikit", "docs/guide", "internal", "scripts", "skills"}
	for _, item := range docRoots {
		target := filepath.Join(repoRoot, filepath.FromSlash(item))
		walkErr := filepath.WalkDir(target, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if shouldSkipRepoWalkDir(repoRoot, path, d, ignored) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".md") {
				return nil
			}
			raw, rErr := os.ReadFile(path)
			if rErr != nil {
				return rErr
			}
			rel, _ := filepath.Rel(repoRoot, path)
			for _, ref := range forbiddenDocRefs {
				if bytes.Contains(raw, []byte(ref)) {
					t.Errorf("markdown file %s contains coupled reference to %q", filepath.ToSlash(rel), ref)
				}
			}
			return nil
		})
		if walkErr != nil {
			t.Fatalf("walk doc root %s: %v", item, walkErr)
		}
	}
}
