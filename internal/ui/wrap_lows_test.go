package ui

// Pins the corrected behaviour of the folder-escaping notes on #267
// (FR1-2, FR1-3, FR1-5, FR1-6). FR1-4 stays a Note and is not tested.

import (
	"strings"
	"testing"
)

// FR1-2: a backslash is shown so that a folder which really contains the
// text \x1b cannot look like one that contains an escape character.
func TestVisibleText_LiteralBackslashTextNeverReadsLikeAControlRune(t *testing.T) {
	inputs := []string{
		`\x1b`, "\x1b",
		`\r`, "\r",
		`\u202e`, "\u202e",
		`\\`, `\`,
		`\n`, "\n",
		`\x7f`, "\x7f",
	}
	seen := map[string]string{}
	for _, in := range inputs {
		out := visibleText(in)
		if prev, dup := seen[out]; dup {
			t.Errorf("visibleText(%q) and visibleText(%q) both read %q: two different folder names look the same", prev, in, out)
		}
		seen[out] = in
	}
}

func TestVisibleText_ABackslashIsShownDoubled(t *testing.T) {
	for in, want := range map[string]string{
		`a\b`:       `a\\b`,
		`\x1b`:      `\\x1b`,
		"\x1b":      `\x1b`,
		`C:\orbit\`: `C:\\orbit\\`,
	} {
		if got := visibleText(in); got != want {
			t.Errorf("visibleText(%q) = %q, want %q", in, got, want)
		}
	}
}

// FR1-3: a long escaped name is cut at the row width only between whole
// escapes, never leaving `\x` at the end of one row and `1b` at the
// start of the next.
func TestWrapHard_NeverCutsInsideAnEscape(t *testing.T) {
	const limit, edge, maxRows = 46, 46, 12
	// Each rune and the text it is shown as.
	runes := map[string]string{
		"\x1b":   `\x1b`,
		"\r":     `\r`,
		"\x7f":   `\x7f`,
		"\u202e": `\u202e`,
	}
	for raw, shown := range runes {
		for prefix := 36; prefix <= 50; prefix++ {
			in := strings.Repeat("a", prefix) + raw + strings.Repeat("z", 100)
			rows := wrapHard(in, limit, edge, maxRows)
			want := strings.Repeat("a", prefix) + shown + strings.Repeat("z", 100)
			if got := strings.Join(rows, ""); got != want {
				t.Errorf("%s after %d chars: rows read back %q, want %q", shown, prefix, got, want)
				continue
			}
			// The row ends that fall inside the escape are the faults.
			start := prefix
			end := prefix + len(shown)
			at := 0
			for i, r := range rows[:len(rows)-1] {
				at += len(r)
				if at > start && at < end {
					t.Errorf("%s after %d chars: row %d ends inside the escape (row %q, next %q)", shown, prefix, i, r, rows[i+1])
				}
			}
		}
	}
}

// FR1-5: at a width of four columns or fewer measure gives 0, so a row
// can hold nothing. It never gives less than one cell, and a normal
// width is unchanged.
func TestMeasure_NeverLessThanOneCell(t *testing.T) {
	for _, width := range []int{-3, 0, 1, 2, 3, 4} {
		if got := measure(width, proseWidth); got < 1 {
			t.Errorf("measure(%d, %d) = %d, want at least 1", width, proseWidth, got)
		}
	}
}

func TestMeasure_NormalWidthsAreUnchanged(t *testing.T) {
	for _, c := range [][3]int{{80, 56, 56}, {200, 56, 56}, {60, 56, 56}, {50, 56, 46}, {70, 72, 66}, {20, 56, 16}} {
		if got := measure(c[0], c[1]); got != c[2] {
			t.Errorf("measure(%d, %d) = %d, want %d", c[0], c[1], got, c[2])
		}
	}
}

// FR1-6: blank-looking characters other than the plain space are shown
// as escapes.
func TestVisibleText_LookAlikeBlanksAreShownAsEscapes(t *testing.T) {
	for in, want := range map[string]string{
		"a\u00a0b": `a\u00a0b`, // no-break space
		"a\u3000b": `a\u3000b`, // ideographic space
		"a\u2003b": `a\u2003b`, // em space
		"a\u202fb": `a\u202fb`, // narrow no-break space
		"a\u2007b": `a\u2007b`, // figure space
	} {
		if got := visibleText(in); got != want {
			t.Errorf("visibleText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVisibleText_ThePlainSpaceStaysASpace(t *testing.T) {
	if got, want := visibleText("my orbit dir"), "my orbit dir"; got != want {
		t.Errorf("visibleText(%q) = %q, want it unchanged", want, got)
	}
}

// A backslash that does not start a complete visibleText escape is one
// character, not the start of an escape to keep whole.
func TestEscapeLen_RejectsWhatIsNotACompleteEscape(t *testing.T) {
	for _, text := range []string{`\q1`, `\x4`, `\u12`, `\xZZ`, `\u12g4`} {
		if got := escapeLen(text); got != 0 {
			t.Errorf("escapeLen(%q) = %d, want 0", text, got)
		}
	}
	for text, want := range map[string]int{`\n`: 2, `\x1b`: 4, `\u00e9!`: 6, `\U0001F600`: 10} {
		if got := escapeLen(text); got != want {
			t.Errorf("escapeLen(%q) = %d, want %d", text, got, want)
		}
	}
}
