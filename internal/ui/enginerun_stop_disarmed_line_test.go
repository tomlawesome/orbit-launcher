package ui

// #293 (audit v0.4.3-c, G4b): the stopping line after the rollback is
// disarmed. Orbit's installer emits phase=optional events only once its
// rollback is disarmed (docs/engine-events.md in Orbit), so a Ctrl-C on a
// fresh install after any phase=optional event undoes nothing: the
// installer ends the optional waits, reports completion and exits 0. The
// stopping line must say so, in the owner's ratified words, rather than
// claim the installer is undoing what it started. Before any
// phase=optional event today's line stays; an Update past its commit
// drops it (#294).
//
// Written from the audit brief alone, without the code. The stop
// machinery (stoppableStream) is in enginerun_stop_test.go;
// optionalWaitScript and the clamav events are in
// enginerun_stop_success_test.go; flat is in installer_rollback_test.go.

import (
	"strings"
	"testing"
)

// finishingWithoutOptional is the owner-ratified stopping line for a
// fresh install whose rollback is disarmed (owner answer 5b, 2026-10-24).
const finishingWithoutOptional = "Orbit is already installed; the installer is finishing without waiting for the optional services"

// undoingLine is today's stopping line, for a stop that undoes the run.
const undoingLine = "the installer is undoing what it started"

// stoppingAfterEvents is an action run on a real engine stream that has
// been fed events and then put in runStopping by Ctrl-C. It returns the
// screen at that point, words joined so a wrapped sentence reads as one.
func stoppingAfterEvents(t *testing.T, action string, events ...any) string {
	t.Helper()
	s := stoppableStream(t, optionalWaitScript)
	cleaned := 0
	r := streamingRunOn(t, action, s, &cleaned)
	for _, msg := range events {
		r, _ = runUpdate(t, r, engineStreamMsg{msg: msg})
	}
	r, _ = runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping:\n%s", r.state, runScreen(r))
	}
	return flat(runScreen(r))
}

// T1: after a phase=optional event, the install is not being undone.
func TestEngineRun_CtrlCAfterAnOptionalEventSaysOrbitIsAlreadyInstalled(t *testing.T) {
	screen := stoppingAfterEvents(t, "install", clamavWaiting())

	if !strings.Contains(screen, "stopping the install") {
		t.Errorf("the stopping screen lacks its title %q:\n%s", "stopping the install", screen)
	}
	if !strings.Contains(screen, finishingWithoutOptional) {
		t.Errorf("the stopping screen lacks the line %q:\n%s", finishingWithoutOptional, screen)
	}
	if strings.Contains(screen, "undoing") {
		t.Errorf("the stopping screen says the install is being undone, but the rollback is disarmed:\n%s", screen)
	}
}

// T2 (guard): before any phase=optional event the rollback is still
// armed. The database event is past the commit, so the line is the one
// for a stop that must finish its step first (#303).
func TestEngineRun_CtrlCBeforeAnyOptionalEventStillSaysTheInstallIsBeingUndone(t *testing.T) {
	screen := stoppingAfterEvents(t, "install", ev("database", "database", "running", "database-health", "health"))

	if !strings.Contains(screen, afterCommitInstallLine) {
		t.Errorf("the stopping screen lacks %q before the rollback is disarmed:\n%s", afterCommitInstallLine, screen)
	}
	if strings.Contains(screen, finishingWithoutOptional) {
		t.Errorf("the stopping screen claims Orbit is installed before the rollback is disarmed:\n%s", screen)
	}
}

// T3 (#294, inverted): an Update past its commit -- and every
// phase=optional event is past it -- undoes nothing when stopped, so its
// stopping screen drops today's undoing line and adds no line of its
// own; the fresh-install line is not used either.
func TestEngineRun_CtrlCOnAnUpdateAfterAnOptionalEventDropsTheUndoingLine(t *testing.T) {
	screen := stoppingAfterEvents(t, "update", clamavWaiting())

	if !strings.Contains(screen, "stopping the update") {
		t.Errorf("the stopping screen lacks its title %q:\n%s", "stopping the update", screen)
	}
	if strings.Contains(screen, undoingLine) {
		t.Errorf("an update past its commit is shown as %q:\n%s", undoingLine, screen)
	}
	if strings.Contains(screen, finishingWithoutOptional) {
		t.Errorf("an update's stopping screen uses the fresh-install line:\n%s", screen)
	}
}
