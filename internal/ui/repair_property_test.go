package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

// TestRepairModel_NeverMutatesAnything statically scans repair.go and
// fails if it contains any direct call capable of touching the
// filesystem or spawning a process. The diagnosis flow's only writes
// and spawns live behind the deploy/engine seams (fetch, stage, run),
// where they carry repair.sh's own read-only contract — the UI layer
// itself must stay incapable of acting on a deployment, so a future
// screen change can't quietly grow a side effect.
func TestRepairModel_NeverMutatesAnything(t *testing.T) {
	src, err := os.ReadFile("repair.go")
	if err != nil {
		t.Fatalf("read repair.go: %v", err)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "repair.go", src, 0)
	if err != nil {
		t.Fatalf("parse repair.go: %v", err)
	}

	forbiddenPackages := map[string]bool{"exec": true, "os": true}

	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok || !forbiddenPackages[pkgIdent.Name] {
			return true
		}
		pos := fset.Position(call.Pos())
		t.Errorf("%s: repair.go calls %s.%s — Repair must not be able to touch the filesystem or spawn a process", pos, pkgIdent.Name, sel.Sel.Name)
		return true
	})
}
