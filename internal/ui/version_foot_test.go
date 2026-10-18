package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Pins #212 (#199 WI-8, UI-6, EN-13): the UI prints the version it is
// given and never invents "dev" — what a version string looks like is
// release's call, and main always hands the UI one. An empty version
// renders no foot at all, as the console already does.

var wi8DevWord = regexp.MustCompile(`\bdev\b`)

// wi8LastLine is the foot row, which is all the failure messages need.
func wi8LastLine(view string) string {
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	return lines[len(lines)-1]
}

func wi8SplashView(version string) string {
	m := settled(NewSplashModel())
	m.version = version
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	return updated.(SplashModel).View().Content
}

func TestSplashFoot_EmptyVersionRendersNoFootAndNoDev(t *testing.T) {
	// The foot can be present: with a version it carries it. This is
	// the case that keeps the empty case below from passing vacuously.
	// Passed already.
	if view := wi8SplashView("v0.1.0"); !strings.Contains(view, "orbit-launcher v0.1.0") {
		t.Fatalf("a given version must still be printed as given; last row: %q", wi8LastLine(view))
	}

	view := wi8SplashView("")
	if wi8DevWord.MatchString(view) {
		t.Errorf("the splash invented %q for an empty version; foot row: %q", "dev", wi8LastLine(view))
	}
	if strings.Contains(view, "orbit-launcher ") {
		t.Errorf("an empty version must render no foot at all; last row: %q", wi8LastLine(view))
	}
}

// The same rule on a whole app that was never given a version: the
// root model must not paper over it either.
func TestAppModel_WithoutAVersionShowsNoDev(t *testing.T) {
	m := NewAppModelNoAnimation()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	view := updated.(AppModel).View().Content
	if wi8DevWord.MatchString(view) {
		t.Errorf("the app invented %q for a missing version; last row: %q", "dev", wi8LastLine(view))
	}
}

// The console's rule is the one the splash adopts, so it must stay:
// empty renders no foot, a version renders it. Passed already.
func TestConsoleFoot_EmptyVersionRendersNoFoot(t *testing.T) {
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return base }

	with := newConsole("Install — Standard", "v9.9.9", clock).setSize(80, 26).view(80, 26)
	if !strings.Contains(with, "orbit-launcher v9.9.9") {
		t.Fatalf("console foot missing for a given version; last row: %q", wi8LastLine(with))
	}
	without := newConsole("Install — Standard", "", clock).setSize(80, 26).view(80, 26)
	if strings.Contains(without, "orbit-launcher") || wi8DevWord.MatchString(without) {
		t.Errorf("an empty version must render no foot and no dev; last row: %q", wi8LastLine(without))
	}
}

// wi8DevLiterals lists the string literals in src that are exactly
// "dev".
func wi8DevLiterals(t *testing.T, filename string, src any) []token.Position {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []token.Position
	ast.Inspect(f, func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil && s == "dev" {
				out = append(out, fset.Position(lit.Pos()))
			}
		}
		return true
	})
	return out
}

// Done-criterion: the UI never writes "dev". The detector is shown to
// find one first, so a clean scan means something.
func TestUISource_NeverWritesDev(t *testing.T) {
	if got := wi8DevLiterals(t, "probe.go", "package p\nvar v = \"dev\"\n"); len(got) != 1 {
		t.Fatalf("the detector missed a planted \"dev\" literal")
	}
	for _, path := range uiSourceFiles(t) {
		for _, pos := range wi8DevLiterals(t, path, nil) {
			t.Errorf("%s: the UI writes \"dev\"; the version is printed as given", pos)
		}
	}
}

// The WithVersion comment must say what the method does. It used to
// claim the success screen shows the version (it does not: the splash
// foot and the console do) and put it bottom-right (the splash foot is
// centred).
func TestWithVersion_CommentIsTrue(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "app.go", nil, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	var doc string
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv != nil && fn.Name.Name == "WithVersion" && fn.Doc != nil {
			doc = fn.Doc.Text()
		}
	}
	if strings.TrimSpace(doc) == "" {
		t.Fatal("WithVersion has no doc comment")
	}
	lower := strings.ToLower(doc)
	for _, claim := range []string{"success", "bottom-right"} {
		if strings.Contains(lower, claim) {
			t.Errorf("WithVersion's comment still says %q, which is not true:\n%s", claim, doc)
		}
	}
	// Absence alone passes any comment that avoids two words; the true
	// claim is that the splash and the console both carry the foot.
	for _, claim := range []string{"splash", "console"} {
		if !strings.Contains(lower, claim) {
			t.Errorf("WithVersion's comment does not name the %s, which shows the version:\n%s", claim, doc)
		}
	}
}
