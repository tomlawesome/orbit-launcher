// A live subtest's result must never be used (#310, SEC-SP1-1). go test
// -skip/-run makes a filtered-out subtest return true, so `installed :=
// t.Run("Install", ...)` cannot tell a sibling that the subtest really ran;
// a quarantined Install would let Remove run against nothing. The subtest
// records its own success in a variable instead.
package supplychain

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var tRunToken = regexp.MustCompile(`(^|[^A-Za-z0-9_])t\.Run\(`)

// usesTRunResult reports whether the line has a t.Run( call outside a //
// comment that is not the start of the statement.
func usesTRunResult(line string) bool {
	if i := strings.Index(line, "//"); i >= 0 {
		line = line[:i]
	}
	if !tRunToken.MatchString(line) {
		return false
	}
	return !strings.HasPrefix(strings.TrimSpace(line), "t.Run(")
}

func TestLiveSubtestResultClassifier(t *testing.T) {
	for _, c := range []struct {
		line string
		bad  bool
	}{
		{"\tt.Run(\"Remove\", func(t *testing.T) {", false},
		{"t.Run(\"x\", f)", false},
		{"\tinstalled := t.Run(\"Install\", func(t *testing.T) {", true},
		{"\tinstalled = t.Run(\"Install\", f)", true},
		{"\tif t.Run(\"Install\", f) {", true},
		{"\tif !t.Run(\"Install\", f) {", true},
		{"\tok := a && t.Run(\"Install\", f)", true},
		{"\treturn t.Run(\"Install\", f)", true},
		{"\t// installed := t.Run(\"Install\", f) is what not to write", false},
		{"\tt.Run(\"Remove\", f) // not installed := t.Run(", false},
		{"\tx := subt.Run(\"Install\", f)", false},
		{"\tx := 1", false},
	} {
		if got := usesTRunResult(c.line); got != c.bad {
			t.Errorf("usesTRunResult(%q) = %v, want %v", c.line, got, c.bad)
		}
	}
}

func TestLiveSubtestsNeverUseTheResultOfRun(t *testing.T) {
	dir := filepath.Join(root, "test", "live")
	var bad []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		for i, l := range strings.Split(string(body), "\n") {
			if usesTRunResult(l) {
				bad = append(bad, fmt.Sprintf("%s:%d: t.Run's result is used; go test -skip/-run makes a filtered subtest return true, so the result cannot tell a sibling the subtest ran (#310, SEC-SP1-1). Record success in a variable at the end of the subtest body instead. Line: %s", filepath.ToSlash(rel), i+1, strings.TrimSpace(l)))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range bad {
		t.Error(b)
	}
}

// installedProblems parses a test/live file and returns one message per
// `var installed bool` whose function has no setup subtest ending in
// `installed = !t.Failed()`. The variable replaces t.Run's result, so a
// subtest that never sets it (or sets it before its last check) leaves the
// dependent Remove skipping, or running against a failed install (#310).
func installedProblems(src string) []string {
	file, err := parser.ParseFile(token.NewFileSet(), "x.go", src, 0)
	if err != nil {
		return []string{"parse: " + err.Error()}
	}
	var problems []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		declares := false
		for _, st := range fn.Body.List {
			ds, ok := st.(*ast.DeclStmt)
			if !ok {
				continue
			}
			gd, ok := ds.Decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, sp := range gd.Specs {
				if vs, ok := sp.(*ast.ValueSpec); ok {
					for _, n := range vs.Names {
						if n.Name == "installed" {
							declares = true
						}
					}
				}
			}
		}
		if !declares {
			continue
		}
		recorded := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Run" {
				return true
			}
			body, ok := call.Args[1].(*ast.FuncLit)
			if !ok || len(body.Body.List) == 0 {
				return true
			}
			as, ok := body.Body.List[len(body.Body.List)-1].(*ast.AssignStmt)
			if !ok || as.Tok != token.ASSIGN || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
				return true
			}
			lhs, lhsOK := as.Lhs[0].(*ast.Ident)
			not, notOK := as.Rhs[0].(*ast.UnaryExpr)
			if !lhsOK || !notOK || lhs.Name != "installed" || not.Op != token.NOT {
				return true
			}
			if c, ok := not.X.(*ast.CallExpr); ok {
				if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Failed" && len(c.Args) == 0 {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == "t" {
						recorded = true
					}
				}
			}
			return true
		})
		if !recorded {
			problems = append(problems, fn.Name.Name+" declares `var installed bool` but no t.Run subtest in it ends with `installed = !t.Failed()`")
		}
	}
	return problems
}

func TestInstalledProblemsClassifier(t *testing.T) {
	const good = `package live
func TestX(t *testing.T) {
	var installed bool
	t.Run("Install", func(t *testing.T) {
		do(t)
		installed = !t.Failed()
	})
	t.Run("Remove", func(t *testing.T) {})
}`
	for _, c := range []struct {
		name, src string
		bad       bool
	}{
		{"good", good, false},
		{"commented-declaration style", strings.Replace(good, "var installed bool", "var installed bool // set by Install", 1), false},
		{"never set", strings.Replace(good, "installed = !t.Failed()", "", 1), true},
		{"set from the wrong thing", strings.Replace(good, "!t.Failed()", "true", 1), true},
		{"not negated", strings.Replace(good, "!t.Failed()", "t.Failed()", 1), true},
		{"set before the last check", strings.Replace(good, "installed = !t.Failed()", "installed = !t.Failed()\n\t\tdo(t)", 1), true},
		{"set outside any subtest", strings.Replace(strings.Replace(good, "installed = !t.Failed()", "", 1), "t.Run(\"Remove\"", "installed = !t.Failed()\n\tt.Run(\"Remove\"", 1), true},
		{"no installed variable", strings.Replace(good, "var installed bool", "", 1), false},
	} {
		if got := len(installedProblems(c.src)) > 0; got != c.bad {
			t.Errorf("%s: problems = %q, want bad=%v", c.name, installedProblems(c.src), c.bad)
		}
	}
}

func TestLiveSubtestsRecordTheirOwnSuccess(t *testing.T) {
	dir := filepath.Join(root, "test", "live")
	found := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return err
		}
		body, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		found += strings.Count(string(body), "var installed bool")
		rel, _ := filepath.Rel(root, p)
		for _, problem := range installedProblems(string(body)) {
			t.Errorf("%s: %s (#310, fr3-1)", filepath.ToSlash(rel), problem)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if found < 3 {
		t.Errorf("found %d `var installed bool` declarations in test/live, want at least 3 (two in live_test.go, one in update_test.go): the check has lost sight of the live subtests", found)
	}
}

func TestQuarantineHeaderSaysADependentSubtestSkipsItself(t *testing.T) {
	header := leadingComment(readRepoFile(t, quarantineFile))
	flat := strings.Join(strings.Fields(strings.ReplaceAll(header, "\n#", " ")), " ")
	for _, s := range regexp.MustCompile(`[.!?]\s`).Split(flat+" ", -1) {
		l := strings.ToLower(s)
		if strings.Contains(l, "skips itself") && strings.Contains(s, "Remove") {
			return
		}
	}
	t.Errorf("%s's header must have a sentence with \"skips itself\" and \"Remove\": quarantining TestX/Install makes the dependent Remove subtest skip with it rather than fail (#310, SEC-SP1-1)", quarantineFile)
}
