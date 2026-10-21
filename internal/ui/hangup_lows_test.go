package ui

// #236: a hangup or terminate signal (SIGHUP, SIGTERM) forwarded to the
// app must not be lost in the two working steps that ignore Ctrl-C and
// Esc on purpose: Remove's stand-down and Repair's run of the safe
// repairs. The signal does not interrupt the step, but once the step has
// finished, succeeded or failed, the launcher quits with the usual
// cleanup instead of showing its next screen and waiting on a terminal
// nobody can see. A Ctrl-C key press in the same window keeps today's
// behaviour: ignored, and the launcher stays open afterwards.
//
// The seam this file names: a signal is told apart from a Ctrl-C key
// press by its own message type, exported from this package for
// cmd/orbit-launcher's forwardSignals to send:
//
//	// SignalQuitMsg asks the app to quit as Ctrl-C does, except that it
//	// is not ignored while a working step runs: it waits for the step
//	// to finish, then quits.
//	type SignalQuitMsg struct{}
//
// This file does not compile until SignalQuitMsg exists; it is kept
// apart so the failure is recorded in one place.

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// Idle, the signal quits as Ctrl-C does (the baseline for the rest).
func TestHangup_SignalOnTheIdleAppQuits(t *testing.T) {
	m := NewAppModelNoAnimation()
	if _, cmd := m.Update(SignalQuitMsg{}); !isQuit(cmd) {
		t.Fatal("a signal on the idle app did not quit")
	}
}

// --- Remove's stand-down --------------------------------------------------

var errStandDownFailed = errors.New("docker daemon not running")

func TestHangup_RemoveSignalDuringStandDownQuitsOnceItFinishes(t *testing.T) {
	for name, result := range map[string]error{"succeeds": nil, "fails": errStandDownFailed} {
		t.Run(name, func(t *testing.T) {
			m, run := removeStandingDown(t, func(context.Context, string) error { return result })

			m, cmd := updateAs(t, m, SignalQuitMsg{})
			if isQuit(cmd) {
				t.Fatal("the signal quit while the stand-down was still running")
			}
			if m.state != removeStateStandingDown {
				t.Fatalf("the signal changed the state to %v", m.state)
			}

			_, cmd = updateAs(t, m, run())
			if !isQuit(cmd) {
				t.Fatalf("the stand-down finished (%s) after a signal and the launcher did not quit", name)
			}
		})
	}
}

func TestHangup_RemoveSignalDoesNotCancelTheRunningStandDown(t *testing.T) {
	release := make(chan struct{})
	gotCtx := make(chan context.Context, 1)
	m, run := removeStandingDown(t, func(ctx context.Context, _ string) error {
		gotCtx <- ctx
		<-release
		return nil
	})
	result := make(chan tea.Msg, 1)
	go func() { result <- run() }()
	ctx := <-gotCtx

	m, cmd := updateAs(t, m, SignalQuitMsg{})
	if isQuit(cmd) || m.state != removeStateStandingDown {
		t.Fatalf("the signal acted during the wait: state = %v, quit = %v", m.state, isQuit(cmd))
	}
	select {
	case <-ctx.Done():
		t.Fatal("the signal cancelled the running stand-down")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	select {
	case msg := <-result:
		if _, cmd := updateAs(t, m, msg); !isQuit(cmd) {
			t.Fatal("the launcher did not quit when the stand-down finished after the signal")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the stand-down's result never arrived")
	}
}

func TestHangup_RemoveCtrlCDuringStandDownLeavesTheLauncherOpenAfterwards(t *testing.T) {
	for name, result := range map[string]error{"succeeds": nil, "fails": errStandDownFailed} {
		t.Run(name, func(t *testing.T) {
			m, run := removeStandingDown(t, func(context.Context, string) error { return result })

			m, cmd := updateAs(t, m, ctrlC())
			if isQuit(cmd) {
				t.Fatal("Ctrl-C quit during the stand-down")
			}
			m, cmd = updateAs(t, m, run())
			if isQuit(cmd) {
				t.Fatalf("a Ctrl-C key during the stand-down made the launcher quit once it finished (%s)", name)
			}
			if m.state != removeStateDone && m.state != removeStateFailed {
				t.Fatalf("state after the result = %v, want a finished screen", m.state)
			}
		})
	}
}

// Through the app, where the program's Send delivers the signal.
func TestHangup_AppSignalDuringStandDownQuitsOnceItFinishes(t *testing.T) {
	for name, result := range map[string]error{"succeeds": nil, "fails": errStandDownFailed} {
		t.Run(name, func(t *testing.T) {
			m, run := appStandingDown(t, func(context.Context, string) error { return result })

			m, cmd := updateAs(t, m, SignalQuitMsg{})
			if isQuit(cmd) {
				t.Fatal("the signal quit the app while the stand-down was still running")
			}
			if m.state != appStateRemove || m.remove.state != removeStateStandingDown {
				t.Fatalf("the signal moved the app to state %v (remove %v)", m.state, m.remove.state)
			}

			m, cmd = updateAs(t, m, run())
			if !isQuit(cmd) {
				t.Fatalf("the stand-down finished (%s) after a signal and the app did not quit", name)
			}
		})
	}
}

func TestHangup_AppCtrlCDuringStandDownLeavesTheAppOpenAfterwards(t *testing.T) {
	m, run := appStandingDown(t, standDownOK)
	m, cmd := updateAs(t, m, ctrlC())
	if isQuit(cmd) {
		t.Fatal("Ctrl-C quit the app during the stand-down")
	}
	m, cmd = updateAs(t, m, run())
	if isQuit(cmd) {
		t.Fatal("a Ctrl-C key during the stand-down made the app quit once it finished")
	}
	if m.state != appStateRemove || m.remove.state != removeStateDone {
		t.Fatalf("app state %v, remove state %v; want the removed screen", m.state, m.remove.state)
	}
}

// --- Repair's run of the safe repairs ------------------------------------

func TestHangup_RepairSignalDuringSafeRepairsQuitsOnceTheyFinish(t *testing.T) {
	cases := map[string]engine.DoneMsg{
		"succeeds": {ExitCode: 0},
		"fails":    {ExitCode: 1, Err: errors.New("exit status 1")},
	}
	for name, done := range cases {
		t.Run(name, func(t *testing.T) {
			m := sizedRepair(t)
			m.state = repairExecuting
			s := sleepingStream(t)
			m, _ = updateAs(t, m, repairReadyMsg{stream: s})

			m, cmd := updateAs(t, m, SignalQuitMsg{})
			if isQuit(cmd) {
				t.Fatal("the signal quit while the safe repairs were still running")
			}
			if m.Done || m.state != repairExecuting {
				t.Fatalf("the signal changed the flow: Done=%v state=%v", m.Done, m.state)
			}
			select {
			case msg := <-s.C:
				t.Fatalf("the signal interrupted the running repair: %#v", msg)
			case <-time.After(100 * time.Millisecond):
			}

			_, cmd = repairDone(t, m, done)
			if !isQuit(cmd) {
				t.Fatalf("the repairs finished (%s) after a signal and the launcher did not quit", name)
			}
		})
	}
}

func TestHangup_RepairCtrlCDuringSafeRepairsLeavesTheLauncherOpenAfterwards(t *testing.T) {
	cases := map[string]engine.DoneMsg{
		"succeeds": {ExitCode: 0},
		"fails":    {ExitCode: 1, Err: errors.New("exit status 1")},
	}
	for name, done := range cases {
		t.Run(name, func(t *testing.T) {
			m := sizedRepair(t)
			m.state = repairExecuting

			m, cmd := updateAs(t, m, ctrlC())
			if isQuit(cmd) {
				t.Fatal("Ctrl-C quit during the safe repairs")
			}
			m, cmd = repairDone(t, m, done)
			if isQuit(cmd) {
				t.Fatalf("a Ctrl-C key during the safe repairs made the launcher quit once they finished (%s)", name)
			}
			if m.Done {
				t.Fatalf("the flow ended after a Ctrl-C key and the repairs finishing (%s)", name)
			}
		})
	}
}
