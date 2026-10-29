package ui

// #297: a fresh install stopped after the installer's commit waits out
// compose up, as an update does. Orbit's `docker compose up` runs under
// its own 300 s bound and ignores signals sent to the installer; after
// the commit a stop waits for it to end, then rolls back. Killing the
// installer inside that window leaves a half-created deployment that was
// never rolled back.
//
// Required behaviour:
//  1. An install stopped after the commit (an event for which
//     engine.Event.AfterCommit() is true; the first is phase=compose
//     component=compose state=completed reason=compose-validation) gets
//     the long grace, as an update does.
//  2. An install stopped before the commit keeps the short stopGrace.
//  3. A second Ctrl-C during the long wait kills the installer and quits
//     at once.
//  4. The long grace follows Orbit's bound: when
//     ORBIT_INSTALLER_COMPOSE_UP_TIMEOUT_SECONDS is an integer from 1 to
//     900 it is that many seconds plus 30 s; otherwise it is
//     stopGraceAfterCommit.
//
// Written from the issue alone, without the code. The stop helpers
// (stoppableStream, streamingRunOn, settleStop, closed, shortenStopGrace,
// stubbornScript) are in enginerun_stop_test.go; composeValidated and
// composeUpStarting are in enginerun_update_rollforward_test.go; hostDone
// and composeRunning are in stopped_screen_test.go.

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// composeUpTimeoutEnv is Orbit's compose-up bound, in seconds.
const composeUpTimeoutEnv = "ORBIT_INSTALLER_COMPOSE_UP_TIMEOUT_SECONDS"

// stoppingInstallArmed is an install run on a real engine stream (script),
// fed events and then put in runStopping by Ctrl-C. It returns the command
// the Ctrl-C armed (the bounded wait), for settleStop to run.
func stoppingInstallArmed(t *testing.T, script string, events ...any) (engineRun, *int, tea.Cmd) {
	t.Helper()
	s := stoppableStream(t, script)
	cleaned := new(int)
	r := streamingRunOn(t, "install", s, cleaned)
	for _, msg := range events {
		r, _ = runUpdate(t, r, engineStreamMsg{msg: msg})
	}
	r, cmd := runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping:\n%s", r.state, runScreen(r))
	}
	return r, cleaned, cmd
}

// 1-3: after the commit a fresh install is given the long grace: the
// short grace does not force the stop within 25 times its length.
// Before the commit the short grace still forces it. A second Ctrl-C
// still kills at once.
func TestEngineRun_InstallStopGraceIsLongerAfterTheCommit(t *testing.T) {
	t.Run("before the commit", func(t *testing.T) {
		shortenStopGrace(t, 200*time.Millisecond)
		r, _, cmd := stoppingInstallArmed(t, stubbornScript, hostDone, composeRunning)

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
		r, _, cmd := stoppingInstallArmed(t, stubbornScript, hostDone, composeValidated, composeUpStarting)
		s := r.stream

		r, stopped, quit := settleStopWithin(t, r, cmd, 25*stopGrace)

		if quit {
			t.Fatal("after the commit the launcher gave up on the installer within 25 times the short grace; it must wait out compose up")
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

// 4: the long grace is Orbit's compose-up bound plus 30 s when the
// launcher's environment sets it to an integer from 1 to 900, otherwise
// stopGraceAfterCommit. Surrounding spaces are treated as invalid: Orbit
// reads the value as a plain integer, and guessing at trimming here
// would give a bound Orbit does not use.
func TestAfterCommitStopGrace(t *testing.T) {
	for _, c := range []struct {
		name  string
		value string
		set   bool
		want  time.Duration
	}{
		{"unset", "", false, stopGraceAfterCommit},
		{"empty", "", true, stopGraceAfterCommit},
		{"not an integer", "abc", true, stopGraceAfterCommit},
		{"zero", "0", true, stopGraceAfterCommit},
		{"negative", "-5", true, stopGraceAfterCommit},
		{"above the maximum", "901", true, stopGraceAfterCommit},
		{"the minimum", "1", true, 1*time.Second + 30*time.Second},
		{"in range", "600", true, 600*time.Second + 30*time.Second},
		{"the maximum", "900", true, 900*time.Second + 30*time.Second},
		{"surrounding spaces", " 600 ", true, stopGraceAfterCommit},
	} {
		t.Run(c.name, func(t *testing.T) {
			getenv := func(key string) string {
				if key != composeUpTimeoutEnv || !c.set {
					return ""
				}
				return c.value
			}

			if got := afterCommitStopGrace(getenv); got != c.want {
				t.Errorf("afterCommitStopGrace(%s=%q) = %v, want %v", composeUpTimeoutEnv, c.value, got, c.want)
			}
		})
	}
}
