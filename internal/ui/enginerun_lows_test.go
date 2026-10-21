package ui

// Tests for the engine lane of the v0.4.3 lows (#195, #236, #267). They
// pin the corrected behaviour, so each fails on the code as it stood at
// 8148607. The items and their evidence are in
// .agents/lows-v043/{lanes,reverify-195-code,reverify-236}.md.
//
// Every test here drives the code only through seams the package's own
// tests already use (engineRunSeams, engine.Start, the update function).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// pumpRun plays the event loop for r: cmd runs, every message it yields
// (batches expanded, the sky's ticks dropped) goes back into update, and
// what that returns runs in turn, until *count reaches want or d passes.
func pumpRun(t *testing.T, r engineRun, cmd tea.Cmd, count *int, want int, d time.Duration) engineRun {
	t.Helper()
	out := make(chan tea.Msg, 256)
	runCmdInto(cmd, out)
	deadline := time.After(d)
	for *count < want {
		select {
		case msg := <-out:
			if _, tick := msg.(tickMsg); tick {
				continue
			}
			var next tea.Cmd
			r, next = r.update(msg)
			runCmdInto(next, out)
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			return r
		}
	}
	return r
}

// --- A1-R2 / SEC-4: Ctrl-C while the manual handoff is being prepared ---------

// The terminal handoff's staged install.sh (and its configuration tree)
// are returned only in installPreparedMsg. A Ctrl-C that arrives before
// that message must not quit at once, because the message would then
// never be handled and the files stay in TMPDIR: the run waits for the
// preparation to report back, as it does while the engine is preparing
// (TestEngineRun_CtrlCWhilePreparingCancelsAndQuitsOnceThePrepareReportsBack),
// removes the files, and then quits without starting the handoff.
func TestLows_CtrlCWhileTheHandoffIsBeingPreparedStillRemovesTheStagedFiles(t *testing.T) {
	t.Setenv(requireInConsoleEnv, "")
	cleaned := 0
	handoffRan := false
	r := startedRun(t, "install", engineRunSeams{
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			return exec.Command("true"), func() error { cleaned++; return nil }, nil
		},
		runHandoff: func(*exec.Cmd) tea.Cmd {
			handoffRan = true
			return func() tea.Msg { return installFinishedMsg{} }
		},
	})
	r, _ = runUpdate(t, r, engineReadyMsg{err: errors.New("boom")})
	r, prepare := runUpdate(t, r, key(tea.KeyEnter)) // "Open the guided installer"
	if r.state != runHandoffRunning {
		t.Fatalf("setup: state = %v, want runHandoffRunning", r.state)
	}
	prepared, ok := prepare().(installPreparedMsg) // the message is on its way...
	if !ok || prepared.err != nil {
		t.Fatalf("setup: prepare produced %#v", prepared)
	}

	r, cmd := runUpdate(t, r, ctrlC()) // ...when Ctrl-C arrives first
	if isQuit(cmd) {
		t.Fatal("Ctrl+C quit before the handoff preparation reported back: its cleanup arrives " +
			"only in that message, which would never be handled, so the staged install.sh stays in TMPDIR")
	}

	r, cmd = runUpdate(t, r, prepared)
	if cleaned != 1 {
		t.Fatalf("the staged install.sh was cleaned up %d times, want once", cleaned)
	}
	if !isQuit(cmd) {
		t.Fatal("the run did not quit once the preparation had reported back")
	}
	if handoffRan {
		t.Error("the terminal handoff started after Ctrl+C")
	}
}

// --- 236-c4: Ctrl-C while preparing, the engine starts just after ---------------

// An engine that arrives after Ctrl-C while the run was still preparing
// is stopped the way a running one is (SIGTERM, grace, then kill), so
// its EXIT trap can roll the install back; today it is killed outright
// and gets no rollback.
func TestLows_CtrlCWhilePreparingLetsAnEngineThatStartedAnywayRollBack(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "rolled-back")
	s := stoppableStream(t, rollbackScript(marker))
	cleaned := 0
	r := newEngineRun("install", t.TempDir(), "Install — Standard", "v9.9.9").withSeams(engineRunSeams{
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			return s, func() error { cleaned++; return nil }, nil
		},
	})
	r, startCmd := r.start(80, 26)
	ready := startCmd()

	r, cmd := r.update(ctrlC())
	if cmd != nil {
		t.Fatal("setup: Ctrl+C while preparing quit before the preparation reported back")
	}
	r, cmd = r.update(ready)
	_, stopped, quit := settleStop(t, r, cmd)

	if !quit {
		t.Fatal("the run did not quit after the late engine was stopped")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the late engine was killed outright; its EXIT trap (the rollback) never ran: %v", err)
	}
	if stopped != nil && stopped.forced {
		t.Error("the late engine's stop was reported as forced although it rolled back inside the grace")
	}
	if cleaned != 1 {
		t.Errorf("staged script cleaned %d times, want once", cleaned)
	}
}

// --- 236-c3: a lost SIGTERM ------------------------------------------------------

// --- Kill is asynchronous ----------------------------------------------------------

// processReaped is true once nothing is left of pid in /proc: not running
// and not a zombie waiting to be reaped.
func processReaped(pid int) bool {
	_, err := os.Stat("/proc/" + strconv.Itoa(pid))
	return os.IsNotExist(err)
}

// A configuration session killed by Esc or Ctrl-C (Stream.Kill signals
// and returns at once) must be gone before the session's configuration
// tree and the run's staged files are removed: a process still ending can
// re-create the tree (ImportTargetConfig's MkdirAll), and a
// configure.sh --check still running reads files that are being deleted.
// Whatever the mechanism (Kill waiting, or the removal moving behind the
// stream's Done), at the moment either release runs the process must
// have been reaped. Repeated, because a single kill can win the race by
// luck.
func TestLows_LeavingTheConfigSessionRemovesTheFilesOnlyOnceTheProcessIsGone(t *testing.T) {
	for _, how := range []struct {
		name string
		key  tea.KeyPressMsg
	}{{"Esc", key(tea.KeyEsc)}, {"Ctrl-C", ctrlC()}} {
		t.Run(how.name, func(t *testing.T) {
			for i := 0; i < 10; i++ {
				pidFile := filepath.Join(t.TempDir(), "pid")
				cmd := exec.Command("bash", "-c", fmt.Sprintf(`echo $$ > "%s"; exec sleep 60`, pidFile))
				cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
				s, stdin, err := engine.StartInteractive(cmd)
				if err != nil {
					t.Fatal(err)
				}
				pid := 0
				for deadline := time.Now().Add(3 * time.Second); pid == 0 && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
					if raw, err := os.ReadFile(pidFile); err == nil {
						pid, _ = strconv.Atoi(strings.TrimSpace(string(raw)))
					}
				}
				if pid == 0 {
					s.Kill()
					t.Fatal("the session process never recorded its pid")
				}

				var releases, early int
				release := func() {
					releases++
					if !processReaped(pid) {
						early++
					}
				}
				r := startedRun(t, "install", engineRunSeams{})
				r, _ = runUpdate(t, r, engineReadyMsg{
					stream:     &engine.Stream{C: make(chan any)},
					configTree: t.TempDir(),
					cleanup:    func() error { release(); return nil },
				})
				r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", "configuration-failure", "retry")})
				r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1}})
				if r.state != runConfigCollect {
					t.Fatalf("setup: state = %v, want the configuration session", r.state)
				}
				r.cfg.stream = s
				r.cfg.stdin = stdin
				r.cfg.plan = configPlan{configTree: t.TempDir(), cleanup: release}

				r, next := r.update(how.key)
				r = pumpRun(t, r, next, &releases, 2, 3*time.Second)
				_ = r

				if releases < 2 {
					t.Fatalf("round %d: the session's files were released %d times, want the plan's and the run's", i, releases)
				}
				if early > 0 {
					t.Fatalf("round %d: %d of the file removals ran while the killed process (pid %d) was still ending", i, early, pid)
				}
				if !closed(s.Done(), 3*time.Second) {
					t.Fatalf("round %d: the session process was left running", i)
				}
			}
		})
	}
}

// --- P2-R1: the configuration check is cancellable --------------------------------

// The first configure.sh --check (the plan) runs on a context the UI
// owns, so leaving the session cancels it. Today it runs on
// context.Background() and Esc does not stop it. (The matching bound for
// a hung check nobody cancels is in internal/deploy/configure_lows_test.go.)
func TestLows_LeavingTheSessionCancelsThePlanCheck(t *testing.T) {
	for _, how := range []struct {
		name string
		key  tea.KeyPressMsg
	}{{"Esc", key(tea.KeyEsc)}, {"Ctrl-C", ctrlC()}} {
		t.Run(how.name, func(t *testing.T) {
			got := make(chan context.Context, 1)
			seams := engineRunSeams{
				prepareConfig: func(ctx context.Context, _, _ string) configPlanMsg {
					got <- ctx
					select {
					case <-ctx.Done():
					case <-time.After(4 * time.Second):
					}
					return configPlanMsg{err: errors.New("check ended")}
				},
			}
			cleaned := 0
			r, prepare := refusedRunWithTree(t, seams, t.TempDir(), &cleaned)
			go prepare()

			var ctx context.Context
			select {
			case ctx = <-got:
			case <-time.After(3 * time.Second):
				t.Fatal("setup: the plan check never started")
			}
			r.update(how.key)

			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
				t.Fatalf("%s left the session but the configure.sh --check it started was not cancelled", how.name)
			}
		})
	}
}

// --- P2-Q1: the retry does not fetch install.sh again -----------------------------

// After the guided answers are adopted the engine is run again. A brief
// network failure on a second fetch would show up as a fetch error on the
// failure screen, as if the answers were bad, so the retry reuses the
// install.sh the first run fetched (or at least does not go back to the
// network for it).
func TestLows_RetryAfterTheAnswersAreAdoptedDoesNotFetchInstallShAgain(t *testing.T) {
	requests := countingScriptSource(t)
	r := newEngineRun("install", t.TempDir(), "Install — Standard", "v9.9.9").withSeams(engineRunSeams{
		prepareConfig: planned(true, false),
	})
	r, startCmd := r.start(80, 26)
	first := awaitReady(t, startCmd)
	if first.err != nil || first.stream == nil {
		t.Fatalf("setup: the first run did not start: %#v", first)
	}
	t.Cleanup(func() {
		first.stream.Kill()
		if first.cleanup != nil {
			_ = first.cleanup()
		}
	})
	if n := requests.Load(); n != 1 {
		t.Fatalf("setup: the first run made %d fetches, want 1", n)
	}

	r, _ = runUpdate(t, r, first)
	r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", "configuration-failure", "retry")})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1}})
	if r.state != runConfigCollect {
		t.Fatalf("setup: state = %v, want the configuration session", r.state)
	}

	r, retry := runUpdate(t, r, configAdoptedMsg{})
	if r.state != runPreparing {
		t.Fatalf("state = %v, want the retried engine preparing", r.state)
	}
	second := awaitReady(t, retry)
	if second.err != nil || second.stream == nil {
		t.Fatalf("the retry did not start: %#v", second)
	}
	t.Cleanup(func() {
		second.stream.Kill()
		if second.cleanup != nil {
			_ = second.cleanup()
		}
	})
	if n := requests.Load(); n != 1 {
		t.Errorf("the retry made %d fetches of install.sh in all, want the first run's one only", n)
	}
}

// awaitReady runs cmd (a batch is expanded) and returns the engineReadyMsg
// it leads to.
func awaitReady(t *testing.T, cmd tea.Cmd) engineReadyMsg {
	t.Helper()
	out := make(chan tea.Msg, 16)
	runCmdInto(cmd, out)
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-out:
			if ready, ok := msg.(engineReadyMsg); ok {
				return ready
			}
		case <-deadline:
			t.Fatal("no engineReadyMsg")
		}
	}
}

// --- 236-9, end to end ------------------------------------------------------------

// The real command (deploy.BuildEngineCommand, with its own bound on
// pipes) run through the real stream: an install.sh that exits leaving a
// background child holding stdout still ends the run within a few
// seconds. (The stream-only version is in internal/engine.)
func TestLows_AnInstallThatLeavesAChildHoldingStdoutStillEnds(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	installScriptAt(t, fmt.Sprintf("#!/usr/bin/env bash\nsleep 30 &\necho $! > %q\necho left behind >&2\nexit 0\n", pidFile))
	t.Cleanup(func() {
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})
	ready := defaultPrepareEngine(context.Background(), t.TempDir(), string(deploy.Action("install")))
	if ready.err != nil || ready.stream == nil {
		t.Fatalf("setup: %#v", ready)
	}
	t.Cleanup(func() {
		ready.stream.Kill()
		if ready.cleanup != nil {
			_ = ready.cleanup()
		}
	})

	deadline := time.After(8 * time.Second)
	for {
		select {
		case msg, ok := <-ready.stream.C:
			if !ok {
				t.Fatal("the stream closed without a DoneMsg")
			}
			if _, isDone := msg.(engine.DoneMsg); isDone {
				return
			}
		case <-deadline:
			t.Fatal("no DoneMsg 8s after install.sh exited: the console sits on a static screen " +
				"until Ctrl-C because the pipes are read to end-of-file before the wait bound can start")
		}
	}
}

// --- #267: a manual handoff whose preparation fails shows both reasons ------------

// The operator chooses "Open the guided installer" on an engine failure,
// and preparing the handoff fails. The stopped screen must keep the first
// failure's reason (the engine's event and its stderr tail) and add the
// preparation error, not replace the one with the other. (The automatic
// fallback from the configuration session replaces the stale refusal on
// purpose; TestConfigCollect_HandoffPreparationFailureReplacesTheStaleRefusal
// keeps pinning that, and this is not that path.)
func TestLows_AManualHandoffThatCannotBePreparedStillShowsTheFirstFailure(t *testing.T) {
	const prepareErr = "stage install.sh: disk full"
	for _, action := range []string{"install", "update"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv(requireInConsoleEnv, "")
			r := startedRun(t, action, engineRunSeams{})
			r.state = runStreaming
			r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("identity", "image", "failed", "image-registry", "retry")})
			r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{
				ExitCode: 1, Err: errors.New("exit status 1"),
				StderrTail: []string{"Orbit installer: Could not pull ghcr.io/example/orbit:latest."},
			}})
			if r.state != runFailed {
				t.Fatalf("setup: state = %v, want runFailed", r.state)
			}
			r, _ = runUpdate(t, r, key(tea.KeyEnter)) // "Open the guided installer"
			if r.state != runHandoffRunning {
				t.Fatalf("setup: state = %v, want runHandoffRunning", r.state)
			}

			r, _ = runUpdate(t, r, installPreparedMsg{err: errors.New(prepareErr)})
			s := runScreen(r)
			if r.state != runFailed || !strings.Contains(s, prepareErr) {
				t.Fatalf("state = %v; the preparation error is not shown:\n%s", r.state, s)
			}
			for _, first := range []string{"image-registry", "Could not pull"} {
				if !strings.Contains(s, first) {
					t.Errorf("the first failure's %q was dropped when the handoff could not be prepared:\n%s", first, s)
				}
			}
		})
	}
}
