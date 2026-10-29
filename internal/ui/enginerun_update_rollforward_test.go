package ui

// #294: an Update stopped after the installer's commit rolls forward.
// Orbit's contract (docs/engine-events.md in Orbit, "install.sh after
// the commit"): the first event after the commit is phase=compose
// state=completed reason=compose-validation. An interrupt then undoes
// nothing; the run ends with one terminal state=failed reason=failure
// event naming the step it stopped in, action=retry while no container
// has been started (image pulls) and action=repair from compose up on
// (phase=preparation component=compose state=starting), exit status
// 130/143/129. In the optional phase it ends complete, exit 0.
//
// The launcher must then land on the existing stopped screens (no new
// wording), drop "the installer is undoing what it started" from the
// stopping screen, give the installer at least Orbit's 300 s compose-up
// bound before killing it (a second Ctrl-C still kills at once), not
// call a forced update stop "half restored", and on a hang-up or
// SIGTERM to the launcher quit without signalling the installer.
//
// Written from the issue alone, without the code. The stop machinery
// (stoppableStream, streamingRunOn, settleStop, closed,
// shortenStopGrace, stubbornScript) is in enginerun_stop_test.go;
// optionalWaitScript, clamavWaiting and installCompleted are in
// enginerun_stop_success_test.go; undoingLine and
// finishingWithoutOptional are in enginerun_stop_disarmed_line_test.go;
// the stopped-screen sentences and synthetic events (hostDone,
// composeRunning) are in stopped_screen_test.go.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

var (
	// composeValidated is the first event after the commit.
	composeValidated = ev("compose", "compose", "completed", "compose-validation", "validate")
	// imagesPulling is an image pull after the commit, before compose up.
	imagesPulling = ev("preparation", "image", "running", "image-pull", "pull")
	// composeUpStarting is the compose-up start.
	composeUpStarting = ev("preparation", "compose", "starting", "service-start", "start")
	// pullStopped and composeUpStopped are the terminal events of a run
	// interrupted in those steps.
	pullStopped      = ev("preparation", "image", "failed", "failure", "retry")
	composeUpStopped = ev("preparation", "compose", "failed", "failure", "repair")
)

// interrupted is the engine's exit after an interrupt, by exit status.
func interrupted(code int) engine.DoneMsg {
	return engine.DoneMsg{Err: fmt.Errorf("exit status %d", code), ExitCode: code}
}

// stoppingUpdate is an Update run on a real engine stream (script), fed
// events and then put in runStopping by Ctrl-C.
func stoppingUpdate(t *testing.T, script string, events ...any) (engineRun, *engine.Stream, *int) {
	t.Helper()
	r, s, cleaned, _ := stoppingUpdateArmed(t, script, events...)
	return r, s, cleaned
}

// stoppingUpdateArmed is stoppingUpdate, also returning the command the
// Ctrl-C armed (the bounded wait), for settleStop to run.
func stoppingUpdateArmed(t *testing.T, script string, events ...any) (engineRun, *engine.Stream, *int, tea.Cmd) {
	t.Helper()
	s := stoppableStream(t, script)
	cleaned := new(int)
	r := startedRun(t, "update", engineRunSeams{detect: fakeDetect(stopSuccessURL)})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: s, cleanup: func() error { *cleaned++; return nil }})
	if r.state != runStreaming {
		t.Fatalf("state = %v, want runStreaming", r.state)
	}
	for _, msg := range events {
		r, _ = runUpdate(t, r, engineStreamMsg{msg: msg})
	}
	r, cmd := runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping:\n%s", r.state, runScreen(r))
	}
	return r, s, cleaned, cmd
}

// endStop delivers the end of an interrupted run in the real order: the
// installer's remaining events, the process exit, then its DoneMsg. Only
// the DoneMsg may decide the outcome.
func endStop(t *testing.T, r engineRun, s *engine.Stream, done engine.DoneMsg, events ...any) (engineRun, bool) {
	t.Helper()
	r = feedNoQuit(t, r, events...)
	if !closed(s.Done(), 5*time.Second) {
		t.Fatal("the installer did not exit after Ctrl+C; the test premise is broken")
	}
	r, cmd := runUpdate(t, r, engineStoppedMsg{forced: false})
	if isQuit(cmd) {
		t.Fatal("the process exit quit the run before its DoneMsg")
	}
	r, cmd = runUpdate(t, r, engineStreamMsg{msg: done})
	return r, isQuit(cmd)
}

// wantStoppedScreen fails unless r is on the stopped screen with
// sentence and the caret on caret.
func wantStoppedScreen(t *testing.T, r engineRun, quit bool, sentence, caret string) {
	t.Helper()
	if quit {
		t.Fatalf("the launcher quit, but the update rolled forward and stopped in a step: the person must see what happened:\n%s", runScreen(r))
	}
	if r.state != runFailed {
		t.Fatalf("state = %v, want the stopped screen (runFailed):\n%s", r.state, runScreen(r))
	}
	s := runScreen(r)
	if !strings.Contains(flat(s), sentence) {
		t.Errorf("the stopped screen lacks %q:\n%s", sentence, s)
	}
	if got := caretLabel(s); got != caret {
		t.Errorf("the caret is on %q, want %q:\n%s", got, caret, s)
	}
	for _, bad := range []string{"stopping the update", undoingLine, "half restored"} {
		if strings.Contains(flat(s), bad) {
			t.Errorf("the stopped screen still shows %q:\n%s", bad, s)
		}
	}
}

// 1 (guard, today's behaviour): a stop before the commit, with no
// terminal event, quits cleanly.
func TestEngineRun_UpdateStoppedBeforeTheCommitWithNoTerminalEventQuits(t *testing.T) {
	for _, code := range []int{130, 143} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			r, s, cleaned := stoppingUpdate(t, optionalWaitScript, hostDone, composeRunning)

			r, quit := endStop(t, r, s, interrupted(code))

			if !quit {
				t.Fatalf("a stop before the commit did not quit (state %v):\n%s", r.state, runScreen(r))
			}
			if r.state == runFailed {
				t.Errorf("a stop before the commit ended on the stopped screen:\n%s", runScreen(r))
			}
			if *cleaned != 1 {
				t.Errorf("staged script cleaned %d times, want once", *cleaned)
			}
		})
	}
}

// 1b (guard): after the commit, but with no terminal event seen, the
// stop still quits cleanly.
func TestEngineRun_UpdateStoppedAfterTheCommitWithNoTerminalEventQuits(t *testing.T) {
	r, s, _ := stoppingUpdate(t, optionalWaitScript, hostDone, composeValidated)

	r, quit := endStop(t, r, s, interrupted(130))

	if !quit {
		t.Fatalf("a stop with no terminal event did not quit (state %v):\n%s", r.state, runScreen(r))
	}
	if r.state == runFailed {
		t.Errorf("a stop with no terminal event ended on the stopped screen:\n%s", runScreen(r))
	}
}

// 2: stopped in the image pulls after the commit: retry, not changed.
func TestEngineRun_UpdateStoppedAfterTheCommitWithRetryShowsServicesNotChanged(t *testing.T) {
	for _, code := range []int{130, 143, 129} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			r, s, cleaned := stoppingUpdate(t, optionalWaitScript, hostDone, composeValidated, imagesPulling)

			r, quit := endStop(t, r, s, interrupted(code), pullStopped)

			wantStoppedScreen(t, r, quit, stopUpdateNotChanged, "Update again")
			if *cleaned != 1 {
				t.Errorf("staged script cleaned %d times, want once", *cleaned)
			}
		})
	}
}

// 3: stopped after compose up began: repair, changed.
func TestEngineRun_UpdateStoppedAfterComposeUpWithRepairShowsServicesChanged(t *testing.T) {
	for _, code := range []int{130, 143, 129} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			r, s, cleaned := stoppingUpdate(t, optionalWaitScript, hostDone, composeValidated, imagesPulling, composeUpStarting)

			r, quit := endStop(t, r, s, interrupted(code), composeUpStopped)

			wantStoppedScreen(t, r, quit, stopUpdateChanged, "Open Repair")
			if *cleaned != 1 {
				t.Errorf("staged script cleaned %d times, want once", *cleaned)
			}
		})
	}
}

// 4: stopped in the optional phase: the update completes, exit 0.
func TestEngineRun_UpdateStoppedInTheOptionalPhaseEndsOnSuccess(t *testing.T) {
	r, s, cleaned := stoppingUpdate(t, optionalWaitScript, hostDone, composeValidated, composeUpStarting, clamavStarting())

	r = feedNoQuit(t, r, clamavWaiting(), installCompleted(), engine.RawLineMsg{Text: scannerStillStarting})
	if !closed(s.Done(), 5*time.Second) {
		t.Fatal("the installer did not exit after Ctrl+C; the test premise is broken")
	}
	r, cmd := runUpdate(t, r, engineStoppedMsg{forced: false})
	if isQuit(cmd) {
		t.Fatal("the process exit quit the run before its DoneMsg: the completed update is lost")
	}
	r, cmd = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 0}})

	if isQuit(cmd) {
		t.Fatal("an update that completed after Ctrl+C quit the launcher instead of showing success")
	}
	if !r.Done || !r.Succeeded {
		t.Fatalf("Done=%v Succeeded=%v, want the success outcome (state %v):\n%s", r.Done, r.Succeeded, r.state, runScreen(r))
	}
	if r.state == runFailed || r.state == runStopping {
		t.Errorf("state = %v, want the success state", r.state)
	}
	if note := outcomeOf(r).ExitNote; note != "" {
		t.Errorf("a completed update left an exit note: %q", note)
	}
	if *cleaned != 1 {
		t.Errorf("staged script cleaned %d times, want once", *cleaned)
	}
}

// 5: the stopping screen's detail line, by where the update stopped.
func TestEngineRun_UpdateStoppingScreenDropsTheUndoingLineAfterTheCommit(t *testing.T) {
	for _, c := range []struct {
		name    string
		events  []any
		undoing bool
	}{
		{"before any event", nil, true},
		{"after host", []any{hostDone}, true},
		{"while compose validates", []any{hostDone, composeRunning}, true},
		{"at the commit", []any{hostDone, composeValidated}, false},
		{"in the image pulls", []any{hostDone, composeValidated, imagesPulling}, false},
		{"after compose up began", []any{hostDone, composeValidated, composeUpStarting}, false},
		{"in the database phase", []any{hostDone, composeValidated, databaseHealth}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			screen := stoppingAfterEvents(t, "update", c.events...)

			if !strings.Contains(screen, "stopping the update") {
				t.Errorf("the stopping screen lacks its title %q:\n%s", "stopping the update", screen)
			}
			if got := strings.Contains(screen, undoingLine); got != c.undoing {
				t.Errorf("the stopping screen shows %q = %v, want %v:\n%s", undoingLine, got, c.undoing, screen)
			}
			if !c.undoing && strings.Contains(screen, "undoing") {
				t.Errorf("an update past its commit is described as being undone:\n%s", screen)
			}
			if strings.Contains(screen, finishingWithoutOptional) {
				t.Errorf("an update's stopping screen uses the fresh-install line:\n%s", screen)
			}
		})
	}
}

// 5b (guard): a fresh install still says it will undo what it started
// on events that are past an Update's commit but before its own
// optional phase, in the after-commit wording (#303).
func TestEngineRun_InstallStoppingScreenKeepsTheUndoingLineAfterComposeValidation(t *testing.T) {
	screen := stoppingAfterEvents(t, "install", hostDone, composeValidated, composeUpStarting)

	if !strings.Contains(screen, afterCommitInstallLine) {
		t.Errorf("an install's stopping screen lacks %q before its optional phase:\n%s", afterCommitInstallLine, screen)
	}
}

// 6: after the commit the installer is given Orbit's compose-up bound:
// a grace that ends before the commit is not used, so the stop is not
// forced within 25 times the short grace. Before the commit the short
// grace still forces it. A second Ctrl-C still kills at once.
func TestEngineRun_UpdateStopGraceIsLongerAfterTheCommit(t *testing.T) {
	t.Run("before the commit", func(t *testing.T) {
		shortenStopGrace(t, 200*time.Millisecond)
		r, _, _, cmd := stoppingUpdateArmed(t, stubbornScript, hostDone, composeRunning)

		_, stopped, quit := settleStop(t, r, cmd)

		if stopped == nil || !stopped.forced || !quit {
			t.Fatalf("before the commit the short grace did not force the stop (stopped %+v, quit %v)", stopped, quit)
		}
	})
	t.Run("after the commit", func(t *testing.T) {
		// The short grace is 40 ms and the wait 25 times that, the same
		// ratio as 200 ms to five seconds: a stop that used the short
		// grace would be forced in the first 40 ms of it.
		shortenStopGrace(t, 40*time.Millisecond)
		r, s, _, cmd := stoppingUpdateArmed(t, stubbornScript, hostDone, composeValidated, composeUpStarting)

		r, stopped, quit := settleStopWithin(t, r, cmd, 25*stopGrace)

		if quit {
			t.Fatal("after the commit the launcher gave up on the installer within 25 times the short grace; it must wait out Orbit's 300 s compose-up bound")
		}
		if stopped != nil && stopped.forced {
			t.Fatal("after the commit the stop was forced within 25 times the short grace")
		}
		if closed(s.Done(), 100*time.Millisecond) {
			t.Fatal("after the commit the installer was killed within 25 times the short grace")
		}
		if r.state != runStopping {
			t.Fatalf("state = %v, want runStopping while the installer finishes:\n%s", r.state, runScreen(r))
		}

		started := time.Now()
		r, cmd = runUpdate(t, r, ctrlC())
		_, _, quit = settleStop(t, r, cmd)

		if !quit {
			t.Fatal("a second Ctrl+C after the commit did not quit")
		}
		if time.Since(started) > 3*time.Second {
			t.Errorf("the second Ctrl+C took %v to quit; it must not wait out the grace", time.Since(started))
		}
		if !closed(s.Done(), 3*time.Second) {
			t.Fatal("the installer was still running after the second Ctrl+C: it must be killed")
		}
	})
}

// 6b: the Update's forced-stop note does not call the deployment "half
// restored": an update past its commit undoes nothing, so there is
// nothing half restored. The install's note is unchanged.
func TestEngineRun_AForcedUpdateStopNoteDoesNotSayHalfRestored(t *testing.T) {
	for _, c := range []struct {
		name   string
		events []any
	}{
		{"before the commit", []any{hostDone, composeRunning}},
		{"after the commit", []any{hostDone, composeValidated, composeUpStarting}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, s, _ := stoppingUpdate(t, stubbornScript, c.events...)

			r, cmd := runUpdate(t, r, ctrlC())
			r, _, quit := settleStop(t, r, cmd)

			if !quit {
				t.Fatal("the second Ctrl+C did not quit")
			}
			if !closed(s.Done(), 3*time.Second) {
				t.Fatal("the installer was still running after the second Ctrl+C; the test premise is broken")
			}
			note := outcomeOf(r).ExitNote
			if strings.Contains(note, "half restored") {
				t.Errorf("the update's forced-stop note says %q: %q", "half restored", note)
			}
		})
	}
}

// 6c (guard): the install's forced-stop note still says half restored.
func TestEngineRun_AForcedInstallStopNoteStillSaysHalfRestored(t *testing.T) {
	s := stoppableStream(t, stubbornScript)
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)
	r, _ = runUpdate(t, r, ctrlC())

	r, cmd := runUpdate(t, r, ctrlC())
	r, _, _ = settleStop(t, r, cmd)

	if note := outcomeOf(r).ExitNote; !strings.Contains(note, "half restored") {
		t.Errorf("the install's forced-stop note lost %q: %q", "half restored", note)
	}
}

// signalMarkingScript is an installer that records any TERM, HUP or INT
// it receives in marker, and keeps running whatever it is sent.
func signalMarkingScript(marker string) string {
	return fmt.Sprintf(`
trap 'echo signalled >> "%s"' TERM HUP INT
sleep 60 &
until read -r c 2>/dev/null </proc/$!/comm && [ "$c" = sleep ]; do :; done
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=1s"
while :; do wait; done
`, marker)
}

// 7: a hang-up or SIGTERM to the launcher (SignalQuitMsg) on an Update
// past its commit leaves the detached installer to finish: no signal is
// sent to it, it is not killed, and the launcher quits.
func TestEngineRun_ASignalOnAnUpdateAfterTheCommitQuitsWithoutSignallingTheInstaller(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "signalled")
	s := stoppableStream(t, signalMarkingScript(marker))
	cleaned := 0
	r := streamingRunOn(t, "update", s, &cleaned)
	for _, msg := range []any{hostDone, composeValidated, composeUpStarting} {
		r, _ = runUpdate(t, r, engineStreamMsg{msg: msg})
	}

	r, cmd := runUpdate(t, r, SignalQuitMsg{})
	if r.state == runStopping {
		t.Errorf("the signal began a stop of an update past its commit; it must leave the installer alone:\n%s", runScreen(r))
	}
	_, stopped, quit := settleStop(t, r, cmd)

	if !quit {
		t.Fatal("the launcher did not quit on the signal")
	}
	if stopped != nil && stopped.forced {
		t.Error("the signal forced a stop of the installer")
	}
	if closed(s.Done(), 300*time.Millisecond) {
		t.Fatal("the installer stopped running after the signal: it must be left to finish")
	}
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the installer was sent a signal (%q): it must be left to finish", strings.TrimSpace(string(b)))
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

// 7b (guard): before the commit the signal still begins the polite stop.
func TestEngineRun_ASignalOnAnUpdateBeforeTheCommitStillBeginsTheStop(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "signalled")
	s := stoppableStream(t, signalMarkingScript(marker))
	cleaned := 0
	r := streamingRunOn(t, "update", s, &cleaned)
	for _, msg := range []any{hostDone, composeRunning} {
		r, _ = runUpdate(t, r, engineStreamMsg{msg: msg})
	}

	r, _ = runUpdate(t, r, SignalQuitMsg{})

	if r.state != runStopping {
		t.Fatalf("state after the signal = %v, want runStopping before the commit", r.state)
	}
}
