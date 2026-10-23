package ui

// Pins #277 A1: text drawn through the wrapping path (cleanLine, used by
// wrapWords and writeWrapped) shows the invisible direction and joining
// characters as visibleText shows them, as it already does for the
// embedding, override and isolate controls. Other text is unchanged.

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

// invisibleRunes are the eight characters of the finding.
var invisibleRunes = []struct {
	name string
	r    rune
}{
	{"LRM", '\u200e'},
	{"RLM", '\u200f'},
	{"ALM", '\u061c'},
	{"ZWSP", '\u200b'},
	{"ZWNJ", '\u200c'},
	{"ZWJ", '\u200d'},
	{"WORD JOINER", '\u2060'},
	{"BOM", '\ufeff'},
}

// alreadyEscapedRunes are the controls the wrapping path escapes today.
var alreadyEscapedRunes = []rune{'\u202a', '\u202b', '\u202c', '\u202d', '\u202e', '\u2066', '\u2067', '\u2068', '\u2069'}

// escapedAs is what visibleText makes of r, after checking that it is
// something other than the character itself (else the pin is empty).
func escapedAs(t *testing.T, r rune) string {
	t.Helper()
	got := visibleText(string(r))
	if got == string(r) || got == "" {
		t.Fatalf("setup: visibleText(%U) = %q, want a visible spelling", r, got)
	}
	return got
}

func TestWrapInvisible_CleanLineShowsThem(t *testing.T) {
	for _, c := range invisibleRunes {
		t.Run(c.name, func(t *testing.T) {
			want := "ab" + escapedAs(t, c.r) + "cd"
			got := cleanLine("ab" + string(c.r) + "cd")
			if got != want {
				t.Errorf("cleanLine(ab%Ucd) = %q, want %q", c.r, got, want)
			}
		})
	}
}

func TestWrapInvisible_WrapWordsShowsThem(t *testing.T) {
	for _, c := range invisibleRunes {
		t.Run(c.name, func(t *testing.T) {
			want := "left" + escapedAs(t, c.r) + "right"
			got := wrapWords("left"+string(c.r)+"right", 60)
			if len(got) != 1 || got[0] != want {
				t.Errorf("wrapWords = %q, want [%q]", got, want)
			}
			if strings.ContainsRune(strings.Join(got, ""), c.r) {
				t.Errorf("%U reached the wrapped rows raw: %q", c.r, got)
			}
		})
	}
}

func TestWrapInvisible_WriteWrappedShowsThem(t *testing.T) {
	for _, c := range invisibleRunes {
		t.Run(c.name, func(t *testing.T) {
			var b strings.Builder
			writeWrapped(&b, lipgloss.NewStyle(), "left"+string(c.r)+"right", 60)
			out := b.String()
			if strings.ContainsRune(out, c.r) {
				t.Errorf("%U reached the output raw: %q", c.r, out)
			}
			if want := "left" + escapedAs(t, c.r) + "right"; !strings.Contains(ansi.Strip(out), want) {
				t.Errorf("output lacks %q: %q", want, ansi.Strip(out))
			}
		})
	}
}

// The characters are shown wherever they sit, including inside a
// sentence that has to wrap.
func TestWrapInvisible_ShownInsideALongSentence(t *testing.T) {
	var all strings.Builder
	var want strings.Builder
	for _, c := range invisibleRunes {
		all.WriteRune(c.r)
		want.WriteString(escapedAs(t, c.r))
	}
	text := "the daemon said" + all.String() + " something that goes on long enough to need a second row"
	rows := wrapWords(text, 70)
	if len(rows) < 2 {
		t.Fatalf("setup: %q did not wrap: %q", text, rows)
	}
	flat := strings.Join(rows, " ")
	for _, c := range invisibleRunes {
		if strings.ContainsRune(flat, c.r) {
			t.Errorf("%s (%U) reached the rows raw: %q", c.name, c.r, rows)
		}
	}
	if !strings.Contains(squeeze(strings.Join(rows, "")), squeeze("the daemon said"+want.String())) {
		t.Errorf("the escaped characters are not where they were: %q", rows)
	}
}

// The controls the path escaped before keep being escaped.
func TestWrapInvisible_EmbeddingAndIsolateControlsStillShown(t *testing.T) {
	for _, r := range alreadyEscapedRunes {
		got := wrapWords("ab"+string(r)+"cd", 60)
		if len(got) != 1 || got[0] != "ab"+visibleText(string(r))+"cd" || strings.ContainsRune(got[0], r) {
			t.Errorf("wrapWords with %U = %q, want it shown as %q", r, got, visibleText(string(r)))
		}
	}
}

// Other text is unchanged: printable ASCII, plain spaces, non-ASCII
// letters, wide characters and punctuation come out as they went in.
func TestWrapInvisible_OtherTextIsUnchanged(t *testing.T) {
	for _, text := range []string{
		"plain words, with: punctuation (and brackets) — and a dash",
		"café naïve résumé Ünïcödé",
		"日本語のテキスト and mixed",
		"path /srv/containers/mail/orbit-production/.env-orbit",
		"emoji ✓ ✗ ! · ▸",
		"עברית and العربية",
	} {
		got := wrapWords(text, 80)
		if len(got) != 1 || got[0] != text {
			t.Errorf("wrapWords(%q) = %q, want it unchanged", text, got)
		}
		if got := cleanLine(text); got != text {
			t.Errorf("cleanLine(%q) = %q, want it unchanged", text, got)
		}
	}
}
