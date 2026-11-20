package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"veri/clikit/i18n"
	"veri/internal/core"
)

func TestCLILocaleCatalogsSymmetryAndKeyIntegrity(t *testing.T) {
	if err := localeBundle.ValidateSymmetry(); err != nil {
		t.Fatalf("locale bundle symmetry check failed: %v", err)
	}

	allCodes := core.AllCodes()
	for _, loc := range i18n.SupportedLocales() {
		cat := localeBundle.For(loc)
		for _, code := range allCodes {
			entry, ok := cat.Errors[string(code)]
			if !ok || entry.Title == "" {
				t.Fatalf("locale %s missing error entry for %s", loc, code)
			}
		}
	}

	// 静态扫描 internal/** 中所有词表键调用（T / WithMsgKey / WithHintKey / WithI18n），双向校验零缺失、零孤儿键
	internalRoot := filepath.Clean(filepath.Join(".."))
	usedKeys := map[string]bool{
		core.HintKeySSRFPrivateBlocked: true,
		core.HintKeyChallengeDetected:  true,
	}
	fset := token.NewFileSet()

	walkErr := filepath.WalkDir(internalRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return err
		}
		fileAST, pErr := parser.ParseFile(fset, path, nil, 0)
		if pErr != nil {
			t.Fatalf("parse %s: %v", path, pErr)
		}
		ast.Inspect(fileAST, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			var fnName string
			switch fn := call.Fun.(type) {
			case *ast.SelectorExpr:
				fnName = fn.Sel.Name
			case *ast.Ident:
				fnName = fn.Name
			default:
				return true
			}
			recordArg := func(argIdx int) {
				if argIdx >= len(call.Args) {
					return
				}
				lit, isLit := call.Args[argIdx].(*ast.BasicLit)
				if !isLit || lit.Kind != token.STRING {
					return
				}
				key, uErr := strconv.Unquote(lit.Value)
				if uErr != nil || key == "" {
					return
				}
				usedKeys[key] = true
				for _, loc := range i18n.SupportedLocales() {
					if !localeBundle.For(loc).Has(key) {
						pos := fset.Position(lit.Pos())
						t.Errorf("%s:%d references missing locale key %q in %s", path, pos.Line, key, loc)
					}
				}
			}

			switch fnName {
			case "T", "WithMsgKey", "WithHintKey":
				recordArg(0)
			case "WithI18n":
				recordArg(0)
				recordArg(1)
			case "requireMinArgs", "requireExactArgs":
				recordArg(1)
				recordArg(2)
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("walk internal for locale keys: %v", walkErr)
	}

	enCat := localeBundle.For(i18n.LocaleEnUS)
	for _, definedKey := range enCat.Keys() {
		if !usedKeys[definedKey] {
			t.Errorf("orphan locale key %q in locales/*.json is never referenced in internal/", definedKey)
		}
	}
}
