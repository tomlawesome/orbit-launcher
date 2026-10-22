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

// #277: a SIGINT sent from outside the terminal (kill -INT, timeout -s
// INT) in the middle of an Install is handled the way Ctrl-C at the
// keyboard and SIGTERM are: the installer is stopped politely, so its
// EXIT trap (the rollback) runs, the launcher's staged install.sh and
// configuration tree are gone from TMPDIR, and the launcher exits.
// Before, it quit at once and left both behind.
func TestConsole_RealPTY_AnExternalSIGINTMidInstallLetsTheInstallerRollBack(t *testing.T) {
	binPath := launcher.BuildBinary(t)
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

	// From outside: the signal goes to the launcher's process, not through
	// the terminal as a Ctrl-C keystroke.
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal the launcher: %v", err)
	}
	sent := time.Now()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(stopGraceCeiling):
		_ = cmd.Process.Kill()
		t.Fatalf("orbit-launcher had not exited %v after SIGINT", stopGraceCeiling)
	}
	t.Logf("exited %v after SIGINT", time.Since(sent))

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the installer was not stopped politely: its EXIT trap (the rollback) left no marker: %v", err)
	}
	if left := launcherTemps(t, tmp); len(left) > 0 {
		t.Errorf("SIGINT left the launcher's staged files in TMPDIR: %s", strings.Join(left, ", "))
	}
}
