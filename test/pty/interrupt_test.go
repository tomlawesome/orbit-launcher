package pty

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
)

// An interrupt signal from outside (kill -INT, not a Ctrl-C keypress,
// which the TUI reads as a key) ends the program with an error: the
// launcher must say so on stderr and exit 1, not 0. This is startApp's
// one failure path; it moved the coverage ratchet when displayVersion
// left main.go (#212), so it is pinned here.
func TestApp_RealPTY_InterruptSignalExitsNonZeroAndSaysWhy(t *testing.T) {
	binPath := launcher.BuildBinary(t)
	console := newConsole(t, 80, 24, 10*time.Second)

	cmd := exec.Command(binPath)
	// startUnderPTY's environment: the arrival kept for skipArrival,
	// nothing off the machine.
	cmd.Env = launcher.HermeticEnv(launcher.Animation)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	console.start(t, cmd)
	skipArrival(t, console)
	if err := console.expectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("signal the launcher: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("an interrupted launcher must exit 1, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("orbit-launcher did not exit after an interrupt signal")
	}
	if got := stderr.String(); !strings.HasPrefix(got, "orbit-launcher: ") || strings.TrimSpace(strings.TrimPrefix(got, "orbit-launcher: ")) == "" {
		t.Errorf("stderr must name the launcher and the error, got %q", got)
	}
}
