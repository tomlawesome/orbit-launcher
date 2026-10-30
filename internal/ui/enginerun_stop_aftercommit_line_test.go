package ui

// Audit v0.4.3-d, G2: a fresh Install stopped after the installer's
// commit, but before any phase=optional event, cannot be undone in a
// few seconds: the installer has to finish the step it is on before its
// rollback runs. The stopping screen must not promise "a few seconds";
// it shows the owner's line instead (owner answer 3, 2026-10-28).
//
// Written from the audit brief alone, without the code. The stop
// machinery is in enginerun_stop_test.go; undoingLine and
// finishingWithoutOptional are in enginerun_stop_disarmed_line_test.go;
// the ready-made events are in stopped_screen_test.go,
// enginerun_update_rollforward_test.go and enginerun_stop_success_test.go.
// Timing and the grace length are covered by
// enginerun_install_composeup_stop_test.go, not here.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

// afterCommitInstallLine is the owner's stopping line for a fresh
// install stopped after the installer's commit and before the optional
// phase (owner answer 3, 2026-10-28).
const afterCommitInstallLine = "the installer has to finish the step it is on first, then it will undo what it started \u2014 this can take a few minutes"

// stoppingScreenAfter is an action run on a real engine stream, resized
// to width x height, fed events and then put in runStopping by Ctrl-C.
// It returns the screen at that size with ANSI styling stripped.
func stoppingScreenAfter(t *testing.T, action string, width, height int, events ...any) string {
	t.Helper()
	s := stoppableStream(t, stubbornScript)
	cleaned := 0
	r := streamingRunOn(t, action, s, &cleaned)
	r, _ = runUpdate(t, r, tea.WindowSizeMsg{Width: width, Height: height})
	for _, msg := range events {
		r, _ = runUpdate(t, r, engineStreamMsg{msg: msg})
	}
	r, _ = runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping:\n%s", r.state, runScreen(r))
	}
	return ansi.Strip(r.view(width, height))
}

// afterCommitSequences are fresh-install event sequences past the
// installer's commit and before any phase=optional event.
var afterCommitSequences = []struct {
	name   string
	events []any
}{
	{"compose up running", []any{hostDone, composeValidated, composeUpStarting}},
	{"image pull after the commit", []any{hostDone, composeValidated, imagesPulling}},
	{"required wait after compose up", []any{
		hostDone, composeValidated, composeUpStarting,
		ev("preparation", "compose", "completed", "service-start", "start"),
		ev("database", "database", "running", "database-health", "health"),
	}},
}

// T1: after the commit, the install's stopping screen gives the owner's
// line and promises no "few seconds".
func TestEngineRun_CtrlCOnAnInstallAfterTheCommitSaysItHasToFinishTheStepFirst(t *testing.T) {
	for _, tc := range afterCommitSequences {
		t.Run(tc.name, func(t *testing.T) {
			screen := flat(stoppingScreenAfter(t, "install", 80, 26, tc.events...))

			if !strings.Contains(screen, "stopping the install") {
				t.Errorf("the stopping screen lacks its title %q:\n%s", "stopping the install", screen)
			}
			if strings.Contains(screen, "a few seconds") {
				t.Errorf("the stopping screen promises %q after the installer's commit:\n%s", "a few seconds", screen)
			}
			if !strings.Contains(screen, afterCommitInstallLine) {
				t.Errorf("the stopping screen lacks the line %q:\n%s", afterCommitInstallLine, screen)
			}
			if strings.Contains(screen, finishingWithoutOptional) {
				t.Errorf("the stopping screen claims Orbit is installed before the optional phase:\n%s", screen)
			}
		})
	}
}

// T2: at 50 columns the after-commit stopping screen stays within the
// screen and keeps the owner's line whole.
func TestEngineRun_AfterCommitStoppingLineFitsAt50Columns(t *testing.T) {
	const width, height = 50, 26
	for _, tc := range afterCommitSequences {
		t.Run(tc.name, func(t *testing.T) {
			screen := stoppingScreenAfter(t, "install", width, height, tc.events...)
			for i, row := range strings.Split(screen, "\n") {
				if w := visibleWidth(row); w > width {
					t.Errorf("row %d is %d cells wide, past the %d-cell screen: %q\n%s", i, w, width, row, screen)
				}
			}
			if !strings.Contains(flat(screen), afterCommitInstallLine) {
				t.Errorf("the stopping screen at %dx%d lacks the whole line %q:\n%s", width, height, afterCommitInstallLine, screen)
			}
		})
	}
}

// T3 (guard): an install stopped before the commit is undone quickly:
// today's undoing line, then "a few seconds", and not the new line.
func TestEngineRun_CtrlCOnAnInstallBeforeTheCommitStillSaysAFewSeconds(t *testing.T) {
	screen := flat(stoppingScreenAfter(t, "install", 80, 26, hostDone, composeRunning))

	undo := strings.Index(screen, undoingLine)
	if undo < 0 {
		t.Fatalf("the stopping screen lacks %q before the commit:\n%s", undoingLine, screen)
	}
	if !strings.Contains(screen[undo+len(undoingLine):], "a few seconds") {
		t.Errorf("the stopping screen lacks %q after %q:\n%s", "a few seconds", undoingLine, screen)
	}
	if strings.Contains(screen, afterCommitInstallLine) {
		t.Errorf("the stopping screen uses the after-commit line before the commit:\n%s", screen)
	}
}

// T4 (guard): an install stopped in the optional phase keeps the
// disarmed line, not the new one, and is not being undone.
func TestEngineRun_CtrlCOnAnInstallInTheOptionalPhaseKeepsTheDisarmedLine(t *testing.T) {
	screen := flat(stoppingScreenAfter(t, "install", 80, 26, clamavWaiting()))

	if !strings.Contains(screen, finishingWithoutOptional) {
		t.Errorf("the stopping screen lacks the line %q:\n%s", finishingWithoutOptional, screen)
	}
	if strings.Contains(screen, afterCommitInstallLine) {
		t.Errorf("the stopping screen uses the after-commit line in the optional phase:\n%s", screen)
	}
	if strings.Contains(screen, "undoing") {
		t.Errorf("the stopping screen says the install is being undone, but the rollback is disarmed:\n%s", screen)
	}
}

// T5 (guard): an update stopped after its commit undoes nothing, so it
// shows none of the install's lines.
func TestEngineRun_CtrlCOnAnUpdateAfterTheCommitUsesNoneOfTheInstallLines(t *testing.T) {
	screen := flat(stoppingScreenAfter(t, "update", 80, 26, composeValidated, composeUpStarting))

	if !strings.Contains(screen, "stopping the update") {
		t.Errorf("the stopping screen lacks its title %q:\n%s", "stopping the update", screen)
	}
	for _, unwanted := range []string{undoingLine, "a few seconds", finishingWithoutOptional, afterCommitInstallLine} {
		if strings.Contains(screen, unwanted) {
			t.Errorf("an update past its commit shows %q:\n%s", unwanted, screen)
		}
	}
}

// T6 (guard): an update stopped before its commit is undone quickly.
func TestEngineRun_CtrlCOnAnUpdateBeforeTheCommitStillSaysAFewSeconds(t *testing.T) {
	screen := flat(stoppingScreenAfter(t, "update", 80, 26, hostDone, composeRunning))

	if !strings.Contains(screen, undoingLine) {
		t.Errorf("the stopping screen lacks %q before the commit:\n%s", undoingLine, screen)
	}
	if !strings.Contains(screen, "a few seconds") {
		t.Errorf("the stopping screen lacks %q before the commit:\n%s", "a few seconds", screen)
	}
}
