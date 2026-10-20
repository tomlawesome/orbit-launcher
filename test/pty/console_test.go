package pty

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/test/internal/launcher"
	"github.com/tomlawesome/orbit-launcher/test/internal/vtscreen"
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

func startConsolePTY(t *testing.T, binPath, dir, scriptURL string) (*vtConsole, *exec.Cmd) {
	t.Helper()

	console := newConsole(t, 80, 26, 10*time.Second)

	cmd := exec.Command(binPath)
	cmd.Dir = dir
	// The arrival animation is kept, as startUnderPTYInDir keeps it.
	cmd.Env = append(launcher.HermeticEnv(launcher.Animation), "ORBIT_LAUNCHER_INSTALL_SCRIPT_URL="+scriptURL)
	// Same leak guard as startUnderPTYInDir: a t.Fatalf exit must not
	// leave the spawned binary alive on a dead pty.
	console.start(t, cmd)
	return console, cmd
}

// driveToInstallNow walks splash -> profile -> Standard, which opens the
// notice (#248), then passes the notice (passNotice): it waits out the
// 20 s countdown, types the phrase and confirms. It does not stop with
// the notice open.
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
	passNotice(t, console)
}

// noticeWait outlasts the development notice's countdown (#175). The
// binary keeps its full 20 s (#250) — nothing outside it can shorten an
// approved gate — so a run through Standard waits it out.
const noticeWait = 22 * time.Second

// passNotice gets past the development notice the way a person does:
// End shows the whole notice, the countdown runs out, the phrase is
// typed and Enter accepts it. Nothing here has to keep reading the pty
// while the countdown redraws: vtscreen's emulator drains the program's
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
	binPath := launcher.BuildBinary(t)
	dir := t.TempDir()
	console, cmd := startConsolePTY(t, binPath, dir, launcher.ServeScript(t, fakeEngineScript))

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

// expectNoRemovedConfigScreen fails if the screen now showing is the one
// the engine's configuration stop used to land on (#251): the stop goes
// straight into guided configuration.
func expectNoRemovedConfigScreen(t *testing.T, console *vtConsole) {
	t.Helper()
	screen := vtscreen.Screen(console.term)
	for _, gone := range []string{"The engine stopped before touching anything", "Continue — guided configuration"} {
		if strings.Contains(screen, gone) {
			t.Errorf("the removed configuration screen is shown (%q):\n%s", gone, screen)
		}
	}
}

func TestConsole_RealPTY_ConfigurationRefusalGoesStraightIntoGuidedConfiguration(t *testing.T) {
	// Parallel: each run waits out the development notice's real
	// countdown (passNotice), and six of those in series would
	// spend most of the package's default timeout.
	t.Parallel()
	binPath := launcher.BuildBinary(t)
	dir := t.TempDir()
	console, cmd := startConsolePTY(t, binPath, dir, launcher.ServeScript(t, handsOverTree(fakeConfigAwareEngine, fakeMachineConfigure)))

	driveToInstallNow(t, console)

	// No key press: the refusal lands on the guided session's first
	// screen.
	if err := console.expectString("How will people sign in?"); err != nil {
		t.Fatalf("expected guided configuration straight after the refusal: %v", err)
	}
	expectNoRemovedConfigScreen(t, console)

	// Escape returns to the menu (the refusal rolled the target back;
	// nothing was changed), and Ctrl-C quits cleanly.
	console.send("\x1b")
	if err := console.expectString("▸ Install"); err != nil {
		t.Fatalf("expected the splash again after Esc: %v", err)
	}
	console.send("\x03")
	waitForExit(t, cmd)
}
