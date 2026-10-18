package engine

// Pins #209 (engine vocabulary, #199 WI-5; EN-10, EN-11): the state
// words and repair.sh exit codes are named once, here, so the UI can
// import names instead of retyping literals. The constants are read
// from the package's own source by the type checker rather than
// referenced directly, so a missing name fails this test alone instead
// of stopping the whole package from compiling.

import (
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sourceFiles parses every non-test Go file of this package.
func sourceFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no non-test source files found")
	}
	return fset, files
}

// packageConstants type-checks this package's own source and returns
// every package-level constant by name.
func packageConstants(t *testing.T) map[string]*types.Const {
	t.Helper()
	fset, files := sourceFiles(t)
	conf := types.Config{Importer: importer.ForCompiler(fset, "source", nil)}
	pkg, err := conf.Check("github.com/tomlawesome/orbit-launcher/internal/engine", fset, files, nil)
	if err != nil {
		t.Fatalf("type-check engine source: %v", err)
	}
	out := map[string]*types.Const{}
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		if c, ok := scope.Lookup(name).(*types.Const); ok {
			out[name] = c
		}
	}
	return out
}

// TestExitCodesAreNamed pins EN-11: repair.sh's exit vocabulary (orbit
// docs/engine-events.md, "Exit codes") as the names the decisions give.
func TestExitCodesAreNamed(t *testing.T) {
	consts := packageConstants(t)
	for _, tc := range []struct {
		name string
		want int64
	}{
		{"ExitHealthy", 0},
		{"ExitUsage", 2},
		{"ExitAttention", 3},
		{"ExitFailed", 4},
		{"ExitNotInstalled", 5},
		{"ExitDangerousRefused", 6},
	} {
		name, want := tc.name, tc.want
		c, ok := consts[name]
		if !ok {
			t.Errorf("engine.%s is not defined", name)
			continue
		}
		if !c.Exported() {
			t.Errorf("engine.%s is not exported", name)
		}
		v, exact := constant.Int64Val(constant.ToInt(c.Val()))
		if !exact || v != want {
			t.Errorf("engine.%s = %s, want %d", name, c.Val(), want)
		}
		if b, ok := c.Type().Underlying().(*types.Basic); !ok || b.Info()&types.IsInteger == 0 {
			t.Errorf("engine.%s has type %s, want an integer type comparable with DoneMsg.ExitCode", name, c.Type())
		}
	}
}

// TestStateWordsAreNamed pins EN-10: every state the contract defines
// (and so every state the console can render) has an exported State*
// constant, so the UI never needs to type one.
func TestStateWordsAreNamed(t *testing.T) {
	consts := packageConstants(t)
	have := map[string]string{}
	for name, c := range consts {
		if !strings.HasPrefix(name, "State") || !c.Exported() || c.Val().Kind() != constant.String {
			continue
		}
		have[constant.StringVal(c.Val())] = name
	}
	for _, word := range []string{"waiting", "starting", "running", "healthy", "skipped", "completed", "blocked", "failed"} {
		if _, ok := have[word]; !ok {
			t.Errorf("no exported engine.State* constant for state %q", word)
		}
	}
}
