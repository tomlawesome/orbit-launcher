package ui

// Pins #214 (widths; #199 WI-10, UI-17 and SU-2): one width measure
// (lipgloss.Width, in terminal cells) everywhere, one named 56-cell
// prose measure that notice references, and the console reading
// escape sequences through the one shared ansi.Strip.

import (
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// wi10BoxRows are the console rows drawn inside its box, plain text.
func wi10BoxRows(c ConsoleModel, cols, rows int) (box, all []string) {
	for _, l := range c.contentLines(cols, rows) {
		p := wi10Plain(l)
		all = append(all, p)
		if strings.HasPrefix(p, "│") {
			box = append(box, p)
		}
	}
	return box, all
}

// A wide character takes two cells. The console cut and padded its rows
// by counting runes, so a line of them ran out of the box.
func TestConsole_WideCharactersAreMeasuredInCells(t *testing.T) {
	c := testConsole().observeRaw(strings.Repeat("日本語", 20)) // 120 cells
	box, all := wi10BoxRows(c, 80, 26)
	for _, row := range all {
		if w := lipgloss.Width(row); w > 76 {
			t.Errorf("a row is %d cells, past the 76-cell box: %q", w, row)
		}
	}
	// The cut keeps the start of the line: shortening to nothing would
	// also satisfy the width check.
	if !strings.Contains(strings.Join(box, "\n"), "日本語") {
		t.Errorf("the over-wide line vanished instead of being cut:\n%s", strings.Join(box, "\n"))
	}
}

// Passed already: a short wide line stays aligned, so the check above
// is about the cut and not about every wide character.
func TestConsole_ShortWideLineStaysInsideTheBox(t *testing.T) {
	c := testConsole().observeRaw("日本語")
	box, _ := wi10BoxRows(c, 80, 26)
	if len(box) == 0 {
		t.Fatal("no box rows")
	}
	for _, row := range box {
		if w := lipgloss.Width(row); w != 76 {
			t.Errorf("a box row is %d cells, want 76: %q", w, row)
		}
	}
	if !strings.Contains(strings.Join(box, "\n"), "日本語") {
		t.Error("the short line is missing")
	}
}

// A line from the engine can carry a cursor or erase sequence that is
// not a colour. The console's own stripper skipped from the ESC to the
// next "m" when it measured the line to cut it to the box, so a long
// line with no "m" after the sequence was cut to what came before it.
func TestConsole_NonColourSequenceInALongEngineLineIsNotMeasuredAsText(t *testing.T) {
	c := testConsole().observeRaw("progress\x1b[2K " + strings.Repeat("abcdefgh ", 12))
	box, all := wi10BoxRows(c, 80, 26)
	for _, row := range all {
		if w := lipgloss.Width(row); w > 76 {
			t.Errorf("a row is %d cells, past the 76-cell box: %q", w, row)
		}
	}
	var text []string
	for _, row := range box {
		if r := strings.Trim(row, "│ "); r != "" {
			text = append(text, r)
		}
	}
	if joined := strings.Join(text, "\n"); !strings.Contains(joined, "progress abcdefgh abcdefgh") {
		t.Errorf("the line was cut at the escape sequence instead of at the box; the box holds %q", joined)
	}
}

// Passed already: a short line with the same sequence keeps its words.
func TestConsole_NonColourSequenceInAShortEngineLineKeepsTheWords(t *testing.T) {
	c := testConsole().observeRaw("progress\x1b[2K 50% complete, now done")
	_, all := wi10BoxRows(c, 80, 26)
	if joined := strings.Join(all, "\n"); !strings.Contains(joined, "progress 50% complete, now done") {
		t.Errorf("console lost words around an erase-line sequence:\n%s", joined)
	}
}

// Passed already: colour codes in an engine line never reach the width
// count or the text.
func TestConsole_ColourCodesInAnEngineLineAreNotText(t *testing.T) {
	c := testConsole().observeRaw("\x1b[31mred words\x1b[0m after")
	_, all := wi10BoxRows(c, 80, 26)
	if joined := strings.Join(all, "\n"); !strings.Contains(joined, "red words after") {
		t.Errorf("colour codes changed the text:\n%s", joined)
	}
}

// No stripper in the product's own code, and the console reads through
// the shared one.
func TestStripping_TheProductKeepsNoStripperOfItsOwn(t *testing.T) {
	var top []string
	for _, f := range uiSourceFiles(t) {
		if !strings.Contains(f, "/") {
			top = append(top, f)
		}
	}
	for _, name := range []string{"stripANSI", "stripEscapes"} {
		if at := wi10Declared(t, top, name); len(at) != 0 {
			t.Errorf("%s is declared at %v: use ansi.Strip", name, at)
		}
	}
	imports := false
	for _, name := range top {
		_, f := wi10Parse(t, name)
		for _, imp := range f.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "github.com/tomlawesome/orbit-launcher/internal/ansi" {
				imports = true
			}
		}
	}
	if !imports {
		t.Error("no ui file imports internal/ansi")
	}
}

// console_test.go's visibleWidth counted runes; production measures in
// cells. If the helper is still there it measures the same way.
func TestStripping_TestsMeasureCellsNotRunes(t *testing.T) {
	_, f := wi10Parse(t, "console_test.go")
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "visibleWidth" {
			continue
		}
		usesWidth, countsRunes := false, false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.SelectorExpr:
				usesWidth = usesWidth || n.Sel.Name == "Width"
			case *ast.ArrayType:
				if id, ok := n.Elt.(*ast.Ident); ok && id.Name == "rune" {
					countsRunes = true
				}
			}
			return true
		})
		if !usesWidth || countsRunes {
			t.Error("visibleWidth in console_test.go must measure with lipgloss.Width, not by counting runes")
		}
	}
}

// One 56-cell measure: the number is typed once in the package's own
// code, and notice's measure refers to it.
func TestWidthMeasure_FiftySixIsTypedOnce(t *testing.T) {
	var where []string
	for _, name := range uiSourceFiles(t) {
		if strings.Contains(name, "/") {
			continue
		}
		fset, f := wi10Parse(t, name)
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.INT && lit.Value == "56" {
				where = append(where, fset.Position(lit.Pos()).String())
			}
			return true
		})
	}
	if len(where) != 1 {
		t.Errorf("the literal 56 is typed at %v, want once", where)
	}
}

// notice.go measured copy with len() on strings, which counts bytes: a
// curly apostrophe (three bytes, one cell) would misalign the box. No
// len() of a string remains. The check is typed so a len() of a slice is
// not mistaken for display text.
func TestNotice_NeverMeasuresDisplayTextWithLen(t *testing.T) {
	fset := token.NewFileSet()
	var files []*ast.File
	for _, name := range uiSourceFiles(t) {
		if strings.Contains(name, "/") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}
	conf := types.Config{
		Importer: importer.ForCompiler(fset, "source", nil),
		Error:    func(error) {}, // a partial result is enough; the sanity check below catches an empty one
	}
	_, _ = conf.Check("ui", fset, files, info)

	typed := 0
	for _, f := range files {
		if fset.Position(f.Pos()).Filename != "notice.go" {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if id, ok := call.Fun.(*ast.Ident); !ok || id.Name != "len" || len(call.Args) != 1 {
				return true
			}
			arg := call.Args[0]
			tv, known := info.Types[arg]
			if !known || tv.Type == nil {
				return true
			}
			typed++
			if b, ok := tv.Type.Underlying().(*types.Basic); ok && b.Info()&types.IsString != 0 {
				t.Errorf("%s: len() of a string measures bytes, not cells: use lipgloss.Width", fset.Position(call.Pos()))
			}
			return true
		})
	}
	// Positive pairing: the typed scan did see len() calls, so an
	// importer failure cannot make this pass by finding nothing.
	if typed == 0 {
		t.Fatal("the typed scan saw no len() call in notice.go: the type check did not run")
	}
}
