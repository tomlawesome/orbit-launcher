package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"testing"
)

// TestRepairModel_NeverMutatesAnything statically scans repair.go and
// fails if it contains any call capable of touching the filesystem or
// spawning a process — the concrete, checked form of "non-mutating dispatch
// seam" (docs/implementation-plan.md section 5, Wave 3), not just a
// docstring someone could quietly invalidate by adding real logic later
// without also removing this test.
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
