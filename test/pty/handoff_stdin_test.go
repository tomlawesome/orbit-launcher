package pty

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
)

// TestHandoff_RealPTY_InstallShGetsTheRealTerminalAsStdin pins that when
// the launcher hands the terminal to install.sh (an engine that does not
// speak the machine prompt protocol), install.sh's stdin is the real
// terminal: [ -t 0 ] is true and a typed line reaches its own read.
func TestHandoff_RealPTY_InstallShGetsTheRealTerminalAsStdin(t *testing.T) {
	t.Parallel()
	binPath := launcher.BuildBinary(t)
	dir := t.TempDir()
	out := filepath.Join(t.TempDir(), "handoff-result")

	// Driven with --plain it refuses and hands over no configuration tree,
	// so the launcher falls back to the handoff, which runs it with no
	// arguments and the real terminal.
	script := `#!/usr/bin/env bash
if [[ " $* " == *" --plain "* ]]; then
  echo "phase=configuration component=configuration state=failed reason=configuration-failure action=retry elapsed=0s"
` + configRefusal + `  exit 1
fi
if [ -t 0 ]; then tty=yes; else tty=no; fi
echo "HANDOFF-READY"
read -r line
printf 'tty=%s\nline=%s\n' "$tty" "$line" > '` + out + `'
exit 0
`
	console, cmd := startConsolePTY(t, binPath, dir, launcher.ServeScript(t, script))
	driveToInstallNow(t, console)

	if err := console.expectWithin(15*time.Second, func(screen string) bool {
		return strings.Contains(screen, "HANDOFF-READY")
	}); err != nil {
		t.Fatalf("the handoff never ran install.sh: %v", err)
	}
	console.send("typed-by-person\r")

	deadline := time.Now().Add(10 * time.Second)
	var got []byte
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(out); err == nil && strings.Contains(string(b), "line=") {
			got = b
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if got == nil {
		t.Fatal("install.sh never recorded its stdin: the typed line did not reach its read")
	}
	if want := "tty=yes\nline=typed-by-person\n"; string(got) != want {
		t.Errorf("install.sh saw %q, want %q", got, want)
	}
	_ = cmd
}
