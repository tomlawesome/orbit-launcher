package ui

// #293 (audit v0.4.3-c, G4c): the owner's stopping line for a fresh
// install whose rollback is disarmed is 96 characters long. On a narrow
// terminal it must wrap onto more than one row like the screen's other
// prose, never run past the screen's width, and keep every word.
//
// Written from the audit brief alone, without the code. The stop
// machinery and finishingWithoutOptional are in
// enginerun_stop_disarmed_line_test.go and the files it names.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

// stoppingScreenAt is a fresh install fed one phase=optional event and
// then stopped with Ctrl-C, resized to and rendered at width x height
// with ANSI styling stripped.
func stoppingScreenAt(t *testing.T, width, height int) string {
	t.Helper()
	s := stoppableStream(t, optionalWaitScript)
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)
	r, _ = runUpdate(t, r, tea.WindowSizeMsg{Width: width, Height: height})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: clamavWaiting()})
	r, _ = runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping:\n%s", r.state, runScreen(r))
	}
	return ansi.Strip(r.view(width, height))
}

// assertDisarmedLineFits checks every row of the stopping screen fits in
// width cells and the owner's sentence survives whole.
func assertDisarmedLineFits(t *testing.T, width, height int) {
	t.Helper()
	screen := stoppingScreenAt(t, width, height)
	for i, row := range strings.Split(screen, "\n") {
		if w := visibleWidth(row); w > width {
			t.Errorf("row %d is %d cells wide, past the %d-cell screen: %q\n%s", i, w, width, row, screen)
		}
	}
	if !strings.Contains(flat(screen), finishingWithoutOptional) {
		t.Errorf("the stopping screen at %dx%d lacks the whole line %q:\n%s", width, height, finishingWithoutOptional, screen)
	}
}

// T1: at 80x26 the line wraps within the screen and keeps every word.
func TestEngineRun_DisarmedStoppingLineFitsAt80Columns(t *testing.T) {
	assertDisarmedLineFits(t, 80, 26)
}

// T2: at 50x26 the same.
func TestEngineRun_DisarmedStoppingLineFitsAt50Columns(t *testing.T) {
	assertDisarmedLineFits(t, 50, 26)
}

// T3 (guard): on a wide screen the sentence is present whole.
func TestEngineRun_DisarmedStoppingLineWholeAt200Columns(t *testing.T) {
	screen := stoppingScreenAt(t, 200, 40)
	if !strings.Contains(flat(screen), finishingWithoutOptional) {
		t.Errorf("the stopping screen at 200x40 lacks the whole line %q:\n%s", finishingWithoutOptional, screen)
	}
}
