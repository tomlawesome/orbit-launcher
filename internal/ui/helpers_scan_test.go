package ui

// Pins #215 (#199 WI-11; UI-8, UI-19): the package's test helpers have
// one home each. The helpers are the subject, so these are scans of the
// package's own test files (go/parser, go/ast), not behaviour checks:
//
//   - one driver that expands tea.BatchMsg (UI-8);
//   - a generic updateAs[T], one screen helper per kind, one engine
//     event builder, and ctrlC() as the only place the Ctrl-C key is
//     spelled out (UI-19);
//   - all of them in helpers_test.go (decisions note, "Test helpers").

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// ownScanFiles are the pinning files themselves; the scans skip them so
// their own examples cannot satisfy or trip a rule.
var ownScanFiles = map[string]bool{
	"helpers_scan_test.go": true,
	"seams_bundle_test.go": true,
}

type scanFile struct {
	name string
	file *ast.File
}

// testFilesParsed parses every *_test.go in this package directory.
func testFilesParsed(t *testing.T) []scanFile {
	t.Helper()
	paths, err := filepath.Glob("*_test.go")
	if err != nil || len(paths) == 0 {
		t.Fatalf("no test files found: %v", err)
	}
	var out []scanFile
	fset := token.NewFileSet()
	for _, p := range paths {
		if ownScanFiles[filepath.Base(p)] {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", p, err)
		}
		out = append(out, scanFile{name: filepath.Base(p), file: f})
	}
	return out
}

// helperFuncs lists the top-level non-method, non-Test functions.
func helperFuncs(files []scanFile) []helperFunc {
	var out []helperFunc
	for _, sf := range files {
		for _, d := range sf.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Body == nil || strings.HasPrefix(fd.Name.Name, "Test") {
				continue
			}
			out = append(out, helperFunc{file: sf.name, decl: fd})
		}
	}
	return out
}

type helperFunc struct {
	file string
	decl *ast.FuncDecl
}

func (h helperFunc) name() string { return h.decl.Name.Name }

// inspectBody walks the function body.
func (h helperFunc) inspect(visit func(ast.Node) bool) { ast.Inspect(h.decl.Body, visit) }

func (h helperFunc) hasBatchAssertion() bool {
	found := false
	h.inspect(func(n ast.Node) bool {
		if ta, ok := n.(*ast.TypeAssertExpr); ok && ta.Type != nil {
			if sel, ok := ta.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "BatchMsg" {
				found = true
			}
		}
		return !found
	})
	return found
}

// isDriver reports whether the function feeds command results back into
// a model: it loops, runs a command held in a local variable (cmd(),
// next()), and hands the result to an Update-like call.
func (h helperFunc) isDriver() bool {
	var loops, runsLocalCmd, updates bool
	h.inspect(func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.ForStmt, *ast.RangeStmt:
			loops = true
		case *ast.CallExpr:
			switch fn := x.Fun.(type) {
			case *ast.Ident:
				if fn.Obj != nil && fn.Obj.Kind == ast.Var {
					runsLocalCmd = true
				}
				if strings.Contains(strings.ToLower(fn.Name), "update") {
					updates = true
				}
			case *ast.SelectorExpr:
				if strings.Contains(strings.ToLower(fn.Sel.Name), "update") {
					updates = true
				}
			}
		}
		return true
	})
	return loops && runsLocalCmd && updates
}

// TestOneDriverExpandsBatches pins UI-8: `drive` (install_test.go) hands
// a tea.BatchMsg straight to Update, which ignores it, while `appDrive`
// (app_paths_test.go) expands it. A test relying on a batched command
// would pass without the command ever running. One driver, with batch
// expansion and an optional sink, lives in helpers_test.go.
func TestOneDriverExpandsBatches(t *testing.T) {
	var drivers []helperFunc
	for _, h := range helperFuncs(testFilesParsed(t)) {
		if h.isDriver() {
			drivers = append(drivers, h)
		}
	}
	var names []string
	for _, d := range drivers {
		names = append(names, d.file+":"+d.name())
	}
	if len(drivers) != 1 {
		t.Fatalf("want exactly one command driver across the test files, found %d: %v", len(drivers), names)
	}
	d := drivers[0]
	if d.file != "helpers_test.go" {
		t.Errorf("the driver %s is in %s; it belongs in helpers_test.go", d.name(), d.file)
	}
	if !d.hasBatchAssertion() {
		t.Errorf("driver %s never expands a tea.BatchMsg: batched commands would not run", d.name())
	}
	// The optional sink (the engine stream's pushed messages) is a
	// trailing variadic parameter, as drive's was.
	variadic := false
	for _, p := range d.decl.Type.Params.List {
		if _, ok := p.Type.(*ast.Ellipsis); ok {
			variadic = true
		}
	}
	if !variadic {
		t.Errorf("driver %s has no optional (variadic) sink parameter", d.name())
	}
}

// TestHelpersTestFileExists pins that the shared helpers have the home
// the decisions note names. Paired with the scans around it, which fail
// when a helper is elsewhere.
func TestHelpersTestFileExists(t *testing.T) {
	for _, sf := range testFilesParsed(t) {
		if sf.name == "helpers_test.go" {
			return
		}
	}
	t.Fatal("internal/ui/helpers_test.go does not exist")
}

// TestUpdateAsIsTheOnlyUpdateLoop pins UI-19: one generic updateAs[T]
// replaces the per-model Update loops (appUpdate, installUpdate,
// successUpdate, removeUpdate) and the single-message versions
// (update, repairUpdate).
func TestUpdateAsIsTheOnlyUpdateLoop(t *testing.T) {
	files := testFilesParsed(t)
	var found []helperFunc
	for _, h := range helperFuncs(files) {
		if h.name() == "updateAs" {
			found = append(found, h)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want updateAs declared exactly once, found %d", len(found))
	}
	u := found[0]
	if u.file != "helpers_test.go" {
		t.Errorf("updateAs is in %s; it belongs in helpers_test.go", u.file)
	}
	if u.decl.Type.TypeParams == nil || len(u.decl.Type.TypeParams.List) == 0 {
		t.Errorf("updateAs is not generic; it must serve every model type")
	}

	legacy := map[string]bool{
		"appUpdate": true, "installUpdate": true, "successUpdate": true,
		"removeUpdate": true, "repairUpdate": true, "update": true,
	}
	for _, h := range helperFuncs(files) {
		if h.name() == "updateAs" {
			continue
		}
		callsUpdate, asserts, ranges := false, false, false
		h.inspect(func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.CallExpr:
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Update" {
					callsUpdate = true
				}
			case *ast.TypeAssertExpr:
				if x.Type != nil {
					asserts = true
				}
			case *ast.RangeStmt:
				ranges = true
			}
			return true
		})
		// A thin wrapper over updateAs is fine; a retyped loop is not.
		if legacy[h.name()] && callsUpdate && asserts {
			t.Errorf("%s (%s) still type-asserts its own model: it duplicates updateAs", h.name(), h.file)
		}
		if ranges && callsUpdate && asserts {
			t.Errorf("%s (%s) is a per-model Update loop; use updateAs", h.name(), h.file)
		}
	}
}

// screenKind classifies a helper whose whole body is
// `return ansi.Strip(<x>.View().Content)` ("view") or
// `return ansi.Strip(<x>.view(w, h))` ("run"); "" otherwise. ansi.Strip
// is the one stripper since #214 (wi10_widths_test.go forbids a local
// stripANSI).
func screenKind(h helperFunc) string {
	stmts := h.decl.Body.List
	if len(stmts) != 1 {
		return ""
	}
	ret, ok := stmts[0].(*ast.ReturnStmt)
	if !ok || len(ret.Results) != 1 {
		return ""
	}
	call, ok := ret.Results[0].(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return ""
	}
	fn, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || fn.Sel.Name != "Strip" {
		return ""
	}
	if pkg, ok := fn.X.(*ast.Ident); !ok || pkg.Name != "ansi" {
		return ""
	}
	switch arg := call.Args[0].(type) {
	case *ast.SelectorExpr: // m.View().Content
		if arg.Sel.Name != "Content" {
			return ""
		}
		if inner, ok := arg.X.(*ast.CallExpr); ok {
			if sel, ok := inner.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "View" {
				return "view"
			}
		}
	case *ast.CallExpr: // r.view(80, 26)
		if sel, ok := arg.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "view" {
			return "run"
		}
	}
	return ""
}

// TestOneScreenHelperPerKind pins UI-19's screen helpers. Today
// screen, repairScreen and appScreen all strip a model's View(), and
// collectScreen and runScreen are identical copies for an engineRun
// (which has view(w, h), not View()). One of each remains, in
// helpers_test.go.
func TestOneScreenHelperPerKind(t *testing.T) {
	byKind := map[string][]helperFunc{}
	for _, h := range helperFuncs(testFilesParsed(t)) {
		if k := screenKind(h); k != "" {
			byKind[k] = append(byKind[k], h)
		}
	}
	for _, kind := range []string{"view", "run"} {
		hs := byKind[kind]
		var names []string
		for _, h := range hs {
			names = append(names, h.file+":"+h.name())
		}
		if len(hs) != 1 {
			t.Errorf("want one %q screen helper, found %d: %v", kind, len(hs), names)
			continue
		}
		if hs[0].file != "helpers_test.go" {
			t.Errorf("screen helper %s is in %s; it belongs in helpers_test.go", hs[0].name(), hs[0].file)
		}
	}
}

// eventBuilder: five string parameters (phase, component, state, reason,
// action) building an engine.Event, bare or wrapped in an EventMsg.
func eventBuilder(h helperFunc) bool {
	nparams := 0
	for _, p := range h.decl.Type.Params.List {
		if id, ok := p.Type.(*ast.Ident); !ok || id.Name != "string" {
			return false
		}
		nparams += len(p.Names)
	}
	if nparams != 5 {
		return false
	}
	built := false
	h.inspect(func(n ast.Node) bool {
		if cl, ok := n.(*ast.CompositeLit); ok {
			if sel, ok := cl.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "Event" {
				built = true
			}
		}
		return !built
	})
	return built
}

// TestOneEngineEventBuilder pins UI-19: ev (install_test.go) and
// consoleEvent (console_test.go) build the same event from the same
// five strings. One remains, in helpers_test.go.
func TestOneEngineEventBuilder(t *testing.T) {
	var builders []helperFunc
	var names []string
	for _, h := range helperFuncs(testFilesParsed(t)) {
		if eventBuilder(h) {
			builders = append(builders, h)
			names = append(names, h.file+":"+h.name())
		}
	}
	if len(builders) != 1 {
		t.Fatalf("want one engine event builder, found %d: %v", len(builders), names)
	}
	if builders[0].file != "helpers_test.go" {
		t.Errorf("event builder %s is in %s; it belongs in helpers_test.go", builders[0].name(), builders[0].file)
	}
}

// isCtrlCLiteral matches tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} in
// either field order.
func isCtrlCLiteral(cl *ast.CompositeLit) bool {
	sel, ok := cl.Type.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "KeyPressMsg" {
		return false
	}
	codeC, modCtrl := false, false
	for _, e := range cl.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, _ := kv.Key.(*ast.Ident)
		if key == nil {
			continue
		}
		switch key.Name {
		case "Code":
			if lit, ok := kv.Value.(*ast.BasicLit); ok && lit.Value == "'c'" {
				codeC = true
			}
		case "Mod":
			if s, ok := kv.Value.(*ast.SelectorExpr); ok && s.Sel.Name == "ModCtrl" {
				modCtrl = true
			}
		}
	}
	return codeC && modCtrl
}

// TestCtrlCIsSpelledOutOnlyInCtrlC pins UI-19's thirteen literals
// (app_test.go twice, configcollect_test.go ten times,
// repair_teatest_test.go once): each becomes ctrlC(). The key itself is
// built in exactly one place, ctrlC in helpers_test.go.
func TestCtrlCIsSpelledOutOnlyInCtrlC(t *testing.T) {
	files := testFilesParsed(t)

	var decls []helperFunc
	for _, h := range helperFuncs(files) {
		if h.name() == "ctrlC" {
			decls = append(decls, h)
		}
	}
	if len(decls) != 1 {
		t.Fatalf("want ctrlC declared exactly once, found %d", len(decls))
	}
	if decls[0].file != "helpers_test.go" {
		t.Errorf("ctrlC is in %s; it belongs in helpers_test.go", decls[0].file)
	}

	for _, sf := range files {
		for _, d := range sf.file.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil || fd.Name.Name == "ctrlC" {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if cl, ok := n.(*ast.CompositeLit); ok && isCtrlCLiteral(cl) {
					t.Errorf("%s: %s spells out the Ctrl-C key; use ctrlC()", sf.name, fd.Name.Name)
				}
				return true
			})
		}
		// Package-level variables and constants count too.
		for _, d := range sf.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			ast.Inspect(gd, func(n ast.Node) bool {
				if cl, ok := n.(*ast.CompositeLit); ok && isCtrlCLiteral(cl) {
					t.Errorf("%s: a package-level declaration spells out the Ctrl-C key; use ctrlC()", sf.name)
				}
				return true
			})
		}
	}
}
