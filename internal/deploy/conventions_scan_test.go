package deploy

// Pins #232 (consistency review C10-C12): the error-text rule, one name and
// one type for what drifted, and one Docker query builder. The done
// criteria are source-level, so most of these are scans of every non-test
// Go file under internal/deploy and internal/ui (internal/ui is read from
// here as ../ui). Where a scan expects an absence, a positive control proves
// the detector can see the pattern.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// c232Files parses every non-test Go file directly under dir (comments
// kept), keyed by slash path.
func c232Files(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()
	out := map[string]*ast.File{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, perr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ParseComments)
		if perr != nil {
			return perr
		}
		out[filepath.ToSlash(path)] = f
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) == 0 {
		t.Fatalf("no non-test source files under %s", dir)
	}
	return out
}

func c232Sorted(m map[string]*ast.File) []string {
	var names []string
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// c232Both is deploy's and ui's non-test sources together.
func c232Both(t *testing.T) map[string]*ast.File {
	t.Helper()
	all := c232Files(t, ".")
	for p, f := range c232Files(t, "../ui") {
		all[p] = f
	}
	return all
}

// c232ErrorCall reports whether call is errors.New(...) or fmt.Errorf(...)
// and returns its first argument's string literals (nil when the argument
// is not made of literals).
func c232ErrorCall(call *ast.CallExpr) (name string, lits []string) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || len(call.Args) == 0 {
		return "", nil
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", nil
	}
	switch pkg.Name + "." + sel.Sel.Name {
	case "errors.New", "fmt.Errorf":
		name = pkg.Name + "." + sel.Sel.Name
	default:
		return "", nil
	}
	ast.Inspect(call.Args[0], func(n ast.Node) bool {
		if bl, ok := n.(*ast.BasicLit); ok && bl.Kind == token.STRING {
			if s, err := strconv.Unquote(bl.Value); err == nil {
				lits = append(lits, s)
			}
		}
		return true
	})
	return name, lits
}

// c232ErrorTextFaults lists the C10 faults in one parsed file: a ";" clause
// join in an error literal, and fmt.Errorf with no verb (errors.New says it).
func c232ErrorTextFaults(fset *token.FileSet, f *ast.File) []string {
	var faults []string
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name, lits := c232ErrorCall(call)
		if name == "" || len(lits) == 0 {
			return true
		}
		text := strings.Join(lits, "")
		pos := fset.Position(call.Pos())
		if strings.Contains(text, "; ") || strings.Contains(text, ";") {
			faults = append(faults, pos.String()+": ';' in error text "+strconv.Quote(text))
		}
		if name == "fmt.Errorf" && !strings.Contains(strings.ReplaceAll(text, "%%", ""), "%") {
			faults = append(faults, pos.String()+": fmt.Errorf with no verb, use errors.New: "+strconv.Quote(text))
		}
		return true
	})
	return faults
}

func c232Faults(t *testing.T, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return c232ErrorTextFaults(fset, f)
}

// The detector sees each forbidden shape and passes the sanctioned ones.
func TestC10_ErrorTextDetectorSeesTheForbiddenShapes(t *testing.T) {
	bad := `package p
import ("errors"; "fmt")
var A = fmt.Errorf("fetch %s; try again", "x")
var B = fmt.Errorf("plain fixed text")
var C = errors.New("one; two")
`
	if got := c232Faults(t, bad); len(got) != 3 {
		t.Errorf("detector found %d faults in the bad snippet, want 3: %v", len(got), got)
	}
	good := `package p
import ("errors"; "fmt")
var A = errors.New("plain fixed text")
var B = fmt.Errorf("fetch %s: %w — try again", "x", errors.New("y"))
var C = fmt.Errorf("100%% sure about %d", 3)
`
	if got := c232Faults(t, good); len(got) != 0 {
		t.Errorf("detector flagged sanctioned shapes: %v", got)
	}
}

// C10: error text reaches the screen verbatim. No ";" clause join, and
// errors.New (not a verb-less fmt.Errorf) for fixed text, in deploy and ui.
func TestC10_ErrorLiteralsFollowTheConvention(t *testing.T) {
	for _, dir := range []string{".", "../ui"} {
		for _, path := range c232Sorted(c232Files(t, dir)) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatal(err)
			}
			for _, fault := range c232ErrorTextFaults(fset, f) {
				t.Errorf("%s", fault)
			}
		}
	}
}

// C10 "one way": the rule is written once in package deploy's comments, as
// the issue words it (doc.go, or beside limits.go).
func TestC10_TheErrorTextRuleIsWrittenOnceInDeploy(t *testing.T) {
	files := c232Files(t, ".")
	var found []string
	for _, path := range c232Sorted(files) {
		for _, cg := range files[path].Comments {
			text := cg.Text()
			if strings.Contains(text, "what: cause") &&
				strings.Contains(text, "errors.New") &&
				strings.Contains(text, "lowercase") &&
				strings.Contains(text, "full stop") &&
				strings.Contains(text, " — ") {
				found = append(found, path)
			}
		}
	}
	if len(found) != 1 {
		t.Errorf("the error-text rule (lowercase, no full stop, `what: cause`, ` — ` before advice, no `;`, errors.New for fixed text) is written in %d comments (%v), want exactly 1", len(found), found)
	}
}

// C11 identifiers: the staged configuration directory is configTree
// everywhere; treeDir is gone from deploy and ui.
func TestC11_TheConfigurationDirectoryIsCalledConfigTreeEverywhere(t *testing.T) {
	treeDir := regexp.MustCompile(`\btreeDir\b`)
	for path, f := range c232Both(t) {
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok && treeDir.MatchString(id.Name) {
				t.Errorf("%s: identifier %s, want configTree", path, id.Name)
			}
			return true
		})
	}
}

// C11 prose: the words people see are "configuration tree", in comments and
// in error text alike; "configure tree" is gone.
func TestC11_ProseSaysConfigurationTree(t *testing.T) {
	for _, dir := range []string{".", "../ui"} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			src, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatal(err)
			}
			for i, line := range strings.Split(string(src), "\n") {
				if strings.Contains(strings.ToLower(line), "configure tree") {
					t.Errorf("%s/%s:%d says \"configure tree\", want \"configuration tree\": %s", dir, name, i+1, strings.TrimSpace(line))
				}
			}
		}
	}
}

// C11 type: deploy.Action is a typed string with the three engine actions,
// as its siblings RepairMode, ConfigStep and AuthMode are.
func TestC11_ActionIsATypedStringWithThreeConstants(t *testing.T) {
	files := c232Files(t, ".")
	declared := false
	consts := map[string]string{}
	for _, f := range files {
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if id, ok := s.Type.(*ast.Ident); ok && s.Name.Name == "Action" && id.Name == "string" {
						declared = true
					}
				case *ast.ValueSpec:
					id, _ := s.Type.(*ast.Ident)
					if id == nil || id.Name != "Action" {
						continue
					}
					for i, n := range s.Names {
						if i < len(s.Values) {
							if bl, ok := s.Values[i].(*ast.BasicLit); ok {
								v, _ := strconv.Unquote(bl.Value)
								consts[n.Name] = v
							}
						}
					}
				}
			}
		}
	}
	if !declared {
		t.Error("no `type Action string` in package deploy")
	}
	want := map[string]string{"ActionInstall": "install", "ActionUpdate": "update", "ActionRepair": "repair"}
	for name, value := range want {
		if consts[name] != value {
			t.Errorf("const %s Action = %q, want %q (found %q)", name, value, value, consts[name])
		}
	}
}

// C11 type: BuildEngineCommand takes an Action, so an unknown action is
// rejected by type, not by comparing a bare string.
func TestC11_BuildEngineCommandTakesAnAction(t *testing.T) {
	files := c232Files(t, ".")
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != "BuildEngineCommand" {
				continue
			}
			params := fn.Type.Params.List
			if len(params) < 3 {
				t.Fatalf("BuildEngineCommand has %d parameter groups, want script, targetDir, action", len(params))
			}
			last := params[len(params)-1]
			id, ok := last.Type.(*ast.Ident)
			if !ok || id.Name != "Action" {
				t.Errorf("BuildEngineCommand's action parameter has type %T %v, want Action", last.Type, last.Type)
			}
			return
		}
	}
	t.Error("BuildEngineCommand not found")
}

// C11 grep: "install", "update", "repair" listed together appears nowhere in
// internal outside the constants; the action switch names the constants.
func TestC11_NoBareActionStringListsRemain(t *testing.T) {
	list := regexp.MustCompile(`"install",\s*"update",\s*"repair"`)
	err := filepath.WalkDir("..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if list.Match(src) {
			t.Errorf("%s still lists \"install\", \"update\", \"repair\" as bare strings", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ---- C12: one Docker query builder ----

func c232DockerLiteralArg(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "exec" || (sel.Sel.Name != "Command" && sel.Sel.Name != "CommandContext") {
		return false
	}
	for _, a := range call.Args {
		if bl, ok := a.(*ast.BasicLit); ok && bl.Kind == token.STRING && bl.Value == `"docker"` {
			return true
		}
	}
	return false
}

// c232DockerBuilders lists "pos in func" for every exec.Command* call with
// a literal "docker" argument, outside dockerCommand.
func c232DockerOutsideHelper(fset *token.FileSet, f *ast.File) []string {
	var out []string
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Name.Name == "dockerCommand" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok && c232DockerLiteralArg(call) {
				out = append(out, fset.Position(call.Pos()).String()+" in "+fn.Name.Name)
			}
			return true
		})
	}
	return out
}

func TestC12_DockerDetectorSeesAHandBuiltCommand(t *testing.T) {
	src := `package p
import ("context"; "os/exec")
func a(ctx context.Context) *exec.Cmd { return exec.CommandContext(ctx, "docker", "ps") }
func dockerCommand(ctx context.Context, args ...string) *exec.Cmd { return exec.CommandContext(ctx, "docker", args...) }
func b() *exec.Cmd { return exec.Command("bash", "x") }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := c232DockerOutsideHelper(fset, f); len(got) != 1 || !strings.HasSuffix(got[0], "in a") {
		t.Errorf("detector found %v, want exactly the hand-built command in a", got)
	}
}

// C12: "docker" appears in exec.Command* only inside the helper.
func TestC12_DockerIsBuiltOnlyInsideTheHelper(t *testing.T) {
	for _, path := range c232Sorted(c232Files(t, ".")) {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, hit := range c232DockerOutsideHelper(fset, f) {
			t.Errorf("hand-built docker command outside dockerCommand: %s", hit)
		}
	}
}

// C12: the helper is dockerCommand(ctx, args ...string) *exec.Cmd in
// detached.go, attached to the launcher's session (no Setsid) and bounded
// by pipeWaitDelay; and the three callers the issue names use it.
func TestC12_TheHelperIsDeclaredInDetachedGoAndUsedByTheThreeQueries(t *testing.T) {
	files := c232Files(t, ".")
	var helper *ast.FuncDecl
	for path, f := range files {
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "dockerCommand" {
				if filepath.Base(path) != "detached.go" {
					t.Errorf("dockerCommand is in %s, want detached.go", path)
				}
				helper = fn
			}
		}
	}
	if helper == nil {
		t.Fatal("no func dockerCommand in package deploy")
	}
	ps := helper.Type.Params.List
	if len(ps) != 2 {
		t.Fatalf("dockerCommand has %d parameter groups, want (ctx context.Context, args ...string)", len(ps))
	}
	if _, ok := ps[1].Type.(*ast.Ellipsis); !ok {
		t.Errorf("dockerCommand's last parameter is %T, want variadic args ...string", ps[1].Type)
	}
	if helper.Type.Results == nil || len(helper.Type.Results.List) != 1 {
		t.Fatal("dockerCommand must return one *exec.Cmd")
	}
	var sawWaitDelay, sawSetsid bool
	ast.Inspect(helper.Body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.SelectorExpr:
			if x.Sel.Name == "WaitDelay" {
				sawWaitDelay = true
			}
		case *ast.Ident:
			if x.Name == "Setsid" || x.Name == "SysProcAttr" {
				sawSetsid = true
			}
		}
		return true
	})
	if !sawWaitDelay {
		t.Error("dockerCommand does not set cmd.WaitDelay")
	}
	if sawSetsid {
		t.Error("dockerCommand must stay attached: no SysProcAttr / Setsid (that is detachedCommand)")
	}

	callers := map[string]bool{"UnownedDatabaseVolumes": false, "InstalledAt": false, "standDownCommand": false}
	for _, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			if _, want := callers[fn.Name.Name]; !want {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "dockerCommand" {
						callers[fn.Name.Name] = true
					}
				}
				return true
			})
		}
	}
	for name, used := range callers {
		if !used {
			t.Errorf("%s does not build its command with dockerCommand", name)
		}
	}
}
