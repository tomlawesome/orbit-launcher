package pty

import (
	"testing"
	"time"

	expect "github.com/Netflix/go-expect"
)

// Issue #157: in CI the launcher's screen stopped changing about twenty
// seconds into the *retried* engine run — the second run, started after
// in-console configuration was collected and adopted — and never moved
// again, while install.sh completed underneath and the stack came up
// healthy. The last thing the console showed was the database phase
// starting.
//
// The cause is in internal/engine's line reading and is pinned there by
// TestStart_AnOverlongLineDoesNotStopTheStream. This test is the same
// defect seen from outside, through the real binary on a real pty: the
// launcher must reach its success screen even when a phase emits a line
// far longer than the reader's buffer, which install.sh's database wait
// does whenever it reports progress with carriage returns for long
// enough.
//
// Only the retried run hits it in CI because the first run refuses for
// configuration long before the database phase.
const fakeLongLineRetryEngine = `#!/usr/bin/env bash
printf '%s\n' "$*" >> engine-args.txt
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
if [[ ! -f .env-orbit ]]; then
  echo "phase=configuration component=configuration state=failed reason=configuration-failure action=retry elapsed=0s"
  echo "Orbit installer: configuration fields requiring attention: APP_URL." >&2
  exit 1
fi
echo "phase=assets component=assets state=completed reason=assets-staged action=stage elapsed=0s"
echo "phase=configuration component=configuration state=completed reason=configuration-ready action=configure elapsed=0s"
echo "phase=compose component=compose state=completed reason=compose-valid action=validate elapsed=1s"
echo "phase=database component=postgres state=starting reason=database-boot action=wait elapsed=1s"
# The database wait, reporting progress with carriage returns and no
# newline — one line, far past the reader's buffer.
for i in $(seq 1 4000); do printf ' waiting for postgres (%s)\r' "$i"; done
printf '\n'
# And then the rest of the install, which must still arrive.
for i in $(seq 1 200); do printf ' Container orbit-service-%03d  Started\n' "$i"; done
echo "phase=database component=postgres state=healthy reason=database-ready action=wait elapsed=8s"
echo "phase=application component=application state=healthy reason=application-health action=health elapsed=9s"
echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=9s"
exit 0
`

// TestConfig_RealPTY_RetriedRunSurvivesAnOverlongEngineLine drives the
// #157 journey — refusal, in-console prompts, adoption, retry — and then
// lets the retried run emit the line that used to end the stream. The
// success screen is the whole assertion: reaching it means the launcher
// kept draining the engine to its exit.
func TestConfig_RealPTY_RetriedRunSurvivesAnOverlongEngineLine(t *testing.T) {
	binPath := buildBinary(t)
	dir := t.TempDir()
	scriptURL := serveOrbitTree(t, map[string]string{
		"/scripts/install.sh":   fakeLongLineRetryEngine,
		"/scripts/configure.sh": fakeMachineConfigure,
		"/.env-orbit.example":   "APP_URL=\n",
	})
	console, cmd := startConsolePTY(t, binPath, dir, scriptURL)

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
	send("https://longline.example.test\r")

	must("OIDC client secret")
	send("longline-secret-value\r")

	if _, err := console.Expect(expect.String("Get into Orbit"), expect.WithTimeout(60*time.Second)); err != nil {
		t.Fatalf("launcher never reached the success screen after the retried run's over-long line (#157): %v", err)
	}

	send("\x1b[B")
	send("\r")
	waitForExit(t, cmd)
}
