package launcher

// Source-scan helpers for the #219 (WI-15) tests. The code those tests pin
// does not exist yet, so they read Go source with go/parser instead of
// naming it: a missing name then fails an assertion, not the build of every
// test in the package.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// goFile is one parsed source file; rel is its path from the repo root.
type goFile struct {
	rel  string
	fset *token.FileSet
	file *ast.File
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("%s is not the repo root: %v", root, err)
	}
	return root
}

// loadDir parses the .go files directly inside rel (a path from the repo
// root). tests selects _test.go files or the rest. A missing directory
// yields no files, so "does it exist" is an assertion, not a crash.
func loadDir(t *testing.T, rel string, tests bool) []goFile {
	t.Helper()
	dir := filepath.Join(repoRoot(t), rel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []goFile
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") != tests {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parse %s: %v", filepath.Join(rel, name), err)
		}
		out = append(out, goFile{rel: filepath.Join(rel, name), fset: fset, file: f})
	}
	return out
}

// loadAll is the test and non-test files of rel together.
func loadAll(t *testing.T, rel string) []goFile {
	t.Helper()
	return append(loadDir(t, rel, true), loadDir(t, rel, false)...)
}

// parseSrc parses a snippet, for the scanners' own checks.
func parseSrc(t *testing.T, src string) goFile {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse snippet: %v", err)
	}
	return goFile{rel: "snippet.go", fset: fset, file: f}
}

// concat folds a literal, or a + chain of literals, into its value.
func concat(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.ParenExpr:
		return concat(x.X)
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		l, ok1 := concat(x.X)
		r, ok2 := concat(x.Y)
		return l + r, ok1 && ok2
	}
	return "", false
}

// stringConstants is every string a file spells out, a "a" + "b" chain
// counting as one string: what a script or fixture reads as, not how the
// source happened to split it.
func stringConstants(f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.BinaryExpr:
			if s, ok := concat(x); ok {
				out = append(out, s)
				return false
			}
		case *ast.BasicLit:
			if s, ok := concat(x); ok {
				out = append(out, s)
			}
		}
		return true
	})
	return out
}

// hasLiteral reports whether node spells out the string s anywhere.
func hasLiteral(node ast.Node, s string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok {
			if v, ok := concat(lit); ok && v == s {
				found = true
			}
		}
		return !found
	})
	return found
}

func hasIdent(node ast.Node, name string) bool {
	found := false
	ast.Inspect(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == name {
			found = true
		}
		return !found
	})
	return found
}

// callee splits a call into package qualifier and name: exec.Command is
// ("exec", "Command"), run(...) is ("", "run"), t.Setenv is ("t", "Setenv").
func callee(call *ast.CallExpr) (string, string) {
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

// calls returns every call in node for which pred holds.
func calls(node ast.Node, pred func(*ast.CallExpr) bool) []*ast.CallExpr {
	var out []*ast.CallExpr
	ast.Inspect(node, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && pred(c) {
			out = append(out, c)
		}
		return true
	})
	return out
}

// funcDecls names ("file:Func") every top-level function in files for
// which pred holds. Closures belong to the function that holds them.
func funcDecls(files []goFile, pred func(*ast.FuncDecl) bool) []string {
	var out []string
	for _, gf := range files {
		for _, d := range gf.file.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Body != nil && pred(fd) {
				out = append(out, gf.rel+":"+fd.Name.Name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// funcNamed reports the top-level functions (not methods) called name.
func funcNamed(files []goFile, name string) []string {
	return funcDecls(files, func(fd *ast.FuncDecl) bool { return fd.Recv == nil && fd.Name.Name == name })
}

func exportedFuncs(files []goFile) []string {
	return funcDecls(files, func(fd *ast.FuncDecl) bool { return fd.Recv == nil && fd.Name.IsExported() })
}

// literalHits names every string constant in files satisfying pred.
func literalHits(files []goFile, pred func(string) bool) []string {
	var out []string
	for _, gf := range files {
		for _, s := range stringConstants(gf.file) {
			if pred(s) {
				out = append(out, gf.rel)
			}
		}
	}
	sort.Strings(out)
	return out
}

// importsOf is the import paths of files.
func importsOf(files []goFile) []string {
	var out []string
	for _, gf := range files {
		for _, imp := range gf.file.Imports {
			p, _ := strconv.Unquote(imp.Path.Value)
			out = append(out, p)
		}
	}
	return out
}

// --- predicates shared by the checks ---

// isGoToolchainCall is exec.Command("go", ...) or exec.CommandContext(ctx, "go", ...).
func isGoToolchainCall(c *ast.CallExpr) bool {
	pkg, name := callee(c)
	if pkg != "exec" || (name != "Command" && name != "CommandContext") {
		return false
	}
	for _, a := range c.Args[:min(len(c.Args), 2)] {
		if s, ok := concat(a); ok && s == "go" {
			return true
		}
	}
	return false
}

// isPathPrepend is Setenv("PATH", <something with os.PathListSeparator>):
// putting a fake executable in front of the real ones.
func isPathPrepend(c *ast.CallExpr) bool {
	_, name := callee(c)
	if name != "Setenv" || len(c.Args) != 2 {
		return false
	}
	if s, ok := concat(c.Args[0]); !ok || s != "PATH" {
		return false
	}
	return hasIdent(c.Args[1], "PathListSeparator")
}

// writesFakeDocker is a function that writes a file and names "docker" in
// a path join: a fake docker writer, however it is parameterised.
func writesFakeDocker(fd *ast.FuncDecl) bool {
	wrote := len(calls(fd.Body, func(c *ast.CallExpr) bool { _, n := callee(c); return n == "WriteFile" })) > 0
	named := len(calls(fd.Body, func(c *ast.CallExpr) bool {
		_, n := callee(c)
		return n == "Join" && hasLiteral(c, "docker")
	})) > 0
	return wrote && named
}

// makesScriptsDir is a function that creates a scripts/ directory: the
// start of every script-tree builder.
func makesScriptsDir(fd *ast.FuncDecl) bool {
	return len(calls(fd.Body, func(c *ast.CallExpr) bool {
		_, n := callee(c)
		return (n == "Mkdir" || n == "MkdirAll") && (hasLiteral(c, "scripts") || hasIdent(c, "scripts"))
	})) > 0
}

// runsCountingServer is a function that starts an HTTP server and counts
// requests with .Add(1).
func runsCountingServer(fd *ast.FuncDecl) bool {
	server := len(calls(fd.Body, func(c *ast.CallExpr) bool { p, n := callee(c); return p == "httptest" && n == "NewServer" })) > 0
	counts := len(calls(fd.Body, func(c *ast.CallExpr) bool {
		_, n := callee(c)
		return n == "Add" && len(c.Args) == 1 && isLiteralInt(c.Args[0], "1")
	})) > 0
	return server && counts
}

// runsCountingScriptServer is runsCountingServer that also points the
// install.sh override at itself: the shape of deploy's noScriptSource and
// ui's countingScriptSource.
func runsCountingScriptServer(fd *ast.FuncDecl) bool {
	return runsCountingServer(fd) && hasLiteral(fd.Body, "ORBIT_LAUNCHER_INSTALL_SCRIPT_URL")
}

func isLiteralInt(e ast.Expr, v string) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == v
}

func countCalls(files []goFile, pred func(*ast.CallExpr) bool) int {
	n := 0
	for _, gf := range files {
		n += len(calls(gf.file, pred))
	}
	return n
}
