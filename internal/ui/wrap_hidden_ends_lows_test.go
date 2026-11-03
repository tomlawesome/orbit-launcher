package ui

// Pins #304 SEC-S3-1: visibleCommand writes a hidden character inside a
// bare word as a $'\xHH' piece. A long path whose word starts and ends
// with such a piece looks like one $'...' word to wrapShellCommand's
// cutUnit, which then cuts it inside a piece (or reads the bare text
// between the pieces as quoted), so the wrapped rows, pasted into bash,
// name a different path. The oracle is bash: the rows joined by
// newlines, as a person pastes them, must read back as the original
// path, as the unwrapped command does.

import (
	"fmt"
	"strings"
	"testing"
)

// hiddenEnds are hidden characters visibleCommand spells as $'\xHH'
// pieces: C0 controls, DEL, an invalid byte, a bidi override.
var hiddenEnds = []struct{ name, s string }{
	{"SOH", "\x01"},
	{"ESC", "\x1b"},
	{"DEL", "\x7f"},
	{"invalid byte", "\xff"},
	{"RLO", "\u202e"},
}

func TestWrapHiddenEnds_WrappedPathReadsBackAsThePath(t *testing.T) {
	type wcase struct {
		label, path string
		width       int
		rows        []string
	}
	var cases []wcase
	widths := []int{30, 40, 56, 80}
	for _, h := range hiddenEnds {
		for n := 0; n <= 30; n++ {
			path := h.s + "/srv/" + strings.Repeat("orbit-", 12) + strings.Repeat("x", n) + "/engine" + h.s
			word := visibleCommand(path)
			if !strings.HasPrefix(word, "$'") || !strings.HasSuffix(word, "'") {
				t.Fatalf("setup: visibleCommand(%q) = %q, want a word starting and ending with a $'...' piece", path, word)
			}
			for _, w := range widths {
				rows := wrapShellCommand([]string{"cd", word}, w)
				if len(rows) < 2 {
					t.Fatalf("setup: %s n=%d did not wrap at %d: %q", h.name, n, w, rows)
				}
				cases = append(cases, wcase{fmt.Sprintf("%s ends, n=%d", h.name, n), path, w, rows})
			}
		}
	}

	// Scripts: every original line once, then every wrapped command.
	var scripts []string
	for _, c := range cases {
		scripts = append(scripts, "cd "+visibleCommand(c.path))
	}
	for _, c := range cases {
		scripts = append(scripts, strings.Join(c.rows, "\n"))
	}
	res := secS3ReadBacks(t, "C.UTF-8", scripts)
	originals, wrapped := res[:len(cases)], res[len(cases):]
	failed := 0
	for i, c := range cases {
		want := "cd\n" + c.path + "\n"
		if o := originals[i]; o.out != want || o.status != 0 || o.stderr != "" {
			t.Fatalf("%s: the unwrapped command does not read back as the path (setup): %q, status %d, %q", c.label, o.out, o.status, o.stderr)
		}
		if got := wrapped[i]; got.out != want || got.status != 0 || got.stderr != "" {
			failed++
			if failed <= 5 {
				t.Errorf("%s at width %d: the wrapped rows name a different path\nrows:\n%s\nbash read: %q (status %d, stderr %q)\nwant: %q",
					c.label, c.width, strings.Join(c.rows, "\n"), got.out, got.status, got.stderr, want)
			}
		}
	}
	if failed > 0 {
		t.Errorf("%d of %d wrapped commands do not read back as their path", failed, len(cases))
	}
}

// The rows still keep the drawn shape: none wider than the limit, every
// row but the last continued with a backslash.
func TestWrapHiddenEnds_RowsKeepTheirShape(t *testing.T) {
	for _, h := range hiddenEnds {
		path := h.s + "/srv/" + strings.Repeat("orbit-", 12) + "/engine" + h.s
		for _, w := range []int{30, 40, 56, 80} {
			rows := wrapShellCommand([]string{"cd", visibleCommand(path)}, w)
			for i, r := range rows {
				if len([]rune(r)) > w {
					t.Errorf("%s at %d: row %d is %d cells: %q", h.name, w, i, len([]rune(r)), r)
				}
				if i < len(rows)-1 && !strings.HasSuffix(r, `\`) {
					t.Errorf("%s at %d: row %d does not continue: %q", h.name, w, i, r)
				}
			}
		}
	}
}
