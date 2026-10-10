package deploy

// #219 (WI-15), EN-17: deploy's tests had two fake-docker writers, the PATH
// prepend typed five times and six script-tree builders whose modes
// differed (0644/0700/0600) with only trustedDeployment setting them
// exactly. The decisions give them one home, helpers_test.go. The helpers
// do not exist yet, so this reads source instead of calling them.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type en17File struct {
	name string
	file *ast.File
}

func en17Parse(t *testing.T, name string, src any) en17File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), name, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return en17File{name: filepath.Base(name), file: f}
}

// en17Tests is this package's _test.go files, except this one: its own
// snippets and predicates must not count toward what it checks.
func en17Tests(t *testing.T) []en17File {
	t.Helper()
	names, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	var out []en17File
	for _, n := range names {
		if n == "helpers_scan_test.go" {
			continue
		}
		out = append(out, en17Parse(t, n, nil))
	}
	return out
}

func en17Name(call *ast.CallExpr) (pkg, name string) {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return "", fn.Name
	case *ast.SelectorExpr:
		if id, ok := fn.X.(*ast.Ident); ok {
			return id.Name, fn.Sel.Name
		}
		return "", fn.Sel.Name
	}
	return "", ""
}

func en17Lit(node ast.Node, s string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"`+s+`"` {
			found = true
		}
		return !found
	})
	return found
}

func en17Ident(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

func en17Calls(node ast.Node, pred func(*ast.CallExpr) bool) int {
	n := 0
	ast.Inspect(node, func(x ast.Node) bool {
		if c, ok := x.(*ast.CallExpr); ok && pred(c) {
			n++
		}
		return true
	})
	return n
}

func en17Funcs(files []en17File, pred func(*ast.FuncDecl) bool) []string {
	var out []string
	for _, f := range files {
		for _, d := range f.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil && pred(fd) {
				out = append(out, f.name+":"+fd.Name.Name)
			}
		}
	}
	return out
}

func en17IsPathPrepend(c *ast.CallExpr) bool {
	_, name := en17Name(c)
	return name == "Setenv" && len(c.Args) == 2 && en17Lit(c.Args[0], "PATH") && en17Ident(c.Args[1], "PathListSeparator")
}

func en17WritesFakeDocker(fd *ast.FuncDecl) bool {
	wrote := en17Calls(fd.Body, func(c *ast.CallExpr) bool { _, n := en17Name(c); return n == "WriteFile" }) > 0
	named := en17Calls(fd.Body, func(c *ast.CallExpr) bool { _, n := en17Name(c); return n == "Join" && en17Lit(c, "docker") }) > 0
	return wrote && named
}

func en17MakesScriptsDir(fd *ast.FuncDecl) bool {
	return en17Calls(fd.Body, func(c *ast.CallExpr) bool {
		_, n := en17Name(c)
		return (n == "Mkdir" || n == "MkdirAll") && (en17Lit(c, "scripts") || en17Ident(c, "scripts"))
	}) > 0
}

// The predicates must recognise what they are named for, or the counts
// below would be zero for the wrong reason.
func TestEN17_ScannersRecogniseTheirPatterns(t *testing.T) {
	f := en17Parse(t, "snippet.go", `package p
func prepend(t T) { t.Setenv("PATH", d+string(os.PathListSeparator)+os.Getenv("PATH")) }
func tempOnly(t T) { t.Setenv("PATH", t.TempDir()) }
func docker() { os.WriteFile(filepath.Join(d, "docker"), nil, 0o755) }
func tree() { os.MkdirAll(filepath.Join(d, "scripts"), 0o755) }
func treeVar() { scripts := x; os.Mkdir(scripts, 0o755) }
func neither() { os.WriteFile(filepath.Join(d, "other"), nil, 0o644) }
`)
	files := []en17File{f}
	prepends := 0
	for _, fn := range en17Funcs(files, func(fd *ast.FuncDecl) bool { return en17Calls(fd.Body, en17IsPathPrepend) > 0 }) {
		if fn != "snippet.go:prepend" {
			t.Errorf("%s was taken for a PATH prepend", fn)
		}
		prepends++
	}
	if prepends != 1 {
		t.Errorf("PATH prepends found = %d, want 1", prepends)
	}
	if got := en17Funcs(files, en17WritesFakeDocker); len(got) != 1 || got[0] != "snippet.go:docker" {
		t.Errorf("fake docker writers = %v, want [docker]", got)
	}
	if got := en17Funcs(files, en17MakesScriptsDir); len(got) != 2 {
		t.Errorf("scripts-dir makers = %v, want [tree treeVar]", got)
	}
}

func TestEN17_HelpersFileExists(t *testing.T) {
	if _, err := os.Stat("helpers_test.go"); err != nil {
		t.Fatalf("internal/deploy/helpers_test.go does not exist: %v", err)
	}
}

// One PATH prepend: every test that needs a fake docker goes through it.
func TestEN17_OnePathPrependInDeployTests(t *testing.T) {
	var where []string
	for _, f := range en17Tests(t) {
		for range en17Calls(f.file, en17IsPathPrepend) {
			where = append(where, f.name)
		}
	}
	if len(where) > 1 {
		t.Errorf("deploy tests prepend to PATH in %d places %v, want at most 1 (helpers_test.go, or testutil's)", len(where), where)
	}
}

// One fake-docker writer: a docker that prints, one that logs calls and
// one that hangs differ in their script, not in how the file is written.
func TestEN17_OneFakeDockerWriter(t *testing.T) {
	got := en17Funcs(en17Tests(t), en17WritesFakeDocker)
	if len(got) > 1 {
		t.Errorf("deploy tests write a fake docker in %d places %v, want at most 1", len(got), got)
	}
}

// One script-tree builder, in helpers_test.go, whose modes are explicit.
// Six builders used 0644/0700/0600 by accident, and only trustedDeployment
// set the mode exactly (through Chmod, not the umask); the one builder must
// take its modes as arguments and set them exactly, so each test says what
// mode it is testing.
func TestEN17_OneScriptTreeBuilderWithExplicitModes(t *testing.T) {
	files := en17Tests(t)
	got := en17Funcs(files, en17MakesScriptsDir)
	if len(got) != 1 {
		t.Fatalf("deploy tests build a scripts/ tree in %d places %v, want exactly 1", len(got), got)
	}
	if !strings.HasPrefix(got[0], "helpers_test.go:") {
		t.Fatalf("the script-tree builder is %s, want it in helpers_test.go", got[0])
	}
	for _, f := range files {
		for _, d := range f.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || f.name+":"+fd.Name.Name != got[0] {
				continue
			}
			if !en17TakesMode(files, fd) {
				t.Errorf("%s takes no file-mode argument: its modes are not explicit", got[0])
			}
			exact := en17Calls(fd.Body, func(c *ast.CallExpr) bool {
				_, n := en17Name(c)
				return n == "Chmod" || n == "chmod"
			}) > 0
			if !exact {
				t.Errorf("%s never calls Chmod: the umask, not the caller, decides the modes", got[0])
			}
		}
	}
}

// en17TakesMode reports whether fd has a parameter that is a FileMode, or
// a struct of this package's test files with a FileMode field.
func en17TakesMode(files []en17File, fd *ast.FuncDecl) bool {
	structsWithMode := map[string]bool{}
	for _, f := range files {
		ast.Inspect(f.file, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			if st, ok := ts.Type.(*ast.StructType); ok {
				for _, fld := range st.Fields.List {
					if en17Mentions(fld.Type, "FileMode") {
						structsWithMode[ts.Name.Name] = true
					}
				}
			}
			return true
		})
	}
	for _, p := range fd.Type.Params.List {
		if en17Mentions(p.Type, "FileMode") {
			return true
		}
		for name := range structsWithMode {
			if en17Mentions(p.Type, name) {
				return true
			}
		}
	}
	return false
}

func en17Mentions(e ast.Expr, name string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			found = found || x.Name == name
		case *ast.SelectorExpr:
			found = found || x.Sel.Name == name
		}
		return true
	})
	return found
}
