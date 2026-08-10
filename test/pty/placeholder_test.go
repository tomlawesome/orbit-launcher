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

func TestPlaceholder_RealPTY_QuitsOnAnyKey(t *testing.T) {
	binPath := buildBinary(t)

	console, err := expect.NewConsole(expect.WithDefaultTimeout(5 * time.Second))
	if err != nil {
		t.Fatalf("create console: %v", err)
	}
	defer console.Close()

	cmd := exec.Command(binPath)
	cmd.Stdin = console.Tty()
	cmd.Stdout = console.Tty()
	cmd.Stderr = console.Tty()
	cmd.Env = append(os.Environ(), "TERM=xterm")

	if err := cmd.Start(); err != nil {
		t.Fatalf("start orbit-launcher: %v", err)
	}

	if _, err := console.ExpectString("hello, orbit-launcher"); err != nil {
		t.Fatalf("did not see greeting: %v", err)
	}

	if _, err := console.Send("q"); err != nil {
		t.Fatalf("send key: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("orbit-launcher exited with error: %v", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("orbit-launcher did not exit after a keypress")
	}
}
