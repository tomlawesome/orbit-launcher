package ui

// Pins the second #214 review round (wrap.go): unsized text reaches the
// screen with no raw control characters, without trailing blank rows,
// and is wrapped in time linear in its length.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// noControls fails if a row holds any control character: a carriage
// return moves the cursor back over the row, and a tab measures 0 cells
// but renders as four.
func noControls(t *testing.T, where string, rows []string) {
	t.Helper()
	for _, row := range rows {
		for _, r := range row {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
				t.Errorf("%s: row holds control %U: %q", where, r, row)
				break
			}
		}
	}
}

func TestWrapWords_NoControlCharactersReachTheRows(t *testing.T) {
	long := strings.Repeat("Pulling fs layer 10%\rPulling fs layer 40%\r", 3) + "Pulling fs layer done"
	cases := map[string]string{
		"carriage returns, wrapped": long,
		"carriage return, fits":     "Pulling 10%\rPulling 40%",
		"tabs, wrapped":             strings.Repeat("col\tvalue\t", 8),
		"tab, fits":                 "a\tb",
		"escape and controls":       "\x1b[31mred\x1b[0m bell\x07 nul\x00 " + strings.Repeat("word ", 12),
	}
	for name, text := range cases {
		rows := wrapWords(text, 40)
		if len(rows) == 0 {
			t.Errorf("%s: no rows", name)
		}
		noControls(t, name, rows)
		for _, row := range rows {
			// The rendered width, so a tab expanded by Render counts.
			if w := lipgloss.Width(style214Render(row)); w > 40 && strings.Contains(row, " ") {
				t.Errorf("%s: row renders %d cells, past 40: %q", name, w, row)
			}
		}
	}
}

func style214Render(s string) string { return lipgloss.NewStyle().Render(s) }

// A carriage return keeps what a terminal would end up showing: the
// last segment, not the overwritten ones. CRLF line ends lose nothing.
func TestWrapWords_CarriageReturnKeepsTheLastSegment(t *testing.T) {
	if got := wrapWords("Pulling 10%\rPulling 40%", 40); len(got) != 1 || got[0] != "Pulling 40%" {
		t.Errorf("progress line = %q, want [\"Pulling 40%%\"]", got)
	}
	if got := wrapWords("line one\r\nline two\r\n", 40); len(got) != 2 || got[0] != "line one" || got[1] != "line two" {
		t.Errorf("CRLF text = %q, want [\"line one\" \"line two\"]", got)
	}
}

// The reviewer's screen: a failed run's stderr tail holding \r progress.
func TestEngineRunFailure_ProgressInTheStderrTailIsNoControl(t *testing.T) {
	r := startedRun(t, "install", engineRunSeams{})
	r.state = runStreaming
	tail := []string{
		strings.Repeat("Pulling fs layer 10%\rPulling fs layer 40%\r", 3) + "Pulling fs layer done",
		"short\rshorter",
		"col\tvalue",
	}
	r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1"), StderrTail: tail}})
	if r.state != runFailed {
		t.Fatalf("state = %v, want runFailed", r.state)
	}
	plain := wi10Plain(r.view(80, 26))
	noControls(t, "failed-run screen", strings.Split(plain, "\n"))
}

// Text ending in a line break gives no trailing blank row.
func TestWrapWords_TrailingLineBreaksAddNoRow(t *testing.T) {
	for _, text := range []string{"x\n", "x\n\n", "x\r\n"} {
		if got := wrapWords(text, 40); len(got) != 1 || got[0] != "x" {
			t.Errorf("wrapWords(%q) = %q, want [\"x\"]", text, got)
		}
	}
}

// StandDown's error carries CombinedOutput, which ends in a newline:
// Remove's failure screen has the one blank row before its menu, not two.
func TestRemoveFailure_OutputEndingInANewlineAddsNoBlankRow(t *testing.T) {
	stand := func(context.Context, string) error {
		return errors.New("docker compose down: exit status 1: network orbit_default has active endpoints\n")
	}
	m := newTestRemoveModel(stand)
	updated, cmd := m.Update(key(tea.KeyEnter))
	updated, _ = updated.Update(cmd())
	rows := strings.Split(wi10Plain(updated.View().Content), "\n")
	at := -1
	for i, row := range rows {
		if strings.Contains(row, "endpoints") {
			at = i
		}
	}
	if at < 0 || at+2 >= len(rows) {
		t.Fatalf("error row not found:\n%s", wi10Compact(strings.Join(rows, "\n")))
	}
	if strings.TrimSpace(strings.Trim(rows[at+1], "·.✦●")) != "" || !strings.Contains(rows[at+2], "Exit") {
		t.Errorf("want one blank row between the error and Exit, got %q then %q", rows[at+1], rows[at+2])
	}
}

// Long runs of spaces wrap in linear time. The bounds are generous; the
// quadratic join took seconds.
func TestWrapWords_LongWhitespaceRunsAreLinear(t *testing.T) {
	cases := map[string]string{
		"mostly spaces":   "a" + strings.Repeat(" ", 112000),
		"only whitespace": strings.Repeat(" ", 120000),
		"spaced words":    strings.Repeat("a          ", 11000),
	}
	for name, text := range cases {
		start := time.Now()
		rows := wrapWords(text, 72)
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Errorf("%s: wrapping %d characters took %v", name, len(text), d)
		}
		for _, row := range rows {
			if w := lipgloss.Width(row); w > 72 {
				t.Errorf("%s: a row is %d cells, past 72", name, w)
				break
			}
		}
	}
}
