package starfield

// Pins #213 (#199 WI-9, UI-14): the 120 ms tick is exported from here
// and tickSeconds is derived from it, so the sky's pace and the UI's
// tick cannot drift apart.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Passed already: the pace itself must not change (the visual baselines
// were taken at 0.12 s per tick).
func TestTickSecondsStillMatchesTheUITick(t *testing.T) {
	if math.Abs(float64(tickSeconds)-0.12) > 1e-9 {
		t.Errorf("tickSeconds = %v, want 0.12", float64(tickSeconds))
	}
}

func starfieldFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[p] = f
	}
	return out
}

func TestTickIntervalIsExportedAndDrivesTickSeconds(t *testing.T) {
	files := starfieldFiles(t)

	var exported []string
	var tickSecondsInit ast.Expr
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, s := range gd.Specs {
				v, ok := s.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for i, id := range v.Names {
					if id.IsExported() && strings.Contains(id.Name, "Tick") {
						exported = append(exported, id.Name)
					}
					if id.Name == "tickSeconds" && i < len(v.Values) {
						tickSecondsInit = v.Values[i]
					}
				}
			}
		}
	}
	if len(exported) != 1 {
		t.Fatalf("exported tick-interval names = %v, want exactly one const/var with Tick in its name", exported)
	}
	if tickSecondsInit == nil {
		t.Fatal("tickSeconds is no longer declared with an initialiser")
	}
	refs, lits := false, []string{}
	ast.Inspect(tickSecondsInit, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if x.Name == exported[0] {
				refs = true
			}
		case *ast.BasicLit:
			if v, err := strconv.ParseFloat(x.Value, 64); err == nil && v == 0.12 {
				lits = append(lits, x.Value)
			}
		}
		return true
	})
	if !refs {
		t.Errorf("tickSeconds is not derived from %s", exported[0])
	}
	if len(lits) != 0 {
		t.Errorf("tickSeconds still types the 0.12 literal %v", lits)
	}
}
