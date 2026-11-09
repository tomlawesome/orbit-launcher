package main

// #1576 step 5: reportExit is the seam for main's exit behaviour after the
// screen closes. An exit note is a failure (exit 1); a leave note is the
// operator's own choice (exit 0). Assumed:
//   func reportExit(w io.Writer, m interface{ ExitNote() string; LeaveNote() string }) int

import (
	"bytes"
	"testing"
)

type fakeExit struct{ exit, leave string }

func (f fakeExit) ExitNote() string  { return f.exit }
func (f fakeExit) LeaveNote() string { return f.leave }

func TestReportExit(t *testing.T) {
	const leave = "orbit-launcher: this Orbit was started from /srv/x; put the folder back there and run the launcher from it"
	for _, c := range []struct {
		name     string
		m        fakeExit
		wantOut  string
		wantCode int
	}{
		{"exit note", fakeExit{exit: "still undoing"}, "\norbit-launcher: still undoing\n", 1},
		{"leave note only", fakeExit{leave: leave}, leave + "\n", 0},
		{"both empty", fakeExit{}, "", 0},
		{"both set: the exit note wins", fakeExit{exit: "still undoing", leave: leave}, "\norbit-launcher: still undoing\n", 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			var w bytes.Buffer
			if got := reportExit(&w, c.m); got != c.wantCode {
				t.Errorf("returned %d, want %d", got, c.wantCode)
			}
			if w.String() != c.wantOut {
				t.Errorf("wrote %q, want %q", w.String(), c.wantOut)
			}
		})
	}
}
