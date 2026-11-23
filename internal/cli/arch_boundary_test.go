package cli

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"veri/clikit"
	"veri/internal/core"
)

var archForbiddenDocRefs = []string{
	"docs/" + "design",
	"docs/" + "providers",
}

func archRepoRoot() string {
	return filepath.Clean(filepath.Join("..", ".."))
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

// TestArchitectureBoundaries 扫描全仓 Go 源码，强制校验分层单向导入白名单、clikit 零业务耦合、HTTP/init 红线与中文字面量隔离。
func TestArchitectureBoundaries(t *testing.T) {
	repoRoot := archRepoRoot()
	const modulePrefix = "veri/"

	cjkAllowlist := map[string]bool{
		"internal/textnorm/time.go":    true, // 中文日期格式解析模板（2006年01月02日）
		"internal/textnorm/quality.go": true, // 中文反爬验证与软 404 特征词匹配
	}

	var goFiles []string
	ignored := loadGitIgnoreRules(repoRoot)
	walkErr := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, wErr error) error {
		if wErr != nil {
			return wErr
		}
		if d.IsDir() {
			if shouldSkipRepoWalkDir(repoRoot, path, d, ignored) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".go") {
			goFiles = append(goFiles, path)
		}
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk module go files: %v", walkErr)
	}

	for _, path := range goFiles {
		rel, _ := filepath.Rel(repoRoot, path)
		rel = filepath.ToSlash(rel)

		raw, rErr := os.ReadFile(path)
		if rErr != nil {
			t.Fatalf("read %s: %v", rel, rErr)
		}
		for _, ref := range archForbiddenDocRefs {
			if bytes.Contains(raw, []byte(ref)) {
				t.Errorf("%s contains coupled reference to %q", rel, ref)
			}
		}

		isTestFile := strings.HasSuffix(rel, "_test.go")
		pkgDir := filepath.ToSlash(filepath.Dir(rel))
		isCliKit := pkgDir == "clikit" || strings.HasPrefix(pkgDir, "clikit/")
		if isTestFile && !isCliKit {
			continue
		}

		fset := token.NewFileSet()
		fileAST, pErr := parser.ParseFile(fset, path, raw, 0)
		if pErr != nil {
			t.Fatalf("parse %s: %v", rel, pErr)
		}

		for _, imp := range fileAST.Imports {
			impPath, _ := strconv.Unquote(imp.Path.Value)
			if isTestFile {
				if strings.HasPrefix(impPath, modulePrefix+"internal/") {
					t.Errorf("%s in reusable chassis clikit imports host business package %q", rel, impPath)
				}
				continue
			}
			checkImportBoundary(t, rel, pkgDir, impPath, modulePrefix)
		}
		if isTestFile {
			continue
		}

		isProviderDefaults := strings.HasPrefix(rel, "internal/provider/") && filepath.Base(rel) == "defaults.go"
		ast.Inspect(fileAST, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				if x.Recv == nil && x.Name.Name == "init" {
					pos := fset.Position(x.Pos())
					t.Errorf("%s:%d defines forbidden package init() function; use explicit initialization", rel, pos.Line)
				}
			case *ast.SelectorExpr:
				if ident, ok := x.X.(*ast.Ident); ok && ident.Name == "http" {
					switch x.Sel.Name {
					case "DefaultClient", "DefaultTransport", "Get", "Head", "Post", "PostForm":
						pos := fset.Position(x.Pos())
						t.Errorf("%s:%d uses forbidden net/http.%s; route all HTTP calls through internal/httpx", rel, pos.Line, x.Sel.Name)
					}
				}
			case *ast.BasicLit:
				if x.Kind != token.STRING {
					return true
				}
				for _, imp := range fileAST.Imports {
					if imp.Path == x {
						return true
					}
				}
				val, uErr := strconv.Unquote(x.Value)
				if uErr != nil {
					val = x.Value
				}
				if isCliKit && strings.Contains(strings.ToLower(val), "veri") {
					pos := fset.Position(x.Pos())
					t.Errorf("%s:%d in reusable chassis clikit contains host-coupled string literal %q", rel, pos.Line, val)
				}
				if isProviderDefaults && (strings.HasPrefix(val, "http://") || strings.HasPrefix(val, "https://")) {
					pos := fset.Position(x.Pos())
					t.Errorf("%s:%d hardcodes URL %q in provider defaults; reference internal/spec/registry.go", rel, pos.Line, val)
				}
				if !cjkAllowlist[rel] {
					for _, r := range val {
						if unicode.Is(unicode.Han, r) {
							pos := fset.Position(x.Pos())
							t.Errorf("%s:%d contains hardcoded CJK literal %q outside i18n catalog", rel, pos.Line, val)
							break
						}
					}
				}
			}
			return true
		})
	}

	// 校验 core.Code.ExitCode() 与 clikit 标准退出码常量严格对称
	if core.CodeInvalidArgument.ExitCode() != clikit.ExitUsage ||
		core.CodeAuthFailed.ExitCode() != clikit.ExitConfig ||
		core.CodeRateLimited.ExitCode() != clikit.ExitUnavailable ||
		core.CodeBlockedByPolicy.ExitCode() != clikit.ExitBlocked ||
		core.CodeInternal.ExitCode() != clikit.ExitFailure {
		t.Fatal("core.Code.ExitCode() drifted out of sync with clikit standard exit codes")
	}
}

func checkImportBoundary(t *testing.T, relFile, pkgDir, impPath, modulePrefix string) {
	t.Helper()

	isNonStdlib := strings.Contains(impPath, ".") || strings.HasPrefix(impPath, modulePrefix) || impPath == "veri"

	switch pkgDir {
	case "clikit", "clikit/buildinfo", "clikit/storage", "clikit/redact",
		"internal/spec", "internal/mdscan", "internal/urlnorm", "internal/keypool":
		if isNonStdlib {
			t.Errorf("%s imports non-stdlib package %q", relFile, impPath)
		}
	case "internal/textnorm":
		if isNonStdlib && impPath != modulePrefix+"internal/mdscan" {
			t.Errorf("%s in internal/textnorm imports forbidden package %q (only stdlib and internal/mdscan are permitted)", relFile, impPath)
		}
	case "internal/core":
		if isNonStdlib && !strings.HasPrefix(impPath, modulePrefix) {
			t.Errorf("%s in internal/core imports external dependency %q", relFile, impPath)
		}
	}

	if impPath == modulePrefix+"clikit/i18n" && pkgDir != "internal/cli" {
		t.Errorf("%s imports clikit/i18n from %s (only internal/cli may import i18n)", relFile, pkgDir)
	}
	if impPath == modulePrefix+"clikit/skill" && pkgDir != "internal/cli" {
		t.Errorf("%s imports clikit/skill outside internal/cli", relFile)
	}

	if strings.HasPrefix(impPath, "github.com/modelcontextprotocol/go-sdk") && pkgDir != "internal/mcpserver" {
		t.Errorf("%s imports MCP SDK outside internal/mcpserver", relFile)
	}
	if (strings.HasPrefix(impPath, "github.com/spf13/cobra") || strings.HasPrefix(impPath, "github.com/spf13/pflag")) && pkgDir != "internal/cli" {
		t.Errorf("%s imports cobra/pflag outside internal/cli", relFile)
	}
	if strings.HasPrefix(impPath, "github.com/BurntSushi/toml") && pkgDir != "internal/config" {
		t.Errorf("%s imports BurntSushi/toml outside internal/config", relFile)
	}
	if strings.HasPrefix(impPath, "codeberg.org/readeck/go-readability") && pkgDir != "internal/provider/local" {
		t.Errorf("%s imports go-readability outside internal/provider/local", relFile)
	}
	if strings.HasPrefix(impPath, "golang.org/x/net") && pkgDir != "internal/provider/local" && pkgDir != "internal/htmlmd" {
		t.Errorf("%s imports golang.org/x/net outside internal/provider/local or internal/htmlmd", relFile)
	}
	if pkgDir == "internal/htmlmd" && isNonStdlib && impPath != "golang.org/x/net/html" && impPath != "golang.org/x/net/html/atom" && impPath != modulePrefix+"internal/mdscan" {
		t.Errorf("%s in internal/htmlmd imports forbidden package %q (only stdlib, golang.org/x/net/html, and internal/mdscan are permitted)", relFile, impPath)
	}
	if impPath == "golang.org/x/term" && pkgDir != "clikit/term" {
		t.Errorf("%s imports golang.org/x/term outside clikit/term", relFile)
	}
	if strings.HasPrefix(impPath, "golang.org/x/sys") && pkgDir != "clikit/i18n" && pkgDir != "clikit/credentials" {
		t.Errorf("%s imports golang.org/x/sys outside clikit/i18n or clikit/credentials", relFile)
	}
	if strings.HasPrefix(impPath, "golang.org/x/sync") {
		if impPath != "golang.org/x/sync/errgroup" || (pkgDir != "internal/router" && pkgDir != "internal/app") {
			t.Errorf("%s imports %q outside internal/router or internal/app (only errgroup is permitted)", relFile, impPath)
		}
	}

	if !strings.HasPrefix(impPath, modulePrefix) {
		return
	}
	sub := strings.TrimPrefix(impPath, modulePrefix)

	if pkgDir == "clikit" || strings.HasPrefix(pkgDir, "clikit/") {
		if strings.HasPrefix(sub, "internal/") {
			t.Errorf("%s in clikit imports host business package %q", relFile, sub)
		}
		switch pkgDir {
		case "clikit/credentials", "clikit/skill":
			if sub != "clikit/storage" {
				t.Errorf("%s in %s imports unexpected package %q", relFile, pkgDir, sub)
			}
		case "clikit/i18n", "clikit/term":
			t.Errorf("%s in %s must not import sibling package %q", relFile, pkgDir, sub)
		}
		return
	}

	switch {
	case pkgDir == ".":
		if !slices.Contains([]string{"clikit/buildinfo", "internal/cli"}, sub) {
			t.Errorf("root entrypoint %s imports forbidden package %q", relFile, sub)
		}
	case pkgDir == "internal/core":
		if !slices.Contains([]string{"internal/spec", "internal/textnorm"}, sub) {
			t.Errorf("%s in internal/core imports forbidden package %q", relFile, sub)
		}
	case pkgDir == "internal/config":
		if !slices.Contains([]string{"clikit/credentials", "clikit/redact", "clikit/storage", "internal/core", "internal/spec"}, sub) {
			t.Errorf("%s in internal/config imports forbidden package %q", relFile, sub)
		}
	case pkgDir == "internal/render":
		if !slices.Contains([]string{"clikit/term", "internal/core", "internal/spec", "internal/textnorm"}, sub) {
			t.Errorf("%s in internal/render imports forbidden package %q", relFile, sub)
		}
	case pkgDir == "internal/mcpserver":
		if !slices.Contains([]string{"clikit/buildinfo", "internal/app", "internal/render", "internal/core", "internal/spec"}, sub) {
			t.Errorf("%s in internal/mcpserver imports forbidden package %q", relFile, sub)
		}
	case pkgDir == "internal/cli":
		allowed := []string{
			"clikit", "clikit/buildinfo", "clikit/credentials", "clikit/i18n",
			"clikit/redact", "clikit/skill", "clikit/storage", "clikit/term",
			"internal/app", "internal/render", "internal/config", "internal/mcpserver",
			"internal/core", "internal/spec", "internal/textnorm", "internal/update",
		}
		if !slices.Contains(allowed, sub) {
			t.Errorf("%s in internal/cli imports forbidden package %q", relFile, sub)
		}
	case pkgDir == "internal/update":
		if !slices.Contains([]string{"clikit/redact", "internal/core", "internal/httpx", "internal/spec"}, sub) {
			t.Errorf("%s in internal/update imports forbidden package %q", relFile, sub)
		}
	case pkgDir == "internal/app":
		allowed := []string{"clikit/buildinfo", "clikit/redact", "internal/router", "internal/config", "internal/httpx", "internal/urlnorm", "internal/core", "internal/spec", "internal/textnorm"}
		if !slices.Contains(allowed, sub) && !strings.HasPrefix(sub, "internal/provider/") {
			t.Errorf("%s in internal/app imports forbidden package %q", relFile, sub)
		}
	case pkgDir == "internal/router":
		if !slices.Contains([]string{"internal/core", "internal/mdscan", "internal/spec", "internal/textnorm", "internal/urlnorm"}, sub) {
			t.Errorf("%s in internal/router imports forbidden package %q", relFile, sub)
		}
	case pkgDir == "internal/httpx":
		if !slices.Contains([]string{"clikit/redact", "internal/core", "internal/spec", "internal/textnorm", "internal/keypool"}, sub) {
			t.Errorf("%s in internal/httpx imports forbidden package %q", relFile, sub)
		}
	case strings.HasPrefix(pkgDir, "internal/provider/") && pkgDir != "internal/provider/providertest":
		allowed := []string{"internal/core", "internal/mdscan", "internal/spec", "internal/textnorm", "internal/httpx", "internal/keypool", "internal/urlnorm"}
		if pkgDir == "internal/provider/local" {
			allowed = append(allowed, "internal/htmlmd")
		}
		if !slices.Contains(allowed, sub) {
			t.Errorf("%s imports forbidden internal package %q", relFile, sub)
		}
	}
}
