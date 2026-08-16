// Package pty holds black-box tests that spawn the real compiled
// orbit-launcher binary under a real pty and drive it with real
// keystrokes — Go's equivalent of pexpect, proving behaviour (raw mode,
// real Escape/Ctrl-C handling, terminal restoration) that an in-memory
// teatest run can't, per docs/implementation-plan.md section 3.3.
package pty

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	expect "github.com/Netflix/go-expect"
	"github.com/creack/pty"
)

// buildBinary compiles cmd/orbit-launcher once per test run and returns
// its path, so these tests exercise the actual binary a release would
// ship, not a stand-in.
func buildBinary(t *testing.T) string {
	t.Helper()

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	binPath := filepath.Join(t.TempDir(), "orbit-launcher")
	cmd := exec.Command("go", "build", "-o", binPath, "./cmd/orbit-launcher")
	cmd.Dir = repoRoot
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build orbit-launcher: %v\n%s", err, out)
	}
	return binPath
}

func startUnderPTY(t *testing.T, binPath string) (*expect.Console, *exec.Cmd) {
	t.Helper()
	return startUnderPTYInDir(t, binPath, "")
}

// startUnderPTYInDir is startUnderPTY, but runs the binary with its
// working directory set to dir — needed to exercise flows (like Update)
// whose behaviour depends on what's already at the target directory. An
// empty dir inherits the test process's own working directory.
func startUnderPTYInDir(t *testing.T, binPath, dir string) (*expect.Console, *exec.Cmd) {
	t.Helper()

	console, err := expect.NewConsole(expect.WithDefaultTimeout(5 * time.Second))
	if err != nil {
		t.Fatalf("create console: %v", err)
	}
	t.Cleanup(func() { console.Close() })

	// A freshly opened pty reports a 0x0 window size until told otherwise;
	// bubbletea renders nothing until its first WindowSizeMsg reports a
	// real size (see SplashModel.View), so the program would otherwise
	// sit there forever with nothing to Expect against.
	if err := pty.Setsize(console.Tty(), &pty.Winsize{Rows: 24, Cols: 80}); err != nil {
		t.Fatalf("set pty size: %v", err)
	}

	cmd := exec.Command(binPath)
	cmd.Dir = dir
	cmd.Stdin = console.Tty()
	cmd.Stdout = console.Tty()
	cmd.Stderr = console.Tty()
	// NO_COLOR keeps assertions to plain text: this layer proves
	// behaviour (does navigation work, does the terminal restore), not
	// appearance — that's test/visual's job.
	// These tests assert on rendered output and navigation, not on
	// whether GitHub happens to be reachable from the test runner — same
	// reason every other test in this repo mocks its network calls
	// (see internal/deploy/fetch_test.go, internal/release/update_test.go).
	cmd.Env = append(os.Environ(), "TERM=xterm", "NO_COLOR=1",
		"ORBIT_LAUNCHER_NO_UPDATE_CHECK=1", "ORBIT_LAUNCHER_NO_HEALTH_PROBE=1",
		"ORBIT_LAUNCHER_NO_VOLUME_CHECK=1")

	if err := cmd.Start(); err != nil {
		t.Fatalf("start orbit-launcher: %v", err)
	}
	// A failed expectation exits the test through t.Fatalf without ever
	// reaching waitForExit — without this, that run leaks a live binary
	// parked on a dead pty (found as five real strays after a local
	// iteration session).
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return console, cmd
}

// skipArrival sends one benign key: any key skips the splash's arrival
// animation and is swallowed, putting the lit room on screen for the
// assertions that follow — the arrival itself is covered by internal/ui's
// own unit tests.
func skipArrival(t *testing.T, console *expect.Console) {
	t.Helper()
	if _, err := console.Send("s"); err != nil {
		t.Fatalf("send skip key: %v", err)
	}
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
	binPath := buildBinary(t)
	console, cmd := startUnderPTY(t, binPath)
	skipArrival(t, console)

	// The wordmark is the letter-spaced normal-size ORBIT.
	if _, err := console.ExpectString("O R B I T"); err != nil {
		t.Fatalf("did not see the wordmark: %v", err)
	}
	if _, err := console.ExpectString("Install"); err != nil {
		t.Fatalf("did not see the menu: %v", err)
	}

	if _, err := console.Send("\x1b"); err != nil { // Escape
		t.Fatalf("send Escape: %v", err)
	}

	waitForExit(t, cmd)
}

func TestSplash_RealPTY_ArrowNavigationMovesTheCaret(t *testing.T) {
	binPath := buildBinary(t)
	console, cmd := startUnderPTY(t, binPath)
	skipArrival(t, console)

	if _, err := console.ExpectString("▸ Install"); err != nil {
		t.Fatalf("did not see the initial selection on Install: %v", err)
	}

	if _, err := console.Send("\x1b[B"); err != nil { // Down
		t.Fatalf("send Down: %v", err)
	}

	if _, err := console.ExpectString("▸ Update"); err != nil {
		t.Fatalf("caret did not move to Update after Down: %v", err)
	}

	if _, err := console.Send("\x1b"); err != nil { // Escape
		t.Fatalf("send Escape: %v", err)
	}

	waitForExit(t, cmd)
}
