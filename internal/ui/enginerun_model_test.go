package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// Plain model tests for engineRun's error, cancel and edge paths, driven
// the way InstallModel and UpdateModel drive it: start, then messages
// into update, with the screen read back from view.

func startedRun(t *testing.T, action string, seams engineRunSeams) engineRun {
	t.Helper()
	r := newEngineRun(action, t.TempDir(), "Install — Standard", "v9.9.9").withSeams(seams)
	r, _ = r.start(80, 26)
	return r
}

func runScreen(r engineRun) string { return stripANSI(r.view(80, 26)) }

func runUpdate(t *testing.T, r engineRun, msg tea.Msg) (engineRun, tea.Cmd) {
	t.Helper()
	return r.update(msg)
}

// installScriptAt writes an install.sh and points the launcher's local
// script override at it, so the real fetch-stage-run path runs offline.
func installScriptAt(t *testing.T, body string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH", path)
}

func TestEngineRun_DefaultPrepareRunsTheScriptInPlainModeForTheAction(t *testing.T) {
	installScriptAt(t, "echo \"args: $*\" >&2\nexit 0\n")
	r := newEngineRun("update", t.TempDir(), "Update", "v9.9.9") // no seam: the real default
	_, cmd := r.start(80, 26)
	ready, ok := cmd().(engineReadyMsg)
	if !ok || ready.err != nil {
		t.Fatalf("default prepare: %#v", ready)
	}
	done := awaitEnd(t, ready.stream)
	if got := strings.Join(done.StderrTail, "\n"); !strings.Contains(got, "args: --plain --update") {
		t.Fatalf("engine ran with %q, want --plain --update", got)
	}
	if err := ready.cleanup(); err != nil {
		t.Fatalf("cleanup of the staged script failed: %v", err)
	}
}

func TestEngineRun_DefaultPrepareFailuresReachTheFailedScreen(t *testing.T) {
	t.Run("script cannot be read", func(t *testing.T) {
		t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH", filepath.Join(t.TempDir(), "missing.sh"))
		r := newEngineRun("install", t.TempDir(), "Install", "v")
		r, cmd := r.start(80, 26)
		r, _ = runUpdate(t, r, cmd())
		s := runScreen(r)
		if r.state != runFailed || !strings.Contains(s, "Installation stopped") || !strings.Contains(s, "ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH") {
			t.Fatalf("state = %v, screen:\n%s", r.state, s)
		}
	})
	t.Run("unknown action", func(t *testing.T) {
		installScriptAt(t, "exit 0\n")
		r := newEngineRun("teleport", t.TempDir(), "Install", "v")
		_, cmd := r.start(80, 26)
		ready := cmd().(engineReadyMsg)
		if ready.err == nil || !strings.Contains(ready.err.Error(), `unknown engine action "teleport"`) {
			t.Fatalf("err = %v", ready.err)
		}
	})
}

func TestEngineRun_UpdateFailureIsTitledUpdateStopped(t *testing.T) {
	r := startedRun(t, "update", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{err: errors.New("fetch install.sh: timed out")})
	s := runScreen(r)
	if !strings.Contains(s, "Update stopped") || strings.Contains(s, "Installation stopped") {
		t.Fatalf("an update's failure must say Update stopped:\n%s", s)
	}
	if !strings.Contains(s, "fetch install.sh: timed out") {
		t.Fatalf("reason missing:\n%s", s)
	}
}

func TestEngineRun_FailedScreenShowsOnlyTheLastSixStderrLines(t *testing.T) {
	r := startedRun(t, "install", engineRunSeams{})
	r.state = runStreaming
	var tail []string
	for i := 1; i <= 9; i++ {
		tail = append(tail, fmt.Sprintf("stderr line %d", i))
	}
	r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1"), StderrTail: tail}})
	s := runScreen(r)
	for i := 1; i <= 3; i++ {
		if strings.Contains(s, fmt.Sprintf("stderr line %d", i)) {
			t.Errorf("line %d should have scrolled off the six-line tail:\n%s", i, s)
		}
	}
	for i := 4; i <= 9; i++ {
		if !strings.Contains(s, fmt.Sprintf("stderr line %d", i)) {
			t.Errorf("line %d missing from the tail:\n%s", i, s)
		}
	}
}

func TestEngineRun_HandoffThatCannotBePreparedReachesFailed(t *testing.T) {
	r := startedRun(t, "install", engineRunSeams{})
	r.state = runHandoffRunning
	if !strings.Contains(runScreen(r), "in the installer — you'll return here when it finishes") {
		t.Fatalf("handoff screen:\n%s", runScreen(r))
	}
	r, cmd := runUpdate(t, r, installPreparedMsg{err: errors.New("stage install.sh: disk full")})
	if cmd != nil || r.state != runFailed || !strings.Contains(runScreen(r), "stage install.sh: disk full") {
		t.Fatalf("state = %v, screen:\n%s", r.state, runScreen(r))
	}
}

func TestEngineRun_FailedScreenOpensTheRealInstallerByDefault(t *testing.T) {
	t.Setenv(requireInConsoleEnv, "")
	installScriptAt(t, "#!/bin/bash\nexit 0\n")
	dir := t.TempDir()
	r := newEngineRun("install", dir, "Install", "v")
	r, _ = r.start(80, 26)
	r, _ = runUpdate(t, r, engineReadyMsg{err: errors.New("boom")})

	// "Open the guided installer" is preselected; no seam means the
	// real install.sh is fetched and staged for the terminal handoff.
	r, cmd := runUpdate(t, r, key(tea.KeyEnter))
	if r.state != runHandoffRunning {
		t.Fatalf("state = %v, want runHandoffRunning", r.state)
	}
	prepared, ok := cmd().(installPreparedMsg)
	if !ok || prepared.err != nil {
		t.Fatalf("prepare: %#v", prepared)
	}
	defer prepared.cleanup()
	if prepared.cmd.Dir != dir || len(prepared.cmd.Args) != 2 || prepared.cmd.Args[0] != "bash" {
		t.Fatalf("handoff command = %v in %q, want a flagless bash run in the target", prepared.cmd.Args, prepared.cmd.Dir)
	}

	// The real handoff gives the terminal to that command.
	cleaned := false
	prepared.cleanup = func() error { cleaned = true; return os.Remove(prepared.cmd.Args[1]) }
	r, handoff := runUpdate(t, r, prepared)
	if handoff == nil {
		t.Fatal("no terminal handoff was started")
	}
	r, _ = runUpdate(t, r, installFinishedMsg{})
	if !cleaned || !r.Succeeded {
		t.Fatalf("a finished handoff should clean up and succeed: cleaned=%v succeeded=%v", cleaned, r.Succeeded)
	}
}

func TestEngineRun_StreamEndingMidRunCleansUpAndSaysSo(t *testing.T) {
	cleaned := 0
	r := startedRun(t, "install", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: &engine.Stream{C: make(chan any)}, cleanup: func() error { cleaned++; return nil }})
	r, _ = runUpdate(t, r, engineStreamEndedMsg{})
	if cleaned != 1 {
		t.Fatalf("staged script cleaned %d times, want once", cleaned)
	}
	if r.state != runFailed || !strings.Contains(runScreen(r), "the engine stopped without reporting how the run finished") {
		t.Fatalf("state = %v, screen:\n%s", r.state, runScreen(r))
	}
}

func TestEngineRun_SuccessReadsTheURLFromTheRealDeployment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte("APP_URL=https://orbit.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := newEngineRun("install", dir, "Install", "v") // no detect seam
	r, _ = r.start(80, 26)
	r.state = runStreaming
	r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 0}})
	if !r.Succeeded || !r.Done || r.URL != "https://orbit.example.test" {
		t.Fatalf("Succeeded=%v Done=%v URL=%q", r.Succeeded, r.Done, r.URL)
	}
	if runScreen(r) != "" {
		t.Fatalf("a succeeded run hands the screen to the success flow, got:\n%s", runScreen(r))
	}
}

func TestEngineRun_ConfigurationPromptMenu(t *testing.T) {
	refused := func(t *testing.T) engineRun {
		r := startedRun(t, "update", engineRunSeams{})
		r.state = runStreaming
		r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", "configuration-failure", "retry")})
		r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1")}})
		if r.state != runConfigPrompt {
			t.Fatalf("state = %v, want runConfigPrompt", r.state)
		}
		if s := runScreen(r); !strings.Contains(s, "then the update resumes") {
			t.Fatalf("prompt should name the action that resumes:\n%s", s)
		}
		return r
	}

	t.Run("Menu", func(t *testing.T) {
		r := refused(t)
		r, _ = runUpdate(t, r, key(tea.KeyDown))
		r, _ = runUpdate(t, r, key(tea.KeyEnter))
		if !r.Done || !r.WantsMenu {
			t.Fatalf("Done=%v WantsMenu=%v", r.Done, r.WantsMenu)
		}
	})
	t.Run("Exit via wrap", func(t *testing.T) {
		r := refused(t)
		r, _ = runUpdate(t, r, key(tea.KeyUp))
		if !strings.Contains(runScreen(r), "▸ Exit") {
			t.Fatalf("Up from the top should wrap to Exit:\n%s", runScreen(r))
		}
		_, cmd := runUpdate(t, r, key(tea.KeyEnter))
		if !isQuit(cmd) {
			t.Fatal("Exit did not quit")
		}
	})
	t.Run("Esc", func(t *testing.T) {
		r := refused(t)
		r, _ = runUpdate(t, r, key(tea.KeyEsc))
		if !r.Done || !r.WantsMenu {
			t.Fatal("Esc did not return to the menu")
		}
	})
	t.Run("unbound key", func(t *testing.T) {
		r := refused(t)
		r, cmd := runUpdate(t, r, runeKey('z'))
		if cmd != nil || r.Done || r.menuSel != 0 || r.state != runConfigPrompt {
			t.Fatal("an unbound key changed the prompt")
		}
	})
}

func TestEngineRun_FailedMenuExitQuits(t *testing.T) {
	r := startedRun(t, "install", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{err: errors.New("boom")})
	r, _ = runUpdate(t, r, key(tea.KeyDown))
	r, _ = runUpdate(t, r, key(tea.KeyDown))
	_, cmd := runUpdate(t, r, key(tea.KeyEnter))
	if !isQuit(cmd) {
		t.Fatal("Exit on the failed screen did not quit")
	}
}

func TestEngineRun_KeysWhileTheEngineRunsDoNothing(t *testing.T) {
	r := startedRun(t, "install", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: &engine.Stream{C: make(chan any)}, cleanup: func() error { return nil }})
	for _, k := range []tea.KeyPressMsg{key(tea.KeyEnter), key(tea.KeyEsc), runeKey('q')} {
		var cmd tea.Cmd
		r, cmd = runUpdate(t, r, k)
		if cmd != nil || r.Done || r.state != runStreaming {
			t.Fatalf("%v during the run changed it: Done=%v state=%v", k, r.Done, r.state)
		}
	}
}

func TestEngineRun_CtrlCKillsTheEngine(t *testing.T) {
	r := startedRun(t, "install", engineRunSeams{})
	s := sleepingStream(t)
	r, _ = runUpdate(t, r, engineReadyMsg{stream: s, cleanup: func() error { return nil }})
	_, cmd := runUpdate(t, r, ctrlC())
	if !isQuit(cmd) {
		t.Fatal("Ctrl+C did not quit")
	}
	if d := awaitEnd(t, s); d.ExitCode == 0 {
		t.Fatalf("Ctrl+C left the engine running: %+v", d)
	}
}

func TestEngineRun_UnexpectedMessagesAreIgnored(t *testing.T) {
	r := startedRun(t, "install", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: &engine.Stream{C: make(chan any)}, cleanup: func() error { return nil }})
	before := runScreen(r)
	r, cmd := runUpdate(t, r, struct{ note string }{"not for us"})
	if cmd != nil {
		t.Fatal("an unrelated message produced a command")
	}
	r, cmd = runUpdate(t, r, engineStreamMsg{msg: 3.14})
	if cmd != nil || r.state != runStreaming || runScreen(r) != before {
		t.Fatal("an unknown stream payload changed the run")
	}
}

func TestEngineRun_UnsizedViewIsBlank(t *testing.T) {
	r := startedRun(t, "install", engineRunSeams{})
	if got := r.view(0, 0); got != "" {
		t.Fatalf("zero-width view = %q, want blank", got)
	}
}

func TestCentreBlock_ContentWiderThanTheScreenStartsAtTheLeftEdge(t *testing.T) {
	line := strings.Repeat("x", 30)
	got := centreBlock(20, 3, line+"\nab")
	rows := strings.Split(got, "\n")
	if rows[0] != line {
		t.Fatalf("an over-wide line must not be shifted: %q", rows[0])
	}
	if rows[1] != strings.Repeat(" ", 9)+"ab" {
		t.Fatalf("a narrow line should still centre: %q", rows[1])
	}
}
