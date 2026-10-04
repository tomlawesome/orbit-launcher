package pty

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
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
# A second on the application stage: the test reads the screen, not the
# byte stream, and a stage overdrawn within 50 ms can fall between looks.
sleep 1
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
// from via ORBIT_LAUNCHER_INSTALL_SCRIPT_URL — the same override orbit's
// launcher-install-compat gate uses.
func serveScript(t *testing.T, script string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(script))
	}))
	t.Cleanup(server.Close)
	return server.URL
}

func startConsolePTY(t *testing.T, binPath, dir, scriptURL string) (*vtConsole, *exec.Cmd) {
	t.Helper()

	console := newConsole(t, 80, 26, 10*time.Second)

	cmd := exec.Command(binPath)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "TERM=xterm", "NO_COLOR=1",
		"ORBIT_LAUNCHER_NO_UPDATE_CHECK=1", "ORBIT_LAUNCHER_NO_HEALTH_PROBE=1",
		"ORBIT_LAUNCHER_NO_VOLUME_CHECK=1",
		"ORBIT_LAUNCHER_INSTALL_SCRIPT_URL="+scriptURL)
	// Same leak guard as startUnderPTYInDir: a t.Fatalf exit must not
	// leave the spawned binary alive on a dead pty.
	console.start(t, cmd)
	return console, cmd
}

// driveToInstallNow walks splash -> profile -> confirm and confirms.
func driveToInstallNow(t *testing.T, console *vtConsole) {
	t.Helper()
	must := func(s string) {
		t.Helper()
		if err := console.expectString(s); err != nil {
			t.Fatalf("expected %q: %v", s, err)
		}
	}
	skipArrival(t, console)
	must("▸ Install")
	console.send("\r")
	must("Choose a deployment profile")
	console.send("\r")
	must("Ready to install")
	console.send("\r")
	passNotice(t, console)
}

// noticeWait outlasts the development notice's countdown (#175). The
// binary keeps its full 70 s — nothing outside it can shorten an
// approved gate — so a run through Install now waits it out.
const noticeWait = 72 * time.Second

// passNotice gets past the development notice the way a person does:
// End shows the whole notice, the countdown runs out, the phrase is
// typed and Enter accepts it. Nothing here has to keep reading the pty
// while the countdown redraws: vttest's emulator drains the program's
// output into the virtual screen on its own goroutine, so the binary
// can never block on its own output while this waits.
func passNotice(t *testing.T, console *vtConsole) {
	t.Helper()
	if err := console.expectString("A note before you install"); err != nil {
		t.Fatalf("expected the development notice: %v", err)
	}
	console.send("\x1b[F") // End
	time.Sleep(noticeWait)
	console.send("I've read this and I understand\r")
}

func TestConsole_RealPTY_InstallStreamsEventsToSuccessScreen(t *testing.T) {
	// Parallel: each run waits out the development notice's real
	// countdown (passNotice), and six of those in series would
	// spend most of the package's default timeout.
	t.Parallel()
	binPath := buildBinary(t)
	dir := t.TempDir()
	console, cmd := startConsolePTY(t, binPath, dir, serveScript(t, fakeEngineScript))

	driveToInstallNow(t, console)

	must := func(s string) {
		t.Helper()
		if err := console.expectString(s); err != nil {
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
	console.send("\x1b[B")
	console.send("\r")
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
	// Parallel: each run waits out the development notice's real
	// countdown (passNotice), and six of those in series would
	// spend most of the package's default timeout.
	t.Parallel()
	binPath := buildBinary(t)
	dir := t.TempDir()
	console, cmd := startConsolePTY(t, binPath, dir, serveScript(t, fakeRefusalScript))

	driveToInstallNow(t, console)

	if err := console.expectString("Orbit needs your configuration"); err != nil {
		t.Fatalf("expected the styled configuration prompt: %v", err)
	}
	if err := console.expectString("Continue — guided configuration"); err != nil {
		t.Fatalf("expected the handoff option: %v", err)
	}

	// Escape returns to the menu (the refusal rolled the target back;
	// nothing was changed), and Escape again quits cleanly.
	console.send("\x1b")
	if err := console.expectString("▸ Install"); err != nil {
		t.Fatalf("expected the splash again after Menu: %v", err)
	}
	console.send("\x1b")
	waitForExit(t, cmd)
}
