package pty

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file proves the two orbit-develop-era flows end to end — real
// binary, real pty, real HTTP fetches, real subprocesses speaking the
// documented contracts:
//
//   - in-console guided configuration over the machine prompt protocol
//     (orbit#297): refusal → prompts inside the TUI → adoption → retry
//     → success, with the launcher's own fetch/stage/import/check
//     plumbing (deploy.FetchConfigTree and friends) on the real path;
//   - repair diagnosis (orbit#261 slice 1): fetch repair.sh, stage it
//     into the deployment, parse findings, exit-code outcome.

// fakeConfigAwareEngine refuses without configuration and succeeds
// with it — how the real engine behaves across the collect-then-retry
// journey.
const fakeConfigAwareEngine = `#!/usr/bin/env bash
printf '%s\n' "$*" >> engine-args.txt
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
if [[ ! -f .env-orbit ]]; then
  echo "phase=configuration component=configuration state=failed reason=configuration-failure action=retry elapsed=0s"
  echo "Orbit installer: configuration fields requiring attention: APP_URL." >&2
  exit 1
fi
echo "phase=application component=application state=healthy reason=application-health action=health elapsed=1s"
echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=1s"
exit 0
`

// fakeMachineConfigure speaks the machine prompt protocol exactly as
// orbit's configure.sh does (verified against the real script), for
// the two steps the launcher drives.
const fakeMachineConfigure = `#!/usr/bin/env bash
case "$1" in
  --check)
    if [[ -f .env-orbit ]]; then
      printf 'ready APP_URL\nready OIDC_CLIENT_SECRET\n'
      exit 0
    fi
    printf 'missing APP_URL\nmissing OIDC_CLIENT_SECRET\n'
    exit 1
    ;;
  --init)
    [[ "${ORBIT_CONFIGURE_PROMPTS:-}" == machine ]] || { echo "Orbit configuration: needs a terminal." >&2; exit 1; }
    echo "prompt field=APP_URL kind=url required=true attempt=1"
    read -r answer || { echo "prompt-abort field=APP_URL"; exit 1; }
    echo "prompt-accept field=APP_URL"
    printf 'APP_URL=%s\n' "$answer" > .env-orbit
    chmod 600 .env-orbit
    exit 0
    ;;
  --set-oidc-secret)
    [[ "${ORBIT_CONFIGURE_PROMPTS:-}" == machine ]] || exit 1
    echo "prompt field=OIDC_CLIENT_SECRET kind=secret required=true attempt=1"
    read -r secret || { echo "prompt-abort field=OIDC_CLIENT_SECRET"; exit 1; }
    echo "prompt-accept field=OIDC_CLIENT_SECRET"
    mkdir -p .orbit-secrets && chmod 700 .orbit-secrets
    printf '%s\n' "$secret" > .orbit-secrets/oidc-client-secret
    chmod 600 .orbit-secrets/oidc-client-secret
    exit 0
    ;;
esac
exit 2
`

const fakeRepairCheck = `#!/usr/bin/env bash
[[ "$1" == "--check" ]] || exit 2
echo "finding class=secret-missing target=postgres-password severity=warn"
echo "diagnosis result=attention checked=12 skipped=1"
exit 3
`

// serveOrbitTree stands up a path-aware fake of orbit's raw file tree
// and returns the install.sh URL for ORBIT_LAUNCHER_INSTALL_SCRIPT_URL
// — sibling scripts and the root template resolve exactly as they do
// against the real repository.
func serveOrbitTree(t *testing.T, files map[string]string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server.URL + "/scripts/install.sh"
}

func TestConfig_RealPTY_InConsolePromptsThenRetrySucceeds(t *testing.T) {
	binPath := buildBinary(t)
	dir := t.TempDir()
	scriptURL := serveOrbitTree(t, map[string]string{
		"/scripts/install.sh":   fakeConfigAwareEngine,
		"/scripts/configure.sh": fakeMachineConfigure,
		"/.env-orbit.example":   "APP_URL=\n",
	})
	console, cmd := startConsolePTY(t, binPath, dir, scriptURL)

	driveToInstallNow(t, console)

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

	// The refusal, then Continue into the in-console prompts.
	must("Orbit needs your configuration")
	must("Continue — guided configuration")
	send("\r")

	// The engine's own prompt for the public origin, answered in-TUI.
	must("Public Orbit origin")
	send("https://pty.example.test\r")

	// The secret step — the label says hidden, and the answer is.
	must("OIDC client secret")
	send("pty-secret-value\r")

	// Adoption + automatic retry: the engine now proceeds to success.
	must("https://pty.example.test")
	must("Get into Orbit")

	// Terminal quits cleanly.
	send("\x1b[B")
	send("\r")
	waitForExit(t, cmd)

	// The collected configuration really landed in the target with the
	// contract's permissions.
	env, err := os.ReadFile(filepath.Join(dir, ".env-orbit"))
	if err != nil {
		t.Fatalf("adopted .env-orbit: %v", err)
	}
	if !strings.Contains(string(env), "APP_URL=https://pty.example.test") {
		t.Errorf(".env-orbit = %q", env)
	}
	info, err := os.Stat(filepath.Join(dir, ".orbit-secrets", "oidc-client-secret"))
	if err != nil {
		t.Fatalf("adopted secret: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("secret mode = %o, want 600", info.Mode().Perm())
	}
	secret, err := os.ReadFile(filepath.Join(dir, ".orbit-secrets", "oidc-client-secret"))
	if err != nil || strings.TrimSpace(string(secret)) != "pty-secret-value" {
		t.Errorf("secret content = %q err=%v", secret, err)
	}

	// Both engine runs were contract-mode invocations.
	args, err := os.ReadFile(filepath.Join(dir, "engine-args.txt"))
	if err != nil {
		t.Fatalf("engine args: %v", err)
	}
	if got := string(args); got != "--plain --install\n--plain --install\n" {
		t.Errorf("engine args = %q, want two --plain --install runs", got)
	}
}

func TestRepair_RealPTY_DiagnosisRendersFindings(t *testing.T) {
	binPath := buildBinary(t)
	dir := t.TempDir()
	// An existing deployment, so the splash offers Repair.
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte("APP_URL=https://repair.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scriptURL := serveOrbitTree(t, map[string]string{
		"/scripts/install.sh": fakeConfigAwareEngine,
		"/scripts/repair.sh":  fakeRepairCheck,
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

	skipArrival(t, console)
	must("▸ Update") // deployment detected preselects Update
	send("\x1b[B")   // Repair
	send("\r")

	// The real fetch → stage → run → parse path, rendered honestly.
	must("Needs attention")
	must("postgres-password secret")
	must("absent or empty")
	must("12 checked · 1 skipped")
	must("repair actions arrive with a later Orbit release")

	// repair.sh really was staged into the deployment's scripts dir.
	if _, err := os.Stat(filepath.Join(dir, "scripts", "repair.sh")); err != nil {
		t.Errorf("staged repair.sh: %v", err)
	}

	// Menu returns to the splash; Escape quits.
	send("\r")
	must("▸ Update")
	send("\x1b")
	waitForExit(t, cmd)
}

func TestRepair_RealPTY_UnavailableOnLegacyOrbitLine(t *testing.T) {
	binPath := buildBinary(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte("APP_URL=https://repair.example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// No repair.sh in the served tree — orbit main today.
	scriptURL := serveOrbitTree(t, map[string]string{
		"/scripts/install.sh": fakeConfigAwareEngine,
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

	skipArrival(t, console)
	must("▸ Update")
	send("\x1b[B")
	send("\r")

	must("Diagnosis needs a newer Orbit")
	if _, err := os.Stat(filepath.Join(dir, "scripts")); !os.IsNotExist(err) {
		t.Error("nothing should have been staged when diagnosis is unavailable")
	}

	send("\r")
	must("▸ Update")
	send("\x1b")
	waitForExit(t, cmd)
}
