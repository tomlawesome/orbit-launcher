package pty

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

// stopGraceCeiling is the launcher's grace for an installer that is
// undoing its work after Ctrl-C (#240): SIGTERM, a bounded wait, then
// SIGKILL. An installer that cleans up promptly must let the launcher
// quit well inside it.
const stopGraceCeiling = 15 * time.Second

// fakeRollbackEngine is an install.sh that rolls back on exit: its EXIT
// trap takes a second to run (a docker rm, a file rollback) and then
// leaves a marker. Under SIGKILL the trap cannot run and no marker
// appears; under SIGTERM it does.
func fakeRollbackEngine(marker string) string {
	return fmt.Sprintf(`#!/usr/bin/env bash
trap 'sleep 1; touch "%s"' EXIT
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
echo "phase=application component=application state=healthy reason=application-health action=health elapsed=1s"
sleep 120
`, marker)
}

// Ctrl-C in the middle of an Install lets the installer undo what it
// started: the launcher shows that it is stopping, waits for the
// installer's own cleanup (its EXIT trap), and only then quits -- within
// the grace, not instantly and not after a SIGKILL that would have
// skipped the cleanup.
func TestConsole_RealPTY_CtrlCMidInstallLetsTheInstallerRollBack(t *testing.T) {
	// Parallel: the run waits out the development notice's real
	// countdown (passNotice), like the other install journeys.
	t.Parallel()
	binPath := launcher.BuildBinary(t)
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "rolled-back")
	console, cmd := startConsolePTY(t, binPath, dir, launcher.ServeScript(t, fakeRollbackEngine(marker)))

	driveToInstallNow(t, console)
	if err := console.expectString("Starting Orbit"); err != nil {
		t.Fatalf("the install did not get under way: %v", err)
	}

	console.send("\x03") // Ctrl-C mid-run
	pressed := time.Now()

	if err := console.expectString("stopping the install"); err != nil {
		t.Fatalf("Ctrl-C mid-install did not show the stopping line: %v", err)
	}
	if err := console.expectString("the installer is undoing what it started"); err != nil {
		t.Fatalf("the stopping screen did not say what is happening: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("a clean stop must exit 0, got %v", err)
		}
	case <-time.After(stopGraceCeiling - time.Since(pressed)):
		_ = cmd.Process.Kill()
		t.Fatalf("orbit-launcher had not exited %v after Ctrl-C", stopGraceCeiling)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the launcher quit before the installer could roll back (its EXIT trap left no marker): %v", err)
	}
	// The installer finished its own cleanup, so the launcher has nothing
	// to warn about (security audit: only a stop that outlived its grace
	// is reported as possibly half undone).
	if screen := squashed(vtscreen.Screen(console.term)); strings.Contains(screen, "stillundoing") {
		t.Errorf("a clean stop was reported as one that outlived its grace:\n%s", vtscreen.Screen(console.term))
	}
}

// slowRollbackEngine is an install.sh whose EXIT trap takes thirty
// seconds to finish (a wedged docker daemon): far longer than a second
// Ctrl-C will wait.
func slowRollbackEngine() string {
	return `#!/usr/bin/env bash
trap 'sleep 30' EXIT
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
echo "phase=application component=application state=healthy reason=application-health action=health elapsed=1s"
sleep 120 &
wait
`
}

// squashed drops every space and line break, so that a sentence the
// terminal wrapped, or the launcher broke into lines, still compares
// equal to the same sentence written in one piece.
func squashed(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// forcedStopMessage is the owner's wording (decision 20a, 2026-10-17)
// for a stop whose installer was still undoing its work when the
// launcher ended it.
const forcedStopMessage = "orbit-launcher: the install was stopped, but Orbit's installer was still undoing its work when the launcher stopped waiting and ended it. The folder and the Docker containers and volumes it created may be half restored \u2014 check them before installing again."

// A second Ctrl-C while the installer is still undoing its work ends the
// launcher at once, but it must say, after the screen has closed, that
// the installer was cut off, and exit 1 so a script notices.
func TestConsole_RealPTY_SecondCtrlCOnASlowRollbackSaysSoAfterExitAndExits1(t *testing.T) {
	t.Parallel()
	binPath := launcher.BuildBinary(t)
	console, cmd := startConsolePTY(t, binPath, t.TempDir(), launcher.ServeScript(t, slowRollbackEngine()))

	driveToInstallNow(t, console)
	if err := console.expectString("Starting Orbit"); err != nil {
		t.Fatalf("the install did not get under way: %v", err)
	}

	console.send("\x03") // first Ctrl-C: ask the installer to stop
	if err := console.expectString("stopping the install"); err != nil {
		t.Fatalf("Ctrl-C mid-install did not show the stopping line: %v", err)
	}
	console.send("\x03") // second Ctrl-C: stop waiting
	pressed := time.Now()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("orbit-launcher did not exit within 5s of the second Ctrl-C")
	}
	t.Logf("exited %v after the second Ctrl-C", time.Since(pressed))

	if code := cmd.ProcessState.ExitCode(); code != 1 {
		t.Errorf("exit status = %d, want 1: the install may be half undone", code)
	}
	if err := console.expectString("still undoing"); err != nil {
		t.Fatalf("nothing on the terminal after exit says the installer was cut off: %v", err)
	}
	if screen := squashed(vtscreen.Screen(console.term)); !strings.Contains(screen, squashed(forcedStopMessage)) {
		t.Errorf("the message after exit is not the owner's wording:\nwant %q\ngot:\n%s", forcedStopMessage, vtscreen.Screen(console.term))
	}
}
