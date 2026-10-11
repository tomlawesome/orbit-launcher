package ui

// Pins #214 (boxes; #199 WI-10, UI-9): one drawBox in scene.go draws the
// console's box, the notice's box and the removal command's box, all in
// style.Border. Removal's box was style.BorderSoft through lipgloss's
// own border; the mockup shows no second border colour.

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// wi10Parse parses one non-test file of package ui.
func wi10Parse(t *testing.T, name string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	return fset, f
}

// wi10Calls reports whether f calls a function or method named name.
func wi10Calls(f *ast.File, name string) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok {
			switch fn := c.Fun.(type) {
			case *ast.Ident:
				found = found || fn.Name == name
			case *ast.SelectorExpr:
				found = found || fn.Sel.Name == name
			}
		}
		return true
	})
	return found
}

// wi10Declared lists where a top-level function, const, var or type
// called name is declared among the given files.
func wi10Declared(t *testing.T, files []string, name string) []string {
	t.Helper()
	var at []string
	for _, file := range files {
		fset, f := wi10Parse(t, file)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil && d.Name.Name == name {
					at = append(at, fset.Position(d.Pos()).String())
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.ValueSpec:
						for _, id := range s.Names {
							if id.Name == name {
								at = append(at, fset.Position(id.Pos()).String())
							}
						}
					case *ast.TypeSpec:
						if s.Name.Name == name {
							at = append(at, fset.Position(s.Pos()).String())
						}
					}
				}
			}
		}
	}
	return at
}

// borderRun is the opening styling sequence of style.Border text, taken
// from the renderer itself so the test names no hex value.
func borderRun(t *testing.T) string {
	t.Helper()
	const probe = "\x00"
	styled := lipgloss.NewStyle().Foreground(style.Border).Render(probe)
	open, _, ok := strings.Cut(styled, probe)
	if !ok || open == "" {
		t.Fatalf("style.Border renders %q: no opening sequence to look for", styled)
	}
	return open
}

func wi10RemovalDoneView(t *testing.T) string {
	t.Helper()
	m := newTestRemoveModel(func(context.Context, string) error { return nil })
	updated, cmd := m.Update(key(tea.KeyEnter))
	updated, _ = updated.Update(cmd())
	if got := updated.(RemoveModel).state; got != removeStateDone {
		t.Fatalf("state = %v, want removeStateDone", got)
	}
	return updated.View().Content
}

func TestBoxes_EveryBoxIsDrawnInTheBorderColour(t *testing.T) {
	border := borderRun(t)
	notice, _, _ := openNotice(t, 80, 26, engineRunSeams{})
	cases := []struct {
		name, view string
	}{
		// Passed already: the console and the notice are the colour
		// the removal box has to match.
		{"console", strings.Join(testConsole().contentLines(80, 26), "\n")},
		{"notice", notice.View().Content},
		{"removal command", wi10RemovalDoneView(t)},
	}
	for _, c := range cases {
		for _, glyph := range []string{"╭", "│", "╰"} {
			if !strings.Contains(c.view, border+glyph) {
				t.Errorf("%s: no %q drawn in style.Border", c.name, glyph)
			}
		}
	}
}

// The soft border colour does not frame the removal command.
func TestBoxes_RemovalCommandBoxIsNotTheSoftBorderColour(t *testing.T) {
	const probe = "\x00"
	soft, _, _ := strings.Cut(lipgloss.NewStyle().Foreground(style.BorderSoft).Render(probe), probe)
	view := wi10RemovalDoneView(t)
	if strings.Contains(view, soft+"╭") || strings.Contains(view, soft+"│") {
		t.Error("the removal command box is still drawn in style.BorderSoft")
	}
}

func TestBoxes_OneDrawBoxInSceneDrawsThemAll(t *testing.T) {
	scene := []string{"scene.go"}
	if at := wi10Declared(t, scene, "drawBox"); len(at) != 1 {
		t.Fatalf("scene.go declares drawBox %d times (%v), want once", len(at), at)
	}
	// Nothing else in the package declares a second one.
	all := uiSourceFiles(t)
	var top []string
	for _, f := range all {
		if !strings.Contains(f, "/") {
			top = append(top, f)
		}
	}
	if at := wi10Declared(t, top, "drawBox"); len(at) != 1 {
		t.Errorf("package declares drawBox %d times (%v), want once", len(at), at)
	}
	for _, name := range []string{"console.go", "notice.go", "remove.go"} {
		if _, f := wi10Parse(t, name); !wi10Calls(f, "drawBox") {
			t.Errorf("%s does not call drawBox", name)
		}
	}
}

// Box corners and lipgloss's own border are written in scene.go only.
func TestBoxes_NoBoxIsDrawnOutsideScene(t *testing.T) {
	corners := map[string]bool{"╭": true, "╮": true, "╰": true, "╯": true}
	inScene := false
	for _, name := range uiSourceFiles(t) {
		if strings.Contains(name, "/") {
			continue
		}
		fset, f := wi10Parse(t, name)
		ast.Inspect(f, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.BasicLit:
				if n.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(n.Value)
				if err != nil {
					return true
				}
				for c := range corners {
					if strings.Contains(s, c) {
						if name == "scene.go" {
							inScene = true
						} else {
							t.Errorf("%s: box corner %q outside scene.go", fset.Position(n.Pos()), c)
						}
					}
				}
			case *ast.SelectorExpr:
				if n.Sel.Name == "RoundedBorder" || n.Sel.Name == "NormalBorder" {
					if name == "scene.go" {
						inScene = true
					} else {
						t.Errorf("%s: lipgloss %s outside scene.go", fset.Position(n.Pos()), n.Sel.Name)
					}
				}
			}
			return true
		})
	}
	if !inScene {
		t.Error("scene.go draws no box corner or lipgloss border: the scan has nothing to find")
	}
}
