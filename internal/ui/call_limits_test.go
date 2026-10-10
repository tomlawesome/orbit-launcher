package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #207 (#199 WI-3: EN-3, UI-18). Each outbound call owns its time limit
// in the deploy or release package that makes it; the UI only cancels.
// So nothing under internal/ui sets a deadline on a context, and no
// timeout or deadline constant lives here.
func TestUISource_SetsNoCallTimeLimits(t *testing.T) {
	limitName := regexp.MustCompile(`(?i)timeout|deadline`)
	limitCall := map[string]bool{"WithTimeout": true, "WithDeadline": true, "WithTimeoutCause": true, "WithDeadlineCause": true}
	fset := token.NewFileSet()
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				sel, ok := n.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "context" && limitCall[sel.Sel.Name] {
					t.Errorf("%s: context.%s in the UI: the call's own package owns its limit; the UI only cancels",
						fset.Position(n.Pos()), sel.Sel.Name)
				}
			case *ast.ValueSpec:
				for _, name := range n.Names {
					if limitName.MatchString(name.Name) {
						t.Errorf("%s: %s is a time-limit constant in the UI: it belongs beside the call in deploy or release",
							fset.Position(name.Pos()), name.Name)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("scan internal/ui: %v", err)
	}
}
