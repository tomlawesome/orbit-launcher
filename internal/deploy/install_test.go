package deploy

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildInstallCommand_StagesScriptAndReturnsARunnableCommand(t *testing.T) {
	script := []byte("#!/usr/bin/env bash\necho 'from stdout'\n")
	dir := t.TempDir()

	cmd, cleanup, err := BuildInstallCommand(script, dir)
	if err != nil {
		t.Fatalf("BuildInstallCommand: %v", err)
	}
	defer cleanup()

	if cmd.Dir != dir {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, dir)
	}
	if len(cmd.Args) != 2 {
		t.Fatalf("cmd.Args = %v, want exactly [bash, <script path>]", cmd.Args)
	}
	staged, err := os.ReadFile(cmd.Args[1])
	if err != nil {
		t.Fatalf("read staged script: %v", err)
	}
	if !bytes.Equal(staged, script) {
		t.Errorf("staged script content = %q, want %q", staged, script)
	}
}

// TestBuildInstallCommand_NeverDetachesOrRedirectsStdio is the load-
// bearing test for issue #51's whole point: install.sh must see a real
// controlling terminal so its own scripts/configure.sh — the single
// source of truth for what configuration it needs — can run its
// guided prompts. Any SysProcAttr detachment, or any Stdin/Stdout/Stderr
// already set here, would prevent tea.ExecProcess (see internal/ui)
// from wiring the real terminal in, since it only fills in fields that
// are still nil.
func TestBuildInstallCommand_NeverDetachesOrRedirectsStdio(t *testing.T) {
	cmd, cleanup, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), t.TempDir())
	if err != nil {
		t.Fatalf("BuildInstallCommand: %v", err)
	}
	defer cleanup()

	if cmd.SysProcAttr != nil {
		t.Error("expected SysProcAttr to be nil — install.sh must not be detached from a controlling terminal")
	}
	if cmd.Stdin != nil {
		t.Error("expected Stdin to be left nil for tea.ExecProcess to wire up")
	}
	if cmd.Stdout != nil {
		t.Error("expected Stdout to be left nil for tea.ExecProcess to wire up")
	}
	if cmd.Stderr != nil {
		t.Error("expected Stderr to be left nil for tea.ExecProcess to wire up")
	}
}

func TestBuildInstallCommand_RunsInTargetDirAndPropagatesExitCode(t *testing.T) {
	dir := t.TempDir()
	script := []byte("#!/usr/bin/env bash\n[[ \"$(pwd)\" == \"$1\" ]] || exit 7\n")

	cmd, cleanup, err := BuildInstallCommand(script, dir)
	if err != nil {
		t.Fatalf("BuildInstallCommand: %v", err)
	}
	defer cleanup()

	// Bare exec.Cmd.Run() (not tea.ExecProcess) connects unset streams to
	// /dev/null, which is fine for this pure exit-code check.
	cmd.Args = append(cmd.Args, dir)
	if err := cmd.Run(); err != nil {
		t.Errorf("expected the script to see its own cmd.Dir as pwd, got: %v", err)
	}
}

func TestBuildInstallCommand_NonZeroExitIsAnError(t *testing.T) {
	script := []byte("#!/usr/bin/env bash\nexit 3\n")
	cmd, cleanup, err := BuildInstallCommand(script, t.TempDir())
	if err != nil {
		t.Fatalf("BuildInstallCommand: %v", err)
	}
	defer cleanup()

	if err := cmd.Run(); err == nil {
		t.Error("expected a non-zero exit to be reported as an error")
	}
}

func TestBuildInstallCommand_CleanupRemovesTheStagedFile(t *testing.T) {
	cmd, cleanup, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), t.TempDir())
	if err != nil {
		t.Fatalf("BuildInstallCommand: %v", err)
	}
	path := cmd.Args[1]
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected the staged script to exist before cleanup: %v", err)
	}

	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected the staged script to be removed after cleanup, stat err = %v", err)
	}
}

// TestBuildEngineCommand_DetachedPlainModeRun is BuildEngineCommand's
// counterpart to TestBuildInstallCommand_NeverDetachesOrRedirectsStdio:
// the mission console's engine run must be the exact opposite of the
// handoff — session-detached (no controlling terminal, so the engine's
// non-interactive contract engages and it can never prompt through the
// TUI) and flagged --plain --<action> so a contract-era install.sh
// selects the event stream and skips its own menus.
func TestBuildEngineCommand_DetachedPlainModeRun(t *testing.T) {
	dir := t.TempDir()
	cmd, cleanup, err := BuildEngineCommand([]byte("#!/usr/bin/env bash\n"), dir, "update")
	if err != nil {
		t.Fatalf("BuildEngineCommand: %v", err)
	}
	defer cleanup()

	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Error("expected Setsid — the engine run must have no controlling terminal")
	}
	if cmd.Dir != dir {
		t.Errorf("cmd.Dir = %q, want %q", cmd.Dir, dir)
	}
	got := strings.Join(cmd.Args[2:], " ")
	if got != "--plain --update" {
		t.Errorf("engine args = %q, want %q", got, "--plain --update")
	}
}

func TestBuildEngineCommand_RejectsUnknownActions(t *testing.T) {
	if _, _, err := BuildEngineCommand([]byte("#!/usr/bin/env bash\n"), t.TempDir(), "obliterate"); err == nil {
		t.Fatal("expected an unknown action to be rejected")
	}
}

// Running out of space while staging install.sh fails the handoff and
// leaves no partial script behind in the temp directory.
func TestBuildInstallCommand_DiskFullWhileStagingLeavesNoScriptBehind(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	script := []byte("#!/usr/bin/env bash\n# " + strings.Repeat("x", 4096) + "\n")

	// The child inherits TMPDIR, so a partial script would land in tmp.
	errText, returnedCommand := runFileSizeLimited(t, "build-install", map[string]string{
		"ORBIT_DEPLOY_HELPER_SCRIPT": string(script), "ORBIT_DEPLOY_HELPER_TARGET": t.TempDir(),
	})
	requireUnlimitedWrites(t)
	if !strings.Contains(errText, "stage install.sh") {
		t.Fatalf("expected a stage-install.sh error, got %q", errText)
	}
	if returnedCommand {
		t.Error("a failed staging still returned a command to run")
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("a partial install.sh was left behind: %v", left)
	}
}

func TestBuildInstallCommand_ErrorsIfTargetDirDoesNotExist(t *testing.T) {
	cmd, cleanup, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), "/nonexistent/orbit-launcher-test-dir")
	if err != nil {
		t.Fatalf("BuildInstallCommand: %v", err)
	}
	defer cleanup()

	err = cmd.Run()
	if err == nil {
		t.Error("expected running against a nonexistent directory to fail")
	}
	if !strings.Contains(err.Error(), "chdir") && !strings.Contains(err.Error(), "no such file") {
		t.Logf("got error (informational, not asserting exact wording): %v", err)
	}
}

// If install.sh can't be staged there is nothing to run: no command and
// no cleanup come back, so a caller can't run a half-built handoff.
func TestBuildInstallCommand_UnstageableScriptReturnsNoCommand(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))

	cmd, cleanup, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "stage install.sh") {
		t.Fatalf("expected a staging error, got %v", err)
	}
	if cmd != nil || cleanup != nil {
		t.Errorf("got cmd=%v cleanup=%v, want neither", cmd, cleanup != nil)
	}
}

func TestBuildEngineCommand_UnstageableScriptReturnsNoCommand(t *testing.T) {
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))

	cmd, cleanup, err := BuildEngineCommand([]byte("#!/usr/bin/env bash\n"), t.TempDir(), "install")
	if err == nil || !strings.Contains(err.Error(), "stage install.sh") {
		t.Fatalf("expected a staging error, got %v", err)
	}
	if cmd != nil || cleanup != nil {
		t.Errorf("got cmd=%v cleanup=%v, want neither", cmd, cleanup != nil)
	}
}

// configTreeEnv returns the ORBIT_LAUNCHER_CONFIG_TREE value cmd hands
// install.sh, and whether it is set at all.
func configTreeEnv(cmd *exec.Cmd) (string, bool) {
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "ORBIT_LAUNCHER_CONFIG_TREE="); ok {
			return v, true
		}
	}
	return "", false
}

// #190: install.sh gets a fresh, private, empty directory to copy the
// verified configure tree into (ai/orbit#1225), and the run's cleanup
// removes it with whatever install.sh left there.
func TestBuildInstallCommand_HandsInstallAFreshPrivateConfigTree(t *testing.T) {
	cmd, cleanup, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), t.TempDir())
	if err != nil {
		t.Fatalf("BuildInstallCommand: %v", err)
	}
	tree, ok := configTreeEnv(cmd)
	if !ok || tree == "" {
		cleanup()
		t.Fatalf("ORBIT_LAUNCHER_CONFIG_TREE not set in the engine's environment: %v", cmd.Env)
	}
	if got := ConfigTreeDir(cmd); got != tree {
		t.Errorf("ConfigTreeDir = %q, want %q", got, tree)
	}
	info, err := os.Lstat(tree)
	if err != nil {
		t.Fatalf("config tree: %v", err)
	}
	if !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Errorf("config tree mode = %v, want a 0700 directory", info.Mode())
	}
	if entries, _ := os.ReadDir(tree); len(entries) != 0 {
		t.Errorf("config tree is not empty: %v", entries)
	}

	// install.sh fills it on a configuration refusal; cleanup still
	// removes all of it.
	if err := os.MkdirAll(filepath.Join(tree, "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tree, "scripts", "configure.sh"), []byte("#!/bin/bash\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Lstat(tree); !os.IsNotExist(err) {
		t.Errorf("cleanup left the config tree behind: %v", err)
	}
}

func TestBuildInstallCommand_EachRunGetsItsOwnConfigTree(t *testing.T) {
	first, cleanupFirst, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupFirst()
	second, cleanupSecond, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupSecond()
	if ConfigTreeDir(first) == ConfigTreeDir(second) {
		t.Fatalf("two runs share the config tree %q", ConfigTreeDir(first))
	}
}

// The variable reaches install.sh alongside the launcher's own
// environment, which must survive: the live suite's
// COMPOSE_PROJECT_NAME and the job's ORBIT_CHANNEL reach the engine
// only because nothing scrubs them.
func TestBuildEngineCommand_EngineSeesTheConfigTreeAndTheInheritedEnvironment(t *testing.T) {
	t.Setenv("ORBIT_LAUNCHER_TEST_INHERITED", "kept")
	script := []byte("#!/usr/bin/env bash\n[[ -d \"$ORBIT_LAUNCHER_CONFIG_TREE\" ]] || exit 7\n[[ \"$ORBIT_LAUNCHER_TEST_INHERITED\" == kept ]] || exit 8\n")
	cmd, cleanup, err := BuildEngineCommand(script, t.TempDir(), "install")
	if err != nil {
		t.Fatalf("BuildEngineCommand: %v", err)
	}
	defer cleanup()
	if err := cmd.Run(); err != nil {
		t.Fatalf("engine did not see its config tree and inherited environment: %v", err)
	}
}

// A run that could not be built leaves no config tree behind either.
func TestBuildInstallCommand_UnstageableScriptLeavesNoConfigTree(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	if err := os.Chmod(tmp, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(tmp, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions don't block writes")
	}
	if _, _, err := BuildInstallCommand([]byte("#!/usr/bin/env bash\n"), t.TempDir()); err == nil {
		t.Fatal("expected a staging error in an unwritable temp dir")
	}
	if left, _ := os.ReadDir(tmp); len(left) != 0 {
		t.Errorf("a failed build left %v behind", left)
	}
}

func TestConfigTreeDir_UnsetIsEmpty(t *testing.T) {
	if got := ConfigTreeDir(exec.Command("true")); got != "" {
		t.Errorf("ConfigTreeDir = %q, want empty for a command with no config tree", got)
	}
}
