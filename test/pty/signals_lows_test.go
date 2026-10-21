package pty

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
)

// A hangup (the SSH session dropped) or a terminate (kill) in the middle
// of an Install stops the launcher the way Ctrl-C does: the installer is
// sent SIGTERM and its EXIT trap rolls the run back, and the launcher's
// staged install.sh and configuration tree are gone from TMPDIR when it
// exits. Before, Bubble Tea's own handler quit at once on SIGTERM and
// SIGHUP was not handled at all, so both were left behind. SIGKILL
// cannot be caught and is not tested.
func TestConsole_RealPTY_HangupAndTerminateMidInstallLeaveNothingInTMPDIR(t *testing.T) {
	binPath := launcher.BuildBinary(t)
	for name, sig := range map[string]syscall.Signal{"SIGTERM": syscall.SIGTERM, "SIGHUP": syscall.SIGHUP} {
		t.Run(name, func(t *testing.T) {
			// Parallel: each waits out the development notice's countdown.
			t.Parallel()
			tmp := t.TempDir()
			marker := filepath.Join(t.TempDir(), "rolled-back")

			console := newConsole(t, 80, 26, 10*time.Second)
			cmd := exec.Command(binPath)
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

			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatalf("signal the launcher: %v", err)
			}
			sent := time.Now()

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case <-done:
			case <-time.After(stopGraceCeiling):
				_ = cmd.Process.Kill()
				t.Fatalf("orbit-launcher had not exited %v after %s", stopGraceCeiling, name)
			}
			t.Logf("exited %v after %s", time.Since(sent), name)

			if _, err := os.Stat(marker); err != nil {
				t.Errorf("the installer was not stopped politely: its EXIT trap (the rollback) left no marker: %v", err)
			}
			if left := launcherTemps(t, tmp); len(left) > 0 {
				t.Errorf("%s left the launcher's staged files in TMPDIR: %s", name, strings.Join(left, ", "))
			}
		})
	}
}

// launcherTemps lists the launcher's staged entries in dir: the
// install.sh copy and the configuration tree.
func launcherTemps(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read TMPDIR: %v", err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "orbit-launcher-install-") || strings.HasPrefix(e.Name(), "orbit-launcher-config-") {
			found = append(found, e.Name())
		}
	}
	return found
}
