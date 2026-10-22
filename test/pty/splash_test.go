// Package pty holds black-box tests that spawn the real compiled
// orbit-launcher binary under a real pty and drive it with real
// keystrokes — Go's equivalent of pexpect, proving behaviour (raw mode,
// real Escape/Ctrl-C handling, terminal restoration) that an in-memory
// teatest run can't, per docs/implementation-plan.md section 3.3.
package pty

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
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

// removedConfigScreenText is what the screen the engine's configuration
// stop used to land on showed (#251): the stop goes straight into guided
// configuration now.
var removedConfigScreenText = []string{"The engine stopped before touching anything", "Continue — guided configuration"}

func showsRemovedConfigScreen(screen string) (string, bool) {
	for _, gone := range removedConfigScreenText {
		if strings.Contains(screen, gone) {
			return gone, true
		}
	}
	return "", false
}

// expectWithin waits for match, and fails if the removed configuration
// screen comes up first. That screen would be on the way to the one
// waited for, and replaced by it, so a check of the screen the wait ends
// on could never see it: it ends the wait here instead.
func (c *vtConsole) expectWithin(d time.Duration, match vtscreen.Match) error {
	screen, err := vtscreen.Wait(c.term, d, func(s string) bool {
		_, removed := showsRemovedConfigScreen(s)
		return removed || match(s)
	})
	if err != nil {
		return err
	}
	if gone, removed := showsRemovedConfigScreen(screen); removed {
		return fmt.Errorf("the removed configuration screen is shown (%q):\n%s", gone, screen)
	}
	return nil
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

// Esc on the menu does nothing; a digit only moves the caret; leaving takes
// Enter on the Exit row (#226, #228).
func TestSplash_RealPTY_RendersAndLeavesByTheExitRowNotEscape(t *testing.T) {
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

	console.send("\x1b")   // Escape: stays on the menu
	console.send("\x1b[B") // Down, so the menu is provably still live
	if err := console.expectString("▸ Update"); err != nil {
		t.Fatalf("the menu stopped responding after Escape: %v", err)
	}

	// A digit only moves the caret (#228): 5 is the Exit row, and the
	// launcher must still be running with the caret on it. Up proves the
	// menu is live; Enter on Exit then leaves.
	console.send("5")
	if err := console.expectString("▸ Exit"); err != nil {
		t.Fatalf("5 did not move the caret to Exit: %v", err)
	}
	console.send("\x1b[A") // Up
	if err := console.expectString("▸ Remove"); err != nil {
		t.Fatalf("the launcher stopped responding after 5 (it must not quit): %v", err)
	}
	console.send("5")
	if err := console.expectString("▸ Exit"); err != nil {
		t.Fatalf("5 did not move the caret back to Exit: %v", err)
	}
	console.send("\r") // Enter on Exit

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

	console.send("\x03") // Ctrl-C

	waitForExit(t, cmd)
}

// A wait ends, and fails, on the screen the engine's configuration stop
// used to land on even when what it waits for is shown next.
func TestConsole_ExpectFailsWhenTheRemovedConfigurationScreenComesUp(t *testing.T) {
	consoleShowing := func(t *testing.T, drawn string) *vtConsole {
		t.Helper()
		console := newConsole(t, 80, 24, 5*time.Second)
		if _, err := io.WriteString(console.term.Output(), drawn); err != nil {
			t.Fatalf("draw: %v", err)
		}
		return console
	}

	t.Run("the removed screen fails the wait", func(t *testing.T) {
		err := consoleShowing(t, "The engine stopped before touching anything\r\n").expectString("How will people sign in?")
		if err == nil || !strings.Contains(err.Error(), "removed configuration screen") {
			t.Fatalf("error = %v, want the removed configuration screen reported", err)
		}
	})
	t.Run("an ordinary screen still matches", func(t *testing.T) {
		if err := consoleShowing(t, "How will people sign in?").expectString("How will people sign in?"); err != nil {
			t.Fatalf("an ordinary screen did not match: %v", err)
		}
	})
}
