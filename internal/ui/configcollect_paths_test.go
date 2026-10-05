package ui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
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

func collectScreen(r engineRun) string { return stripANSI(r.view(80, 26)) }

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
	if r.state != runFailed || !strings.Contains(collectScreen(r), "in-console configuration required — terminal handoff refused") {
		t.Fatalf("state = %v, screen:\n%s", r.state, collectScreen(r))
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

func TestConfigCollect_AdoptFailureStopsTheRunWithTheReason(t *testing.T) {
	r := collectingRun(t, engineRunSeams{})
	r, cmd := r.update(configAdoptedMsg{err: errors.New("configuration session left no .env-orbit")})
	if cmd != nil {
		t.Fatal("a failed adoption must not retry the engine")
	}
	s := collectScreen(r)
	if r.state != runFailed || !strings.Contains(s, "Installation stopped") || !strings.Contains(s, "configuration session left no .env-orbit") {
		t.Fatalf("state = %v, screen:\n%s", r.state, s)
	}
}

func TestConfigCollect_MessagesAfterCancelBelongToNothing(t *testing.T) {
	r := collectingRun(t, engineRunSeams{})
	r.state = runConfigPrompt // the session was cancelled
	for _, msg := range []tea.Msg{
		configStepMsg{err: errors.New("late")},
		configAdoptedMsg{err: errors.New("late")},
		configStreamMsg{msg: engine.DoneMsg{}},
	} {
		var cmd tea.Cmd
		r, cmd = r.update(msg)
		if cmd != nil || r.state != runConfigPrompt {
			t.Fatalf("%T after cancel changed the run: state = %v", msg, r.state)
		}
	}
}

func TestConfigCollect_TypingEditsTheAnswerAndSubmitsIt(t *testing.T) {
	stdin := &recordingStdin{}
	r := collectingRun(t, engineRunSeams{})
	r.cfg.stream = &engine.Stream{C: make(chan any)}
	r.cfg.stdin = stdin
	if !strings.Contains(collectScreen(r), "talking to the engine…") {
		t.Fatalf("before a prompt:\n%s", collectScreen(r))
	}
	// No prompt yet: typing goes nowhere.
	r, _ = r.update(runeKey('h'))
	if len(r.cfg.input) != 0 {
		t.Fatal("typing before a prompt was captured")
	}

	r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: "prompt field=OIDC_CLIENT_ID kind=text required=true attempt=1"}})
	for _, k := range []tea.KeyPressMsg{runeKey('m'), runeKey('y'), key(tea.KeySpace), runeKey('i'), runeKey('x'), key(tea.KeyBackspace), runeKey('d')} {
		r, _ = r.update(k)
	}
	if !strings.Contains(collectScreen(r), "my id▏") {
		t.Fatalf("the typed answer should read 'my id':\n%s", collectScreen(r))
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
	if len(r.cfg.input) != 0 {
		t.Fatal("backspace on empty input produced input")
	}
}

func TestConfigCollect_EventLinesKeepTheSessionReading(t *testing.T) {
	r := collectingRun(t, engineRunSeams{})
	r.cfg.stream = &engine.Stream{C: make(chan any)}
	r, cmd := r.update(configStreamMsg{msg: engine.EventMsg{Event: engine.Event{Phase: "configuration"}}})
	if cmd == nil || r.state != runConfigCollect {
		t.Fatal("an event line stopped the configuration session")
	}
}

func TestPumpConfig_ClosedStreamDeliversNothing(t *testing.T) {
	ch := make(chan any)
	close(ch)
	if msg := pumpConfig(&engine.Stream{C: ch})(); msg != nil {
		t.Fatalf("a closed stream produced %#v", msg)
	}
}

func TestConfigSignInMode_UnboundKeyChangesNothing(t *testing.T) {
	r := signInModeRun()
	r, cmd := r.handleKey(runeKey('s'))
	if cmd != nil || r.state != runConfigSignInMode || r.cfg.authModeSel != 0 {
		t.Fatal("an unbound key acted on the sign-in screen")
	}
}

func TestConfigCollect_RejectionReasonsInWords(t *testing.T) {
	reasons := map[string]string{
		"empty":              "cannot be empty",
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
		s := collectScreen(r)
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
	if err := os.MkdirAll(filepath.Join(dir, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	script := "printf '%s\\n' " + shellQuoteLines(checkReport) + "\n"
	if err := os.WriteFile(filepath.Join(dir, "scripts", "configure.sh"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
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
	r.cfg.plan = configPlan{treeDir: configTree(t, "ready APP_URL\nmissing OIDC_CLIENT_SECRET"), needInit: true}
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
	r, cmd := r.update(configPlanMsg{plan: configPlan{treeDir: tree, cleanup: func() {}}})
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

// configSource serves a configuration tree the way Orbit's script
// source does.
func configSource(t *testing.T, files map[string]string) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	t.Setenv("ORBIT_LAUNCHER_INSTALL_SCRIPT_URL", server.URL+"/scripts/install.sh")
}

func TestDefaultPrepareConfig_StagesTheTreeAndReportsWhatIsOwed(t *testing.T) {
	configSource(t, map[string]string{
		"/scripts/configure.sh": "#!/usr/bin/env bash\nprintf 'missing APP_URL\\nmissing OIDC_CLIENT_SECRET\\nmissing SMTP_HOST\\nready ORBIT_IMAGE\\n'; exit 1\n",
		"/.env-orbit.example":   "APP_URL=\n",
	})
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, ".env-orbit"), []byte("ORBIT_IMAGE=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := defaultPrepareConfig(context.Background(), target)
	if msg.err != nil {
		t.Fatalf("prepare: %v", msg.err)
	}
	defer msg.plan.cleanup()
	if !msg.plan.needInit || !msg.plan.needSecret {
		t.Fatalf("plan = %+v, want init and secret owed", msg.plan)
	}
	if len(msg.plan.unfixable) != 1 || msg.plan.unfixable[0] != "SMTP_HOST" {
		t.Fatalf("unfixable = %v, want [SMTP_HOST]", msg.plan.unfixable)
	}
	// The target's own configuration was imported into the tree, so the
	// check judged this deployment rather than a blank one.
	if got, _ := os.ReadFile(filepath.Join(msg.plan.treeDir, ".env-orbit")); string(got) != "ORBIT_IMAGE=x\n" {
		t.Fatalf("target configuration not imported: %q", got)
	}
}

func TestDefaultPrepareConfig_Failures(t *testing.T) {
	t.Run("no configure.sh", func(t *testing.T) {
		configSource(t, map[string]string{"/.env-orbit.example": "APP_URL=\n"})
		if msg := defaultPrepareConfig(context.Background(), t.TempDir()); msg.err == nil || !strings.Contains(msg.err.Error(), "configure.sh") {
			t.Fatalf("err = %v, want a configure.sh fetch error", msg.err)
		}
	})
	t.Run("check with no report", func(t *testing.T) {
		configSource(t, map[string]string{
			"/scripts/configure.sh": "#!/usr/bin/env bash\necho 'Usage: configure.sh' >&2; exit 2\n",
			"/.env-orbit.example":   "APP_URL=\n",
		})
		if msg := defaultPrepareConfig(context.Background(), t.TempDir()); msg.err == nil || !strings.Contains(msg.err.Error(), "configuration check failed") {
			t.Fatalf("err = %v, want the check's failure", msg.err)
		}
	})
	t.Run("unreadable target configuration", func(t *testing.T) {
		configSource(t, map[string]string{
			"/scripts/configure.sh": "#!/usr/bin/env bash\necho ready APP_URL\n",
			"/.env-orbit.example":   "APP_URL=\n",
		})
		target := t.TempDir()
		// A directory where the configuration file should be.
		if err := os.Mkdir(filepath.Join(target, ".env-orbit"), 0o700); err != nil {
			t.Fatal(err)
		}
		if msg := defaultPrepareConfig(context.Background(), target); msg.err == nil {
			t.Fatal("a target whose .env-orbit cannot be read should fail the prepare")
		}
	})
}
