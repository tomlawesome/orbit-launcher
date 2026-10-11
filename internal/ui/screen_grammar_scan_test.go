package ui

// Pins #213 (screen grammar that must not move a pixel, #199 WI-9;
// UI-13 header, UI-14 sky, UI-15 optical centre, UI-16 literals). The
// done criteria are source-level, so these are scans of every non-test
// Go file under internal/ui (subpackages included, the style package
// excepted where a literal's home is style). Where a scan expects an
// absence, a positive control proves the detector can see the pattern.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// wi9Parse parses every non-test source file under internal/ui, keyed by
// slash path relative to it ("scene.go", "style/style.go").
func wi9Parse(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	files := map[string]*ast.File{}
	for _, path := range uiSourceFiles(t) {
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.ToSlash(path)] = f
	}
	return fset, files
}

func wi9InStyle(path string) bool { return strings.HasPrefix(path, "style/") }

// wi9UIOnly drops the style package: its literals are the homes.
func wi9UIOnly(files map[string]*ast.File) map[string]*ast.File {
	out := map[string]*ast.File{}
	for p, f := range files {
		if !wi9InStyle(p) {
			out[p] = f
		}
	}
	return out
}

// chainCall is one call in a method chain: name and source of its args.
type chainCall struct {
	name string
	args []string
}

// chainOf flattens a.B(x).C(y) into [B(x) C(y)] plus the chain's root.
func chainOf(e ast.Expr) (calls []chainCall, root ast.Expr) {
	for {
		c, ok := e.(*ast.CallExpr)
		if !ok {
			return calls, e
		}
		sel, ok := c.Fun.(*ast.SelectorExpr)
		if !ok {
			calls = append(calls, chainCall{name: exprString(c.Fun)})
			return calls, c
		}
		var args []string
		for _, a := range c.Args {
			args = append(args, exprString(a))
		}
		calls = append(calls, chainCall{name: sel.Sel.Name, args: args})
		e = sel.X
	}
}

func exprString(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprString(x.X) + "." + x.Sel.Name
	case *ast.BasicLit:
		return x.Value
	case *ast.CallExpr:
		return exprString(x.Fun) + "(...)"
	}
	return "?"
}

func hasCall(calls []chainCall, name string, args ...string) bool {
	for _, c := range calls {
		if c.name != name || len(c.args) != len(args) {
			continue
		}
		match := true
		for i := range args {
			if c.args[i] != args[i] {
				match = false
			}
		}
		if match {
			return true
		}
	}
	return false
}

// titleChains returns the position of every style chain that sets both
// Bold(true) and Foreground(<textExpr>): the flow header title.
func titleChains(fset *token.FileSet, f *ast.File, textExpr string) []token.Position {
	seen := map[token.Pos]bool{}
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		calls, root := chainOf(c)
		if hasCall(calls, "Bold", "true") && hasCall(calls, "Foreground", textExpr) && !seen[root.Pos()] {
			seen[root.Pos()] = true
			out = append(out, fset.Position(root.Pos()))
		}
		return true
	})
	return out
}

func TestScreenGrammar_DetectorSeesTheTitleChain(t *testing.T) {
	// Positive control for the absence scan below: both orders of the
	// chain, and a near miss that must not count.
	src := `package p
var a = lipgloss.NewStyle().Bold(true).Foreground(style.Text)
var b = lipgloss.NewStyle().
	Foreground(style.Text).
	Bold(true)
var c = lipgloss.NewStyle().Bold(true).Foreground(style.Accent)
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(titleChains(fset, f, "style.Text")); got != 2 {
		t.Fatalf("detector found %d title chains in the control source, want 2", got)
	}
}

// UI-13: the bold-text title style has one home, style.Title; flow files
// no longer rebuild it (20 sites at the snapshot).
func TestScreenGrammar_NoBoldTextTitleStyleOutsideStyle(t *testing.T) {
	fset, files := wi9Parse(t)
	for _, f := range wi9UIOnly(files) {
		for _, pos := range titleChains(fset, f, "style.Text") {
			t.Errorf("%s: rebuilds Bold(true).Foreground(style.Text); use style.Title", pos)
		}
	}
}

// declsOf returns the top-level declarations named name, per file.
func declsOf(files map[string]*ast.File, name string) []string {
	var where []string
	for path, f := range files {
		for _, d := range f.Decls {
			switch x := d.(type) {
			case *ast.FuncDecl:
				if x.Recv == nil && x.Name.Name == name {
					where = append(where, path)
				}
			case *ast.GenDecl:
				for _, s := range x.Specs {
					if v, ok := s.(*ast.ValueSpec); ok {
						for _, id := range v.Names {
							if id.Name == name {
								where = append(where, path)
							}
						}
					}
				}
			}
		}
	}
	return where
}

// UI-13/14/15: the screen grammar helpers each have exactly one
// declaration, in scene.go (the macro rule: scene.go owns the grammar).
func TestScreenGrammar_HelpersLiveInSceneGo(t *testing.T) {
	_, files := wi9Parse(t)
	for _, name := range []string{"writeHeader", "writeInlineHeader", "newSky", "topOffsetFor"} {
		got := declsOf(files, name)
		if len(got) != 1 || got[0] != "scene.go" {
			t.Errorf("%s declared in %v, want exactly once, in scene.go", name, got)
		}
	}
}

// callsIn lists the files that call the package-level function name.
func callsIn(files map[string]*ast.File, name string) map[string]int {
	out := map[string]int{}
	for path, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if c, ok := n.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == name {
					out[path]++
				}
			}
			return true
		})
	}
	return out
}

// UI-13: every flow file that had a header now calls the helpers. The
// stacked and inline forms are checked together because the lane report
// does not say which file keeps which form after the merge.
func TestScreenGrammar_FlowFilesUseTheHeaderHelpers(t *testing.T) {
	_, files := wi9Parse(t)
	stacked, inline := callsIn(files, "writeHeader"), callsIn(files, "writeInlineHeader")
	for _, p := range []string{"install.go", "configcollect.go", "notice.go", "update.go", "enginerun.go", "remove.go", "repair.go"} {
		if stacked[p]+inline[p] == 0 {
			t.Errorf("%s calls neither writeHeader nor writeInlineHeader", p)
		}
	}
	// The glyph-and-title-on-one-line variant (remove, enginerun, repair
	// at the snapshot) is its own helper, not a stacked header with the
	// glyph glued on.
	if len(inline) == 0 {
		t.Error("writeInlineHeader is never called")
	}
}

// UI-14: one sky constructor. The seven starfield.New(w, h, 1) calls
// become newSky(w, h); the seed is typed once, as 1, so skies stay
// pixel-identical.
func TestScreenGrammar_SkyIsBuiltOnlyByNewSky(t *testing.T) {
	_, files := wi9Parse(t)
	type site struct {
		path string
		call *ast.CallExpr
		fn   string
	}
	var sites []site
	for path, f := range wi9UIOnly(files) {
		if strings.HasPrefix(path, "starfield/") {
			continue
		}
		for _, d := range f.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				c, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if s, ok := c.Fun.(*ast.SelectorExpr); ok && exprString(s) == "starfield.New" {
					sites = append(sites, site{path, c, fd.Name.Name})
				}
				return true
			})
		}
		// Package-level initialisers too.
		for _, d := range f.Decls {
			if gd, ok := d.(*ast.GenDecl); ok {
				ast.Inspect(gd, func(n ast.Node) bool {
					if c, ok := n.(*ast.CallExpr); ok {
						if s, ok := c.Fun.(*ast.SelectorExpr); ok && exprString(s) == "starfield.New" {
							sites = append(sites, site{path, c, "<package level>"})
						}
					}
					return true
				})
			}
		}
	}
	if len(sites) != 1 {
		t.Fatalf("starfield.New is called at %d sites, want exactly 1 (inside newSky)", len(sites))
	}
	s := sites[0]
	if s.path != "scene.go" || s.fn != "newSky" {
		t.Errorf("the one starfield.New call is in %s func %s, want scene.go newSky", s.path, s.fn)
	}
	if len(s.call.Args) == 3 {
		// A named constant is fine; a literal other than 1 would move
		// every sky.
		if lit, ok := s.call.Args[2].(*ast.BasicLit); ok && lit.Value != "1" {
			t.Errorf("newSky seeds the sky with %s, want 1 (the seed the baselines were taken with)", lit.Value)
		}
	}

	calls := callsIn(files, "newSky")
	for _, p := range []string{"repair.go", "install.go", "update.go", "success.go", "console.go", "splash.go", "remove.go"} {
		if calls[p] == 0 {
			t.Errorf("%s never calls newSky", p)
		}
	}
}

// UI-15: the optical-centre factor is typed once, in topOffsetFor.
func TestScreenGrammar_OpticalCentreFactorTypedOnce(t *testing.T) {
	fset, files := wi9Parse(t)
	var where, wherePath []string
	var inFunc string
	for path, f := range files {
		for _, d := range f.Decls {
			ast.Inspect(d, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || (lit.Kind != token.FLOAT && lit.Kind != token.INT) {
					return true
				}
				if v, err := strconv.ParseFloat(lit.Value, 64); err == nil && v == 0.42 {
					where = append(where, fset.Position(lit.Pos()).String())
					wherePath = append(wherePath, path)
					if fd, ok := d.(*ast.FuncDecl); ok && path == "scene.go" {
						inFunc = fd.Name.Name
					}
				}
				return true
			})
		}
	}
	if len(where) != 1 {
		t.Fatalf("0.42 is typed at %d sites %v, want exactly 1, in scene.go topOffsetFor", len(where), where)
	}
	// where[0] is "file:line:col", so the file is checked by its path.
	if wherePath[0] != "scene.go" || inFunc != "topOffsetFor" {
		t.Errorf("the one 0.42 is at %s (func %q), want scene.go topOffsetFor", where[0], inFunc)
	}
}

// UI-15: the screens that centred on 0.42 call the shared helper.
func TestScreenGrammar_CentredScreensCallTopOffsetFor(t *testing.T) {
	_, files := wi9Parse(t)
	calls := callsIn(files, "topOffsetFor")
	for _, p := range []string{"enginerun.go", "splash.go", "success.go"} {
		if calls[p] == 0 {
			t.Errorf("%s never calls topOffsetFor", p)
		}
	}
}

var hexColour = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6}|[0-9a-fA-F]{8})$`)

// stringLits lists every string literal's value and position outside
// the style package.
func stringLits(fset *token.FileSet, files map[string]*ast.File) (vals []string, pos []string) {
	for _, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					vals = append(vals, s)
					pos = append(pos, fset.Position(lit.Pos()).String())
				}
			}
			return true
		})
	}
	return vals, pos
}

// UI-16: glyphs, spinner frames, the wordmark and raw colours belong to
// style. The scan covers the whole UI tree outside style (starfield
// included).
func TestScreenGrammar_NoStyleLiteralsOutsideStyle(t *testing.T) {
	fset, files := wi9Parse(t)
	vals, pos := stringLits(fset, wi9UIOnly(files))
	if len(vals) == 0 {
		t.Fatal("scan saw no string literals")
	}
	for i, v := range vals {
		switch {
		case strings.ContainsAny(v, "⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏"):
			t.Errorf("%s: spinner frame literal %q; use style.SpinnerFrames", pos[i], v)
		case strings.Contains(v, "▏"):
			t.Errorf("%s: cursor glyph literal %q; use style.SymbolCursor", pos[i], v)
		case v == "!":
			t.Errorf("%s: warn glyph literal %q; use style.SymbolWarn", pos[i], v)
		case v == "O R B I T":
			t.Errorf("%s: retyped wordmark %q; use style.Wordmark(\"ORBIT\")", pos[i], v)
		case hexColour.MatchString(v):
			t.Errorf("%s: raw colour %q outside style", pos[i], v)
		}
	}
}

// The scan above can only pass honestly if the literals are still
// reachable somewhere: the style package keeps them (no vacuous pass
// from deleting the glyphs).
func TestScreenGrammar_StyleKeepsTheLiterals(t *testing.T) {
	fset, files := wi9Parse(t)
	styleOnly := map[string]*ast.File{}
	for p, f := range files {
		if wi9InStyle(p) {
			styleOnly[p] = f
		}
	}
	vals, _ := stringLits(fset, styleOnly)
	// Rune literals (the spinner may be []rune{'⠋', ...}) are not string
	// literals, so read the style sources' text for the braille frame.
	var text strings.Builder
	for p := range styleOnly {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		text.Write(b)
	}
	for _, want := range []string{"▏", "⠋"} {
		found := strings.Contains(text.String(), want)
		for _, v := range vals {
			if strings.Contains(v, want) {
				found = true
			}
		}
		if !found {
			t.Errorf("style sources no longer contain %q", want)
		}
	}
	hasWarn := false
	for _, v := range vals {
		if v == "!" {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Error(`style sources do not contain the "!" warn glyph`)
	}
}

// UI-16: splash draws its wordmark through style.Wordmark.
func TestScreenGrammar_SplashUsesWordmark(t *testing.T) {
	_, files := wi9Parse(t)
	found := false
	ast.Inspect(files["splash.go"], func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && exprString(c.Fun) == "style.Wordmark" {
			found = true
		}
		return true
	})
	if !found {
		t.Error("splash.go never calls style.Wordmark")
	}
}

// UI-16: notice.go no longer rebuilds style.MutedText / style.Tagline
// (a bare NewStyle().Foreground(TextMuted / TextFaint)).
func TestScreenGrammar_NoticeUsesExistingStyles(t *testing.T) {
	fset, files := wi9Parse(t)
	f := files["notice.go"]
	rebuilds := func(fset *token.FileSet, f *ast.File) []token.Position {
		var out []token.Position
		seen := map[token.Pos]bool{}
		ast.Inspect(f, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			calls, root := chainOf(c)
			if len(calls) == 2 && calls[1].name == "NewStyle" &&
				calls[0].name == "Foreground" && len(calls[0].args) == 1 &&
				(calls[0].args[0] == "style.TextMuted" || calls[0].args[0] == "style.TextFaint") && !seen[root.Pos()] {
				seen[root.Pos()] = true
				out = append(out, fset.Position(c.Pos()))
			}
			return true
		})
		return out
	}
	// Positive control.
	cf, err := parser.ParseFile(token.NewFileSet(), "c.go", "package p\nvar a = lipgloss.NewStyle().Foreground(style.TextMuted)\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rebuilds(token.NewFileSet(), cf)) != 1 {
		t.Fatal("detector does not see a bare NewStyle().Foreground(style.TextMuted)")
	}
	for _, pos := range rebuilds(fset, f) {
		t.Errorf("%s: rebuilds style.MutedText or style.Tagline; use the existing style", pos)
	}
}

// UI-14: the 120 ms tick is typed in starfield only; the UI uses the
// exported interval.
func TestScreenGrammar_TickIntervalNotRetypedInUI(t *testing.T) {
	fset, files := wi9Parse(t)
	for path, f := range wi9UIOnly(files) {
		if strings.HasPrefix(path, "starfield/") {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			b, ok := n.(*ast.BinaryExpr)
			if !ok || b.Op != token.MUL {
				return true
			}
			for _, pair := range [][2]ast.Expr{{b.X, b.Y}, {b.Y, b.X}} {
				if lit, ok := pair[0].(*ast.BasicLit); ok && lit.Value == "120" && exprString(pair[1]) == "time.Millisecond" {
					t.Errorf("%s: retypes the 120ms tick; use the interval starfield exports", fset.Position(b.Pos()))
				}
			}
			return true
		})
	}
	uses := false
	ast.Inspect(files["splash.go"], func(n ast.Node) bool {
		if s, ok := n.(*ast.SelectorExpr); ok && exprString(s.X) == "starfield" && strings.Contains(s.Sel.Name, "Tick") {
			uses = true
		}
		return true
	})
	if !uses {
		t.Error("splash.go does not read a starfield.*Tick* name for its tick interval")
	}
}
