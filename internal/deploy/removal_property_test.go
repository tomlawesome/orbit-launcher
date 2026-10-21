package deploy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// destructiveArgs says whether one string argument of a process start is a
// way to delete Docker data: the volume flag in its short, long and
// bundled-short forms, or the rm subcommand.
func destructiveArgs(value string) bool {
	switch value {
	case "rm", "--volumes", "--volume":
		return true
	}
	// -v, or any bundle of short flags that holds it (-fv, -vf).
	return len(value) > 1 && value[0] == '-' && value[1] != '-' && strings.ContainsRune(value, 'v')
}

// TestNoExecCallEverIncludesTheDestructiveFlags statically scans every
// non-test .go file under cmd and internal for calls that start a process
// (exec.Command and exec.CommandContext, however os/exec is imported, and
// this package's detachedCommand and dockerCommand) and asserts none of
// their string-literal arguments is a destructive flag: "-v" or a bundle
// holding it, "--volumes", or "rm". These are the flags RemovalCommand's
// returned string deliberately contains, but which the launcher must never
// itself pass to a process. This is the concrete, checked form of the
// promise in design/mockups.html section 11 and
// docs/implementation-plan.md section 5 (Wave 4): the app automates only
// the safe half; a human runs the destructive command themselves. A
// future edit that added `-v` to StandDown's docker invocation, for
// example, would fail this test even though it wouldn't fail any
// behavioural test that only checks the current fake-docker fixture.
//
// Arguments are searched wherever they sit in the call: inside a slice
// literal or a joined string. When the call spreads a slice (args...), the
// slice was built somewhere else, so every string literal in the function
// around the call is searched too.
func TestNoExecCallEverIncludesTheDestructiveFlags(t *testing.T) {
	roots := []string{"../../cmd", "../../internal"}
	scanned, calls := 0, 0
	fset := token.NewFileSet()
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "testdata" {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			file, err := parser.ParseFile(fset, path, src, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			scanned++
			calls += scanFileForDestructiveArgs(t, fset, file)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	// A scan that found nothing to look at proves nothing: the launcher
	// starts processes in many places (docker queries, the stand-down, the
	// install, configure and repair scripts).
	t.Logf("scanned %d files, %d process starts", scanned, calls)
	if scanned < 30 || calls < 10 {
		t.Fatalf("the scan saw %d files and %d process starts, so it proves nothing", scanned, calls)
	}
}

// scanFileForDestructiveArgs reports every destructive argument in file's
// process starts and returns how many process starts it saw.
func scanFileForDestructiveArgs(t *testing.T, fset *token.FileSet, file *ast.File) (calls int) {
	t.Helper()
	execNames := map[string]bool{} // local names os/exec is imported as
	for _, im := range file.Imports {
		if im.Path.Value != `"os/exec"` {
			continue
		}
		name := "exec"
		if im.Name != nil {
			name = im.Name.Name
		}
		execNames[name] = true
	}

	report := func(root ast.Node, call *ast.CallExpr) {
		ast.Inspect(root, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				value = strings.Trim(lit.Value, "\"`")
			}
			if destructiveArgs(value) {
				t.Errorf("%s: process start at %s includes destructive argument %q", fset.Position(lit.Pos()), fset.Position(call.Pos()), value)
			}
			return true
		})
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isExecCall(call, execNames) {
				return true
			}
			calls++
			for _, arg := range call.Args {
				report(arg, call)
			}
			if call.Ellipsis.IsValid() {
				report(fn.Body, call)
			}
			return true
		})
	}
	return calls
}

// isExecCall reports whether call starts a process: exec.Command or
// exec.CommandContext (under whatever name os/exec is imported as), or
// this package's detachedCommand or dockerCommand.
func isExecCall(call *ast.CallExpr, execNames map[string]bool) bool {
	if id, ok := call.Fun.(*ast.Ident); ok {
		return id.Name == "detachedCommand" || id.Name == "dockerCommand"
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
		return false
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	return ok && execNames[pkgIdent.Name]
}

func TestStandDown_NeverPassesTheVolumeFlag(t *testing.T) {
	callLog := fakeDockerLogging(t)

	if err := StandDown(t.Context(), trustedDeployment(t)); err != nil {
		t.Fatalf("StandDown: %v", err)
	}

	logged, err := os.ReadFile(callLog)
	if err != nil {
		t.Fatalf("read call log: %v", err)
	}
	calls := string(logged)

	if !strings.Contains(calls, "down") {
		t.Error("expected StandDown to invoke docker compose down")
	}
	if strings.Contains(calls, "-v") {
		t.Errorf("StandDown must never pass -v (destroys volumes): logged calls = %q", calls)
	}
	if strings.Contains(calls, "rm") {
		t.Errorf("StandDown must never invoke rm: logged calls = %q", calls)
	}
}
