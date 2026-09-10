package engine

import (
	"strings"
	"testing"
)

// Issue #157: the launcher's screen stopped changing part-way into an
// install and never moved again, while the install completed underneath
// and the stack came up healthy. The engine stream had stopped
// delivering: its last message was the phase event for the database
// wait, and no DoneMsg ever arrived.
//
// The cause is a single over-long stdout line. bufio.Scanner treats one
// as a permanent error — Scan returns false and the read loop ends — and
// that loop is the only thing draining the engine's stdout pipe. The
// engine then blocks writing to a full pipe, its stderr never reaches
// EOF, cmd.Wait is never called, and DoneMsg is never sent. install.sh
// produces such a line whenever a phase reports progress with carriage
// returns and no newline for long enough, which is what its database
// wait does.
//
// These tests pin the two halves of the repair: an over-long line loses
// only its own excess, and it never stops the stream.

const overlongPreamble = `printf 'phase=database component=postgres state=starting reason=database-boot action=wait elapsed=1s\n'
printf '%0.sx' $(seq 1 20000); printf '\n'
`

// TestStart_AnOverlongLineDoesNotStopTheStream is #157 itself: output
// after an over-long line must still arrive, and the run must still
// finish. Before the fix this wedged — one event, then silence forever.
func TestStart_AnOverlongLineDoesNotStopTheStream(t *testing.T) {
	s := startScript(t, overlongPreamble+`for i in $(seq 1 5000); do printf ' Container orbit-service-%s  Started\n' "$i"; done
printf 'phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=2s\n'
exit 0`)

	events, raw, done := collect(t, s)
	if done.ExitCode != 0 || done.Err != nil {
		t.Errorf("DoneMsg = exit %d err %v, want a clean exit", done.ExitCode, done.Err)
	}
	if len(events) != 2 || events[len(events)-1].Phase != "complete" {
		t.Errorf("events = %d, last phase %q; want the database and complete events", len(events), lastPhase(events))
	}
	// 5000 container lines plus the truncated over-long line itself.
	if len(raw) != 5001 {
		t.Errorf("raw lines = %d, want 5001 (the truncated over-long line and 5000 after it)", len(raw))
	}
}

// TestStart_AnOverlongLineIsTruncatedNotDropped keeps the bound doing
// its job: the excess goes, the line does not, and nothing after it is
// lost. Before the fix the trailing event vanished silently.
func TestStart_AnOverlongLineIsTruncatedNotDropped(t *testing.T) {
	s := startScript(t, overlongPreamble+`printf 'phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=2s\n'
exit 0`)

	events, raw, done := collect(t, s)
	if done.ExitCode != 0 {
		t.Errorf("DoneMsg = exit %d, want 0", done.ExitCode)
	}
	if len(events) != 2 || events[len(events)-1].Phase != "complete" {
		t.Errorf("events = %d, last phase %q; want the event after the over-long line to survive", len(events), lastPhase(events))
	}
	if len(raw) != 1 {
		t.Fatalf("raw lines = %d, want the one truncated line", len(raw))
	}
	if got := len(raw[0]); got != maxLineBytes {
		t.Errorf("truncated line = %d bytes, want maxLineBytes (%d)", got, maxLineBytes)
	}
	if strings.Trim(raw[0], "x") != "" {
		t.Errorf("truncated line lost its content")
	}
}

// TestStart_AnOverlongStderrLineStillYieldsTheTail proves the same
// repair on the stderr drain: a wedged stderr reader never closes
// tailCh, which blocks the stdout goroutine before cmd.Wait just as
// surely — the same freeze by another route.
func TestStart_AnOverlongStderrLineStillYieldsTheTail(t *testing.T) {
	s := startScript(t, `printf '%0.sy' $(seq 1 20000) >&2; printf '\n' >&2
printf 'Orbit installer: could not reach the database.\n' >&2
printf 'phase=database component=postgres state=failed reason=database-unreachable action=retry elapsed=2s\n'
exit 1`)

	_, _, done := collect(t, s)
	if done.ExitCode != 1 {
		t.Errorf("DoneMsg = exit %d, want 1", done.ExitCode)
	}
	if len(done.StderrTail) != 2 {
		t.Fatalf("stderr tail = %d lines (%q), want the truncated line and the guidance after it", len(done.StderrTail), done.StderrTail)
	}
	if got := done.StderrTail[len(done.StderrTail)-1]; got != "Orbit installer: could not reach the database." {
		t.Errorf("last stderr line = %q, want the guidance that followed the over-long line", got)
	}
}

func lastPhase(events []Event) string {
	if len(events) == 0 {
		return ""
	}
	return events[len(events)-1].Phase
}
