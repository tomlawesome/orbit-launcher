package cli_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const cliImport = "github.com/tomlawesome/orbit-launcher/tools/internal/cli"

// SU-17: calculateversion, licencenotices, licencereview and
// supplychainpolicy each typed the same flag skeleton (a ContinueOnError
// flag set, flag.ErrHelp -> 0, other flag errors -> 2, run errors -> 1).
// It lives once in tools/internal/cli.Run. Run's signature is not known
// to these tests, so the source is scanned; each tool's own flag and
// exit-code tests (which must keep passing) prove the behaviour.

// parseDir parses the non-test Go files directly in dir.
func parseDir(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = f
	}
	return files
}

// selectorUses counts pkg.name references in f where pkg is the
// identifier local.
func selectorUses(f *ast.File, local, name string) int {
	n := 0
	ast.Inspect(f, func(node ast.Node) bool {
		if sel, ok := node.(*ast.SelectorExpr); ok && sel.Sel.Name == name {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == local {
				n++
			}
		}
		return true
	})
	return n
}

func TestRunIsDeclaredOnceInTheSharedPackage(t *testing.T) {
	files := parseDir(t, ".")
	if len(files) == 0 {
		t.Fatal("tools/internal/cli has no Go source; the shared skeleton is not written")
	}
	runs, newFlagSets := 0, 0
	for _, f := range files {
		if f.Name.Name != "cli" {
			t.Errorf("package is %q, want cli", f.Name.Name)
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "Run" {
				runs++
			}
		}
		newFlagSets += selectorUses(f, "flag", "NewFlagSet")
	}
	if runs != 1 {
		t.Errorf("cli declares Run %d times, want exactly once", runs)
	}
	// The skeleton's whole point: the flag set is built here, once.
	if newFlagSets != 1 {
		t.Errorf("cli builds a flag set %d times, want once", newFlagSets)
	}
}

func TestFourToolsShareCliRun(t *testing.T) {
	for _, tool := range []string{"calculateversion", "licencenotices", "licencereview", "supplychainpolicy"} {
		t.Run(tool, func(t *testing.T) {
			files := parseDir(t, filepath.Join("..", "..", tool))
			if len(files) == 0 {
				t.Fatalf("no source found for tools/%s", tool)
			}
			calls := 0
			for name, f := range files {
				// The package may be imported under an alias, because
				// licencenotices and licencereview already own a func
				// called cli.
				local := ""
				for _, imp := range f.Imports {
					path, _ := strconv.Unquote(imp.Path.Value)
					if path != cliImport {
						continue
					}
					local = "cli"
					if imp.Name != nil {
						local = imp.Name.Name
					}
				}
				if local != "" {
					calls += selectorUses(f, local, "Run")
				}
				if n := selectorUses(f, "flag", "NewFlagSet"); n != 0 {
					t.Errorf("%s still builds its own flag set; that is cli.Run's job", name)
				}
				if n := selectorUses(f, "flag", "ErrHelp"); n != 0 {
					t.Errorf("%s still maps flag.ErrHelp itself; that is cli.Run's job", name)
				}
			}
			if calls == 0 {
				t.Errorf("tools/%s does not call cli.Run", tool)
			}
		})
	}
}
