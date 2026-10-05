package deploy

import (
	"bytes"
	"os"
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
