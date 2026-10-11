package ui

// Pins #210 (one menu value, #199 WI-6; UI-12) from the issue and the
// #199 decisions note, not from the code: one `menu` type (items,
// selection, up/down/enter by label); two-row toggles are two-item
// menus; the index-dispatch helper and the hand-typed wrap counts go.
// Splash keeps its digit shortcuts and userNavigated, so splash.go is
// left out of the index-switch scan.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

func parseSnippet(t *testing.T, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", "package p\n"+src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fset, f
}

func mentionsSel(e ast.Expr) bool {
	return strings.Contains(strings.ToLower(types.ExprString(e)), "sel")
}

func isIntLit(e ast.Expr, val string) bool {
	lit, ok := e.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && (val == "" || lit.Value == val)
}

// toggleSites finds `1 - <selection>`: the hand-typed flip of a
// two-row menu.
func toggleSites(fset *token.FileSet, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BinaryExpr); ok && b.Op == token.SUB && isIntLit(b.X, "1") && mentionsSel(b.Y) {
			out = append(out, fset.Position(b.Pos()).String()+": "+types.ExprString(b))
		}
		return true
	})
	return out
}

// wrapSites finds `<selection arithmetic> % <integer literal>`: a wrap
// count typed by hand instead of taken from the menu's items.
func wrapSites(fset *token.FileSet, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		if b, ok := n.(*ast.BinaryExpr); ok && b.Op == token.REM && isIntLit(b.Y, "") && mentionsSel(b.X) {
			out = append(out, fset.Position(b.Pos()).String()+": "+types.ExprString(b))
		}
		return true
	})
	return out
}

// indexSwitchSites finds `switch <selection> { case 0: ... }`: dispatch
// by position, which drifts when an item is added, removed or hidden.
func indexSwitchSites(fset *token.FileSet, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok || sw.Tag == nil || !mentionsSel(sw.Tag) {
			return true
		}
		for _, s := range sw.Body.List {
			if cc, ok := s.(*ast.CaseClause); ok {
				for _, e := range cc.List {
					if isIntLit(e, "") {
						out = append(out, fset.Position(sw.Pos()).String()+": switch "+types.ExprString(sw.Tag))
						return true
					}
				}
			}
		}
		return true
	})
	return out
}

// The detectors must see the patterns they forbid, or the scans below
// would pass on any code.
func TestMenuScanDetectorsSeeTheirPatterns(t *testing.T) {
	fset, f := parseSnippet(t, `
func a(m M) { m.sel = 1 - m.sel }
func b(m M) { m.profileSel = (m.profileSel + 1) % 3 }
func c(m M) { switch m.confirmSel { case 0: x(); case 1: y() } }
func d(m M) { m.sel = (m.sel + 1) % len(m.items); switch m.label { case "a": x() } }
`)
	if got := toggleSites(fset, f); len(got) != 1 {
		t.Errorf("toggle detector found %v, want exactly the 1 - sel site", got)
	}
	if got := wrapSites(fset, f); len(got) != 1 || !strings.Contains(got[0], "% 3") {
		t.Errorf("wrap detector found %v, want exactly the %% 3 site (len(items) is fine)", got)
	}
	if got := indexSwitchSites(fset, f); len(got) != 1 || !strings.Contains(got[0], "confirmSel") {
		t.Errorf("index-switch detector found %v, want exactly the confirmSel site (a label switch is fine)", got)
	}
}

func scanUI(t *testing.T, skip string, sites func(*token.FileSet, *ast.File) []string) []string {
	t.Helper()
	var out []string
	fset := token.NewFileSet()
	for _, path := range uiSourceFiles(t) {
		if filepath.Dir(path) != "." || filepath.Base(path) == skip {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, sites(fset, f)...)
	}
	return out
}

// Failed first when no `type menu` exists; the model keeps one.
func TestOneMenuTypeIsDeclared(t *testing.T) {
	fset := token.NewFileSet()
	var found []string
	for _, path := range uiSourceFiles(t) {
		if filepath.Dir(path) != "." {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, s := range gd.Specs {
				if ts := s.(*ast.TypeSpec); ts.Name.Name == "menu" {
					if _, isStruct := ts.Type.(*ast.StructType); !isStruct {
						t.Errorf("%s: type menu is not a struct holding items and a selection", fset.Position(ts.Pos()))
					}
					found = append(found, fset.Position(ts.Pos()).String())
				}
			}
		}
	}
	if len(found) != 1 {
		t.Fatalf("type menu declared %d times (%v), want exactly once", len(found), found)
	}
}

func TestNoHandTypedTwoRowToggle(t *testing.T) {
	for _, s := range scanUI(t, "", toggleSites) {
		t.Errorf("two-row toggle typed by hand, use a two-item menu: %s", s)
	}
}

func TestNoHandTypedMenuWrapCount(t *testing.T) {
	for _, s := range scanUI(t, "", wrapSites) {
		t.Errorf("wrap count typed by hand, take it from the menu's items: %s", s)
	}
}

// splash.go is excluded: its digit shortcuts stay with the splash.
func TestNoIndexDispatchOnAMenuSelection(t *testing.T) {
	for _, s := range scanUI(t, "splash.go", indexSwitchSites) {
		t.Errorf("menu dispatched by position, dispatch by label: %s", s)
	}
}

// Passed already: the two-row confirm screens flip between their rows
// with either arrow and ignore other keys. The shared menu must keep it.
func TestTwoRowConfirmScreensToggleWithEitherArrow(t *testing.T) {
	rows := []struct {
		name         string
		model        func(*testing.T) tea.Model
		first, other string
	}{
		{"Update", confirmUpdate, "▸ Update Orbit", "▸ Cancel"},
		{"Remove", confirmRemove, "▸ Stand down Orbit", "▸ Cancel"},
	}
	view := func(m tea.Model) string { return ansi.Strip(m.View().Content) }
	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			m := r.model(t)
			if !strings.Contains(view(m), r.first) {
				t.Fatalf("first row not selected on arrival:\n%s", view(m))
			}
			for _, k := range []rune{tea.KeyDown, tea.KeyUp} {
				moved, _ := leavePress(t, m, key(k))
				if !strings.Contains(view(moved), r.other) {
					t.Fatalf("key %v from the first row did not select %q:\n%s", k, r.other, view(moved))
				}
				back, _ := leavePress(t, moved, key(k))
				if !strings.Contains(view(back), r.first) {
					t.Fatalf("key %v from the second row did not wrap to %q:\n%s", k, r.first, view(back))
				}
			}
			same, cmd := leavePress(t, m, runeKey('x'))
			if cmd != nil || view(same) != view(m) {
				t.Fatal("an unbound key acted on the confirm screen")
			}
		})
	}
}

// Passed already: Install's profile list wraps in both directions over
// its three rows (the count must come from the menu, not a typed 3).
func TestInstallProfileMenuWrapsOverAllRows(t *testing.T) {
	m := profileInstall(t)
	view := func(m tea.Model) string { return ansi.Strip(m.View().Content) }
	seen := []string{}
	for _, want := range []string{"▸ AI", "▸ Full", "▸ Standard"} {
		m, _ = leavePress(t, m, key(tea.KeyDown))
		if !strings.Contains(view(m), want) {
			t.Fatalf("Down did not reach %q (visited %v):\n%s", want, seen, view(m))
		}
		seen = append(seen, want)
	}
	for _, want := range []string{"▸ Full", "▸ AI", "▸ Standard"} {
		m, _ = leavePress(t, m, key(tea.KeyUp))
		if !strings.Contains(view(m), want) {
			t.Fatalf("Up did not reach %q:\n%s", want, view(m))
		}
	}
}
