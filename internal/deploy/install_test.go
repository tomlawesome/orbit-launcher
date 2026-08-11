package deploy

import (
	"context"
	"strings"
	"sync"
	"testing"
)

func TestRunInstallScript_StreamsStdoutAndStderrLines(t *testing.T) {
	script := []byte("#!/usr/bin/env bash\necho 'from stdout'\necho 'from stderr' >&2\n")

	var mu sync.Mutex
	var lines []string
	onLine := func(line string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, line)
	}

	if err := RunInstallScript(context.Background(), script, t.TempDir(), onLine); err != nil {
		t.Fatalf("RunInstallScript: %v", err)
	}

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "from stdout") {
		t.Errorf("missing stdout line; got: %v", lines)
	}
	if !strings.Contains(joined, "from stderr") {
		t.Errorf("missing stderr line; got: %v", lines)
	}
}

func TestRunInstallScript_ReturnsErrorOnNonZeroExit(t *testing.T) {
	script := []byte("#!/usr/bin/env bash\necho 'about to fail'\nexit 3\n")

	err := RunInstallScript(context.Background(), script, t.TempDir(), func(string) {})
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
}

func TestRunInstallScript_RunsInTargetDir(t *testing.T) {
	dir := t.TempDir()
	script := []byte("#!/usr/bin/env bash\npwd\n")

	var lines []string
	err := RunInstallScript(context.Background(), script, dir, func(line string) {
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("RunInstallScript: %v", err)
	}

	found := false
	for _, l := range lines {
		if l == dir {
			found = true
		}
	}
	if !found {
		t.Errorf("expected pwd output to equal target dir %q; got %v", dir, lines)
	}
}

// TestRunInstallScript_DetachesFromTheControllingTerminal is the load-
// bearing test for this package's whole approach to non-interactive
// operation: install.sh's has_controlling_terminal check (verified by
// reading scripts/install.sh directly) opens /dev/tty, not stdin — so
// only detaching the child into its own session (no controlling terminal
// at all) reliably makes that check fail, forcing the non-interactive
// path. This proves that property against the real mechanism (a script
// that itself tries to open /dev/tty, exactly like install.sh does),
// not just against the reasoning for why it should work.
func TestRunInstallScript_DetachesFromTheControllingTerminal(t *testing.T) {
	script := []byte(`#!/usr/bin/env bash
if { exec 3<>/dev/tty; } 2>/dev/null; then
  echo "HAS_CONTROLLING_TERMINAL"
else
  echo "NO_CONTROLLING_TERMINAL"
fi
`)

	var lines []string
	var mu sync.Mutex
	err := RunInstallScript(context.Background(), script, t.TempDir(), func(line string) {
		mu.Lock()
		defer mu.Unlock()
		lines = append(lines, line)
	})
	if err != nil {
		t.Fatalf("RunInstallScript: %v", err)
	}

	joined := strings.Join(lines, "\n")
	if strings.Contains(joined, "HAS_CONTROLLING_TERMINAL") {
		t.Errorf("child process retained access to /dev/tty — Setsid detachment did not work; got: %v", lines)
	}
	if !strings.Contains(joined, "NO_CONTROLLING_TERMINAL") {
		t.Errorf("expected the child to report no controlling terminal; got: %v", lines)
	}
}
