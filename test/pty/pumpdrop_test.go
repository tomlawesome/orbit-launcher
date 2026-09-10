package pty

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	expect "github.com/Netflix/go-expect"
	"github.com/creack/pty"
)

// Issue #159: the launcher stops asking the engine for its next message
// part-way through a run. The engine finishes, posts DoneMsg and exits;
// nothing ever reads it, and the launcher sits on a static screen.
//
// TestConfig_RealPTY_InConsolePromptsThenRetrySucceeds drives the same
// journey and passes, so the journey is not the variable. What CI has
// and that test does not is startLive's environment. Three differences,
// all reproduced here:
//
//   - ORBIT_LAUNCHER_NO_ANIMATION=1, which makes SplashModel.Init skip
//     tick() entirely, so the engine pump is the only message chain in
//     the process — a dropped pump is then unrecoverable rather than
//     merely stale;
//   - the health probe left enabled against an unresolvable APP_URL,
//     whose result lands as a message part-way through the run;
//   - the launcher started as a session leader with a controlling
//     terminal.
//
// startCIShapedPTY is startConsolePTY with startLive's environment and
// process attributes, so a failure here is the CI failure and not a
// different one.
func startCIShapedPTY(t *testing.T, binPath, dir, scriptURL string) (*expect.Console, *exec.Cmd) {
	t.Helper()

	console, err := expect.NewConsole(expect.WithDefaultTimeout(30 * time.Second))
	if err != nil {
		t.Fatalf("create console: %v", err)
	}
	t.Cleanup(func() { console.Close() })
	if err := pty.Setsize(console.Tty(), &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Fatalf("set pty size: %v", err)
	}

	stderrFile, err := os.Create(filepath.Join(t.TempDir(), "launcher-stderr.log"))
	if err != nil {
		t.Fatalf("create stderr log: %v", err)
	}
	t.Cleanup(func() { stderrFile.Close() })

	cmd := exec.Command(binPath)
	cmd.Dir = dir
	cmd.Stdin = console.Tty()
	cmd.Stdout = console.Tty()
	cmd.Stderr = stderrFile
	cmd.Env = append(os.Environ(),
		"TERM=xterm", "NO_COLOR=1",
		"ORBIT_LAUNCHER_NO_UPDATE_CHECK=1",
		"ORBIT_LAUNCHER_NO_VOLUME_CHECK=1",
		"ORBIT_LAUNCHER_NO_ANIMATION=1",
		"ORBIT_LAUNCHER_REQUIRE_IN_CONSOLE_CONFIG=1",
		"ORBIT_LAUNCHER_INSTALL_SCRIPT_URL="+scriptURL)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orbit-launcher: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return console, cmd
}

// fakeDatabasePhaseEngine refuses without configuration, then on the
// retried run reaches the database phase and narrates it the way
// install.sh does — a steady mix of events and prose, over seconds
// rather than instantly.
const fakeDatabasePhaseEngine = `#!/usr/bin/env bash
printf '%s\n' "$*" >> engine-args.txt
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
if [[ ! -f .env-orbit ]]; then
  echo "phase=configuration component=configuration state=failed reason=configuration-failure action=retry elapsed=0s"
  echo "Orbit installer: configuration fields requiring attention: APP_URL." >&2
  exit 1
fi
echo "phase=image component=image state=completed reason=image-ready action=pull elapsed=0s"
echo "phase=assets component=assets state=completed reason=assets-staged action=stage elapsed=0s"
echo "Orbit configuration is ready. Existing values were preserved."
echo "phase=configuration component=configuration state=completed reason=configuration-ready action=configure elapsed=1s"
echo "phase=oidc component=oidc state=completed reason=oidc-discovered action=discover elapsed=1s"
echo "phase=compose component=compose state=completed reason=compose-valid action=validate elapsed=1s"
echo "Orbit installer: configuration, OIDC discovery, and Docker Compose preflight all passed."
echo "phase=database component=postgres state=starting reason=database-boot action=wait elapsed=2s"
for i in $(seq 1 400); do
  printf ' Container orbit-service-%04d  Started\n' "$i"
  if (( i %% 250 == 0 )); then
    printf 'phase=database component=postgres state=starting reason=database-boot action=wait elapsed=%%ss\n' "$((i/50+2))"
  fi
done
sleep 1
echo "phase=database component=postgres state=healthy reason=database-ready action=wait elapsed=8s"
echo "phase=application component=application state=healthy reason=application-health action=health elapsed=9s"
echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=9s"
exit 0
`

// TestConfig_CIShapedPTY_RetriedRunReachesSuccess is #159 as CI meets
// it: the launcher must reach its success screen with no tick chain to
// carry it, which means it must keep reading the engine to the end.
func TestConfig_CIShapedPTY_RetriedRunReachesSuccess(t *testing.T) {
	binPath := buildBinary(t)
	dir := t.TempDir()
	scriptURL := serveOrbitTree(t, map[string]string{
		"/scripts/install.sh":   fakeDatabasePhaseEngine,
		"/scripts/configure.sh": fakeMachineConfigure,
		"/.env-orbit.example":   "APP_URL=\n",
	})
	console, cmd := startCIShapedPTY(t, binPath, dir, scriptURL)

	must := func(s string) {
		t.Helper()
		if _, err := console.ExpectString(s); err != nil {
			t.Fatalf("expected %q: %v", s, err)
		}
	}
	send := func(s string) {
		t.Helper()
		if _, err := console.Send(s); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	driveToInstallNow(t, console)

	must("Orbit needs your configuration")
	must("Continue — guided configuration")
	send("\r")

	must("Public Orbit origin")
	send("https://pumpdrop.example.test\r")

	must("OIDC client secret")
	send("pumpdrop-secret-value\r")

	if _, err := console.Expect(expect.String("Get into Orbit"), expect.WithTimeout(60*time.Second)); err != nil {
		t.Fatalf("launcher never reached the success screen — the engine pump was dropped (#159): %v", err)
	}

	send("\x1b[B")
	send("\r")
	waitForExit(t, cmd)
}
