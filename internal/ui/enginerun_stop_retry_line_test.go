package ui

// #293 (audit v0.4.3-c, S2): the "Orbit is already installed" stopping
// line must not outlive the run that earned it. A fresh install whose
// installer reached the optional phase and then died offers "Install
// again"; the retried run starts a new installer whose rollback is armed
// until that run's own phase=optional event. A Ctrl-C before then must
// say the installer is undoing what it started, not that Orbit is
// already installed — that line invites the second Ctrl-C that kills
// the rollback mid-way.
//
// Written from the audit brief alone, without the code. The stop
// machinery (stoppableStream) is in enginerun_stop_test.go;
// optionalWaitScript and clamavWaiting are in
// enginerun_stop_success_test.go; the two stopping lines are in
// enginerun_stop_disarmed_line_test.go; stopDied is in
// stopped_screen_test.go.

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// retriedInstall runs a fresh install on a live installer that reaches
// the optional phase and then ends by runEnd without saying how it
// ended, checks the stopped screen, presses Enter on "Install again"
// and hands the retried run a new live installer. It returns that
// second run, streaming.
func retriedInstall(t *testing.T, runEnd tea.Msg) engineRun {
	t.Helper()
	r := startedRun(t, "install", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: stoppableStream(t, optionalWaitScript), cleanup: func() error { return nil }})
	if r.state != runStreaming {
		t.Fatalf("setup: first run state = %v, want runStreaming", r.state)
	}
	r, _ = runUpdate(t, r, engineStreamMsg{msg: clamavWaiting()})
	r, _ = runUpdate(t, r, runEnd)
	if r.state != runFailed {
		t.Fatalf("setup: state after the installer died = %v, want runFailed:\n%s", r.state, runScreen(r))
	}
	stopped := runScreen(r)
	if !strings.Contains(flat(stopped), stopDied) {
		t.Fatalf("setup: the stopped screen lacks %q:\n%s", stopDied, stopped)
	}
	if got := caretLabel(stopped); got != "Install again" {
		t.Fatalf("setup: the caret is on %q, want %q:\n%s", got, "Install again", stopped)
	}

	r, _ = runUpdate(t, r, key(tea.KeyEnter))
	if r.state != runPreparing {
		t.Fatalf("state after Enter on Install again = %v, want runPreparing:\n%s", r.state, runScreen(r))
	}
	r, _ = runUpdate(t, r, engineReadyMsg{stream: stoppableStream(t, optionalWaitScript), cleanup: func() error { return nil }})
	if r.state != runStreaming {
		t.Fatalf("the retried run's state = %v, want runStreaming:\n%s", r.state, runScreen(r))
	}
	return r
}

// stopRetried feeds the retried run its events, presses Ctrl-C and
// returns the stopping screen as one line.
func stopRetried(t *testing.T, r engineRun, events ...any) string {
	t.Helper()
	for _, msg := range events {
		r, _ = runUpdate(t, r, engineStreamMsg{msg: msg})
	}
	r, _ = runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping:\n%s", r.state, runScreen(r))
	}
	return flat(runScreen(r))
}

// wantUndoing fails unless the screen is the install's stopping screen
// with the given rollback line and without the disarmed line: undoingLine
// before the run's commit, afterCommitInstallLine past it.
func wantUndoing(t *testing.T, screen, line string) {
	t.Helper()
	if !strings.Contains(screen, "stopping the install") {
		t.Errorf("the stopping screen lacks its title %q:\n%s", "stopping the install", screen)
	}
	if !strings.Contains(screen, line) {
		t.Errorf("the retried run's stopping screen lacks %q before its own optional phase:\n%s", line, screen)
	}
	if strings.Contains(screen, finishingWithoutOptional) {
		t.Errorf("the retried run's stopping screen claims Orbit is installed while its rollback is armed:\n%s", screen)
	}
}

var (
	diedExit137    = engineStreamMsg{msg: engine.DoneMsg{Err: errors.New("exit status 137")}}
	databaseHealth = ev("database", "database", "running", "database-health", "health")
)

// T1: run 1 died by a failing exit; run 2 stopped after an early event.
func TestEngineRun_RetriedInstallStoppedBeforeItsOptionalPhaseSaysItIsUndoing(t *testing.T) {
	wantUndoing(t, stopRetried(t, retriedInstall(t, diedExit137), databaseHealth), afterCommitInstallLine)
}

// T2: as T1, but run 1's stream closed early.
func TestEngineRun_RetriedInstallAfterAStreamEndSaysItIsUndoing(t *testing.T) {
	wantUndoing(t, stopRetried(t, retriedInstall(t, engineStreamEndedMsg{}), databaseHealth), afterCommitInstallLine)
}

// T3: as T1, but run 2 had no event at all before the Ctrl-C.
func TestEngineRun_RetriedInstallStoppedBeforeAnyEventSaysItIsUndoing(t *testing.T) {
	wantUndoing(t, stopRetried(t, retriedInstall(t, diedExit137)), undoingLine)
}

// T4 (guard): run 2's own phase=optional event still switches the line.
func TestEngineRun_RetriedInstallStoppedAfterItsOwnOptionalPhaseSaysOrbitIsInstalled(t *testing.T) {
	screen := stopRetried(t, retriedInstall(t, diedExit137), databaseHealth, clamavWaiting())

	if !strings.Contains(screen, finishingWithoutOptional) {
		t.Errorf("the stopping screen lacks %q after the retried run's own optional phase:\n%s", finishingWithoutOptional, screen)
	}
	if strings.Contains(screen, "undoing") {
		t.Errorf("the stopping screen says the install is being undone, but the retried run's rollback is disarmed:\n%s", screen)
	}
}
