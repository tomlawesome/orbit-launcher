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

// #208 (#199 WI-4, EN-2, EN-7, EN-8, EN-9). The deployment's file names,
// the env-value rule and the script lookup each live once, in deploy:
// a second typing of a file name is a second place to forget when it
// changes, and a staging suffix that drifts from its sweep leaves a
// crashed adoption's half-written secret to be imported back.

// fileNameConstants maps each deployment file name, or the fragment of
// one, to the deploy constant that is its only spelling (EN-8, EN-9),
// and says whether the constant's value must be exactly the fragment or
// only contain it.
var fileNameConstants = []struct {
	fragment, constant string
	exact              bool
}{
	{".env-orbit", "EnvFile", true},
	{".orbit-secrets", "SecretsDir", true},
	{".tmp-", "stagingSuffix", false},
}

// moduleRoot is the directory holding go.mod, found upward from the
// package under test.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the package under test")
		}
		dir = parent
	}
}

// parsedSource is one non-test Go file of the module.
type parsedSource struct {
	rel  string // slash-separated, relative to the module root
	file *ast.File
}

// moduleSources parses every non-test Go file in the module, skipping
// hidden directories (.git, .claude worktrees), testdata and any nested
// module.
func moduleSources(t *testing.T, fset *token.FileSet) []parsedSource {
	t.Helper()
	root := moduleRoot(t)
	var out []parsedSource
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root {
				if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata" || name == "vendor" {
					return filepath.SkipDir
				}
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		out = append(out, parsedSource{rel: filepath.ToSlash(rel), file: file})
		return nil
	})
	if err != nil {
		t.Fatalf("walk the module: %v", err)
	}
	return out
}

// at is pos as file:line:column relative to the module root.
func at(fset *token.FileSet, src parsedSource, pos token.Pos) string {
	p := fset.Position(pos)
	return src.rel + ":" + strconv.Itoa(p.Line) + ":" + strconv.Itoa(p.Column)
}

// isDeployPackage reports whether src belongs to this package: the one
// place the constants and helpers may be defined.
func isDeployPackage(src parsedSource) bool {
	return src.file.Name.Name == "deploy" && filepath.ToSlash(filepath.Dir(src.rel)) == "internal/deploy"
}

// constDefinition is one `const name = "value"` in the deploy package.
type constDefinition struct {
	at    string
	value string
	lit   *ast.BasicLit
}

// deployStringConsts finds every package-level or local const in deploy
// whose value is a single string literal, keyed by the constant's name.
func deployStringConsts(fset *token.FileSet, sources []parsedSource) map[string][]constDefinition {
	defs := map[string][]constDefinition{}
	for _, src := range sources {
		if !isDeployPackage(src) {
			continue
		}
		ast.Inspect(src.file, func(n ast.Node) bool {
			decl, ok := n.(*ast.GenDecl)
			if !ok || decl.Tok != token.CONST {
				return true
			}
			for _, spec := range decl.Specs {
				vs := spec.(*ast.ValueSpec)
				for i, name := range vs.Names {
					if i >= len(vs.Values) {
						continue
					}
					lit, ok := vs.Values[i].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					value, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					defs[name.Name] = append(defs[name.Name], constDefinition{
						at: at(fset, src, lit.Pos()), value: value, lit: lit,
					})
				}
			}
			return true
		})
	}
	return defs
}

// EN-8, EN-9: each file name is defined once, as the constant the
// decisions name, in deploy.
func TestDeploy_DeploymentFileNamesAreDefinedOnceAsConstants(t *testing.T) {
	fset := token.NewFileSet()
	defs := deployStringConsts(fset, moduleSources(t, fset))
	for _, c := range fileNameConstants {
		got := defs[c.constant]
		switch {
		case len(got) == 0:
			t.Errorf("deploy defines no string constant %s for %q", c.constant, c.fragment)
			continue
		case len(got) > 1:
			var at []string
			for _, d := range got {
				at = append(at, d.at)
			}
			t.Errorf("%s is defined %d times (%s), want once", c.constant, len(got), strings.Join(at, ", "))
		}
		v := got[0].value
		if c.exact && v != c.fragment {
			t.Errorf("%s = %q at %s, want %q", c.constant, v, got[0].at, c.fragment)
		}
		if !c.exact && !strings.Contains(v, c.fragment) {
			t.Errorf("%s = %q at %s, want it to hold %q", c.constant, v, got[0].at, c.fragment)
		}
	}
}

// Done-criterion of #208: no bare .env-orbit, .orbit-secrets or .tmp-
// literal anywhere in the module's non-test code except the one constant
// that spells it, the UI's display mapping included.
func TestModuleSource_DeploymentFileNamesAreTypedOnlyInTheirConstants(t *testing.T) {
	fset := token.NewFileSet()
	sources := moduleSources(t, fset)
	allowed := map[*ast.BasicLit]bool{}
	defs := deployStringConsts(fset, sources)
	for _, c := range fileNameConstants {
		if got := defs[c.constant]; len(got) == 1 {
			allowed[got[0].lit] = true
		}
	}
	for _, src := range sources {
		ast.Inspect(src.file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING || allowed[lit] {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, c := range fileNameConstants {
				if strings.Contains(value, c.fragment) {
					t.Errorf("%s: %s typed bare; use deploy's %s", at(fset, src, lit.Pos()), lit.Value, c.constant)
				}
			}
			return true
		})
	}
}

// funcDecls lists every top-level function (not method) named name in the
// deploy package's non-test files.
func funcDecls(fset *token.FileSet, sources []parsedSource, name string) []*ast.FuncDecl {
	var out []*ast.FuncDecl
	for _, src := range sources {
		if !isDeployPackage(src) {
			continue
		}
		for _, decl := range src.file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
				out = append(out, fn)
			}
		}
	}
	return out
}

// paramTypes flattens a function's parameter list to one type expression
// per parameter, as source text.
func paramTypes(fn *ast.FuncDecl) []string {
	var out []string
	for _, field := range fn.Type.Params.List {
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		for range n {
			if ident, ok := field.Type.(*ast.Ident); ok {
				out = append(out, ident.Name)
			} else {
				out = append(out, "?")
			}
		}
	}
	return out
}

// callsTo lists, for each call of the package function name in deploy's
// non-test files, the source text of its last argument when that is a
// plain identifier ("" otherwise).
func callsTo(sources []parsedSource, name string) []string {
	var lastArgs []string
	for _, src := range sources {
		if !isDeployPackage(src) {
			continue
		}
		ast.Inspect(src.file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); !ok || ident.Name != name {
				return true
			}
			last := ""
			if len(call.Args) > 0 {
				if ident, ok := call.Args[len(call.Args)-1].(*ast.Ident); ok {
					last = ident.Name
				}
			}
			lastArgs = append(lastArgs, last)
			return true
		})
	}
	return lastArgs
}

// EN-2: one envValue(raw) in deploy, the only value rule Detect applies.
func TestDeploy_OneEnvValueRule(t *testing.T) {
	fset := token.NewFileSet()
	sources := moduleSources(t, fset)
	fns := funcDecls(fset, sources, "envValue")
	if len(fns) != 1 {
		t.Fatalf("deploy declares envValue %d times, want once", len(fns))
	}
	if got := paramTypes(fns[0]); len(got) != 1 || got[0] != "string" {
		t.Errorf("envValue takes %v, want one raw string", got)
	}
	if n := len(callsTo(sources, "envValue")); n == 0 {
		t.Error("nothing in deploy calls envValue")
	}
}

// EN-7: one script lookup, requireScript(dir, name, sentinel), used by
// both the repair and the configure lookups with their own sentinel.
func TestDeploy_OneScriptLookupHelper(t *testing.T) {
	fset := token.NewFileSet()
	sources := moduleSources(t, fset)
	fns := funcDecls(fset, sources, "requireScript")
	if len(fns) != 1 {
		t.Fatalf("deploy declares requireScript %d times, want once", len(fns))
	}
	if got := paramTypes(fns[0]); len(got) != 3 || got[0] != "string" || got[1] != "string" || got[2] != "error" {
		t.Errorf("requireScript takes %v, want (dir, name string, sentinel error)", got)
	}
	sentinels := map[string]bool{}
	for _, last := range callsTo(sources, "requireScript") {
		sentinels[last] = true
	}
	for _, want := range []string{"ErrRepairUnavailable", "ErrNoConfigTree"} {
		if !sentinels[want] {
			t.Errorf("no requireScript call passes %s: that lookup is still its own copy", want)
		}
	}
}
