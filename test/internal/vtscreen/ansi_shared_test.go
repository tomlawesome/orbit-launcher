package vtscreen

// Pins #214 (SU-2): vtscreen reads screens through the one shared
// stripper, internal/ansi.Strip, and keeps no escape stripper of its own.
// The scan is by source because the package cannot be named at compile
// time until it exists.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const ansiPath = "github.com/tomlawesome/orbit-launcher/internal/ansi"

func TestVtscreenKeepsNoStripperOfItsOwn(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files found (err %v)", err)
	}
	fset := token.NewFileSet()
	imports := false
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		isTest := strings.HasSuffix(path, "_test.go")
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == ansiPath && !isTest {
				imports = true
			}
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && (fn.Name.Name == "stripEscapes" || fn.Name.Name == "stripANSI") {
				t.Errorf("%s declares %s: use ansi.Strip", fset.Position(fn.Pos()), fn.Name.Name)
			}
		}
	}
	if !imports {
		t.Errorf("no non-test file in vtscreen imports %s", ansiPath)
	}
}
