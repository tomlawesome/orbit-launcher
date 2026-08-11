package deploy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoExecCallEverIncludesTheDestructiveFlags statically scans every
// non-test .go file in this package for exec.Command/exec.CommandContext
// call sites and asserts none of their string-literal arguments contain
// "-v" or "rm" — the destructive flags RemovalCommand's returned string
// deliberately contains, but which this package must never itself pass to
// exec.Command. This is the concrete, checked form of the promise in
// design/mockups.html section 11 and docs/implementation-plan.md section
// 5 (Wave 4): the app automates only the safe half; a human runs the
// destructive command themselves. A future edit that added `-v` to
// StandDown's docker invocation, for example, would fail this test even
// though it wouldn't fail any behavioural test that only checks the
// current fake-docker fixture.
func TestNoExecCallEverIncludesTheDestructiveFlags(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package files: %v", err)
	}

	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}

		src, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		file, err := parser.ParseFile(fset, path, src, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext" {
				return true
			}
			pkgIdent, ok := sel.X.(*ast.Ident)
			if !ok || pkgIdent.Name != "exec" {
				return true
			}

			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value := strings.Trim(lit.Value, `"`)
				if value == "-v" || value == "rm" {
					pos := fset.Position(arg.Pos())
					t.Errorf("%s: exec.Command/CommandContext call includes destructive flag %q", pos, value)
				}
			}
			return true
		})
	}
}

func TestStandDown_NeverPassesTheVolumeFlag(t *testing.T) {
	fakeBin := t.TempDir()
	callLog := filepath.Join(fakeBin, "calls.log")
	writeFakeDocker(t, fakeBin, callLog)

	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	if err := StandDown(t.Context(), "/tmp/some-orbit-deployment"); err != nil {
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

func writeFakeDocker(t *testing.T, binDir, callLog string) {
	t.Helper()
	script := "#!/usr/bin/env bash\necho \"$@\" >> " + callLog + "\n"
	path := filepath.Join(binDir, "docker")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake docker: %v", err)
	}
}
