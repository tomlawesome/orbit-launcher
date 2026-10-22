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

// A hangup or terminate that arrives twice (a terminal closing can send
// both, and a service manager repeats TERM) must not turn the polite
// stop into a kill: the fake engine's rollback takes a second, and the
// second signal arrives in the middle of it. The rollback finishes, the
// staged files are gone, and the launcher exits cleanly, without the
// forced-stop note.
func TestConsole_RealPTY_ARepeatedSignalMidInstallLetsTheRollbackFinish(t *testing.T) {
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
			time.Sleep(300 * time.Millisecond) // the rollback is still running
			if err := cmd.Process.Signal(sig); err != nil {
				t.Fatalf("signal the launcher again: %v", err)
			}

			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("the launcher exited with %v after a repeated %s, want a clean exit (no forced-stop note)", err, name)
				}
			case <-time.After(stopGraceCeiling):
				_ = cmd.Process.Kill()
				t.Fatalf("orbit-launcher had not exited %v after a repeated %s", stopGraceCeiling, name)
			}

			if _, err := os.Stat(marker); err != nil {
				t.Errorf("a repeated %s cut the rollback short: its EXIT trap left no marker: %v", name, err)
			}
			if left := launcherTemps(t, tmp); len(left) > 0 {
				t.Errorf("a repeated %s left the launcher's staged files in TMPDIR: %s", name, strings.Join(left, ", "))
			}
		})
	}
}
