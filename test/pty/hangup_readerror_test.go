package pty

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
)

// startMidInstallNoCtty starts the launcher with the pty slave only as
// stdin/stdout/stderr (no controlling terminal, so closing the master
// sends no SIGHUP) and drives it into a running install whose installer
// rolls back on exit (#335). Closing the terminal there is a read error
// on its own, with no signal at all: deterministic, not a race.
func startMidInstallNoCtty(t *testing.T) (console *vtConsole, cmd *exec.Cmd, tmp, marker string) {
	t.Helper()
	binPath := launcher.BuildBinary(t)
	tmp = t.TempDir()
	marker = filepath.Join(t.TempDir(), "rolled-back")

	console = newConsole(t, 80, 26, 10*time.Second)
	cmd = exec.Command(binPath)
	cmd.Dir = t.TempDir()
	cmd.Env = append(launcher.HermeticEnv(launcher.Animation),
		"ORBIT_LAUNCHER_INSTALL_SCRIPT_URL="+launcher.ServeScript(t, fakeRollbackEngine(marker)),
		"TMPDIR="+tmp)
	console.start(t, cmd)

	driveToInstallNow(t, console)
	if err := console.expectString("Starting Orbit"); err != nil {
		t.Fatalf("the install did not get under way: %v", err)
	}
	if staged := launcherTemps(t, tmp); len(staged) == 0 {
		t.Fatal("setup: the running install staged nothing in TMPDIR, so there is nothing to check")
	}
	return console, cmd, tmp, marker
}

// checkPoliteStop waits for the launcher to exit within the grace ceiling
// and checks the stop was the polite one: exit as SIGHUP gives it (0 for
// an install, or 129 once an update was applying; never 1), the
// installer's EXIT trap ran, and nothing is left in TMPDIR.
func checkPoliteStop(t *testing.T, cmd *exec.Cmd, tmp, marker string, since time.Time) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("wait: %v", err)
		}
		t.Logf("exited %d after %v", code, time.Since(since))
		if code != 0 && code != 128+int(syscall.SIGHUP) {
			t.Errorf("exit status %d: a stop like SIGHUP's exits 0 (or 129), not as an error", code)
		}
	case <-time.After(stopGraceCeiling):
		_ = cmd.Process.Kill()
		t.Fatalf("orbit-launcher had not exited %v after the terminal closed", stopGraceCeiling)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the installer was not stopped politely: its EXIT trap (the rollback) left no marker: %v", err)
	}
	if left := launcherTemps(t, tmp); len(left) > 0 {
		t.Errorf("the launcher's staged files were left in TMPDIR: %s", strings.Join(left, ", "))
	}
}

// A read error that arrives while a stop is already under way (SIGHUP
// has begun it, the installer is rolling back) must not cut the stop
// short: the rollback still finishes and its marker appears (#335).
func TestConsole_RealPTY_ATerminalReadErrorDuringAStopDoesNotCutItShort(t *testing.T) {
	console, cmd, tmp, marker := startMidInstallNoCtty(t)
	if err := cmd.Process.Signal(syscall.SIGHUP); err != nil {
		t.Fatalf("signal the launcher: %v", err)
	}
	if err := console.expectString("stopping the install"); err != nil {
		t.Fatalf("SIGHUP did not start a stop: %v", err)
	}
	// The stop is under way and the installer's trap is still sleeping.
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("setup: the rollback had already finished before the terminal closed")
	}
	if err := console.term.Close(); err != nil {
		t.Fatalf("close the terminal: %v", err)
	}
	checkPoliteStop(t, cmd, tmp, marker, time.Now())
}

// A read error on the terminal with no SIGHUP at all is treated as a
// hang-up, which is the first and third bullets of #335 at once: with the
// terminal only as stdin/stdout/stderr and no controlling terminal, closing it still stops the launcher the same way, within the
// grace ceiling (#335; 7182388 saw it not exit within 15 s).
func TestConsole_RealPTY_ClosingTheTerminalWithNoControllingTerminalStopsItPolitely(t *testing.T) {
	console, cmd, tmp, marker := startMidInstallNoCtty(t)
	if err := console.term.Close(); err != nil {
		t.Fatalf("close the terminal: %v", err)
	}
	checkPoliteStop(t, cmd, tmp, marker, time.Now())
}
