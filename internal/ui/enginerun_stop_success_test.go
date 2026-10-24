package ui

// #293 (audit v0.4.3-c, G4): a Ctrl-C that lands after the installer has
// disarmed its rollback -- a fresh install waiting only on optional
// services -- does not undo the install. Orbit's contract
// (docs/engine-events.md): the interrupt ends the optional waits, each
// component not yet ready ends `waiting reason=optional-status
// action=continue`, and the run ends with the normal `phase=complete ...
// state=completed` and exit status 0. The launcher must then land on the
// success screen with the address, not quit as though the install had
// been undone.
//
// Written from the audit brief alone, without the code. The stop
// machinery (stoppableStream, streamingRunOn, settleStop, closed) is in
// enginerun_stop_test.go; the app driver installApp is in
// installer_rollback_test.go.

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// stopSuccessURL is what the detect seam reports for the deployment.
const stopSuccessURL = "http://orbit.example"

// scannerStillStarting is the installer's completion line for a scanner
// whose wait the interrupt ended.
const scannerStillStarting = "Scanner: still starting; Orbit runs without it until it is ready."

// optionalWaitScript is an installer past its rollback, waiting on an
// optional service. It has no TERM trap of its own: the test feeds the
// stream's messages itself, so the process only has to go away.
const optionalWaitScript = `
sleep 60 &
until read -r c 2>/dev/null </proc/$!/comm && [ "$c" = sleep ]; do :; done
echo "phase=optional component=clamav state=starting reason=optional-status action=health elapsed=1s"
wait
`

func clamavStarting() engine.EventMsg {
	return ev("optional", "clamav", "starting", "optional-status", "health")
}

func clamavWaiting() engine.EventMsg {
	return ev("optional", "clamav", "waiting", "optional-status", "continue")
}

func installCompleted() engine.EventMsg {
	return ev("complete", "installer", "completed", "deployment-ready", "complete")
}

// stoppingAfterDisarm is an install run on a real engine stream, waiting
// on clamav, that Ctrl-C has just put in runStopping. cleaned counts the
// staged-script cleanup's calls.
func stoppingAfterDisarm(t *testing.T, script string) (engineRun, *engine.Stream, *int) {
	t.Helper()
	s := stoppableStream(t, script)
	cleaned := new(int)
	r := startedRun(t, "install", engineRunSeams{detect: fakeDetect(stopSuccessURL)})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: s, cleanup: func() error { *cleaned++; return nil }})
	if r.state != runStreaming {
		t.Fatalf("state = %v, want runStreaming", r.state)
	}
	r, _ = runUpdate(t, r, engineStreamMsg{msg: clamavStarting()})
	r, _ = runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping", r.state)
	}
	return r, s, cleaned
}

// feedNoQuit feeds msgs to the stopping run as stream messages and fails
// if any of them quits it: the installer is still reporting.
func feedNoQuit(t *testing.T, r engineRun, msgs ...any) engineRun {
	t.Helper()
	for _, msg := range msgs {
		var cmd tea.Cmd
		r, cmd = runUpdate(t, r, engineStreamMsg{msg: msg})
		if isQuit(cmd) {
			t.Fatalf("the run quit on %#v, before the engine's DoneMsg", msg)
		}
	}
	return r
}

// assertStopEndedInSuccess checks the outcome of a stop whose installer
// completed: the success outcome, not a quit and not a failure.
func assertStopEndedInSuccess(t *testing.T, r engineRun, cmd tea.Cmd, cleaned int) {
	t.Helper()
	if isQuit(cmd) {
		t.Fatal("the DoneMsg of an install that completed after Ctrl+C quit the launcher: the person never sees the address or the claim link")
	}
	if !r.Done || !r.Succeeded {
		t.Fatalf("Done=%v Succeeded=%v, want the success outcome (state %v):\n%s", r.Done, r.Succeeded, r.state, runScreen(r))
	}
	if r.state == runFailed || r.state == runStopping {
		t.Errorf("state = %v, want the success state", r.state)
	}
	screen := runScreen(r)
	for _, bad := range []string{"Install stopped", "stopping the install", "undoing what it started"} {
		if strings.Contains(screen, bad) {
			t.Errorf("a completed install still shows %q:\n%s", bad, screen)
		}
	}
	if r.URL != stopSuccessURL {
		t.Errorf("URL = %q, want %q from the deployment", r.URL, stopSuccessURL)
	}
	found := false
	for _, note := range r.ServiceNotes {
		if strings.Contains(note, scannerStillStarting) {
			found = true
		}
	}
	if !found {
		t.Errorf("ServiceNotes = %q, want the installer's line %q", r.ServiceNotes, scannerStillStarting)
	}
	if note := outcomeOf(r).ExitNote; note != "" {
		t.Errorf("a completed install left an exit note: %q", note)
	}
	if cleaned != 1 {
		t.Errorf("staged script cleaned %d times, want once", cleaned)
	}
}

// 1. The success event, a completion line, then DoneMsg{ExitCode: 0}.
func TestEngineRun_CtrlCAfterTheRollbackIsDisarmedEndsOnTheSuccessOutcome(t *testing.T) {
	r, _, cleaned := stoppingAfterDisarm(t, optionalWaitScript)

	r = feedNoQuit(t, r,
		clamavWaiting(),
		installCompleted(),
		engine.RawLineMsg{Text: ""},
		engine.RawLineMsg{Text: "Orbit is ready."},
		engine.RawLineMsg{Text: scannerStillStarting},
	)
	r, cmd := runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 0, Err: nil}})

	assertStopEndedInSuccess(t, r, cmd, *cleaned)
}

// 2. The process exit (unforced) reaches the run after the success event
// but before the DoneMsg -- the real ordering, since the stream closes
// Done() first. The exit alone must not quit; the DoneMsg decides.
func TestEngineRun_CtrlCAfterTheRollbackIsDisarmedWaitsPastTheExitForTheDoneMsg(t *testing.T) {
	r, s, cleaned := stoppingAfterDisarm(t, optionalWaitScript)
	r = feedNoQuit(t, r, clamavWaiting(), installCompleted())
	if !closed(s.Done(), 5*time.Second) {
		t.Fatal("the installer did not exit after Ctrl+C; the test premise is broken")
	}

	r, cmd := runUpdate(t, r, engineStoppedMsg{forced: false})

	if isQuit(cmd) {
		t.Fatal("the process exit quit the run after the success event, before its DoneMsg: the completed install is lost")
	}
	if r.Done {
		t.Fatal("the run was over on the process exit, before the DoneMsg")
	}
	if screen := runScreen(r); !strings.Contains(screen, "stopping the install") {
		t.Errorf("between the exit and the DoneMsg the screen no longer says it is stopping:\n%s", screen)
	}
	if *cleaned > 1 {
		t.Errorf("staged script cleaned %d times by the exit, want at most once", *cleaned)
	}

	r = feedNoQuit(t, r, engine.RawLineMsg{Text: scannerStillStarting})
	r, cmd = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 0}})

	assertStopEndedInSuccess(t, r, cmd, *cleaned)
}

// 3. Unchanged: a stop with no success event, or one the engine did not
// finish cleanly, quits without the failure screen.
func TestEngineRun_AStopThatDidNotCompleteStillQuits(t *testing.T) {
	terminated := engine.DoneMsg{Err: errors.New("signal: terminated"), ExitCode: 143, StderrTail: []string{"rolling back"}}
	for _, c := range []struct {
		name   string
		before []any
		done   engine.DoneMsg
	}{
		{"no success event, exit 0", nil, engine.DoneMsg{ExitCode: 0}},
		{"no success event, terminated", nil, terminated},
		{"success event, then terminated", []any{clamavWaiting(), installCompleted()}, terminated},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, _, cleaned := stoppingAfterDisarm(t, optionalWaitScript)
			r = feedNoQuit(t, r, c.before...)

			r, cmd := runUpdate(t, r, engineStreamMsg{msg: c.done})

			if !isQuit(cmd) {
				t.Fatal("the DoneMsg of a stop that did not complete did not lead to tea.Quit")
			}
			if r.state == runFailed {
				t.Errorf("a stop the operator asked for ended on the failure screen:\n%s", runScreen(r))
			}
			if *cleaned != 1 {
				t.Errorf("staged script cleaned %d times, want once", *cleaned)
			}
		})
	}
}

// 3. Unchanged: a forced exit quits at once and kills the engine, even
// after the success event.
func TestEngineRun_AForcedExitWhileStoppingQuitsAtOnceAndKills(t *testing.T) {
	for _, c := range []struct {
		name   string
		before []any
	}{
		{"no success event", nil},
		{"after the success event", []any{clamavWaiting(), installCompleted()}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, s, cleaned := stoppingAfterDisarm(t, stubbornScript)
			r = feedNoQuit(t, r, c.before...)

			_, cmd := runUpdate(t, r, engineStoppedMsg{forced: true})

			if !isQuit(cmd) {
				t.Fatal("a forced exit did not quit at once")
			}
			if !closed(s.Done(), 3*time.Second) {
				t.Fatal("the installer was still running after a forced exit: it must be killed")
			}
			if *cleaned != 1 {
				t.Errorf("staged script cleaned %d times, want once", *cleaned)
			}
		})
	}
}

// 3. Unchanged: a second Ctrl-C while the run waits for the DoneMsg
// after an unforced exit quits at once.
func TestEngineRun_ASecondCtrlCWhileWaitingForTheDoneMsgQuitsAtOnce(t *testing.T) {
	for _, c := range []struct {
		name   string
		before []any
	}{
		{"no success event", nil},
		{"after the success event", []any{clamavWaiting(), installCompleted()}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, s, cleaned := stoppingAfterDisarm(t, optionalWaitScript)
			r = feedNoQuit(t, r, c.before...)
			if !closed(s.Done(), 5*time.Second) {
				t.Fatal("the installer did not exit after Ctrl+C; the test premise is broken")
			}
			r, _ = runUpdate(t, r, engineStoppedMsg{forced: false})

			r, cmd := runUpdate(t, r, ctrlC())

			if !isQuit(cmd) {
				t.Fatal("a second Ctrl+C while waiting for the DoneMsg did not quit at once")
			}
			if note := outcomeOf(r).ExitNote; note != "" {
				t.Errorf("the installer had already gone, so the stop was clean, but it left an exit note: %q", note)
			}
			if *cleaned != 1 {
				t.Errorf("staged script cleaned %d times, want once", *cleaned)
			}
		})
	}
}

// --- end to end ------------------------------------------------------------

// disarmedInstallScript is a fresh install past its rollback, waiting on
// the scanner. TERM ends the wait the way Orbit's installer does: the
// scanner's last event says it is still waiting, the success event and
// the completion text follow, and the installer exits 0.
const disarmedInstallScript = `#!/usr/bin/env bash
` + startEvents + `echo "phase=application component=application state=healthy reason=application-health action=health elapsed=2s"
finish() {
  kill "$sleeper" 2>/dev/null
  echo "phase=optional component=clamav state=waiting reason=optional-status action=continue elapsed=9s"
  echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=9s"
  echo
  echo "Orbit is ready."
  echo "Public URL: https://mail.example.com"
  echo "Scanner: still starting; Orbit runs without it until it is ready."
  exit 0
}
trap finish TERM
sleep 60 &
sleeper=$!
until read -r c 2>/dev/null </proc/$sleeper/comm && [ "$c" = sleep ]; do :; done
echo "phase=optional component=clamav state=starting reason=optional-status action=health elapsed=3s"
wait
exit 1
`

// quitSpy is the app with a note of any tea.Quit that drive feeds back
// to it, so a test can tell the app quitting from the app settling.
type quitSpy struct {
	AppModel
	quit *bool
}

func (q quitSpy) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.QuitMsg); ok {
		*q.quit = true
	}
	m, cmd := q.AppModel.Update(msg)
	q.AppModel = m.(AppModel)
	return q, cmd
}

func TestInstall_CtrlCAfterTheRollbackIsDisarmedLandsOnTheSuccessScreen(t *testing.T) {
	m, queue := installApp(t, disarmedInstallScript, 80, 40)
	if s := screen(m); m.state != appStateInstall || !strings.Contains(s, "clamav") && !strings.Contains(s, "Install") {
		t.Fatalf("the fake installer is not mid-run (state %v); the test premise is broken:\n%s", m.state, s)
	}

	quit := false
	spy, cmd := updateAs(t, quitSpy{AppModel: m, quit: &quit}, ctrlC())
	m = drive(t, spy, cmd, queue).AppModel

	if quit {
		t.Error("the launcher quit after Ctrl+C although the install completed: the person never sees the address")
	}
	if m.state != appStateSuccess {
		t.Fatalf("after Ctrl+C on an install that completed, state = %v, want the success screen:\n%s", m.state, screen(m))
	}
	s := screen(m)
	if !strings.Contains(s, "https://mail.example.com") {
		t.Errorf("the success screen lacks the address:\n%s", s)
	}
	if !strings.Contains(flat(s), scannerStillStarting) {
		t.Errorf("the success screen lacks the installer's line %q:\n%s", scannerStillStarting, s)
	}
	if strings.Contains(s, "undoing what it started") {
		t.Errorf("the success screen still says the install is being undone:\n%s", s)
	}
}
