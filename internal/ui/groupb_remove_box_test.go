package ui

// Pins #277 L2 (ratified, answer 5a): Remove's command box keeps the END
// of the command on a short terminal. When the box is given fewer rows
// than the command has, its first row is `…` and the rest are the
// command's last room-1 rows, so the half that deletes everything
// (`&& sudo rm -rf '<folder>'`) is always in the box. Room 1 is `…`
// alone. Copy command still carries the whole command at every height,
// and the caption, the danger sentence, Copy command, Menu and Exit stay
// on screen.

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// groupBBoxRows is the box's inside rows, right-trimmed, with the frame
// and padding off.
func groupBBoxRows(t *testing.T, s string) []string {
	t.Helper()
	return narrowBoxInside(t, s)
}

func groupBTrim(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(r, " ")
	}
	return out
}

func TestGroupB_L2_ShortTerminalKeepsTheEndOfTheCommand(t *testing.T) {
	for _, w := range []int{50, 80} {
		tall := narrowDoneModel(t, c9LongDir, w, 80, &bytes.Buffer{})
		full := groupBTrim(groupBBoxRows(t, screen(tall)))
		n := len(full)
		if n < 4 {
			t.Fatalf("setup: width %d: the long folder's command is only %d box rows", w, n)
		}
		if strings.Contains(strings.Join(full, "\n"), "…") {
			t.Fatalf("setup: the tall box holds a `…`:\n%s", screen(tall))
		}
		chrome := contentRows(screen(tall)) - (n + 2) // everything but the box (frame included in the +2)
		for h := chrome + 2 + 3; h <= chrome+2+n+2; h++ {
			h := h
			t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
				var buf bytes.Buffer
				m := narrowDoneModel(t, c9LongDir, w, h, &buf)
				s := screen(m)
				got := groupBTrim(groupBBoxRows(t, s))
				k := len(got)
				room := h - 1 - chrome - 2 // rows the box can have inside its frame, last row left to the sky
				if room < 1 {
					room = 1
				}
				if k > n {
					t.Fatalf("the box has %d rows, the command only %d:\n%s", k, n, s)
				}
				switch {
				case k == n:
					if strings.Join(got, "\n") != strings.Join(full, "\n") || strings.Contains(strings.Join(got, "\n"), "…") {
						t.Errorf("room for every row, yet the box differs from the whole command or holds `…`:\n%s", s)
					}
				default:
					if strings.TrimSpace(got[0]) != "…" {
						t.Errorf("the box is short (%d of %d rows) and its first row is %q, want `…`:\n%s", k, n, got[0], s)
					}
					if k >= 2 {
						want := full[n-(k-1):]
						if strings.Join(got[1:], "\n") != strings.Join(want, "\n") {
							t.Errorf("the box's rows after `…` are not the command's last %d rows:\ngot  %q\nwant %q\n%s", k-1, got[1:], want, s)
						}
						if got[k-1] != full[n-1] {
							t.Errorf("the command's last row is not in the box:\n%s", s)
						}
					}
				}
				if room >= 2 && n >= 2 && k < min(room, n) {
					t.Errorf("the box has %d rows but %d fit (height %d): it is shorter than it needs to be:\n%s", k, min(room, n), h, s)
				}
				if !strings.Contains(strings.Join(got, "\n"), "rm") && room >= 3 {
					// the end of the command is the half that deletes: rm -rf <folder>
					t.Errorf("the deleting half (`rm`) is not in the box:\n%s", s)
				}

				flat := proseOnly(s)
				for _, want := range []string{
					"to fully remove Orbit from this machine, copy and run",
					"this deletes all mail, documents and configuration",
					"Copy command", "Menu", "Exit",
				} {
					if !strings.Contains(flat, want) {
						t.Errorf("%q is not on screen at height %d:\n%s", want, h, s)
					}
				}
				if nrows := len(strings.Split(strings.TrimRight(s, "\n"), "\n")); nrows > h {
					t.Errorf("the screen is %d rows, over the %d available:\n%s", nrows, h, s)
				}

				// Copy command copies the whole command at this height.
				_, cmd := updateAs(t, m, key(tea.KeyEnter))
				if cmd == nil {
					t.Fatal("Copy command ran nothing")
				}
				cmd()
				const prefix, suffix = "\x1b]52;c;", "\x07"
				raw := buf.String()
				if !strings.HasPrefix(raw, prefix) || !strings.HasSuffix(raw, suffix) {
					t.Fatalf("clipboard write = %q, want an OSC 52 sequence", raw)
				}
				dec, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(raw, prefix), suffix))
				if err != nil {
					t.Fatalf("payload is not base64: %v", err)
				}
				if want := removalLine(t, c9LongDir); string(dec) != want {
					t.Errorf("copied %q, want the whole command %q", dec, want)
				}
			})
		}
	}
}
