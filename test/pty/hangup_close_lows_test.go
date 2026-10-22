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

// When an SSH session drops, sshd closes the pty master. The launcher's
// next read of the terminal fails (EIO) and, being the session's
// controlling process, it gets SIGHUP. It must still stop the installer
// politely: the installer's EXIT trap (the rollback) runs, the launcher's
// staged files are gone from TMPDIR, and the launcher exits (#284).
//
// The launcher is given the slave as its controlling terminal (its own
// session), as under sshd. The other tests' harness only passes the slave
// as stdin/stdout/stderr, and closing the master there sends no SIGHUP.
func TestConsole_RealPTY_ClosingTheTerminalMidInstallLetsTheInstallerRollBack(t *testing.T) {
	binPath := launcher.BuildBinary(t)
	tmp := t.TempDir()
	marker := filepath.Join(t.TempDir(), "rolled-back")

	console := newConsole(t, 80, 26, 10*time.Second)
	cmd := exec.Command(binPath)
	cmd.Dir = t.TempDir()
	cmd.Env = append(launcher.HermeticEnv(launcher.Animation),
		"ORBIT_LAUNCHER_INSTALL_SCRIPT_URL="+launcher.ServeScript(t, fakeRollbackEngine(marker)),
		"TMPDIR="+tmp)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	console.start(t, cmd)

	driveToInstallNow(t, console)
	if err := console.expectString("Starting Orbit"); err != nil {
		t.Fatalf("the install did not get under way: %v", err)
	}
	if staged := launcherTemps(t, tmp); len(staged) == 0 {
		t.Fatal("setup: the running install staged nothing in TMPDIR, so there is nothing to check")
	}

	// What sshd does when the session drops: the master, and this side's
	// copy of the slave, are closed.
	if err := console.term.Close(); err != nil {
		t.Fatalf("close the terminal: %v", err)
	}
	closed := time.Now()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		t.Logf("exited %v after the terminal closed: %v", time.Since(closed), err)
	case <-time.After(stopGraceCeiling):
		_ = cmd.Process.Kill()
		t.Fatalf("orbit-launcher had not exited %v after the terminal closed", stopGraceCeiling)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("the installer was not stopped politely: its EXIT trap (the rollback) left no marker: %v", err)
	}
	if left := launcherTemps(t, tmp); len(left) > 0 {
		t.Errorf("closing the terminal left the launcher's staged files in TMPDIR: %s", strings.Join(left, ", "))
	}
}
