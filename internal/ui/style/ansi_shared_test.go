package style

// Pins #214 (SU-2): style's tests strip styling with the one shared
// internal/ansi.Strip, not a colour-codes-only regexp of their own.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestStyleTestsUseTheSharedStripper(t *testing.T) {
	files, err := filepath.Glob("*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no test files found (err %v)", err)
	}
	fset := token.NewFileSet()
	imports := false
	for _, path := range files {
		if path == "ansi_shared_test.go" {
			continue // this file names the pattern it forbids
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "github.com/tomlawesome/orbit-launcher/internal/ansi" {
				imports = true
			}
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "stripANSI" {
				t.Errorf("%s declares stripANSI: use ansi.Strip", fset.Position(fn.Pos()))
			}
		}
		// The colour-only expression itself, wherever it is declared.
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(s, `\[[0-9;]*m`) {
				t.Errorf("%s holds a colour-only escape pattern: use ansi.Strip", fset.Position(lit.Pos()))
			}
			return true
		})
	}
	if !imports {
		t.Error("no style test file imports internal/ansi")
	}
}
