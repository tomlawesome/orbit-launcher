package ui

// Pins #277 A2: text drawn through the wrapping path never gives a row
// wider than the limit; a word that fits stays whole, a longer word is cut
// across rows with every character kept, in order. On the Repair results
// screen the engine's refusal wraps within the screen.

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

// squeeze drops all whitespace, so rows can be compared with the text
// they were cut from.
func squeeze(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
}

func checkWrapped(t *testing.T, text string, limit int) []string {
	t.Helper()
	rows := wrapWords(text, limit)
	for i, row := range rows {
		if got := lipgloss.Width(row); got > limit {
			t.Errorf("row %d is %d cells wide, limit %d: %q", i, got, limit, row)
		}
	}
	if got, want := squeeze(strings.Join(rows, "")), squeeze(text); got != want {
		t.Errorf("the rows do not hold every character in order:\n got %q\nwant %q", got, want)
	}
	return rows
}

func TestWrapOverflow_ALongWordIsCutWithinTheLimit(t *testing.T) {
	long := strings.Repeat("abcdefghij", 15) // 150 cells
	for _, limit := range []int{10, 20, 46, 60} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			rows := checkWrapped(t, long, limit)
			if len(rows) < 150/limit {
				t.Errorf("%d rows for a 150-cell word at limit %d: %q", len(rows), limit, rows)
			}
		})
	}
}

func TestWrapOverflow_ALongWordAmongOthersIsCutAndTheRestKept(t *testing.T) {
	long := strings.Repeat("0123456789", 12)
	text := "nothing was changed — the engine refused to run (" + long + ") and then more words follow after it"
	for _, limit := range []int{15, 30, 46, 72} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			checkWrapped(t, text, limit)
		})
	}
}

func TestWrapOverflow_ALongWordAfterAnIndentIsCut(t *testing.T) {
	checkWrapped(t, "    "+strings.Repeat("x", 90), 30)
	checkWrapped(t, "line one\n"+strings.Repeat("y", 90)+"\nline three", 30)
}

func TestWrapOverflow_WideCharactersAreCutByCells(t *testing.T) {
	long := strings.Repeat("日本語", 30) // 180 cells
	for _, limit := range []int{9, 20, 31} {
		t.Run(fmt.Sprintf("limit_%d", limit), func(t *testing.T) {
			checkWrapped(t, long, limit)
		})
	}
}

// A word that fits the limit is not split, wherever it falls.
func TestWrapOverflow_AWordThatFitsStaysWhole(t *testing.T) {
	const limit = 20
	fits := "abcdefghijklmnopqrst" // exactly the limit
	almost := "abcdefghijklmnopqrs"
	for _, word := range []string{fits, almost, "short"} {
		for _, text := range []string{
			word,
			"first " + word,
			word + " last",
			"some words before " + word + " and some words after it",
			"a " + word + " b " + word + " c " + word,
		} {
			rows := wrapWords(text, limit)
			whole := 0
			for _, row := range rows {
				whole += strings.Count(row, word)
			}
			if want := strings.Count(text, word); whole < want {
				t.Errorf("wrapWords(%q, %d) = %q: %q was split (whole in %d of %d places)", text, limit, rows, word, whole, want)
			}
			for i, row := range rows {
				if w := lipgloss.Width(row); w > limit {
					t.Errorf("wrapWords(%q, %d): row %d is %d cells: %q", text, limit, i, w, row)
				}
			}
			if got, want := squeeze(strings.Join(rows, "")), squeeze(text); got != want {
				t.Errorf("wrapWords(%q, %d) lost characters:\n got %q\nwant %q", text, limit, got, want)
			}
		}
	}
}

// Cutting a longer word does not cut the words that fit around it.
func TestWrapOverflow_AWordThatFitsStaysWholeBesideACutWord(t *testing.T) {
	const limit = 20
	word := "abcdefghijklmnopqrs"
	text := word + " " + strings.Repeat("z", 50) + " " + word
	rows := wrapWords(text, limit)
	whole := 0
	for _, row := range rows {
		whole += strings.Count(row, word)
	}
	if whole != 2 {
		t.Errorf("wrapWords(%q, %d) = %q: the fitting word is whole %d times, want 2", text, limit, rows, whole)
	}
	if got, want := squeeze(strings.Join(rows, "")), squeeze(text); got != want {
		t.Errorf("characters lost or moved:\n got %q\nwant %q", got, want)
	}
}

func TestWrapOverflow_WriteWrappedNeverRunsPastTheLimit(t *testing.T) {
	const limit = 24
	text := "see " + strings.Repeat("path/segment/", 12) + " for details"
	var b strings.Builder
	writeWrapped(&b, lipgloss.NewStyle(), text, limit)
	out := ansi.Strip(b.String())
	for i, row := range strings.Split(out, "\n") {
		if w := lipgloss.Width(row); w > limit {
			t.Errorf("row %d is %d cells, limit %d: %q", i, w, limit, row)
		}
	}
	if got, want := squeeze(out), squeeze(text); got != want {
		t.Errorf("writeWrapped lost characters:\n got %q\nwant %q", got, want)
	}
}

// ---- Repair results: the engine's refusal ----

// alnum keeps only ASCII letters and digits, so the sky's glyphs and the
// spaces a cut word leaves between rows drop out.
func alnum(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 128 && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return r
		}
		return -1
	}, s)
}

func TestWrapOverflow_RepairRefusalWrapsWithinTheScreen(t *testing.T) {
	long := strings.Repeat("abcdefghij", 15) // one unbroken 150-character reason
	for _, w := range c9Widths {
		for _, tc := range []struct{ name, reason string }{
			{"ordinary", "lock-held"},
			{"unbroken", long},
		} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, w), func(t *testing.T) {
				m := repairRefusedScreen(t, tc.reason, w)
				plainScreen := ansi.Strip(m.View().Content)
				for i, row := range strings.Split(plainScreen, "\n") {
					if got := visibleWidth(row); got > w {
						t.Errorf("row %d is %d cells wide on a %d-column screen: %q", i, got, w, strings.TrimSpace(row))
					}
				}
				if !strings.Contains(plainScreen, "nothing was changed") {
					t.Errorf("the refusal text is not on the screen:\n%s", plainScreen)
				}
				if tc.name == "ordinary" {
					if flat := proseOnly(plainScreen); !strings.Contains(flat, "nothing was changed — the engine refused to run (lock-held)") {
						t.Errorf("the refusal is not there in full:\n%s", plainScreen)
					}
					return
				}
				if !strings.Contains(alnum(plainScreen), long) {
					t.Errorf("every character of the reason is not on the screen, in order:\n%s", plainScreen)
				}
			})
		}
	}
}
