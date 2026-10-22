package ui

// A hangup or terminate signal (SignalQuitMsg) during Install or Update's
// graceful stop, and during Repair's credential rotation.
//
// A first signal begins the polite stop (runStopping, SIGTERM to the
// installer, bounded wait). A repeated signal while stopping does nothing:
// no quit, no kill. Only a second Ctrl-C key forces the kill.
//
// A signal while Repair's rotation runs closes the prompt's input as
// Ctrl-C and Esc do, neither kills the engine nor quits, and the launcher
// quits once the engine has ended.

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

func TestEngineRun_ARepeatedSignalWhileStoppingDoesNotForceTheKill(t *testing.T) {
	for _, action := range []string{"install", "update"} {
		t.Run(action, func(t *testing.T) {
			s := stoppableStream(t, stubbornScript)
			cleaned := 0
			r := streamingRunOn(t, action, s, &cleaned)

			r, _ = runUpdate(t, r, SignalQuitMsg{})
			if r.state != runStopping {
				t.Fatalf("state after the first signal = %v, want runStopping: the signal did not begin the stop", r.state)
			}

			r2, cmd := runUpdate(t, r, SignalQuitMsg{})
			if isQuit(cmd) {
				t.Fatal("a repeated signal quit the run while the installer was still undoing its work")
			}
			if r2.state != runStopping {
				t.Fatalf("state after a repeated signal = %v, want runStopping", r2.state)
			}
			if closed(s.Done(), 300*time.Millisecond) {
				t.Fatal("a repeated signal force-killed the installer")
			}
			if cleaned != 0 {
				t.Fatalf("staged script cleaned %d times before the installer had stopped, want 0", cleaned)
			}

			_, cmd = runUpdate(t, r2, ctrlC())
			if !isQuit(cmd) {
				t.Fatal("a Ctrl-C key after the signal no longer forces the quit")
			}
			if !closed(s.Done(), 3*time.Second) {
				t.Fatal("a Ctrl-C key after the signal did not kill the installer")
			}
		})
	}
}

func TestAppModel_ARepeatedSignalDuringAnInstallStopDoesNotForceTheKill(t *testing.T) {
	s := stoppableStream(t, stubbornScript)
	cleaned := 0
	run := streamingRunOn(t, "install", s, &cleaned)

	inst := NewInstallModel(t.TempDir(), "v9.9.9")
	inst.state = installStateRunning
	inst.run = run
	app := NewAppModel()
	app.state = appStateInstall
	app.install = inst

	model, cmd := app.Update(SignalQuitMsg{})
	app = model.(AppModel)
	if cmd == nil {
		t.Fatal("the first signal mid-install armed nothing: it must begin a graceful stop")
	}
	if app.install.run.state != runStopping {
		t.Fatalf("run state after the first signal = %v, want runStopping", app.install.run.state)
	}

	model, cmd = app.Update(SignalQuitMsg{})
	app = model.(AppModel)
	if isQuit(cmd) {
		t.Fatal("a repeated signal quit the app while the installer was still undoing its work")
	}
	if app.install.run.state != runStopping {
		t.Fatalf("run state after a repeated signal = %v, want runStopping", app.install.run.state)
	}
	if closed(s.Done(), 300*time.Millisecond) {
		t.Fatal("a repeated signal force-killed the installer")
	}
	if note := app.ExitNote(); note != "" {
		t.Fatalf("a repeated signal left a forced-stop exit note: %q", note)
	}
}

// A signal on a screen that is not mid-run quits like Ctrl-C does.
func TestSignal_OnInstallProfileAndUpdateConfirmScreensQuits(t *testing.T) {
	t.Run("install profile screen, direct", func(t *testing.T) {
		m := NewInstallModel(t.TempDir(), "v9.9.9")
		if _, cmd := updateAs(t, m, SignalQuitMsg{}); !isQuit(cmd) {
			t.Fatalf("a signal on Install's profile screen did not quit:\n%s", screen(m))
		}
	})
	t.Run("update confirm screen, direct", func(t *testing.T) {
		d := testDeployment()
		m := NewUpdateModel(d, "/opt/orbit", "v9.9.9")
		if _, cmd := updateAs(t, m, SignalQuitMsg{}); !isQuit(cmd) {
			t.Fatalf("a signal on Update's confirm screen did not quit:\n%s", screen(m))
		}
	})
	t.Run("install profile screen, through the app", func(t *testing.T) {
		app := NewAppModelNoAnimation()
		app.state = appStateInstall
		app.install = NewInstallModel(t.TempDir(), "v9.9.9")
		if _, cmd := updateAs(t, app, SignalQuitMsg{}); !isQuit(cmd) {
			t.Fatal("a signal on Install's profile screen through the app did not quit")
		}
	})
}

// --- Repair's credential rotation -----------------------------------------

// rotatingAtThePrompt is a Repair flow whose rotation is waiting at its
// first prompt.
func rotatingAtThePrompt(t *testing.T) (RepairModel, *engine.Stream, *recordingStdin) {
	t.Helper()
	m := sizedRepair(t)
	m.state = repairRotating
	s := sleepingStream(t)
	stdin := &recordingStdin{}
	m, _ = updateAs(t, m, repairRotateReadyMsg{stream: s, stdin: stdin})
	m = repairLines(t, m, "prompt field=action-word kind=typed-word required=true attempt=1")
	return m, s, stdin
}

func TestRepairModel_ASignalWhileRotatingClosesInputAndQuitsOnceTheEngineEnds(t *testing.T) {
	endings := map[string]func(*testing.T, RepairModel) (RepairModel, bool){
		"succeeds": func(t *testing.T, m RepairModel) (RepairModel, bool) {
			m = repairLines(t, m, "dangerous result=refused done=0 failed=0 reason=refused-by-operator")
			m, cmd := repairDone(t, m, engine.DoneMsg{ExitCode: 6})
			return m, isQuit(cmd)
		},
		"fails": func(t *testing.T, m RepairModel) (RepairModel, bool) {
			m, cmd := repairDone(t, m, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})
			return m, isQuit(cmd)
		},
	}
	for name, end := range endings {
		t.Run(name, func(t *testing.T) {
			m, s, stdin := rotatingAtThePrompt(t)

			m, cmd := updateAs(t, m, SignalQuitMsg{})
			if !stdin.closed {
				t.Fatal("the signal did not close the session's input")
			}
			if isQuit(cmd) {
				t.Fatal("the signal quit the launcher while the engine was still running")
			}
			if m.state != repairRotating {
				t.Fatalf("state = %v, want repairRotating until the engine exits", m.state)
			}
			select {
			case msg := <-s.C:
				t.Fatalf("the signal killed the rotation instead of leaving it to back out: %#v", msg)
			case <-time.After(100 * time.Millisecond):
			}

			if _, quit := end(t, m); !quit {
				t.Fatalf("the rotation ended (%s) after a signal and the launcher did not quit", name)
			}
		})
	}
}

func TestRepairModel_ASignalBeforeTheRotationHasStartedQuitsOnceItEnds(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairRotating

	m, cmd := updateAs(t, m, SignalQuitMsg{})
	if isQuit(cmd) {
		t.Fatal("the signal quit while the rotation was still starting")
	}

	stdin := &recordingStdin{}
	m, cmd = updateAs(t, m, repairRotateReadyMsg{stream: sleepingStream(t), stdin: stdin})
	if !stdin.closed {
		t.Fatal("the rotation started after a signal and its input was not closed")
	}
	if cmd == nil {
		t.Fatal("the stream reader was not started for the rotation that began after a signal")
	}
	if isQuit(cmd) {
		t.Fatal("the rotation's start quit the launcher while the engine was still running")
	}
	if m.state != repairRotating {
		t.Fatalf("state = %v, want repairRotating", m.state)
	}

	_, cmd = repairDone(t, m, engine.DoneMsg{ExitCode: 6})
	if !isQuit(cmd) {
		t.Fatal("the rotation ended after a signal and the launcher did not quit")
	}
}

// Guard: remembering a signal must not swallow the sky's tick chain or
// make a second signal act.
func TestRepairModel_ASignalWhileRotatingKeepsTheTicksAndIgnoresARepeat(t *testing.T) {
	m, _, _ := rotatingAtThePrompt(t)
	m, _ = updateAs(t, m, SignalQuitMsg{})

	if _, cmd := updateAs(t, m, tickMsg{}); cmd == nil {
		t.Fatal("after a signal the tick chain stopped")
	}

	state := m.state
	m, cmd := updateAs(t, m, SignalQuitMsg{})
	if isQuit(cmd) {
		t.Fatal("a repeated signal quit while the engine was still running")
	}
	if m.state != state {
		t.Fatalf("a repeated signal changed the state from %v to %v", state, m.state)
	}
}

// Guard: the safe repairs already wait for the engine, however many
// signals arrive.
func TestRepairModel_RepeatedSignalsDuringSafeRepairsQuitOnlyOnceTheyFinish(t *testing.T) {
	m := sizedRepair(t)
	m.state = repairExecuting
	s := sleepingStream(t)
	m, _ = updateAs(t, m, repairReadyMsg{stream: s})

	for i := 0; i < 2; i++ {
		var cmd tea.Cmd
		m, cmd = updateAs(t, m, SignalQuitMsg{})
		if isQuit(cmd) {
			t.Fatalf("signal %d quit while the safe repairs were running", i+1)
		}
		if m.state != repairExecuting {
			t.Fatalf("signal %d changed the state to %v", i+1, m.state)
		}
	}
	select {
	case msg := <-s.C:
		t.Fatalf("the signals interrupted the running repair: %#v", msg)
	case <-time.After(100 * time.Millisecond):
	}

	_, cmd := repairDone(t, m, engine.DoneMsg{ExitCode: 0})
	if !isQuit(cmd) {
		t.Fatal("the repairs finished after signals and the launcher did not quit")
	}
}
