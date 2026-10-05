package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// #190: in-console configuration runs configure.sh only from the tree
// install.sh handed over in ORBIT_LAUNCHER_CONFIG_TREE — never from a
// download — and without one it falls back to the terminal handoff.

// handedOverTree is a configure tree as install.sh leaves it on a
// configuration refusal (ai/orbit#1225), with the given configure.sh.
func handedOverTree(t *testing.T, configure string) string {
	t.Helper()
	tree := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tree, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "scripts", "configure.sh"), []byte(configure), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".env-orbit.example"), []byte("APP_URL=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return tree
}

func TestDefaultPrepareConfig_RunsInTheHandedOverTreeAndReportsWhatIsOwed(t *testing.T) {
	requests := countingScriptSource(t)
	tree := handedOverTree(t, "#!/usr/bin/env bash\nprintf 'missing APP_URL\\nmissing OIDC_CLIENT_SECRET\\nmissing SMTP_HOST\\nready ORBIT_IMAGE\\n'; exit 1\n")
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, ".env-orbit"), []byte("ORBIT_IMAGE=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	msg := defaultPrepareConfig(context.Background(), target, tree)
	if msg.err != nil {
		t.Fatalf("prepare: %v", msg.err)
	}
	if msg.plan.treeDir != tree {
		t.Fatalf("treeDir = %q, want the handed-over tree %q", msg.plan.treeDir, tree)
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("preparing configuration made %d HTTP requests; it must fetch nothing", n)
	}
	if !msg.plan.needInit || !msg.plan.needSecret {
		t.Fatalf("plan = %+v, want init and secret owed", msg.plan)
	}
	if len(msg.plan.unfixable) != 1 || msg.plan.unfixable[0] != "SMTP_HOST" {
		t.Fatalf("unfixable = %v, want [SMTP_HOST]", msg.plan.unfixable)
	}
	// The target's own configuration was imported into the tree, so the
	// check judged this deployment rather than a blank one.
	if got, _ := os.ReadFile(filepath.Join(tree, ".env-orbit")); string(got) != "ORBIT_IMAGE=x\n" {
		t.Fatalf("target configuration not imported: %q", got)
	}
	// Ending the session clears what it collected but leaves the
	// verified scripts for the run's own cleanup.
	msg.plan.cleanup()
	if _, err := os.Lstat(filepath.Join(tree, ".env-orbit")); !os.IsNotExist(err) {
		t.Error("the session's .env-orbit survived its end")
	}
	if _, err := os.Lstat(filepath.Join(tree, "scripts", "configure.sh")); err != nil {
		t.Errorf("the session's end removed the verified configure.sh: %v", err)
	}
}

func TestDefaultPrepareConfig_Failures(t *testing.T) {
	t.Run("install.sh handed over nothing", func(t *testing.T) {
		requests := countingScriptSource(t)
		msg := defaultPrepareConfig(context.Background(), t.TempDir(), t.TempDir())
		if !errors.Is(msg.err, deploy.ErrNoConfigTree) {
			t.Fatalf("err = %v, want ErrNoConfigTree", msg.err)
		}
		if n := requests.Load(); n != 0 {
			t.Errorf("an empty tree made %d HTTP requests; there is no fallback fetch", n)
		}
	})
	t.Run("no tree at all", func(t *testing.T) {
		if msg := defaultPrepareConfig(context.Background(), t.TempDir(), ""); !errors.Is(msg.err, deploy.ErrNoConfigTree) {
			t.Fatalf("err = %v, want ErrNoConfigTree", msg.err)
		}
	})
	t.Run("check with no report", func(t *testing.T) {
		tree := handedOverTree(t, "#!/usr/bin/env bash\necho 'Usage: configure.sh' >&2; exit 2\n")
		if msg := defaultPrepareConfig(context.Background(), t.TempDir(), tree); msg.err == nil || !strings.Contains(msg.err.Error(), "configuration check failed") {
			t.Fatalf("err = %v, want the check's failure", msg.err)
		}
	})
	t.Run("unreadable target configuration", func(t *testing.T) {
		tree := handedOverTree(t, "#!/usr/bin/env bash\necho ready APP_URL\n")
		target := t.TempDir()
		// A directory where the configuration file should be.
		if err := os.Mkdir(filepath.Join(target, ".env-orbit"), 0o700); err != nil {
			t.Fatal(err)
		}
		if msg := defaultPrepareConfig(context.Background(), target, tree); msg.err == nil {
			t.Fatal("a target whose .env-orbit cannot be read should fail the prepare")
		}
	})
}

// refusedRunWithTree is an engine run whose engine handed over tree and
// then refused for missing configuration. cleaned counts the run's
// cleanup calls.
func refusedRunWithTree(t *testing.T, seams engineRunSeams, tree string, cleaned *int) engineRun {
	t.Helper()
	r := startedRun(t, "install", seams)
	r, _ = runUpdate(t, r, engineReadyMsg{
		stream:     &engine.Stream{C: make(chan any)},
		configTree: tree,
		cleanup:    func() error { *cleaned++; return nil },
	})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", "configuration-failure", "retry")})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1}})
	if r.state != runConfigPrompt {
		t.Fatalf("state = %v, want the configuration prompt", r.state)
	}
	return r
}

// The engine's tree survives its refusal — the run's cleanup waits for
// the configuration session — and is what the session is prepared in.
func TestEngineRun_ConfigSessionRunsInTheTreeTheEngineHandedOver(t *testing.T) {
	tree := t.TempDir()
	var gotTree string
	cleaned := 0
	r := refusedRunWithTree(t, engineRunSeams{
		prepareConfig: func(_ context.Context, _ string, configTree string) configPlanMsg {
			gotTree = configTree
			return configPlanMsg{err: errors.New("stop here")}
		},
		prepareInstall: handoffSeams(new(bool)).prepareInstall,
	}, tree, &cleaned)
	if cleaned != 0 {
		t.Fatal("the run's cleanup removed the configure tree before the session could use it")
	}

	r, cmd := r.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) // Continue — guided configuration
	if r.state != runConfigCollect {
		t.Fatalf("state = %v, want the configuration session", r.state)
	}
	r, _ = runUpdate(t, r, cmd())
	if gotTree != tree {
		t.Fatalf("configuration prepared in %q, want the engine's tree %q", gotTree, tree)
	}
	// No session there: the terminal handoff, and the refused run's
	// files are released before the handoff stages its own.
	if r.state != runHandoffRunning {
		t.Fatalf("state = %v, want the terminal handoff", r.state)
	}
	if cleaned != 1 {
		t.Fatalf("run cleanup ran %d times, want once", cleaned)
	}
}

// Without a tree from install.sh the session never starts and the flow
// takes the terminal handoff — no fetch in between.
func TestEngineRun_NoTreeFromTheEngineFallsBackToTheHandoff(t *testing.T) {
	t.Setenv(requireInConsoleEnv, "")
	requests := countingScriptSource(t)
	prepared := false
	cleaned := 0
	r := refusedRunWithTree(t, handoffSeams(&prepared), t.TempDir(), &cleaned) // empty: an install.sh without ai/orbit#1225
	r, cmd := r.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	r, cmd = runUpdate(t, r, cmd())
	if r.state != runHandoffRunning {
		t.Fatalf("state = %v, want the terminal handoff", r.state)
	}
	cmd()
	if !prepared {
		t.Fatal("the guided installer was never prepared")
	}
	if n := requests.Load(); n != 0 {
		t.Errorf("the fallback made %d HTTP requests besides install.sh's own handoff", n)
	}
}

// Cancelling the session keeps the tree for another attempt; leaving
// the refusal for the menu releases it.
func TestEngineRun_TreeOutlivesACancelledSessionAndGoesWithTheRun(t *testing.T) {
	cleaned := 0
	r := refusedRunWithTree(t, engineRunSeams{
		prepareConfig: planned(true, false),
	}, t.TempDir(), &cleaned)
	r, cmd := r.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	r, _ = runUpdate(t, r, cmd())
	if r.state != runConfigSignInMode {
		t.Fatalf("state = %v, want the sign-in-mode screen", r.state)
	}
	r, _ = r.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if r.state != runConfigPrompt || cleaned != 0 {
		t.Fatalf("state = %v cleaned = %d; a cancelled session must keep the run's tree", r.state, cleaned)
	}
	r.menuSel = 1 // Menu
	r, _ = r.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !r.Done || !r.WantsMenu {
		t.Fatalf("Done=%v WantsMenu=%v, want back to the menu", r.Done, r.WantsMenu)
	}
	if cleaned != 1 {
		t.Fatalf("run cleanup ran %d times on leaving, want once", cleaned)
	}
}

// The retry after adoption is a new engine run with a new tree; the
// refused run's is released first.
func TestEngineRun_RetryReleasesTheRefusedRunsTree(t *testing.T) {
	cleaned := 0
	r := refusedRunWithTree(t, engineRunSeams{
		prepareConfig: planned(true, false),
	}, t.TempDir(), &cleaned)
	r.state = runConfigCollect
	r, _ = runUpdate(t, r, configAdoptedMsg{})
	if r.state != runPreparing {
		t.Fatalf("state = %v, want the retried engine preparing", r.state)
	}
	if cleaned != 1 {
		t.Fatalf("run cleanup ran %d times before the retry, want once", cleaned)
	}
}

func TestEngineRun_CtrlCOnTheRefusalReleasesTheTree(t *testing.T) {
	cleaned := 0
	r := refusedRunWithTree(t, engineRunSeams{}, t.TempDir(), &cleaned)
	_, cmd := r.handleKey(ctrlC())
	if !isQuit(cmd) {
		t.Fatal("Ctrl+C on the refusal did not quit")
	}
	if cleaned != 1 {
		t.Fatalf("run cleanup ran %d times on Ctrl+C, want once", cleaned)
	}
}
