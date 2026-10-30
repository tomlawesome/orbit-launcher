package pty

// #304 (owner answer 10a, 2026-10-30): SIGTERM or SIGHUP to the launcher
// while an Update is past its commit makes it quit without waiting for the
// installer, which rolls forward on its own. The launcher then says so on
// stderr, in one line, and exits with the shell's 128 + signal number
// (143 for SIGTERM, 129 for SIGHUP), so a script or a supervisor notices
// that the update may not have finished. It used to exit 0 and say
// nothing.
//
// Process level: the built binary runs under a pty against a fake
// install.sh; the signal is sent to the process and its stderr and exit
// status are read. Every other stop keeps what it did before.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

const stoppedAfterCommitLine = "stopped during an update that had already started applying; run orbit-launcher and choose Repair to check Orbit"

// updateEnginePastCommit is an install.sh that reports the commit (the
// first event after it is phase=compose state=completed
// reason=compose-validation), then an image pull, and keeps working for a
// few seconds, ignoring a polite stop as a roll-forward does. It ends by
// itself so nothing is left running.
const updateEnginePastCommit = `#!/usr/bin/env bash
trap '' TERM HUP INT
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
echo "phase=compose component=compose state=completed reason=compose-validation action=validate elapsed=0s"
echo "phase=preparation component=image state=running reason=image-pull action=pull elapsed=0s"
sleep 6
`

// updateEngineBeforeCommit reports only host checks, then waits; it stops
// promptly when asked to, as an engine does before its commit.
const updateEngineBeforeCommit = `#!/usr/bin/env bash
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
sleep 20 &
trap 'kill $! 2>/dev/null; exit 143' TERM HUP INT
wait
`

// existingDeployment is a folder with an installed deployment's files.
func existingDeployment(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(dir, deploy.EnvFile))
	testutil.WriteComposeFile(t, filepath.Join(dir, deploy.ComposeFile))
	return dir
}

// startedUpdate runs the launcher on an existing deployment against the
// fake install.sh, confirms the Update and returns once the run is under
// way (the screen shows the stage the engine has reached). stderr is
// captured in a file so it is read apart from the screen.
func startedUpdate(t *testing.T, script string, waitFor string) (*vtConsole, *exec.Cmd, string) {
	t.Helper()
	stderrPath := filepath.Join(t.TempDir(), "stderr.txt")
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stderr.Close() })

	console := newConsole(t, 80, 26, 15*time.Second)
	cmd := exec.Command(launcher.BuildBinary(t))
	cmd.Dir = existingDeployment(t)
	cmd.Env = append(launcher.HermeticEnv(launcher.Animation),
		"ORBIT_LAUNCHER_INSTALL_SCRIPT_URL="+launcher.ServeScript(t, script),
		"TMPDIR="+t.TempDir())
	cmd.Stderr = stderr
	console.start(t, cmd)

	skipArrival(t, console)
	if err := console.expectString("▸ Update"); err != nil {
		t.Fatalf("Update was not preselected for the deployment: %v", err)
	}
	console.send("\r")
	if err := console.expectString("Pull the latest Orbit and update this deployment"); err != nil {
		t.Fatalf("did not reach the Update confirm screen: %v", err)
	}
	console.send("\r")
	if err := console.expectWithin(15*time.Second, vtscreen.ContainsAny(waitFor)); err != nil {
		t.Fatalf("the update did not get under way (wanted %q on screen): %v\n%s", waitFor, err, vtscreen.Screen(console.term))
	}
	return console, cmd, stderrPath
}

// exitOf waits for the launcher to end and returns its exit status.
func exitOf(t *testing.T, cmd *exec.Cmd, after string) int {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(stopGraceCeiling):
		_ = cmd.Process.Kill()
		t.Fatalf("orbit-launcher had not exited %v after %s", stopGraceCeiling, after)
	}
	return cmd.ProcessState.ExitCode()
}

func readStderr(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Catches: SIGTERM/SIGHUP on an Update past its commit still exiting 0 and
// saying nothing (the fault #304 reports), or saying it twice, or with the
// wrong status.
func TestUpdatePastCommit_RealPTY_TerminateAndHangupSayWhereTheUpdateStoppedAndExit128PlusTheSignal(t *testing.T) {
	cases := []struct {
		name string
		sig  syscall.Signal
		code int
	}{
		{"SIGTERM", syscall.SIGTERM, 143},
		{"SIGHUP", syscall.SIGHUP, 129},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, cmd, stderrPath := startedUpdate(t, updateEnginePastCommit, "Preparing services")
			if err := cmd.Process.Signal(c.sig); err != nil {
				t.Fatalf("signal the launcher: %v", err)
			}
			code := exitOf(t, cmd, c.name)
			if code != c.code {
				t.Errorf("exit status after %s = %d, want %d", c.name, code, c.code)
			}
			if got := readStderr(t, stderrPath); strings.TrimSpace(got) != stoppedAfterCommitLine || strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
				t.Errorf("stderr after %s is not exactly the one line %q:\n%q", c.name, stoppedAfterCommitLine, got)
			}
		})
	}
}

// Catches: the new line and status leaking onto the Ctrl-C key. Ctrl-C
// past the commit is a polite stop (the installer gets its grace), not
// the quit-without-waiting, and keeps its exit status and wording.
func TestUpdatePastCommit_RealPTY_CtrlCKeyKeepsItsOwnWordingAndStatus(t *testing.T) {
	t.Parallel()
	console, cmd, stderrPath := startedUpdate(t, updateEnginePastCommit, "Preparing services")
	console.send("\x03")
	code := exitOf(t, cmd, "Ctrl-C")
	if code != 0 {
		t.Errorf("Ctrl-C exit status = %d, want 0 (a stop the installer finished, as before)", code)
	}
	if got := readStderr(t, stderrPath); strings.Contains(got, stoppedAfterCommitLine) {
		t.Errorf("Ctrl-C printed the signal line:\n%q", got)
	}
}

// Catches: the new line and status leaking onto SIGINT from outside
// (kill -INT), which stays the interrupted exit it was (status 1).
func TestUpdatePastCommit_RealPTY_SIGINTKeepsItsOwnStatusAndPrintsNoSignalLine(t *testing.T) {
	t.Parallel()
	_, cmd, stderrPath := startedUpdate(t, updateEnginePastCommit, "Preparing services")
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal the launcher: %v", err)
	}
	code := exitOf(t, cmd, "SIGINT")
	if code != 1 {
		t.Errorf("exit status after SIGINT = %d, want 1 (the interrupted exit, as before)", code)
	}
	got := readStderr(t, stderrPath)
	if strings.Contains(got, stoppedAfterCommitLine) {
		t.Errorf("SIGINT printed the signal line:\n%q", got)
	}
	if !strings.Contains(got, "interrupt") {
		t.Errorf("SIGINT lost its interrupted message on stderr:\n%q", got)
	}
}

// Catches: the new line and status applying to an Update stopped before
// its commit, which stops politely and exits 0 silently as before.
func TestUpdateBeforeCommit_RealPTY_TerminateAndHangupKeepTheCleanSilentExit(t *testing.T) {
	for name, sig := range map[string]syscall.Signal{"SIGTERM": syscall.SIGTERM, "SIGHUP": syscall.SIGHUP} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, cmd, stderrPath := startedUpdate(t, updateEngineBeforeCommit, "Checking host")
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatalf("signal the launcher: %v", err)
			}
			code := exitOf(t, cmd, name)
			got := readStderr(t, stderrPath)
			if strings.Contains(got, stoppedAfterCommitLine) {
				t.Errorf("%s before the commit printed the after-commit line:\n%q", name, got)
			}
			if code != 0 {
				t.Errorf("%s before the commit exited %d, want 0 (a clean stop, as before)", name, code)
			}
		})
	}
}

// Catches: the new line and status applying to an Install past its commit
// (only an Update rolls forward and is left to finish).
func TestInstallPastCommit_RealPTY_TerminateKeepsItsExitAndPrintsNoUpdateLine(t *testing.T) {
	t.Parallel()
	binPath := launcher.BuildBinary(t)
	stderrPath := filepath.Join(t.TempDir(), "stderr.txt")
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stderr.Close() })
	marker := filepath.Join(t.TempDir(), "rolled-back")

	console := newConsole(t, 80, 26, 10*time.Second)
	cmd := exec.Command(binPath)
	cmd.Dir = t.TempDir()
	cmd.Env = append(launcher.HermeticEnv(launcher.Animation),
		"ORBIT_LAUNCHER_INSTALL_SCRIPT_URL="+launcher.ServeScript(t, fakeRollbackEngine(marker)),
		"TMPDIR="+t.TempDir())
	cmd.Stderr = stderr
	console.start(t, cmd)
	driveToInstallNow(t, console)
	if err := console.expectString("Starting Orbit"); err != nil {
		t.Fatalf("the install did not get under way: %v", err)
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal the launcher: %v", err)
	}
	code := exitOf(t, cmd, "SIGTERM")
	if code != 0 {
		t.Errorf("an Install stopped by SIGTERM exited %d, want 0 (a clean stop, as before)", code)
	}
	if got := readStderr(t, stderrPath); strings.Contains(got, stoppedAfterCommitLine) {
		t.Errorf("an Install printed the Update line:\n%q", got)
	}
}
