package vtscreen

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// draw writes s to term's pty from the program's side, as a program
// running on it would.
func draw(t *testing.T, term *Terminal, s string) {
	t.Helper()
	if _, err := io.WriteString(term.Output(), s); err != nil {
		t.Fatalf("write to pty: %v", err)
	}
}

func newTerminal(t *testing.T) *Terminal {
	t.Helper()
	term, err := New(120, 40, nil)
	if err != nil {
		t.Fatalf("create virtual terminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })
	return term
}

// TestScreenWait_FindsTextSplitAcrossRedraws is the case the byte stream
// could not match (#181): a host name drawn in pieces, with styling and
// a cursor move elsewhere between them, the way a diff renderer paints
// a styled, centred line. test/live's Remove subtest could not assert
// the splash's FQDN because of it. The screen holds the text whole once
// the last piece lands, and not before.
func TestScreenWait_FindsTextSplitAcrossRedraws(t *testing.T) {
	term := newTerminal(t)
	const host = "orbit-live-test-42.internal"

	draw(t, term, "\x1b[2J\x1b[5;40H\x1b[38;5;69morbit-live\x1b[0m")
	_, err := Wait(term, 300*time.Millisecond, ContainsAny(host))
	var timeout *TimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("half-drawn host: err = %v, want a *TimeoutError", err)
	}
	if !strings.Contains(timeout.Screen, "orbit-live") {
		t.Errorf("timeout's last screen = %q, want the half-drawn host", timeout.Screen)
	}

	draw(t, term, "\x1b[1;1H\x1b[1m·\x1b[0m\x1b[5;50H-test-42\x1b[38;5;69m.internal\x1b[0m")
	screen, err := Wait(term, 5*time.Second, ContainsAll(host, "·"))
	if err != nil {
		t.Fatalf("host completed across two writes was not found: %v", err)
	}
	if got := strings.Split(screen, "\n")[4]; strings.TrimSpace(got) != host {
		t.Errorf("row 5 = %q, want just %q", got, host)
	}
}

// TestScreenWait_KeepsUpWithFastRepaints is #159 / #165 in the small:
// a program repaints launcher-shaped frames far faster than anyone
// reads them, then shows the success marker. go-expect's rescanning
// matcher once fell so far behind that output that a finished install
// looked frozen. A screen wait looks at one screen whatever came
// before it, so the marker must be found well inside the budget.
func TestScreenWait_KeepsUpWithFastRepaints(t *testing.T) {
	term := newTerminal(t)

	// One frame is the shape the mission console paints on every engine
	// event: a boxed, padded, spinner-carrying screen a little over 2 KB.
	var frame strings.Builder
	frame.WriteString("\x1b[H\x1b[2J")
	for range 30 {
		fmt.Fprintf(&frame, "\x1b[38;5;69m│\x1b[0m ⠋ application starting %-60s │\r\n", "")
	}
	const frames = 120
	go func() {
		for range frames {
			if _, err := io.WriteString(term.Output(), frame.String()); err != nil {
				return
			}
		}
		_, _ = io.WriteString(term.Output(), "Get into Orbit\r\n")
	}()

	const budget = 60 * time.Second
	start := time.Now()
	screen, err := Wait(term, budget, ContainsAny("Get into Orbit", "Installation stopped"))
	if err != nil {
		t.Fatalf("the marker after %d frames (%d bytes) was not reached: %v", frames, frames*frame.Len(), err)
	}
	if !strings.Contains(screen, "Get into Orbit") {
		t.Fatalf("returned screen does not carry the marker:\n%s", screen)
	}
	t.Logf("marker found after %s", time.Since(start).Round(time.Millisecond))
}

// TestStripEscapes covers each kind of sequence the emulator's Render
// writes, next to multi-byte text that must come through whole.
func TestStripEscapes(t *testing.T) {
	cases := map[string]string{
		"\x1b[1;38;5;69m▸ Install\x1b[m":                          "▸ Install",
		"\x1b]8;;https://orbit.example\x1b\\link\x1b]8;;\x1b\\ ✓": "link ✓",
		"\x1b]0;title\x07after":                                   "after",
		"a\x1b(Bb":                                                "ab",
		"trailing\x1b":                                            "trailing",
	}
	for in, want := range cases {
		if got := stripEscapes(in); got != want {
			t.Errorf("stripEscapes(%q) = %q, want %q", in, got, want)
		}
	}
}
