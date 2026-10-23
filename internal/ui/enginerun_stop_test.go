package ui

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// #240: Ctrl-C during Install or Update is a graceful stop. The engine
// is sent SIGTERM so its own cleanup (the EXIT trap that rolls the
// install back) can run, the run shows a stopping line (runStopping)
// while it does, and the launcher quits when the engine has gone
// (engine.Stream.Done) or when stopGrace has run out, in which case the
// engine is killed (engineStoppedMsg{forced: true}). A second Ctrl-C
// forces the kill at once.

// stoppableStream starts script the way the console runs the engine
// (session-detached, stdout piped), waits for its first line so that any
// trap it sets is in place, then drains the rest in the background into
// the stream's tail (streamTail), where settleStop finds the engine's
// DoneMsg to feed back into update as the event loop would. The
// script's whole process group is killed when the test ends.
func stoppableStream(t *testing.T, script string) *engine.Stream {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	s, err := engine.Start(cmd)
	if err != nil {
		t.Fatalf("start engine: %v", err)
	}
	t.Cleanup(func() {
		s.Kill()
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	})
	select {
	case <-s.C:
	case <-time.After(5 * time.Second):
		t.Fatal("the engine script printed nothing")
	}
	tail := make(chan any, 256)
	streamTails.Store(s, tail)
	t.Cleanup(func() { streamTails.Delete(s) })
	go func() {
		for msg := range s.C {
			select {
			case tail <- msg:
			default: // nobody is reading this stream's tail; drop it
			}
		}
		close(tail)
	}()
	return s
}

// streamTails maps a stoppableStream to the channel its background
// drain copies the stream's remaining messages into.
var streamTails sync.Map // *engine.Stream -> chan any

// streamTail is s's drained messages, or nil for a stream stoppableStream
// did not start.
func streamTail(s *engine.Stream) <-chan any {
	if s == nil {
		return nil
	}
	if v, ok := streamTails.Load(s); ok {
		return v.(chan any)
	}
	return nil
}

// The scripts start their long sleep, and wait until it is really
// sleep (its /proc comm), before printing the line the test waits for.
// A SIGTERM that lands between bash's fork and the child's exec is
// caught by the child's copy of bash's handler and lost at exec, so the
// sleep would outlive the stop and hold stdout open.
//
// rollbackScript is an installer that rolls back on exit: its EXIT trap
// touches marker (after a moment, as a docker rm and a file rollback
// take one), and it says so on its first line, which the stream reports
// as a progress event.
func rollbackScript(marker string) string {
	return fmt.Sprintf(`
trap 'sleep 0.2; touch "%s"' EXIT
sleep 60 &
until read -r c 2>/dev/null </proc/$!/comm && [ "$c" = sleep ]; do :; done
echo "phase=application component=application state=starting reason=application-health action=start elapsed=1s"
wait
`, marker)
}

// stubbornScript is an installer that ignores SIGTERM (a wedged cleanup).
const stubbornScript = `
trap '' TERM
sleep 60 &
until read -r c 2>/dev/null </proc/$!/comm && [ "$c" = sleep ]; do :; done
echo "phase=application component=application state=starting reason=application-health action=start elapsed=1s"
wait
`

// streamingRunOn is an install or update run that is mid-flight on s.
func streamingRunOn(t *testing.T, action string, s *engine.Stream, cleaned *int) engineRun {
	t.Helper()
	r := startedRun(t, action, engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: s, cleanup: func() error { *cleaned++; return nil }})
	if r.state != runStreaming {
		t.Fatalf("state = %v, want runStreaming", r.state)
	}
	return r
}

// runCmdInto runs cmd on its own goroutine, expanding batches, and
// sends everything it yields to out (which must have room for it).
func runCmdInto(cmd tea.Cmd, out chan<- tea.Msg) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				runCmdInto(c, out)
			}
			return
		}
		if msg != nil {
			out <- msg
		}
	}()
}

// settleStop plays the event loop for a stopping run: the commands run,
// engineStoppedMsg goes back into update, and so does what the run's
// engine stream still delivers (its DoneMsg above all), as the stream
// reader pushes it in production; what update returns runs in turn,
// until the run quits or five seconds pass. stopped is the
// engineStoppedMsg seen, if any. The stream is read from its tail
// (stoppableStream), so a test can still wait on s.Done() itself.
func settleStop(t *testing.T, r engineRun, cmd tea.Cmd) (after engineRun, stopped *engineStoppedMsg, quit bool) {
	t.Helper()
	out := make(chan tea.Msg, 256)
	runCmdInto(cmd, out)
	tail := streamTail(r.stream)
	deadline := time.After(5 * time.Second)
	for {
		var next tea.Cmd
		select {
		case msg := <-out:
			if msg == tea.Quit() {
				return r, stopped, true
			}
			if _, tick := msg.(tickMsg); tick {
				continue
			}
			if m, ok := msg.(engineStoppedMsg); ok {
				stopped = &m
			}
			r, next = r.update(msg)
		case msg, ok := <-tail:
			if !ok {
				tail = nil
				continue
			}
			r, next = r.update(engineStreamMsg{msg: msg})
		case <-deadline:
			return r, stopped, false
		}
		runCmdInto(next, out)
	}
}

func closed(ch <-chan struct{}, within time.Duration) bool {
	select {
	case <-ch:
		return true
	case <-time.After(within):
		return false
	}
}

func shortenStopGrace(t *testing.T, d time.Duration) {
	t.Helper()
	old := stopGrace
	stopGrace = d
	t.Cleanup(func() { stopGrace = old })
}

func TestEngineRun_CtrlCWhileStreamingLetsTheInstallerRollBackBeforeQuitting(t *testing.T) {
	for _, c := range []struct{ action, stopping string }{
		{"install", "stopping the install"},
		{"update", "stopping the update"},
	} {
		t.Run(c.action, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "rolled-back")
			s := stoppableStream(t, rollbackScript(marker))
			cleaned := 0
			r := streamingRunOn(t, c.action, s, &cleaned)

			r, cmd := runUpdate(t, r, ctrlC())

			if r.state != runStopping {
				t.Fatalf("state after Ctrl+C = %v, want runStopping", r.state)
			}
			if cmd == nil {
				t.Fatal("Ctrl+C armed nothing, so the run would wait for ever")
			}
			if r.Done {
				t.Fatal("the run was over the moment Ctrl+C was pressed")
			}
			screen := runScreen(r)
			for _, want := range []string{c.stopping, "the installer is undoing what it started"} {
				if !strings.Contains(screen, want) {
					t.Errorf("the stopping screen lacks %q:\n%s", want, screen)
				}
			}
			for _, bad := range []string{"Install stopped", "Update stopped"} {
				if strings.Contains(screen, bad) {
					t.Errorf("a stop the operator asked for is shown as a failure (%q):\n%s", bad, screen)
				}
			}

			r, stopped, quit := settleStop(t, r, cmd)

			if stopped == nil {
				t.Fatal("no engineStoppedMsg after the installer's cleanup finished")
			}
			if stopped.forced {
				t.Error("the stop was reported as forced, but the installer cleaned up and exited within the grace")
			}
			if !quit {
				t.Fatal("the run did not quit once the installer had stopped")
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatalf("the launcher quit before the installer's EXIT trap had run: %v", err)
			}
			if cleaned != 1 {
				t.Errorf("staged script cleaned %d times, want once", cleaned)
			}
		})
	}
}

func TestEngineRun_EngineStoppedMsgAfterCtrlCWaitsForTheDoneMsgToQuit(t *testing.T) {
	// engineStoppedMsg{} -- not forced -- says the installer process has
	// gone, but not how it ended: its DoneMsg (and any success event
	// still queued before it) may yet be on the way, so the run waits
	// for the DoneMsg to quit (#293). It must not kill an engine that
	// has already gone, and it must release the run's files.
	marker := filepath.Join(t.TempDir(), "rolled-back")
	s := stoppableStream(t, rollbackScript(marker))
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)
	r, _ = runUpdate(t, r, ctrlC())
	if !closed(s.Done(), 5*time.Second) {
		t.Fatal("the installer never finished its cleanup")
	}

	r, cmd := runUpdate(t, r, engineStoppedMsg{})

	if isQuit(cmd) {
		t.Fatal("the process exit alone quit the stopping run; the quit must wait for the engine's DoneMsg")
	}
	if !strings.Contains(runScreen(r), "stopping the install") {
		t.Errorf("after the process exit, before the DoneMsg, the screen no longer says it is stopping:\n%s", runScreen(r))
	}
	if cleaned > 1 {
		t.Errorf("staged script cleaned %d times by the process exit, want at most once", cleaned)
	}

	r, cmd = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{
		Err: errors.New("signal: terminated"), ExitCode: 143, StderrTail: []string{"rolled back"},
	}})

	if !isQuit(cmd) {
		t.Fatal("the DoneMsg that followed the process exit did not lead to tea.Quit")
	}
	if r.state == runFailed {
		t.Errorf("a stop the operator asked for ended on the failure screen:\n%s", runScreen(r))
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the installer's EXIT trap did not run: %v", err)
	}
	if cleaned != 1 {
		t.Errorf("staged script cleaned %d times, want once", cleaned)
	}
}

func TestEngineRun_CtrlCOnAnInstallerThatIgnoresTermKillsItWhenTheGraceRunsOut(t *testing.T) {
	shortenStopGrace(t, 200*time.Millisecond)
	s := stoppableStream(t, stubbornScript)
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)

	started := time.Now()
	r, cmd := runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping", r.state)
	}
	r, stopped, quit := settleStop(t, r, cmd)
	waited := time.Since(started)

	if stopped == nil {
		t.Fatal("no engineStoppedMsg after the grace ran out")
	}
	if !stopped.forced {
		t.Error("the stop was reported as clean, but the installer never exited by itself")
	}
	if !quit {
		t.Fatal("the run did not quit when the grace ran out")
	}
	if waited < 150*time.Millisecond {
		t.Errorf("quit after %v, before the %v grace had run: the installer was given no time", waited, stopGrace)
	}
	if !closed(s.Done(), 3*time.Second) {
		t.Fatal("the installer was still running after the run quit: a forced stop must Kill it")
	}
	if cleaned != 1 {
		t.Errorf("staged script cleaned %d times, want once", cleaned)
	}
}

func TestEngineRun_ASecondCtrlCWhileStoppingKillsAndQuitsAtOnce(t *testing.T) {
	// The default 15 s grace is left alone: the point is that the second
	// press does not wait for it.
	s := stoppableStream(t, stubbornScript)
	cleaned := 0
	r := streamingRunOn(t, "update", s, &cleaned)
	r, _ = runUpdate(t, r, ctrlC()) // the wait it arms is deliberately not run
	if r.state != runStopping {
		t.Fatalf("state after the first Ctrl+C = %v, want runStopping", r.state)
	}

	started := time.Now()
	_, cmd := runUpdate(t, r, ctrlC())
	_, _, quit := settleStop(t, r, cmd)

	if !quit {
		t.Fatal("the second Ctrl+C did not quit")
	}
	if time.Since(started) > 3*time.Second {
		t.Errorf("the second Ctrl+C took %v to quit; it must not wait out the grace", time.Since(started))
	}
	if !closed(s.Done(), 3*time.Second) {
		t.Fatal("the installer was still running after the second Ctrl+C: it must Kill")
	}
	if cleaned != 1 {
		t.Errorf("staged script cleaned %d times, want once", cleaned)
	}
}

func TestEngineRun_AnEngineDoneWhileStoppingQuitsInsteadOfShowingAFailure(t *testing.T) {
	for _, c := range []struct {
		name string
		done engine.DoneMsg
	}{
		{"terminated", engine.DoneMsg{Err: errors.New("signal: terminated"), ExitCode: -1, StderrTail: []string{"rolling back"}}},
		{"failed", engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1}},
		{"finished anyway", engine.DoneMsg{ExitCode: 0}},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := stoppableStream(t, rollbackScript(filepath.Join(t.TempDir(), "rolled-back")))
			cleaned := 0
			r := streamingRunOn(t, "install", s, &cleaned)
			r, _ = runUpdate(t, r, ctrlC())
			if r.state != runStopping {
				t.Fatalf("state after Ctrl+C = %v, want runStopping", r.state)
			}

			r, cmd := runUpdate(t, r, engineStreamMsg{msg: c.done})

			if r.state == runFailed {
				t.Fatalf("a stop the operator asked for ended on the failure screen:\n%s", runScreen(r))
			}
			if !isQuit(cmd) {
				t.Fatal("the engine's DoneMsg during a stop did not lead to tea.Quit")
			}
			if cleaned != 1 {
				t.Errorf("staged script cleaned %d times, want once", cleaned)
			}
		})
	}
}

// Security audit: a stop whose installer was still undoing its work when
// the launcher gave up on it (grace ran out, or a second Ctrl-C) leaves
// the folder and the containers half restored. The launcher says so
// after the screen closes, through flowOutcome.ExitNote (and
// AppModel.ExitNote). A clean stop says nothing.

// stillUndoing is the launcher's fixed phrase in that note (owner
// decision 20a, 2026-10-17).
const stillUndoing = "still undoing"

// forcedStopNoteWords are the parts of the owner's wording, by action,
// that every forced-stop note carries. The note ends "check them before
// installing again." for an install and "check them before updating
// again." for an update (owner decision 21a, 2026-10-18).
func forcedStopNoteWords(action string) []string {
	again := "installing"
	if action == "update" {
		again = "updating"
	}
	return []string{
		"the " + action + " was stopped, but Orbit's installer was still undoing its work",
		"when the launcher stopped waiting and ended it",
		"The folder and the Docker containers and volumes it created may be half restored",
		"check them before " + again + " again.",
	}
}

func TestEngineRun_AStopForcedAfterTheGraceLeavesAnExitNoteAndIsNotAFailure(t *testing.T) {
	for _, action := range []string{"install", "update"} {
		t.Run(action, func(t *testing.T) {
			shortenStopGrace(t, 200*time.Millisecond)
			s := stoppableStream(t, stubbornScript)
			cleaned := 0
			r := streamingRunOn(t, action, s, &cleaned)

			r, cmd := runUpdate(t, r, ctrlC())
			r, stopped, quit := settleStop(t, r, cmd)

			if stopped == nil || !stopped.forced {
				t.Fatalf("the stop was not forced (stopped = %+v); the test premise is broken", stopped)
			}
			if !quit {
				t.Fatal("the run did not quit when the grace ran out")
			}
			note := outcomeOf(r).ExitNote
			if note == "" {
				t.Fatal("a stop that outlived its grace left no exit note: the operator would never be told the " + action + " may be half undone")
			}
			if !strings.Contains(note, stillUndoing) {
				t.Errorf("exit note lacks %q: %q", stillUndoing, note)
			}
			for _, want := range forcedStopNoteWords(action) {
				if !strings.Contains(note, want) {
					t.Errorf("exit note lacks %q: %q", want, note)
				}
			}
			if r.state == runFailed {
				t.Errorf("a stop the operator asked for ended on the failure screen:\n%s", runScreen(r))
			}
		})
	}
}

func TestEngineRun_ASecondCtrlCWhileStoppingLeavesTheExitNote(t *testing.T) {
	// The default grace is left alone, as in the test above for the
	// kill itself: the second press is what forces the stop.
	s := stoppableStream(t, stubbornScript)
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)
	r, _ = runUpdate(t, r, ctrlC()) // the wait it arms is deliberately not run
	if r.state != runStopping {
		t.Fatalf("state after the first Ctrl+C = %v, want runStopping", r.state)
	}

	r, cmd := runUpdate(t, r, ctrlC())
	r, _, quit := settleStop(t, r, cmd)

	if !quit {
		t.Fatal("the second Ctrl+C did not quit")
	}
	note := outcomeOf(r).ExitNote
	if !strings.Contains(note, stillUndoing) {
		t.Errorf("a second Ctrl+C gave up on the installer without saying so: exit note %q", note)
	}
}

func TestEngineRun_ACleanStopLeavesNoExitNote(t *testing.T) {
	stderr := []string{
		"Orbit installer: The install stopped before it finished; rolling it back.",
		"Orbit installer: This install was rolled back: the folder is as it was before, and the containers and volumes it created are removed.",
	}
	s := stoppableStream(t, rollbackScript(filepath.Join(t.TempDir(), "rolled-back")))
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)
	r, _ = runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping", r.state)
	}

	r, cmd := runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{
		Err: errors.New("signal: terminated"), ExitCode: -1, StderrTail: stderr,
	}})

	if !isQuit(cmd) {
		t.Fatal("the engine's DoneMsg during a stop did not lead to tea.Quit")
	}
	if r.state == runFailed {
		t.Fatalf("a clean stop ended on the failure screen:\n%s", runScreen(r))
	}
	note := outcomeOf(r).ExitNote
	if note != "" {
		t.Errorf("a clean stop left an exit note; nothing the installer wrote may be printed after exit: %q", note)
	}
	if strings.Contains(note, stillUndoing) {
		t.Errorf("a clean stop is described as one that outlived its grace: %q", note)
	}
}

// The real ordering: the stream closes Done() before it delivers the
// DoneMsg, so the stop wait reports engineStoppedMsg{} first. The run
// waits for the DoneMsg to quit (#293), and neither message leaves an
// exit note for a clean stop.
func TestEngineRun_ACleanStoppedMsgBeforeTheDoneMsgLeavesNoExitNote(t *testing.T) {
	s := stoppableStream(t, rollbackScript(filepath.Join(t.TempDir(), "rolled-back")))
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)
	r, _ = runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping", r.state)
	}
	if !closed(s.Done(), 5*time.Second) {
		t.Fatal("the installer did not exit after the Ctrl+C; the test premise is broken")
	}

	r, cmd := runUpdate(t, r, engineStoppedMsg{})

	if isQuit(cmd) {
		t.Fatal("engineStoppedMsg alone led to tea.Quit; the quit must wait for the DoneMsg")
	}
	if note := outcomeOf(r).ExitNote; note != "" {
		t.Errorf("a clean stop left an exit note: %q", note)
	}

	r, cmd = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{
		Err: errors.New("signal: terminated"), ExitCode: -1, StderrTail: []string{"rolled back"},
	}})

	if !isQuit(cmd) {
		t.Error("the DoneMsg after the stop does not lead to tea.Quit")
	}
	if note := outcomeOf(r).ExitNote; note != "" {
		t.Errorf("the DoneMsg after a clean stop invented an exit note: %q", note)
	}
	if cleaned != 1 {
		t.Errorf("staged script cleaned %d times, want once", cleaned)
	}
}

// A second Ctrl-C can be handled after the installer has exited by
// itself but before the queued quit: state is still runStopping, yet no
// kill lands, so the stop was clean.
func TestEngineRun_ASecondCtrlCAfterTheInstallerHasAlreadyGoneLeavesNoExitNote(t *testing.T) {
	s := stoppableStream(t, rollbackScript(filepath.Join(t.TempDir(), "rolled-back")))
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)
	r, _ = runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after the first Ctrl+C = %v, want runStopping", r.state)
	}
	if !closed(s.Done(), 5*time.Second) {
		t.Fatal("the installer did not exit after the first Ctrl+C; the test premise is broken")
	}

	r, cmd := runUpdate(t, r, ctrlC())

	if !isQuit(cmd) {
		t.Fatal("the second Ctrl+C did not quit")
	}
	if note := outcomeOf(r).ExitNote; note != "" {
		t.Errorf("a second Ctrl-C found the installer already gone, but the stop was reported as forced: %q", note)
	}
}

func TestEngineRun_ACleanStopWithNothingOnStderrHasNoExitNote(t *testing.T) {
	s := stoppableStream(t, rollbackScript(filepath.Join(t.TempDir(), "rolled-back")))
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)
	r, _ = runUpdate(t, r, ctrlC())

	r, cmd := runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 0}})

	if !isQuit(cmd) {
		t.Fatal("the engine's DoneMsg during a stop did not lead to tea.Quit")
	}
	if note := outcomeOf(r).ExitNote; note != "" {
		t.Errorf("exit note = %q, want none: the installer said nothing", note)
	}
}

func TestEngineRun_CtrlCOutsideAStopLeavesNoExitNote(t *testing.T) {
	failed := func(t *testing.T) engineRun {
		r := startedRun(t, "install", engineRunSeams{})
		r, _ = runUpdate(t, r, engineReadyMsg{stream: &engine.Stream{C: make(chan any)}, cleanup: func() error { return nil }})
		r, _ = runUpdate(t, r, engineStreamEndedMsg{})
		return r
	}
	configSession := func(t *testing.T) engineRun {
		r := startedRun(t, "update", engineRunSeams{})
		r.state = runStreaming
		r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", "configuration-failure", "retry")})
		r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1")}})
		return r
	}
	for _, c := range []struct {
		name  string
		build func(*testing.T) engineRun
		state engineRunState
	}{
		{"failed", failed, runFailed},
		{"configuration session", configSession, runConfigCollect},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := c.build(t)
			if r.state != c.state {
				t.Fatalf("state = %v, want %v", r.state, c.state)
			}

			r, _ = runUpdate(t, r, ctrlC())

			if note := outcomeOf(r).ExitNote; note != "" {
				t.Errorf("Ctrl+C outside a stop left an exit note: %q", note)
			}
		})
	}
}
