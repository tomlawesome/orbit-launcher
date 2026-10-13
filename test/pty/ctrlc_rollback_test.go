package pty

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
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
	case <-done:
	case <-time.After(stopGraceCeiling - time.Since(pressed)):
		_ = cmd.Process.Kill()
		t.Fatalf("orbit-launcher had not exited %v after Ctrl-C", stopGraceCeiling)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the launcher quit before the installer could roll back (its EXIT trap left no marker): %v", err)
	}
}
