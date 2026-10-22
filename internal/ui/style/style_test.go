package style

import (
	"image/color"
	"strconv"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

func TestWordmark_TracksLetters(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"ascii word", "ORBIT", "O R B I T"},
		{"single letter has no leading space", "O", "O"},
		{"empty string stays empty", "", ""},
		// Wordmark iterates by rune, not by byte, so a multi-byte
		// character must be tracked as one unit, not split apart.
		{"multi-byte rune", "ÖRBIT", "Ö R B I T"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ansi.Strip(Wordmark(c.in))
			if got != c.want {
				t.Errorf("Wordmark(%q) visible text = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// sgrOf returns the parameters of the escape sequence a styled string
// opens with ("1", "38", "2", "231", ... for bold in the text colour),
// and the visible text after it.
func sgrOf(t *testing.T, rendered string) (params []string, text string) {
	t.Helper()
	const open = "\x1b["
	if !strings.HasPrefix(rendered, open) {
		t.Fatalf("%q does not start with an escape sequence", rendered)
	}
	end := strings.IndexByte(rendered, 'm')
	if end < 0 {
		t.Fatalf("%q: escape sequence never ends", rendered)
	}
	return strings.Split(rendered[len(open):end], ";"), rendered[end+1:]
}

// foregroundOf is the SGR parameters a truecolor foreground c is written
// as: 38;2;r;g;b.
func foregroundOf(t *testing.T, c color.Color) []string {
	t.Helper()
	rgba, ok := c.(color.RGBA)
	if !ok {
		t.Fatalf("%T is not color.RGBA", c)
	}
	return []string{"38", "2", strconv.Itoa(int(rgba.R)), strconv.Itoa(int(rgba.G)), strconv.Itoa(int(rgba.B))}
}

func TestWordmark_AppliesBoldAndTextColour(t *testing.T) {
	// The rendering must actually be styled, not just the plain text --
	// otherwise Wordmark degenerates to the letter-spacing loop alone and
	// the splash's wordmark silently loses its styling. Bold and the text
	// colour are what the wordmark is, so both are read back out of the
	// escape sequence.
	params, text := sgrOf(t, Wordmark("HI"))
	if ansi.Strip(Wordmark("HI")) != "H I" {
		t.Errorf("Wordmark(%q) visible text = %q, want %q", "HI", ansi.Strip(Wordmark("HI")), "H I")
	}
	if !strings.HasPrefix(text, "H I") {
		t.Errorf("Wordmark(%q) text after the escape = %q, want it to start with %q", "HI", text, "H I")
	}
	if len(params) == 0 || params[0] != "1" {
		t.Errorf("Wordmark is not bold: escape parameters %v, want a leading 1", params)
	}
	want := strings.Join(foregroundOf(t, Text), ";")
	if got := strings.Join(params, ";"); !strings.Contains(got, want) {
		t.Errorf("Wordmark colour parameters %q do not hold the text colour %q", got, want)
	}
}

// TestDistinctStylesRenderDifferently guards against the easy copy-paste
// mistake in a palette file: two styles meant to carry different meaning
// (success vs. error, selected vs. unselected) accidentally sharing one
// colour, which would make real output indistinguishable even though the
// package compiles and every style "works".
func TestDistinctStylesRenderDifferently(t *testing.T) {
	const text = "x"
	groups := [][2]struct {
		name  string
		style interface{ Render(...string) string }
	}{
		{{"SuccessText", SuccessText}, {"ErrorText", ErrorText}},
		{{"MenuSelected", MenuSelected}, {"MenuUnselected", MenuUnselected}},
		{{"AccentText", AccentText}, {"MutedText", MutedText}},
		{{"DegradedText", DegradedText}, {"ErrorText", ErrorText}},
		{{"PlanetLeadText", PlanetLeadText}, {"PlanetPartnerText", PlanetPartnerText}},
		{{"PlanetPaleText", PlanetPaleText}, {"PlanetEmberText", PlanetEmberText}},
	}
	for _, g := range groups {
		a, b := g[0], g[1]
		ra, rb := a.style.Render(text), b.style.Render(text)
		if ra == rb {
			t.Errorf("%s and %s render identically (%q) -- they should be visually distinct", a.name, b.name, ra)
		}
		if ansi.Strip(ra) != text || ansi.Strip(rb) != text {
			t.Errorf("%s or %s altered the text itself: %q, %q", a.name, b.name, ansi.Strip(ra), ansi.Strip(rb))
		}
	}
}

func TestMarkStyle_RendersAccentColour(t *testing.T) {
	rendered := MarkStyle.Render("⟡")
	if ansi.Strip(rendered) != "⟡" {
		t.Errorf("MarkStyle.Render altered the mark glyph: got %q", ansi.Strip(rendered))
	}
	params, _ := sgrOf(t, rendered)
	if got, want := strings.Join(params, ";"), strings.Join(foregroundOf(t, Accent), ";"); got != want {
		t.Errorf("MarkStyle colour parameters = %q, want the accent colour %q", got, want)
	}
}

func TestSymbolGlossary(t *testing.T) {
	// These are read by the rest of the UI as fixed constants; a change
	// here is a deliberate glossary change (design/mockups.html section
	// 01), not an accident, so pin the agreed values.
	cases := map[string]string{
		SymbolSelected: "▸",
		SymbolSuccess:  "✓",
		SymbolFailure:  "✗",
		SymbolQueued:   "·",
		SymbolMark:     "⟡",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("symbol = %q, want %q", got, want)
		}
	}
}

// hexRGB decodes a "#rrggbb" fixture the same way style.go's own source
// comments write one, so the test's expectation is read the same way a
// reviewer reads the source.
func hexRGB(t *testing.T, hex string) (r, g, b uint8) {
	t.Helper()
	if len(hex) != 7 || hex[0] != '#' {
		t.Fatalf("test fixture %q is not '#rrggbb'", hex)
	}
	v, err := strconv.ParseUint(hex[1:], 16, 32)
	if err != nil {
		t.Fatalf("test fixture %q: %v", hex, err)
	}
	return uint8(v >> 16), uint8(v >> 8), uint8(v)
}

// TestPaletteColoursMatchDocumentedHex pins every palette colour to the
// hex value style.go documents it as. lipgloss.Color parses a hex string
// into a color.RGBA; reading that back out and comparing channel-by-
// channel catches a typo'd digit that a looser "is this a colour" check
// would miss.
func TestPaletteColoursMatchDocumentedHex(t *testing.T) {
	cases := map[string]struct {
		c   color.Color
		hex string
	}{
		"Background":    {Background, "#05070d"},
		"Panel":         {Panel, "#0b0f1a"},
		"Border":        {Border, "#1c2434"},
		"BorderSoft":    {BorderSoft, "#151b28"},
		"Text":          {Text, "#e7e9ee"},
		"TextMuted":     {TextMuted, "#7c8699"},
		"TextFaint":     {TextFaint, "#4a5468"},
		"Accent":        {Accent, "#d8b45a"},
		"AccentDim":     {AccentDim, "#5a4c28"},
		"Warm":          {Warm, "#f0b429"},
		"Success":       {Success, "#4ade80"},
		"Error":         {Error, "#f87171"},
		"Degraded":      {Degraded, "#fb923c"},
		"PlanetLead":    {PlanetLead, "#d8b45a"},
		"PlanetPartner": {PlanetPartner, "#8fb8ff"},
		"PlanetPale":    {PlanetPale, "#cbd5e1"},
		"PlanetEmber":   {PlanetEmber, "#fb7185"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rgba, ok := c.c.(color.RGBA)
			if !ok {
				t.Fatalf("%s is a %T, not color.RGBA -- lipgloss.Color's hex parser must have changed shape", name, c.c)
			}
			wantR, wantG, wantB := hexRGB(t, c.hex)
			if rgba.R != wantR || rgba.G != wantG || rgba.B != wantB {
				t.Errorf("%s = #%02x%02x%02x, want %s", name, rgba.R, rgba.G, rgba.B, c.hex)
			}
		})
	}
}
