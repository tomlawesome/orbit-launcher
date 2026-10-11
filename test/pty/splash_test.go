// Package pty holds black-box tests that spawn the real compiled
// orbit-launcher binary under a real pty and drive it with real
// keystrokes — Go's equivalent of pexpect, proving behaviour (raw mode,
// real Escape/Ctrl-C handling, terminal restoration) that an in-memory
// teatest run can't, per docs/implementation-plan.md section 3.3.
package pty

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
)

// TestMain lets the harness build the binary once for the whole package
// and remove it after the last test.
func TestMain(m *testing.M) { os.Exit(launcher.Run(m)) }

// vtConsole is one orbit-launcher process on a virtual terminal: it runs
// on a real pty, Charm's x/vt emulator keeps the rendered screen, and every
// expectation here is about that screen (#181). It replaces go-expect's
// Console, which matched the raw byte stream instead.
type vtConsole struct {
	term *vtscreen.Terminal
	// timeout is the wall-clock ceiling for one expectation. go-expect's
	// own timeout measured idleness between reads, which a repainting
	// screen never reaches; this is a real deadline.
	timeout time.Duration
}

// newConsole opens a cols x rows virtual terminal and closes it when the
// test ends. vtscreen sizes the pty before the program starts, so its first
// WindowSizeMsg reports a real size and bubbletea renders (see
// SplashModel.View).
func newConsole(t *testing.T, cols, rows int, timeout time.Duration) *vtConsole {
	t.Helper()
	term, err := vtscreen.New(cols, rows, nil)
	if err != nil {
		t.Fatalf("create virtual terminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })
	return &vtConsole{term: term, timeout: timeout}
}

// start runs cmd on the console's pty. Stdin, stdout and stderr default
// to the pty; a stream the caller already set is left alone.
func (c *vtConsole) start(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if err := c.term.Start(cmd); err != nil {
		t.Fatalf("start orbit-launcher: %v", err)
	}
	// A failed expectation exits the test through t.Fatalf without ever
	// reaching waitForExit — without this, that run leaks a live binary
	// parked on a dead pty (found as five real strays after a local
	// iteration session). Registered after the terminal's own Close, so
	// it runs first.
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
}

// expectString waits up to the console's timeout for s on screen.
func (c *vtConsole) expectString(s string) error {
	return c.expectWithin(c.timeout, vtscreen.ContainsAny(s))
}

// expectScreen waits up to the console's timeout for one screen showing
// every one of texts — for an assertion that two things share a screen,
// where separate waits could each be satisfied by a different one.
func (c *vtConsole) expectScreen(texts ...string) error {
	return c.expectWithin(c.timeout, vtscreen.ContainsAll(texts...))
}

func (c *vtConsole) expectWithin(d time.Duration, match vtscreen.Match) error {
	_, err := vtscreen.Wait(c.term, d, match)
	return err
}

// send types s as raw input, escape sequences and all.
func (c *vtConsole) send(s string) {
	vtscreen.Send(c.term, s)
}

func startUnderPTY(t *testing.T, binPath string) (*vtConsole, *exec.Cmd) {
	t.Helper()
	return startUnderPTYInDir(t, binPath, "")
}

// startUnderPTYInDir is startUnderPTY, but runs the binary with its
// working directory set to dir — needed to exercise flows (like Update)
// whose behaviour depends on what's already at the target directory. An
// empty dir inherits the test process's own working directory.
func startUnderPTYInDir(t *testing.T, binPath, dir string) (*vtConsole, *exec.Cmd) {
	t.Helper()

	console := newConsole(t, 80, 24, 10*time.Second)

	cmd := exec.Command(binPath)
	cmd.Dir = dir
	// The arrival animation is kept: skipArrival is how these tests get
	// past it, the way a person does.
	cmd.Env = launcher.HermeticEnv(launcher.Animation)
	console.start(t, cmd)
	return console, cmd
}

// skipArrival sends one benign key: any key skips the splash's arrival
// animation and is swallowed, putting the lit room on screen for the
// assertions that follow — the arrival itself is covered by internal/ui's
// own unit tests.
func skipArrival(t *testing.T, console *vtConsole) {
	t.Helper()
	console.send("s")
}

func waitForExit(t *testing.T, cmd *exec.Cmd) {
	t.Helper()

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("orbit-launcher exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("orbit-launcher did not exit in time")
	}
}

func TestSplash_RealPTY_RendersAndQuitsOnEscape(t *testing.T) {
	binPath := launcher.BuildBinary(t)
	console, cmd := startUnderPTY(t, binPath)
	skipArrival(t, console)

	// The wordmark is the letter-spaced normal-size ORBIT.
	if err := console.expectString("O R B I T"); err != nil {
		t.Fatalf("did not see the wordmark: %v", err)
	}
	if err := console.expectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}

	console.send("\x1b") // Escape

	waitForExit(t, cmd)
}

func TestSplash_RealPTY_ArrowNavigationMovesTheCaret(t *testing.T) {
	binPath := launcher.BuildBinary(t)
	console, cmd := startUnderPTY(t, binPath)
	skipArrival(t, console)

	if err := console.expectString("▸ Install"); err != nil {
		t.Fatalf("did not see the initial selection on Install: %v", err)
	}

	console.send("\x1b[B") // Down

	if err := console.expectString("▸ Update"); err != nil {
		t.Fatalf("caret did not move to Update after Down: %v", err)
	}

	console.send("\x1b") // Escape

	waitForExit(t, cmd)
}
