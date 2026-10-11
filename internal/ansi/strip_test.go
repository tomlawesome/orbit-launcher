package ansi

import (
	"strings"
	"testing"
)

// Pins #214 (boxes, wrapping, widths; #199 WI-10, SU-2): one Strip with
// the full escape-sequence handling that test/internal/vtscreen had, in
// place of three strippers that each handled a different subset. The
// cases are chosen so that a colour-only stripper, and the product
// stripper that skipped from ESC to the next "m", each fail one.

func TestStrip_RemovesColourCodes(t *testing.T) {
	cases := map[string]string{
		"\x1b[1;38;5;69m▸ Install\x1b[m":             "▸ Install",
		"\x1b[38;2;28;36;52m│\x1b[m text \x1b[0m":    "│ text ",
		"\x1b[31mred\x1b[0m and \x1b[1mbold\x1b[22m": "red and bold",
	}
	for in, want := range cases {
		if got := Strip(in); got != want {
			t.Errorf("Strip(%q) = %q, want %q", in, got, want)
		}
	}
}

// The case the old product copy got wrong: it removed everything from
// the ESC up to the next "m", so a sequence that does not end in "m"
// swallowed the text after it. Every sentence here carries an "m" after
// the sequence, to make that swallowing visible.
func TestStrip_RemovesNonColourCSIWithoutEatingTheText(t *testing.T) {
	cases := map[string]string{
		"progress\x1b[2K 50% complete":            "progress 50% complete",
		"\x1b[10;5Hmore text here":                "more text here",
		"\x1b[3Aup, then some more":               "up, then some more",
		"\x1b[?25lhidden cursor, mostly\x1b[?25h": "hidden cursor, mostly",
		"\x1b[2J\x1b[Hscreen cleared, then more":  "screen cleared, then more",
		"\x1b[2 qblock cursor, or some such":      "block cursor, or some such",
	}
	for in, want := range cases {
		if got := Strip(in); got != want {
			t.Errorf("Strip(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStrip_RemovesOSCAndOtherEscapes(t *testing.T) {
	cases := map[string]string{
		// A hyperlink, closed by the string terminator ESC \.
		"\x1b]8;;https://orbit.example\x1b\\link\x1b]8;;\x1b\\ ✓": "link ✓",
		// A title, closed by BEL.
		"\x1b]0;title\x07after": "after",
		// A character-set designation: ESC, one intermediate, one final.
		"a\x1b(Bb": "ab",
		// An escape with nothing after it is dropped, not kept.
		"trailing\x1b": "trailing",
	}
	for in, want := range cases {
		if got := Strip(in); got != want {
			t.Errorf("Strip(%q) = %q, want %q", in, got, want)
		}
	}
}

// Without this the cases above could pass by deleting too much.
func TestStrip_LeavesPlainTextWhole(t *testing.T) {
	cases := []string{
		"",
		"plain text, with an m and a [bracket]",
		"▸ Install ✓ · 日本語 ’quoted’ — dash",
		"a line\nand another\twith a tab",
		"[2K is just text without its escape",
	}
	for _, in := range cases {
		if got := Strip(in); got != in {
			t.Errorf("Strip(%q) = %q, want it unchanged", in, got)
		}
	}
}

func TestStrip_LeavesNoEscapeByteBehind(t *testing.T) {
	in := "\x1b[1mA\x1b[2KB\x1b]0;t\x07C\x1b(BD\x1b[?25lE\x1b[mF"
	got := Strip(in)
	if strings.ContainsRune(got, '\x1b') {
		t.Errorf("Strip(%q) = %q, still holds an escape byte", in, got)
	}
	if got != "ABCDEF" {
		t.Errorf("Strip(%q) = %q, want ABCDEF", in, got)
	}
}
