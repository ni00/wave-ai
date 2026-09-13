package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Keep persistence and transport constraints executable as the project grows.
func TestBoundaries(t *testing.T) {
	root := ".."
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, e := filepath.Rel(root, path)
		if e != nil {
			return e
		}
		group := strings.Split(filepath.ToSlash(rel), "/")[0]
		allowed := map[string][]string{
			"app":      {"app", "modules", "adapters", "platform"},
			"modules":  {"modules", "adapters", "platform"},
			"adapters": {"adapters", "platform"},
			"platform": {"platform"},
		}
		if _, ok := allowed[group]; !ok {
			t.Errorf("%s: production code must belong to app, modules, adapters or platform", path)
		}
		fset := token.NewFileSet()
		f, e := parser.ParseFile(fset, path, nil, 0)
		if e != nil {
			return e
		}
		for _, im := range f.Imports {
			name, _ := strconv.Unquote(im.Path.Value)
			if target, internal := strings.CutPrefix(name, "wave-ai.local/wave/internal/"); internal {
				targetGroup := strings.Split(target, "/")[0]
				permitted := false
				for _, candidate := range allowed[group] {
					permitted = permitted || candidate == targetGroup
				}
				if !permitted {
					t.Errorf("%s: %s must not import %s", path, group, name)
				}
			}
			if name == "database/sql" || strings.HasPrefix(name, "github.com/jackc/pgx") {
				t.Errorf("%s imports a database driver directly", path)
			}
			if name == "github.com/gin-gonic/gin" && !strings.HasSuffix(path, "handler.go") && !strings.Contains(path, "/httpx/") && !strings.HasSuffix(path, "/app/app.go") {
				t.Errorf("%s leaks Gin outside the HTTP boundary", path)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch sel.Sel.Name {
			case "Raw", "Expr":
				t.Errorf("%s: raw SQL escape hatch", fset.Position(call.Pos()))
			case "Where", "Or", "Not", "Joins", "Order", "Having":
				if len(call.Args) > 0 {
					if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
						t.Errorf("%s: use structured ORM expressions", fset.Position(call.Pos()))
					}
				}
			case "Exec":
				// Exec is also the sandbox process API. Only the adapter and its app
				// integration may call it; persistence use cases cannot.
				if !strings.Contains(path, "/adapters/sandbox/") && !strings.HasSuffix(path, "/app/tools.go") && !strings.HasSuffix(path, "/app/workspace.go") {
					t.Errorf("%s: unexpected Exec", fset.Position(call.Pos()))
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
