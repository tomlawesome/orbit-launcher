package ansi

import (
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

// Pins the #214 review (finding 2): Strip on malformed or less common
// input. It must never return invalid UTF-8, never leak the payload of
// a control string, and never let a broken sequence eat the text after
// it.

func TestStrip_MalformedSequencesKeepTheText(t *testing.T) {
	cases := map[string]string{
		// ESC before a multi-byte character: the character is not part
		// of any sequence and stays whole.
		"\x1bé":   "é",
		"x\x1b日y": "x日y",
		// ESC ESC: the first escape is cut short, the second starts the
		// real sequence.
		"\x1b\x1b[31mX": "X",
		// Control strings run to their string terminator, payload and all.
		"\x1bP1;2q\x1b\\z":     "z", // DCS
		"\x1b_apc data\x1b\\z": "z", // APC
		"\x1b^pm data\x1b\\z":  "z", // PM
		"\x1bXsos data\x1b\\z": "z", // SOS
		// An unterminated CSI ends at the first byte that cannot be part
		// of one: the newline and what follows are text.
		"a\x1b[3\nb":     "a\nb",
		"a\x1b[3;é rest": "aé rest",
		"a\x1b[1\x1b[mb": "ab",
		// ESC before a control byte drops the ESC, keeps the control.
		"a\x1b\nb": "a\nb",
	}
	for in, want := range cases {
		if got := Strip(in); got != want {
			t.Errorf("Strip(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestStrip_AlwaysReturnsValidUTF8(t *testing.T) {
	for _, in := range []string{"\x1bé", "\xff plain", "a\x1b\xc3", "\x1b[\xe6\x97\xa5"} {
		if got := Strip(in); !utf8.ValidString(got) {
			t.Errorf("Strip(%q) = %q, not valid UTF-8", in, got)
		}
	}
	// A deterministic sweep over the bytes that matter to the parser.
	alphabet := []string{"\x1b", "[", "]", "P", "_", "^", "X", "\\", "\x07", "\n", "m", "3", ";", "(", " ", "é", "日", "\xff", "a"}
	r := rand.New(rand.NewSource(1))
	for n := 0; n < 20000; n++ {
		var b strings.Builder
		for k := r.Intn(12); k >= 0; k-- {
			b.WriteString(alphabet[r.Intn(len(alphabet))])
		}
		in := b.String()
		got := Strip(in)
		if !utf8.ValidString(got) {
			t.Fatalf("Strip(%q) = %q, not valid UTF-8", in, got)
		}
		if strings.ContainsRune(got, '\x1b') {
			t.Fatalf("Strip(%q) = %q, still holds an escape byte", in, got)
		}
	}
}

func FuzzStrip(f *testing.F) {
	for _, seed := range []string{"\x1bé", "\x1b\x1b[31mX", "\x1bP1;2q\x1b\\z", "a\x1b[3\nb", "\x1b]8;;u\x1b\\l"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, in string) {
		got := Strip(in)
		if !utf8.ValidString(got) || strings.ContainsRune(got, '\x1b') {
			t.Errorf("Strip(%q) = %q", in, got)
		}
	})
}
