package ui

// Pins the third #214 review round (wrap.go): a long line breaks at any
// Unicode space, as strings.Fields did, and trailing rows that clean to
// nothing are not rows.

import (
	"reflect"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestWrapWords_BreaksAtAnyUnicodeSpace(t *testing.T) {
	cases := map[string][]string{
		"aaa bbb ccc ddd": {"aaa bbb", "ccc ddd"},    // no-break space
		"日本　語の　エラー　です":    {"日本", "語の", "エラー", "です"}, // ideographic space
		"aaa bbb ccc ddd": {"aaa bbb", "ccc ddd"},    // line separator
	}
	for in, want := range cases {
		got := wrapWords(in, 8)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("wrapWords(%q, 8) = %q, want %q", in, got, want)
		}
		for _, row := range got {
			if w := lipgloss.Width(row); w > 8 {
				t.Errorf("wrapWords(%q, 8): row %q is %d cells", in, row, w)
			}
		}
	}
}

func TestWrapWords_TrailingRowsThatCleanToNothingGo(t *testing.T) {
	for _, in := range []string{"err\n\x00", "err\n ", "err\n\t", "err\n \n\x07\n", "err\n　"} {
		if got := wrapWords(in, 40); !reflect.DeepEqual(got, []string{"err"}) {
			t.Errorf("wrapWords(%q, 40) = %q, want [\"err\"]", in, got)
		}
	}
	// A blank row inside the text stays: only the trailing ones go.
	if got := wrapWords("err\n\nmore", 40); !reflect.DeepEqual(got, []string{"err", "", "more"}) {
		t.Errorf("an inner blank row was lost: %q", got)
	}
}
