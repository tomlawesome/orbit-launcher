package ui

// Pins #209 (engine vocabulary, #199 WI-5; EN-10, EN-11): the UI
// imports engine state words and repair.sh exit codes by name and never
// retypes them. The scans read every non-test Go file under internal/ui
// (subpackages included).

import (
	"bufio"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// uiSourceFiles lists every non-test Go file under this package's tree.
func uiSourceFiles(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "testdata" || strings.HasPrefix(d.Name(), ".")) && path != "." {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatal("no non-test source files found under internal/ui")
	}
	return out
}

// TestUIRetypesNoEngineVocabulary is the issue's done-criterion grep,
// verbatim: grep -n '"failed"\|"blocked"\|"completed"\|== 5\|== 3'
// over internal/ui must be empty.
func TestUIRetypesNoEngineVocabulary(t *testing.T) {
	patterns := []string{`"failed"`, `"blocked"`, `"completed"`, `== 5`, `== 3`}
	for _, path := range uiSourceFiles(t) {
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(f)
		sc.Buffer(make([]byte, 1024*1024), 1024*1024)
		for n := 1; sc.Scan(); n++ {
			for _, p := range patterns {
				if strings.Contains(sc.Text(), p) {
					t.Errorf("%s:%d retypes engine vocabulary (%s): %s", path, n, p, strings.TrimSpace(sc.Text()))
				}
			}
		}
		if err := sc.Err(); err != nil {
			t.Fatal(err)
		}
		f.Close()
	}
}

// TestUIUsesEngineStateNames pins EN-10 beyond the grep: no string
// literal in UI code is exactly one of the contract's state words
// (orbit docs/engine-events.md, "state") — the console renders them
// through engine's names.
func TestUIUsesEngineStateNames(t *testing.T) {
	states := map[string]bool{
		"waiting": true, "starting": true, "running": true, "healthy": true,
		"skipped": true, "completed": true, "blocked": true, "failed": true,
	}
	fset := token.NewFileSet()
	for _, path := range uiSourceFiles(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil && states[s] {
				t.Errorf("%s: string literal %s retypes an engine state word", fset.Position(lit.Pos()), lit.Value)
			}
			return true
		})
	}
}

// exitCodeLike reports whether e reads an engine exit code.
func exitCodeLike(e ast.Expr) bool {
	s := strings.ToLower(types.ExprString(e))
	return strings.Contains(s, "exitcode") || strings.Contains(s, "exit") || s == "code" || strings.HasSuffix(s, ".code")
}

// repairExitLiteral reports whether e is a bare integer literal naming
// one of repair.sh's non-zero, non-generic exit codes (2 usage,
// 3 attention, 4 failed, 5 not installed, 6 dangerous refused).
func repairExitLiteral(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return "", false
	}
	switch lit.Value {
	case "2", "3", "4", "5", "6":
		return lit.Value, true
	}
	return "", false
}

// TestUIUsesEngineExitNames pins EN-11 beyond the grep: no comparison
// or switch case in UI code tests an exit code against a bare repair.sh
// exit number; engine.ExitUsage/Attention/Failed/NotInstalled/
// DangerousRefused name them.
func TestUIUsesEngineExitNames(t *testing.T) {
	fset := token.NewFileSet()
	for _, path := range uiSourceFiles(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BinaryExpr:
				if x.Op != token.EQL && x.Op != token.NEQ {
					return true
				}
				for _, pair := range [][2]ast.Expr{{x.X, x.Y}, {x.Y, x.X}} {
					if v, ok := repairExitLiteral(pair[0]); ok && exitCodeLike(pair[1]) {
						t.Errorf("%s: %s compares an exit code with bare %s", fset.Position(x.Pos()), types.ExprString(x), v)
					}
				}
			case *ast.SwitchStmt:
				if x.Tag == nil || !exitCodeLike(x.Tag) {
					return true
				}
				for _, s := range x.Body.List {
					cc, ok := s.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, e := range cc.List {
						if v, ok := repairExitLiteral(e); ok {
							t.Errorf("%s: switch on %s has bare case %s", fset.Position(e.Pos()), types.ExprString(x.Tag), v)
						}
					}
				}
			}
			return true
		})
	}
}
