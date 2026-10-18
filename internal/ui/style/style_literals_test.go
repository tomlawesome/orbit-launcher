package style

// Pins #213 (#199 WI-9, UI-13, UI-16): the new homes for the header
// title, the warn and cursor glyphs and the spinner frames exist here,
// declared once, with the exact values the screens draw today (the
// baselines must not move).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func parseStyle(t *testing.T) map[string]*ast.File {
	t.Helper()
	paths, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, p, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[p] = f
	}
	return out
}

// valueSpecs returns every top-level value spec naming name.
func valueSpecs(files map[string]*ast.File, name string) []*ast.ValueSpec {
	var out []*ast.ValueSpec
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, s := range gd.Specs {
				if v, ok := s.(*ast.ValueSpec); ok {
					for _, id := range v.Names {
						if id.Name == name {
							out = append(out, v)
						}
					}
				}
			}
		}
	}
	return out
}

func specValue(v *ast.ValueSpec, name string) ast.Expr {
	for i, id := range v.Names {
		if id.Name == name && i < len(v.Values) {
			return v.Values[i]
		}
	}
	return nil
}

func TestStyleNewNamesDeclaredOnce(t *testing.T) {
	files := parseStyle(t)
	for _, name := range []string{"SymbolWarn", "SymbolCursor", "SpinnerFrames"} {
		if got := len(valueSpecs(files, name)); got != 1 {
			t.Errorf("%s declared %d times in style, want once", name, got)
		}
	}
	// Title is a style value or a function; either way exactly one home.
	n := len(valueSpecs(files, "Title"))
	for _, f := range files {
		for _, d := range f.Decls {
			if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "Title" {
				n++
			}
		}
	}
	if n != 1 {
		t.Errorf("Title declared %d times in style, want once", n)
	}
}

// The glyph values are what the screens draw now; a different glyph
// moves pixels.
func TestStyleGlyphValuesAreUnchanged(t *testing.T) {
	files := parseStyle(t)
	for name, want := range map[string]string{"SymbolWarn": "!", "SymbolCursor": "▏"} {
		specs := valueSpecs(files, name)
		if len(specs) != 1 {
			t.Errorf("%s declared %d times, want once", name, len(specs))
			continue
		}
		lit, ok := specValue(specs[0], name).(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			t.Errorf("%s is not a string literal", name)
			continue
		}
		if got, _ := strconv.Unquote(lit.Value); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
}

// The spinner starts on "⠋" and has more than one frame (the console
// reads frame 0 and frame 1).
func TestStyleSpinnerFramesStartOnTheDotFrame(t *testing.T) {
	specs := valueSpecs(parseStyle(t), "SpinnerFrames")
	if len(specs) != 1 {
		t.Fatalf("SpinnerFrames declared %d times, want once", len(specs))
	}
	var frames []rune
	ast.Inspect(specs[0], func(n ast.Node) bool {
		if lit, ok := n.(*ast.BasicLit); ok {
			switch lit.Kind {
			case token.STRING:
				s, _ := strconv.Unquote(lit.Value)
				frames = append(frames, []rune(s)...)
			case token.CHAR:
				r, _, _, _ := strconv.UnquoteChar(strings.Trim(lit.Value, "'"), '\'')
				frames = append(frames, r)
			}
		}
		return true
	})
	if len(frames) < 2 || frames[0] != '⠋' {
		t.Errorf("SpinnerFrames literal frames = %q, want at least two starting with ⠋", string(frames))
	}
}

// UI-16: PlanetLead is Accent by definition, not a second copy of its hex.
func TestStylePlanetLeadIsDefinedAsAccent(t *testing.T) {
	specs := valueSpecs(parseStyle(t), "PlanetLead")
	if len(specs) != 1 {
		t.Fatalf("PlanetLead declared %d times, want once", len(specs))
	}
	id, ok := specValue(specs[0], "PlanetLead").(*ast.Ident)
	if !ok || id.Name != "Accent" {
		t.Errorf("PlanetLead is not defined as Accent (got %T)", specValue(specs[0], "PlanetLead"))
	}
}

// Passed already: whatever the definition, the colour must not change.
func TestStylePlanetLeadStillEqualsAccent(t *testing.T) {
	if PlanetLead != Accent {
		t.Errorf("PlanetLead = %q, want Accent %q", PlanetLead, Accent)
	}
}
