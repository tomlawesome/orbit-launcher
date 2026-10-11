package ui

// Pins #231 (screen-grammar leftovers, #200 C9): three things written
// by hand across the flow screens each get one home, so "style owns
// every text style, scene.go owns the screen grammar" (#199's macro
// rule) holds for them too.
//
//   - plain full-ink text: style.Ink, not a rebuilt Foreground(style.Text)
//   - the wrap measure: measure(width, limit int) int in wrap.go, not
//     min(width-4, proseWidth|errorWidth) typed at each screen
//   - the thin progress bar: drawBar(fill, cells int, fillStyle
//     lipgloss.Style) string in scene.go, not two Repeat("─") pairs
//
// These are scans of the non-test source, like screen_grammar_scan_test.go.
// That nothing visible moves is pinned separately, by
// screen_grammar_c9_output_test.go.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

func c9ParseSrc(t *testing.T, src string) *ast.File {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "c.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// c9FuncDecl finds the top-level function name in the file at path.
func c9FuncDecl(files map[string]*ast.File, path, name string) *ast.FuncDecl {
	f := files[path]
	if f == nil {
		return nil
	}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
			return fd
		}
	}
	return nil
}

// c9Signature renders a function's parameters and results as
// "name type" pairs, e.g. "(width int, limit int) int".
func c9Signature(fd *ast.FuncDecl) string {
	var params []string
	for _, f := range fd.Type.Params.List {
		typ := exprString(f.Type)
		for _, n := range f.Names {
			params = append(params, n.Name+" "+typ)
		}
	}
	var results []string
	if fd.Type.Results != nil {
		for _, f := range fd.Type.Results.List {
			results = append(results, exprString(f.Type))
		}
	}
	return "(" + strings.Join(params, ", ") + ") " + strings.Join(results, ", ")
}

// c9ForegroundCalls returns the position of every .Foreground(<arg>)
// call in the file, wherever it sits in a chain.
func c9ForegroundCalls(fset *token.FileSet, f *ast.File, arg string) []token.Position {
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Foreground" &&
			len(c.Args) == 1 && exprString(c.Args[0]) == arg {
			out = append(out, fset.Position(c.Pos()))
		}
		return true
	})
	return out
}

func TestScreenGrammarC9_DetectorSeesForegroundText(t *testing.T) {
	src := `package p
var a = lipgloss.NewStyle().Foreground(style.Text)
var b = lipgloss.NewStyle().Bold(true).Foreground(style.Text).Italic(true)
var c = lipgloss.NewStyle().Foreground(style.TextMuted)
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(c9ForegroundCalls(fset, f, "style.Text")); got != 2 {
		t.Fatalf("detector found %d Foreground(style.Text) calls in the control source, want 2", got)
	}
}

// style owns the plain-ink text style, beside Title: one declaration,
// foreground style.Text's own colour, and no bold (bold is Title).
func TestScreenGrammarC9_StyleDeclaresInk(t *testing.T) {
	_, files := wi9Parse(t)
	got := declsOf(files, "Ink")
	var inStyle []string
	for _, p := range got {
		if wi9InStyle(p) {
			inStyle = append(inStyle, p)
		}
	}
	if len(got) != 1 || len(inStyle) != 1 {
		t.Fatalf("Ink is declared in %v, want exactly once, in the style package", got)
	}
	var calls []chainCall
	ast.Inspect(files[inStyle[0]], func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok {
			for i, id := range vs.Names {
				if id.Name == "Ink" && i < len(vs.Values) {
					calls, _ = chainOf(vs.Values[i])
				}
			}
		}
		return true
	})
	if !hasCall(calls, "Foreground", "Text") {
		t.Errorf("style.Ink does not set Foreground(Text); its chain is %v", calls)
	}
	if hasCall(calls, "Bold", "true") {
		t.Error("style.Ink is bold; plain ink is not (Title is the bold one)")
	}
}

// No flow file rebuilds the plain-ink style: nine sites at the snapshot
// (install, promptsession x2, remove, update, notice, repair x3).
func TestScreenGrammarC9_NoForegroundTextOutsideStyle(t *testing.T) {
	fset, files := wi9Parse(t)
	for path, f := range wi9UIOnly(files) {
		for _, pos := range c9ForegroundCalls(fset, f, "style.Text") {
			t.Errorf("%s (%s): builds Foreground(style.Text) by hand; use style.Ink", path, pos)
		}
	}
}

// The files that built it now use it.
func TestScreenGrammarC9_InkSitesUseStyleInk(t *testing.T) {
	_, files := wi9Parse(t)
	uses := func(path string) int {
		n := 0
		ast.Inspect(files[path], func(x ast.Node) bool {
			if s, ok := x.(*ast.SelectorExpr); ok && exprString(s) == "style.Ink" {
				n++
			}
			return true
		})
		return n
	}
	// Minimum uses: how many hand-built sites each file had.
	for path, min := range map[string]int{
		"install.go": 1, "promptsession.go": 2, "remove.go": 1, "update.go": 1, "notice.go": 1, "repair.go": 3,
	} {
		if files[path] == nil {
			t.Errorf("%s not found", path)
			continue
		}
		if got := uses(path); got < min {
			t.Errorf("%s uses style.Ink %d times, want at least %d (one per hand-built site it had)", path, got, min)
		}
	}
}

// c9MinWidth4 returns the position of every min(<x>width-4, ...) call:
// the wrap measure typed by hand.
func c9MinWidth4(fset *token.FileSet, f *ast.File) []token.Position {
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := c.Fun.(*ast.Ident); !ok || id.Name != "min" {
			return true
		}
		for _, a := range c.Args {
			b, ok := a.(*ast.BinaryExpr)
			if !ok || b.Op != token.SUB {
				continue
			}
			lit, ok := b.Y.(*ast.BasicLit)
			if !ok || lit.Value != "4" {
				continue
			}
			if name := strings.ToLower(exprString(b.X)); name == "width" || strings.HasSuffix(name, ".width") {
				out = append(out, fset.Position(c.Pos()))
			}
		}
		return true
	})
	return out
}

func TestScreenGrammarC9_DetectorSeesTheHandTypedMeasure(t *testing.T) {
	f := c9ParseSrc(t, `package p
var a = min(m.width-4, proseWidth)
var b = min(width-4, errorWidth)
var c = min(m.width-4-2, proseWidth)
var d = min(m.width, proseWidth)
`)
	if got := len(c9MinWidth4(token.NewFileSet(), f)); got != 2 {
		t.Fatalf("detector found %d hand-typed measures in the control source, want 2", got)
	}
}

// measure(width, limit int) int is the one place the wrap measure is
// computed, in wrap.go.
func TestScreenGrammarC9_MeasureLivesInWrapGo(t *testing.T) {
	_, files := wi9Parse(t)
	got := declsOf(files, "measure")
	if len(got) != 1 || got[0] != "wrap.go" {
		t.Fatalf("measure declared in %v, want exactly once, in wrap.go", got)
	}
	fd := c9FuncDecl(files, "wrap.go", "measure")
	if fd == nil {
		t.Fatal("measure is not a top-level function in wrap.go")
	}
	if sig := c9Signature(fd); sig != "(width int, limit int) int" {
		t.Errorf("measure has signature %s, want (width, limit int) int", sig)
	}
}

// The six hand-typed sites: enginerun.go:630, remove.go:280,313,352,
// repair.go:794 and install.go:474. (remove.go's wrapShellCommand
// limit, a bare width-4 with no cap, is not a measure: no cap to apply.)
func TestScreenGrammarC9_NoHandTypedMeasureOutsideWrapGo(t *testing.T) {
	fset, files := wi9Parse(t)
	for path, f := range wi9UIOnly(files) {
		if path == "wrap.go" {
			continue
		}
		for _, pos := range c9MinWidth4(fset, f) {
			t.Errorf("%s (%s): types min(width-4, limit) by hand; call measure(width, limit)", path, pos)
		}
	}
}

func TestScreenGrammarC9_MeasureSitesCallMeasure(t *testing.T) {
	_, files := wi9Parse(t)
	calls := callsIn(files, "measure")
	for path, want := range map[string]int{
		// remove.go's not-found site moved into scene.go's shared
		// no-deployment screen with #229.
		"enginerun.go": 1, "install.go": 1, "remove.go": 2, "repair.go": 1, "scene.go": 1,
	} {
		if got := calls[path]; got < want {
			t.Errorf("%s calls measure %d times, want at least %d (one per hand-typed site it had)", path, got, want)
		}
	}
}

// proseWidth and errorWidth are limits handed to measure, and the
// notice's own constant is the only other reader.
func TestScreenGrammarC9_LimitsOnlyFeedMeasure(t *testing.T) {
	fset, files := wi9Parse(t)
	for path, f := range wi9UIOnly(files) {
		if path == "wrap.go" {
			continue
		}
		var walk func(n ast.Node, inMeasure bool)
		walk = func(n ast.Node, inMeasure bool) {
			ast.Inspect(n, func(x ast.Node) bool {
				switch v := x.(type) {
				case *ast.CallExpr:
					if id, ok := v.Fun.(*ast.Ident); ok && id.Name == "measure" {
						for _, a := range v.Args {
							walk(a, true)
						}
						return false
					}
				case *ast.GenDecl:
					if v.Tok == token.CONST { // noticeMeasure = proseWidth
						return false
					}
				case *ast.Ident:
					if (v.Name == "proseWidth" || v.Name == "errorWidth") && !inMeasure {
						t.Errorf("%s (%s): %s used outside a measure(...) call", path, fset.Position(v.Pos()), v.Name)
					}
				}
				return true
			})
		}
		walk(f, false)
	}
}

// c9RepeatBars returns the position of every strings.Repeat("─", n):
// a progress bar's fill or track drawn by hand.
func c9RepeatBars(fset *token.FileSet, f *ast.File) []token.Position {
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok || exprString(c.Fun) != "strings.Repeat" || len(c.Args) != 2 {
			return true
		}
		if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil && s == "─" {
				out = append(out, fset.Position(c.Pos()))
			}
		}
		return true
	})
	return out
}

func TestScreenGrammarC9_DetectorSeesTheHandDrawnBar(t *testing.T) {
	f := c9ParseSrc(t, `package p
var a = strings.Repeat("─", n)
var b = strings.Repeat(" ", n)
var c = strings.Repeat("═", n)
`)
	if got := len(c9RepeatBars(token.NewFileSet(), f)); got != 1 {
		t.Fatalf("detector found %d hand-drawn bars in the control source, want 1", got)
	}
}

// drawBar(fill, cells int, fillStyle lipgloss.Style) string is the one
// place a bar is drawn, in scene.go.
func TestScreenGrammarC9_DrawBarLivesInSceneGo(t *testing.T) {
	_, files := wi9Parse(t)
	got := declsOf(files, "drawBar")
	if len(got) != 1 || got[0] != "scene.go" {
		t.Fatalf("drawBar declared in %v, want exactly once, in scene.go", got)
	}
	fd := c9FuncDecl(files, "scene.go", "drawBar")
	if fd == nil {
		t.Fatal("drawBar is not a top-level function in scene.go")
	}
	if sig := c9Signature(fd); sig != "(fill int, cells int, fillStyle lipgloss.Style) string" {
		t.Errorf("drawBar has signature %s, want (fill, cells int, fillStyle lipgloss.Style) string", sig)
	}
}

// Every file outside scene.go, the style and starfield packages
// included, leaves bar drawing to drawBar.
func TestScreenGrammarC9_NoHandDrawnBarOutsideSceneGo(t *testing.T) {
	fset, files := wi9Parse(t)
	for path, f := range files {
		if path == "scene.go" {
			continue
		}
		for _, pos := range c9RepeatBars(fset, f) {
			t.Errorf("%s (%s): draws a ─ run by hand; use drawBar", path, pos)
		}
	}
}

// The two bars (the console's stage bar, the notice's countdown) call it.
func TestScreenGrammarC9_BothBarsCallDrawBar(t *testing.T) {
	_, files := wi9Parse(t)
	calls := callsIn(files, "drawBar")
	for _, p := range []string{"console.go", "notice.go"} {
		if calls[p] == 0 {
			t.Errorf("%s never calls drawBar", p)
		}
	}
}
