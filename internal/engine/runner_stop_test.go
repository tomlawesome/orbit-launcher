package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// #240: Ctrl-C during Install or Update must let the installer undo its
// work. A stop is a graceful one: Terminate sends SIGTERM to the whole
// process group and nothing more, Done closes once the engine has been
// reaped, and Kill is the SIGKILL that follows only when the grace has
// run out.

const stopEvent = "phase=application component=application state=starting reason=application-health action=start elapsed=1s"

// startStoppable starts script, waits for its first event (so any trap it
// sets is in place), then drains the stream in the background so that
// nothing the stream sends can block it. Whatever the test leaves behind
// is killed when it ends. The returned channel yields the DoneMsg.
func startStoppable(t *testing.T, script string) (*Stream, <-chan DoneMsg) {
	t.Helper()
	s := startScript(t, script)
	pid := s.cmd.Process.Pid
	t.Cleanup(func() {
		s.Kill()
		_ = syscall.Kill(-pid, syscall.SIGKILL)
	})

	select {
	case msg := <-s.C:
		if _, ok := msg.(EventMsg); !ok {
			t.Fatalf("first message = %#v, want the script's event", msg)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no first event")
	}

	doneCh := make(chan DoneMsg, 1)
	go func() {
		var done DoneMsg
		for msg := range s.C {
			if d, ok := msg.(DoneMsg); ok {
				done = d
			}
		}
		doneCh <- done
	}()
	return s, doneCh
}

// closesWithin reports whether ch is closed within d.
func closesWithin(ch <-chan struct{}, d time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(d):
		return false
	}
}

// processGone is true once pid no longer runs; a zombie that nothing has
// reaped yet counts as gone.
func processGone(pid int) bool {
	if err := syscall.Kill(pid, 0); err == syscall.ESRCH {
		return true
	}
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	// "pid (comm) S ..." -- the state follows the last ')'.
	s := string(stat)
	rest := strings.TrimSpace(s[strings.LastIndex(s, ")")+1:])
	return strings.HasPrefix(rest, "Z")
}

// The scripts start their long sleep, and wait until it is really
// sleep (its /proc comm), before printing the line the test waits for.
// A SIGTERM that lands between bash's fork and the child's exec is
// caught by the child's copy of bash's handler and lost at exec, so the
// sleep would outlive the stop and hold stdout open.
//
// The engine's own EXIT trap is the rollback: Terminate must let it run,
// and Done must close only after it has.
func TestStream_TerminateLetsTheEngineRunItsExitTrap(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "rolled-back")
	s, doneCh := startStoppable(t, fmt.Sprintf(`
trap 'touch "%s"' EXIT
sleep 60 &
until read -r c 2>/dev/null </proc/$!/comm && [ "$c" = sleep ]; do :; done
echo "%s"
wait
`, marker, stopEvent))

	s.Terminate()

	if !closesWithin(s.Done(), 5*time.Second) {
		t.Fatal("Done did not close within 5s of Terminate")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the engine's EXIT trap did not run before Done closed: %v", err)
	}
	select {
	case done := <-doneCh:
		if done.Err == nil {
			t.Fatalf("a terminated engine reported a clean exit: %+v", done)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no DoneMsg after Terminate")
	}
}

// An engine that ignores SIGTERM stays up through Terminate (the grace
// period is the caller's), and Kill is what ends it.
func TestStream_KillEndsAnEngineThatIgnoresTerminate(t *testing.T) {
	s, doneCh := startStoppable(t, fmt.Sprintf(`
trap '' TERM
sleep 60 &
until read -r c 2>/dev/null </proc/$!/comm && [ "$c" = sleep ]; do :; done
echo "%s"
wait
`, stopEvent))

	s.Terminate()
	if closesWithin(s.Done(), time.Second) {
		t.Fatal("Terminate ended an engine that ignores SIGTERM; it must send SIGTERM and nothing stronger")
	}

	s.Kill()
	if !closesWithin(s.Done(), 5*time.Second) {
		t.Fatal("Done did not close promptly after Kill")
	}
	select {
	case done := <-doneCh:
		if done.Err == nil {
			t.Fatalf("a killed engine reported a clean exit: %+v", done)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no DoneMsg after Kill")
	}
}

// Done closes when the engine ends on its own, with no signal sent.
func TestStream_DoneClosesWhenTheEngineExitsByItself(t *testing.T) {
	s, _ := startStoppable(t, fmt.Sprintf(`
echo "%s"
exit 0
`, stopEvent))

	if !closesWithin(s.Done(), 5*time.Second) {
		t.Fatal("Done did not close after the engine exited")
	}
}

// Terminate, like Kill, signals the process group only while the engine
// is alive: once it has been reaped its PID may be someone else's.
func TestStream_TerminateAfterExitSignalsNothing(t *testing.T) {
	s := startScript(t, "exit 0")
	collect(t, s)
	if !closesWithin(s.Done(), 5*time.Second) {
		t.Fatal("Done did not close after the engine was reaped")
	}

	victim := startScript(t, `echo "`+stopEvent+`"; sleep 60`)
	victimPid := victim.cmd.Process.Pid
	t.Cleanup(func() {
		victim.Kill()
		_ = syscall.Kill(-victimPid, syscall.SIGKILL)
	})
	go func() {
		for range victim.C {
		}
	}()

	// The finished stream's goroutine is done, so nothing else reads cmd.
	s.cmd.Process.Pid = victimPid
	s.Terminate()

	if closesWithin(victim.Done(), 300*time.Millisecond) {
		t.Fatal("Terminate after exit signalled another process group")
	}
}

// Kill ends the whole group, not just bash: a child that ignores SIGTERM
// (here a background sleep) must not outlive a forced stop holding the
// installer's docker children, say, orphaned.
func TestStream_KillEndsTheWholeProcessGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s, _ := startStoppable(t, fmt.Sprintf(`
trap '' TERM
sleep 60 &
echo $! > "%s"
echo "%s"
wait
`, pidFile, stopEvent))

	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the script's child did not record its PID: %v", err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("child PID %q: %v", raw, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	s.Kill()

	deadline := time.Now().Add(3 * time.Second)
	for !processGone(child) {
		if time.Now().After(deadline) {
			t.Fatalf("Kill left the engine's child %d running", child)
		}
		time.Sleep(25 * time.Millisecond)
	}
}
