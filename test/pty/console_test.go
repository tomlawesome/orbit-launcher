package pty

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	expect "github.com/Netflix/go-expect"
	"github.com/creack/pty"
)

// fakeEngineScript is a stand-in install.sh that speaks engine event
// stream v0 exactly as orbit's contract documents it (one key=value
// line per event on stdout in plain mode), writes the .env-orbit a real
// install leaves behind, and exits 0. It lets this layer prove the
// whole mission-console flow — real binary, real pty, real subprocess,
// real pipe — with no Docker and no network beyond localhost.
const fakeEngineScript = `#!/usr/bin/env bash
printf '%s\n' "$*" > engine-args.txt
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
sleep 0.05
echo "phase=identity component=image state=completed reason=image-identity action=verify elapsed=0s"
sleep 0.05
echo "phase=application component=application state=healthy reason=application-health action=health elapsed=1s"
sleep 0.05
printf 'APP_URL=https://mail.example.com\nORBIT_IMAGE=ghcr.io/tomlawesome/orbit@sha256:abc\n' > .env-orbit
echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=1s"
exit 0
`

// fakeRefusalScript is the engine's documented non-interactive
// configuration refusal: a failed configuration event, guidance on
// stderr, exit 1, target untouched.
const fakeRefusalScript = `#!/usr/bin/env bash
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
sleep 0.05
echo "phase=configuration component=configuration state=failed reason=configuration-failure action=retry elapsed=0s"
echo "Orbit installer: configuration fields requiring attention: APP_URL OIDC_ISSUER." >&2
exit 1
`

// serveScript stands up a local server the launcher fetches "install.sh"
// from via ORBIT_LAUNCHER_INSTALL_SCRIPT_URL — the same override the
// installer-compat-watch CI harness uses.
func serveScript(t *testing.T, script string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(script))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func startConsolePTY(t *testing.T, binPath, dir, scriptURL string) (*expect.Console, *exec.Cmd) {
	t.Helper()

	console, err := expect.NewConsole(expect.WithDefaultTimeout(10 * time.Second))
	if err != nil {
		t.Fatalf("create console: %v", err)
	}
	t.Cleanup(func() { console.Close() })
	if err := pty.Setsize(console.Tty(), &pty.Winsize{Rows: 26, Cols: 80}); err != nil {
		t.Fatalf("set pty size: %v", err)
	}

	cmd := exec.Command(binPath)
	cmd.Dir = dir
	cmd.Stdin = console.Tty()
	cmd.Stdout = console.Tty()
	cmd.Stderr = console.Tty()
	cmd.Env = append(os.Environ(), "TERM=xterm", "NO_COLOR=1",
		"ORBIT_LAUNCHER_NO_UPDATE_CHECK=1", "ORBIT_LAUNCHER_NO_HEALTH_PROBE=1",
		"ORBIT_LAUNCHER_INSTALL_SCRIPT_URL="+scriptURL)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orbit-launcher: %v", err)
	}
	return console, cmd
}

// driveToInstallNow walks splash -> profile -> confirm and confirms.
func driveToInstallNow(t *testing.T, console *expect.Console) {
	t.Helper()
	must := func(s string) {
		t.Helper()
		if _, err := console.ExpectString(s); err != nil {
			t.Fatalf("expected %q: %v", s, err)
		}
	}
	skipArrival(t, console)
	must("▸ Install")
	if _, err := console.Send("\r"); err != nil {
		t.Fatalf("send: %v", err)
	}
	must("Choose a deployment profile")
	if _, err := console.Send("\r"); err != nil {
		t.Fatalf("send: %v", err)
	}
	must("Ready to install")
	if _, err := console.Send("\r"); err != nil {
		t.Fatalf("send: %v", err)
	}
}

func TestConsole_RealPTY_InstallStreamsEventsToSuccessScreen(t *testing.T) {
	binPath := buildBinary(t)
	dir := t.TempDir()
	console, cmd := startConsolePTY(t, binPath, dir, serveScript(t, fakeEngineScript))

	driveToInstallNow(t, console)

	must := func(s string) {
		t.Helper()
		if _, err := console.ExpectString(s); err != nil {
			t.Fatalf("expected %q: %v", s, err)
		}
	}

	// The mission console: streamed events render natively, inside the
	// TUI — the immersive end-to-end promise.
	must("ORBIT · Install — Standard")
	must("Starting Orbit") // the application phase's stage word

	// The success screen: hero URL in the identity slot, achieved
	// footer, stacked menu.
	must("https://mail.example.com")
	must("alive")
	must("Get into Orbit")
	must("Orbit achieved in")

	// Terminal quits cleanly, restoring the terminal.
	if _, err := console.Send("\x1b[B"); err != nil {
		t.Fatalf("send Down: %v", err)
	}
	if _, err := console.Send("\r"); err != nil {
		t.Fatalf("send Enter: %v", err)
	}
	waitForExit(t, cmd)

	// The engine really was invoked in contract mode: plain, with the
	// explicit action flag.
	args, err := os.ReadFile(filepath.Join(dir, "engine-args.txt"))
	if err != nil {
		t.Fatalf("engine was not run in the target dir: %v", err)
	}
	if got := string(args); got != "--plain --install\n" {
		t.Errorf("engine args = %q, want --plain --install", got)
	}
}

func TestConsole_RealPTY_ConfigurationRefusalShowsStyledPrompt(t *testing.T) {
	binPath := buildBinary(t)
	dir := t.TempDir()
	console, cmd := startConsolePTY(t, binPath, dir, serveScript(t, fakeRefusalScript))

	driveToInstallNow(t, console)

	if _, err := console.ExpectString("Orbit needs your configuration"); err != nil {
		t.Fatalf("expected the styled configuration prompt: %v", err)
	}
	if _, err := console.ExpectString("Continue — guided configuration"); err != nil {
		t.Fatalf("expected the handoff option: %v", err)
	}

	// Escape returns to the menu (the refusal rolled the target back;
	// nothing was changed), and Escape again quits cleanly.
	if _, err := console.Send("\x1b"); err != nil {
		t.Fatalf("send Escape: %v", err)
	}
	if _, err := console.ExpectString("▸ Install"); err != nil {
		t.Fatalf("expected the splash again after Menu: %v", err)
	}
	if _, err := console.Send("\x1b"); err != nil {
		t.Fatalf("send Escape: %v", err)
	}
	waitForExit(t, cmd)
}
