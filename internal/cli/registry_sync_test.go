package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veri/internal/app"
	"veri/internal/config"
	"veri/internal/spec"
)

// TestProviderRegistrationCompletenessAndRouterNeutrality 确保新增或调整 Provider 时：
// 1. spec.Providers() 登记表与 app 装配工厂、配置模板、docs/guide/providers.md 完全同步；
// 2. internal/router 业务代码不硬编码任何云端厂商名称常量（仅允许区分本地提取角色 ProviderLocal）。
func TestProviderRegistrationCompletenessAndRouterNeutrality(t *testing.T) {
	repoRoot := filepath.Clean(filepath.Join("..", ".."))

	loaded := &config.Loaded{
		Config:      config.Default(),
		Credentials: make(map[string]config.Credential),
	}
	application, err := app.New(loaded, "1.0.0")
	if err != nil {
		t.Fatalf("app.New with default config: %v", err)
	}

	statusByName := make(map[string]app.ProviderStatus)
	for _, st := range application.ProviderStatuses() {
		statusByName[st.Name] = st
	}

	providersGuideBytes, err := os.ReadFile(filepath.Join(repoRoot, "docs", "guide", "providers.md"))
	if err != nil {
		t.Fatalf("read docs/guide/providers.md: %v", err)
	}
	providersGuide := string(providersGuideBytes)
	zhTpl := config.TemplateForLocale("zh-CN")
	enTpl := config.TemplateForLocale("en-US")

	registered := spec.Providers()
	if len(statusByName) != len(registered) {
		t.Fatalf("app.ProviderStatuses() count=%d mismatches spec.Providers() count=%d", len(statusByName), len(registered))
	}
	for _, info := range registered {
		st, ok := statusByName[info.Name]
		if !ok {
			t.Errorf("provider %q registered in spec.Providers() has no factory in internal/app/registry.go", info.Name)
			continue
		}
		if st.Role != info.Roles.String() {
			t.Errorf("provider %q factory role=%q mismatches spec role=%q", info.Name, st.Role, info.Roles.String())
		}
		if !strings.Contains(providersGuide, "| `"+info.Name+"` |") {
			t.Errorf("docs/guide/providers.md missing adapter matrix row for %q", info.Name)
		}
		section := "[providers." + info.Name + "]"
		if !strings.Contains(zhTpl, section) || !strings.Contains(enTpl, section) {
			t.Errorf("config templates missing section %s", section)
		}
	}

	// 校验路由层非测试代码不直接引用具体云端厂商标识常量（仅允许 spec.ProviderLocal）。
	routerDir := filepath.Join(repoRoot, "internal", "router")
	entries, err := os.ReadDir(routerDir)
	if err != nil {
		t.Fatalf("readdir internal/router: %v", err)
	}
	fset := token.NewFileSet()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		fullPath := filepath.Join(routerDir, e.Name())
		fileAST, pErr := parser.ParseFile(fset, fullPath, nil, 0)
		if pErr != nil {
			t.Fatalf("parse %s: %v", fullPath, pErr)
		}
		ast.Inspect(fileAST, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok || pkgIdent.Name != "spec" {
				return true
			}
			name := sel.Sel.Name
			if (strings.HasPrefix(name, "Provider") && name != "ProviderLocal") || strings.HasPrefix(name, "Env") {
				pos := fset.Position(sel.Pos())
				t.Errorf("internal/router/%s:%d references vendor-specific constant spec.%s; route by capability instead", e.Name(), pos.Line, name)
			}
			return true
		})
	}
}
