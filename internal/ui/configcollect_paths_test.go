package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// Plain model tests for in-console configuration's failure, cancel and
// edge paths: each drives an engineRun already in the configuration
// session and checks which screen follows and what reached the engine.

func collectingRun(t *testing.T, seams engineRunSeams) engineRun {
	t.Helper()
	r := newEngineRun("install", t.TempDir(), "Install — Standard", "v9.9.9").withSeams(seams)
	r.width, r.height = 80, 26
	r.console = newConsole("Install", "v9.9.9", time.Now).setSize(80, 26)
	r.state = runConfigCollect
	return r
}

// handoffSeams records whether the terminal handoff was prepared.
func handoffSeams(prepared *bool) engineRunSeams {
	return engineRunSeams{
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			*prepared = true
			return exec.Command("true"), func() error { return nil }, nil
		},
	}
}

func TestConfigCollect_StepThatWillNotStartFallsBackToTheHandoff(t *testing.T) {
	t.Setenv(requireInConsoleEnv, "")
	prepared := false
	r := collectingRun(t, handoffSeams(&prepared))
	r, cmd := r.update(configStepMsg{err: errors.New("bash: not found")})
	if r.state != runHandoffRunning {
		t.Fatalf("state = %v, want the terminal handoff", r.state)
	}
	cmd()
	if !prepared {
		t.Fatal("the guided installer was never prepared")
	}
}

func TestConfigCollect_StepThatWillNotStartStopsLoudlyInStrictMode(t *testing.T) {
	t.Setenv(requireInConsoleEnv, "1")
	prepared := false
	r := collectingRun(t, handoffSeams(&prepared))
	r, cmd := r.update(configStepMsg{err: errors.New("bash: not found")})
	if cmd != nil || prepared {
		t.Fatal("strict mode must never prepare the terminal handoff")
	}
	if r.state != runFailed || !strings.Contains(runScreen(r), "in-console configuration required — terminal handoff refused") {
		t.Fatalf("state = %v, screen:\n%s", r.state, runScreen(r))
	}
}

func TestConfigCollect_RecheckFailureFallsBackToTheHandoff(t *testing.T) {
	t.Setenv(requireInConsoleEnv, "")
	prepared := false
	r := collectingRun(t, handoffSeams(&prepared))
	r, cmd := r.update(configRecheckMsg{err: errors.New("configuration check produced no readiness report")})
	if r.state != runHandoffRunning {
		t.Fatalf("state = %v, want the terminal handoff", r.state)
	}
	cmd()
	if !prepared {
		t.Fatal("the guided installer was never prepared")
	}
}

// v0.4.2 audit A4: the run reached the session through the engine's
// configuration refusal, so it still carries that event and the
// engine's stderr tail. When adopting the collected configuration then
// fails, the stopped screen shows the adoption error, not the earlier
// refusal or its tail.
func TestConfigCollect_AdoptFailureStopsTheRunWithTheReason(t *testing.T) {
	// holdFiles gives the run the two things a failed adoption must let go
	// of, each counting its releases: what the engine run staged (install.sh
	// and the configuration tree) and the configuration session's own
	// handle on the tree.
	holdFiles := func(r *engineRun) (staged, session *int) {
		staged, session = new(int), new(int)
		r.cleanup = func() error { *staged++; return nil }
		r.cfg.plan.cleanup = func() { *session++ }
		return staged, session
	}
	requireReleasedOnce := func(t *testing.T, r engineRun, staged, session *int) {
		t.Helper()
		if *staged != 1 || *session != 1 {
			t.Errorf("released the staged files %d times and the session's tree %d times, want each once", *staged, *session)
		}
		if r.cleanup != nil || r.cfg.plan.cleanup != nil {
			t.Error("a released cleanup is still held, so a later exit would run it again")
		}
	}
	const (
		adoptErr   = "cannot write .env-orbit: permission denied"
		refusal    = "configuration-failure"
		stderrLine = "configure.sh: APP_URL is not set yet"
	)
	for action, title := range map[string]string{"install": "Install stopped", "update": "Update stopped"} {
		t.Run(action, func(t *testing.T) {
			r := startedRun(t, action, engineRunSeams{prepareConfig: planned(true, false)})
			r.state = runStreaming
			r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", refusal, "retry")})
			r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1"), StderrTail: []string{stderrLine}}})
			if r.state != runConfigCollect {
				t.Fatalf("setup: state = %v, want the configuration session", r.state)
			}

			staged, session := holdFiles(&r)
			r, cmd := r.update(configAdoptedMsg{err: errors.New(adoptErr)})
			if cmd != nil {
				t.Fatal("a failed adoption must not retry the engine")
			}
			requireReleasedOnce(t, r, staged, session)
			s := runScreen(r)
			if r.state != runFailed || !strings.Contains(s, title) || !strings.Contains(s, adoptErr) {
				t.Fatalf("state = %v, want %q showing the adoption error %q; screen:\n%s", r.state, title, adoptErr, s)
			}
			for _, earlier := range []string{refusal, stderrLine} {
				if strings.Contains(s, earlier) {
					t.Errorf("the stopped screen shows the earlier %q instead of only the adoption error:\n%s", earlier, s)
				}
			}
		})
	}
	t.Run("session only", func(t *testing.T) {
		r := collectingRun(t, engineRunSeams{})
		staged, session := holdFiles(&r)
		r, cmd := r.update(configAdoptedMsg{err: errors.New("configuration session left no .env-orbit")})
		if cmd != nil {
			t.Fatal("a failed adoption must not retry the engine")
		}
		requireReleasedOnce(t, r, staged, session)
		s := runScreen(r)
		if r.state != runFailed || !strings.Contains(s, "Install stopped") || !strings.Contains(s, "configuration session left no .env-orbit") {
			t.Fatalf("state = %v, screen:\n%s", r.state, s)
		}
	})
}

// #251/#253: the run reached the configuration session through the
// engine's refusal, so it carries that event and the engine's stderr
// tail. When the session cannot start (no tree was handed over) the run
// falls back to the terminal handoff; if preparing that installer then
// fails, the stopped screen shows the preparation error, not the earlier
// refusal or its tail.
func TestConfigCollect_HandoffPreparationFailureReplacesTheStaleRefusal(t *testing.T) {
	const (
		prepareErr = "stage install.sh: disk full"
		refusal    = "configuration-failure"
		stderrLine = "configure.sh: APP_URL is not set yet"
	)
	for action, title := range map[string]string{"install": "Install stopped", "update": "Update stopped"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv(requireInConsoleEnv, "")
			// The engine handed over no configuration tree, so the
			// session cannot be prepared (as in
			// TestEngineRun_NoTreeFromTheEngineFallsBackToTheHandoff).
			prepared := false
			r := startedRun(t, action, handoffSeams(&prepared))
			r, _ = runUpdate(t, r, engineReadyMsg{stream: &engine.Stream{C: make(chan any)}, configTree: t.TempDir()})
			r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", refusal, "retry")})
			r, cmd := runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1"), StderrTail: []string{stderrLine}}})
			if r.state != runConfigCollect || cmd == nil {
				t.Fatalf("setup: state = %v, want the configuration session being prepared", r.state)
			}
			r, _ = runUpdate(t, r, cmd())
			if r.state != runHandoffRunning {
				t.Fatalf("setup: state = %v, want the terminal handoff; screen:\n%s", r.state, runScreen(r))
			}

			r, cmd = runUpdate(t, r, installPreparedMsg{err: errors.New(prepareErr)})
			if cmd != nil {
				t.Fatal("a failed preparation must not start the handoff")
			}
			s := runScreen(r)
			if r.state != runFailed || !strings.Contains(s, title) || !strings.Contains(s, prepareErr) {
				t.Fatalf("state = %v, want %q showing the preparation error %q; screen:\n%s", r.state, title, prepareErr, s)
			}
			for _, earlier := range []string{refusal, stderrLine} {
				if strings.Contains(s, earlier) {
					t.Errorf("the stopped screen shows the earlier %q instead of only the preparation error:\n%s", earlier, s)
				}
			}
		})
	}
}

// #251/#253: same stale refusal and stderr tail, but the guided
// installer is prepared and RUNS, then exits with an error. The stopped
// screen shows the handoff's own failure, not the earlier engine failure.
func TestConfigCollect_FailedHandoffShowsItsOwnResultNotTheEarlierFailure(t *testing.T) {
	const (
		handoffErr = "install.sh: exit status 3"
		refusal    = "configuration-failure"
		stderrLine = "configure.sh: APP_URL is not set yet"
	)
	for action, title := range map[string]string{"install": "Install stopped", "update": "Update stopped"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv(requireInConsoleEnv, "")
			prepared := false
			r := startedRun(t, action, handoffSeams(&prepared))
			r, _ = runUpdate(t, r, engineReadyMsg{stream: &engine.Stream{C: make(chan any)}, configTree: t.TempDir()})
			r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", refusal, "retry")})
			r, cmd := runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1"), StderrTail: []string{stderrLine}}})
			if r.state != runConfigCollect || cmd == nil {
				t.Fatalf("setup: state = %v, want the configuration session being prepared", r.state)
			}
			r, _ = runUpdate(t, r, cmd())
			if r.state != runHandoffRunning {
				t.Fatalf("setup: state = %v, want the terminal handoff; screen:\n%s", r.state, runScreen(r))
			}

			// Preparation succeeds and the installer runs...
			r, cmd = runUpdate(t, r, installPreparedMsg{cmd: exec.Command("true"), cleanup: func() error { return nil }})
			if cmd == nil {
				t.Fatal("setup: no terminal handoff was started")
			}
			// ...then exits with an error.
			r, _ = runUpdate(t, r, installFinishedMsg{err: errors.New(handoffErr)})
			s := runScreen(r)
			if r.state != runFailed || r.Succeeded || !strings.Contains(s, title) || !strings.Contains(s, handoffErr) {
				t.Fatalf("state = %v, want %q showing the handoff's own failure %q; screen:\n%s", r.state, title, handoffErr, s)
			}
			for _, earlier := range []string{refusal, stderrLine} {
				if strings.Contains(s, earlier) {
					t.Errorf("the stopped screen shows the earlier %q instead of only the handoff's failure:\n%s", earlier, s)
				}
			}
		})
	}
}

func TestConfigCollect_MessagesAfterCancelBelongToNothing(t *testing.T) {
	r := collectingRun(t, engineRunSeams{})
	s := sleepingStream(t)
	r.cfg.stream = s
	r.cfg.stdin = &recordingStdin{}
	r, _ = r.update(key(tea.KeyEsc)) // the session was cancelled: back to the menu
	if !r.Done || !r.WantsMenu {
		t.Fatalf("Done=%v WantsMenu=%v after Esc, want back to the menu", r.Done, r.WantsMenu)
	}
	state := r.state
	for _, msg := range []tea.Msg{
		configStepMsg{err: errors.New("late")},
		configAdoptedMsg{err: errors.New("late")},
		configStreamMsg{msg: engine.DoneMsg{}},
	} {
		var cmd tea.Cmd
		r, cmd = r.update(msg)
		if cmd != nil || r.state != state || r.state == runFailed || !r.Done || !r.WantsMenu || r.Succeeded {
			t.Fatalf("%T after cancel changed the run: state = %v Done=%v WantsMenu=%v Succeeded=%v", msg, r.state, r.Done, r.WantsMenu, r.Succeeded)
		}
	}
}

func TestConfigCollect_TypingEditsTheAnswerAndSubmitsIt(t *testing.T) {
	stdin := &recordingStdin{}
	r := collectingRun(t, engineRunSeams{})
	r.cfg.stream = &engine.Stream{C: make(chan any)}
	r.cfg.stdin = stdin
	if !strings.Contains(runScreen(r), "talking to the engine") {
		t.Fatalf("before a prompt:\n%s", runScreen(r))
	}
	// No prompt yet: typing goes nowhere.
	r, _ = r.update(runeKey('h'))
	if !r.cfg.session.input.empty() {
		t.Fatal("typing before a prompt was captured")
	}

	r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: "prompt field=OIDC_CLIENT_ID kind=text required=true attempt=1"}})
	for _, k := range []tea.KeyPressMsg{runeKey('m'), runeKey('y'), key(tea.KeySpace), runeKey('i'), runeKey('x'), key(tea.KeyBackspace), runeKey('d')} {
		r, _ = r.update(k)
	}
	if !strings.Contains(runScreen(r), "my id▏") {
		t.Fatalf("the typed answer should read 'my id':\n%s", runScreen(r))
	}
	r, _ = r.update(key(tea.KeyEnter))
	if stdin.String() != "my id\n" {
		t.Fatalf("engine received %q, want the edited answer", stdin.String())
	}
	if r.cfg.origin != "" {
		t.Fatal("only APP_URL is remembered as the origin")
	}

	// Backspace on an empty answer is harmless.
	r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: "prompt field=OIDC_ISSUER kind=url required=true attempt=1"}})
	r, _ = r.update(key(tea.KeyBackspace))
	if !r.cfg.session.input.empty() {
		t.Fatal("backspace on empty input produced input")
	}
}

func TestConfigCollect_EventLinesKeepTheSessionReading(t *testing.T) {
	r := collectingRun(t, engineRunSeams{})
	r.cfg.stream = &engine.Stream{C: make(chan any)}
	// The shared reader keeps reading whatever the handler returns
	// (#206), so what an event line must not do is end the session.
	r, cmd := r.update(configStreamMsg{msg: engine.EventMsg{Event: engine.Event{Phase: "configuration"}}})
	if cmd != nil || r.state != runConfigCollect || r.cfg.stream == nil {
		t.Fatal("an event line stopped the configuration session")
	}
}

func TestConfigSignInMode_UnboundKeyChangesNothing(t *testing.T) {
	r := signInModeRun()
	r, cmd := r.handleKey(runeKey('s'))
	if cmd != nil || r.state != runConfigSignInMode || r.cfg.signIn.sel != 0 {
		t.Fatal("an unbound key acted on the sign-in screen")
	}
}

func TestConfigCollect_RejectionReasonsInWords(t *testing.T) {
	reasons := map[string]string{
		"empty":              "can't be empty",
		"invalid-characters": "contains whitespace or control characters",
		"not-https":          "must start with https://",
		"not-absolute-url":   "isn't a plain https:// address",
		"forbidden-host":     "loopback and placeholder hosts aren't allowed",
		"too-large":          "too large",
		"future-reason":      "future-reason",
	}
	for reason, want := range reasons {
		r := collectingRun(t, engineRunSeams{})
		r.cfg.stream = &engine.Stream{C: make(chan any)}
		r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: "prompt-reject field=APP_URL reason=" + reason}})
		r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: "prompt field=APP_URL kind=url required=true attempt=2"}})
		s := runScreen(r)
		if !strings.Contains(s, want) || !strings.Contains(s, "attempt 2 of 3") {
			t.Errorf("reason %q should read %q with the attempt count:\n%s", reason, want, s)
		}
	}
}

func TestPromptWords_SafeBatchConfirmation(t *testing.T) {
	label, hint, notes := promptWords("safe-batch", "")
	if label != "Run the safe repairs?" || hint != "y to proceed" || notes != nil {
		t.Fatalf("safe-batch prompt = %q / %q / %v", label, hint, notes)
	}
}

// configTree is a staged configuration tree whose configure.sh --check
// reports the given lines.
func configTree(t *testing.T, checkReport string) string {
	t.Helper()
	dir := t.TempDir()
	script := "printf '%s\\n' " + shellQuoteLines(checkReport) + "\n"
	testutil.StageTree(t, dir, 0o700, testutil.File{Path: "scripts/configure.sh", Body: script, Mode: 0o700})
	return dir
}

func shellQuoteLines(report string) string {
	var parts []string
	for _, line := range strings.Split(report, "\n") {
		parts = append(parts, "'"+line+"'")
	}
	return strings.Join(parts, " ")
}

func TestConfigCollect_InitCompletionRechecksWithTheRealCheck(t *testing.T) {
	r := collectingRun(t, engineRunSeams{}) // no recheck seam
	r.cfg.plan = configPlan{configTree: configTree(t, "ready APP_URL\nmissing OIDC_CLIENT_SECRET"), needInit: true}
	r.cfg.step = deploy.ConfigStepInit
	r.cfg.stream = &engine.Stream{C: make(chan any)}
	r, cmd := r.update(configStreamMsg{msg: engine.DoneMsg{}})
	if r.cfg.plan.needInit {
		t.Fatal("a completed --init is still marked owed")
	}
	msg, ok := cmd().(configRecheckMsg)
	if !ok || msg.err != nil {
		t.Fatalf("recheck: %#v", msg)
	}
	if !msg.check.NeedsSecret() {
		t.Fatalf("the real check's report was not read: %+v", msg.check)
	}
}

func TestConfigCollect_NothingOwedAdoptsIntoTheTargetAndRetries(t *testing.T) {
	tree := t.TempDir()
	if err := os.WriteFile(filepath.Join(tree, ".env-orbit"), []byte("APP_URL=https://orbit.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	engineStarts := 0
	r := collectingRun(t, engineRunSeams{ // no adopt seam
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			engineStarts++
			return nil, nil, errors.New("stop after the retry starts")
		},
	})
	r, cmd := r.update(configPlanMsg{plan: configPlan{configTree: tree, cleanup: func() {}}})
	adopted, ok := cmd().(configAdoptedMsg)
	if !ok || adopted.err != nil {
		t.Fatalf("adopt: %#v", adopted)
	}
	got, err := os.ReadFile(filepath.Join(r.targetDir, ".env-orbit"))
	if err != nil || !strings.Contains(string(got), "https://orbit.example.test") {
		t.Fatalf("the configuration did not land in the target: %q, %v", got, err)
	}

	r, cmd = r.update(adopted)
	if r.state != runPreparing || cmd == nil {
		t.Fatalf("state = %v; adoption should restart the engine", r.state)
	}
	cmd()
	if engineStarts != 1 {
		t.Fatalf("engine started %d times on the retry, want once", engineStarts)
	}
}
