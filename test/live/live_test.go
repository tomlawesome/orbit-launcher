//go:build live

// Package live holds the real virtualized live test — see
// docs/implementation-plan.md section 3.5. It spawns the actual
// compiled binary under a real controlling terminal, drives it through
// a real Install against a real Docker daemon and a real OIDC
// discovery endpoint, asserts the deployed app actually answers, then
// drives a real Remove and asserts real cleanup. This is slow (real
// image pulls, real health checks — minutes, not seconds) and needs a
// real Docker daemon and real network access, so it's gated behind the
// "live" build tag and only ever run by its own CI job
// (.github/workflows/live-install-test.yml), never by `go test ./...`.
package live

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	expect "github.com/Netflix/go-expect"
	"github.com/creack/pty"
)

// testOIDCIssuer is a real, public, stable OIDC discovery endpoint.
// install.sh's verify_oidc_discovery only validates the discovery
// document's shape (issuer match, HTTPS endpoints) — it never proves a
// completable login, which is main orbit's own test responsibility,
// not orbit-launcher's. Using a real IdP here (rather than standing up
// a throwaway one) proves discovery validation against genuine HTTPS,
// with zero additional CI infrastructure.
const testOIDCIssuer = "https://accounts.google.com"

// binaryPath resolves the binary under test. Set
// ORBIT_LAUNCHER_LIVE_BINARY to test a specific artifact (the CI job
// sets this to the real, just-downloaded preview-latest binary, fetched
// via the real bootstrap script — see the workflow); unset, it builds
// fresh from source, which is what local runs and PR-triggered CI use.
func binaryPath(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("ORBIT_LAUNCHER_LIVE_BINARY"); p != "" {
		return p
	}

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

// startLive spawns binPath with a real controlling terminal attached —
// not just a pty on stdin/stdout, but a real session controlling
// terminal, which install.sh's has_controlling_terminal() (opens
// /dev/tty directly) actually requires. go-expect's Console alone does
// not establish this; creack/pty's own Start() helper does, via
// Setsid+Setctty — discovered the hard way verifying this test
// manually before writing it (see orbit-launcher issue #51/#52).
func startLive(t *testing.T, binPath, dir string) *expect.Console {
	t.Helper()

	// install.sh's own readiness wait defaults to 180s
	// (ORBIT_INSTALLER_READINESS_TIMEOUT_SECONDS) counted from a later
	// starting point than this console (after config collection, image
	// resolve, and asset staging) — a first boot's ClamAV virus-database
	// download in particular can approach that budget on its own. 180s
	// here (measured from process start) genuinely wasn't enough and
	// caused a false failure on an install that had, in fact, fully
	// succeeded (confirmed by Remove finding a real, complete
	// deployment afterward) — verified by actually running this against
	// real Docker before trusting the number. The mission console's
	// piped first attempt (which, on a fresh target, does the image
	// pull and asset staging before its configuration refusal) front-
	// loads more of that work, so the budget is higher still.
	opts := []expect.ConsoleOpt{expect.WithDefaultTimeout(600 * time.Second)}
	if rawLogPath := os.Getenv("ORBIT_LAUNCHER_LIVE_RAW_LOG"); rawLogPath != "" {
		// Suffixed per (sub)test — Install and Remove each call startLive
		// once, and os.Create truncates, so a single shared path would
		// silently let Remove's log erase Install's, exactly the run
		// that matters most when Install is the one hanging or failing.
		suffix := strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
		rawLog, err := os.Create(rawLogPath + "." + suffix)
		if err != nil {
			t.Fatalf("create raw log: %v", err)
		}
		t.Cleanup(func() { rawLog.Close() })
		opts = append(opts, expect.WithStdout(rawLog))
	}
	console, err := expect.NewConsole(opts...)
	if err != nil {
		t.Fatalf("create console: %v", err)
	}
	t.Cleanup(func() { console.Close() })
	if err := pty.Setsize(console.Tty(), &pty.Winsize{Rows: 40, Cols: 120}); err != nil {
		t.Fatalf("set pty size: %v", err)
	}

	cmd := exec.Command(binPath)
	cmd.Dir = dir
	cmd.Stdin = console.Tty()
	cmd.Stdout = console.Tty()
	cmd.Stderr = console.Tty()
	cmd.Env = append(os.Environ(), "TERM=xterm", "NO_COLOR=1", "ORBIT_LAUNCHER_NO_UPDATE_CHECK=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orbit-launcher: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	// Skip the splash's arrival animation with one benign key (any key
	// skips and is swallowed). Load-bearing twice over, learned from a
	// real 20-minute release-gate hang: menu text appears mid-cascade,
	// so an Enter sent right after matching it lands while the arrival
	// is still playing and gets swallowed as the skip — and because the
	// splash animates continuously, go-expect's read timeout never goes
	// idle, so the next expectation waits forever, not five seconds.
	if _, err := console.Send("s"); err != nil {
		t.Fatalf("send arrival-skip key: %v", err)
	}
	return console
}

// acceptMenusUntil sends Enter to accept the default choice on any of
// install.sh's own interactive menus/reviews until target is seen —
// which install.sh version is fetched (main branch, always moving) can
// change exactly which menus appear before the guided configuration
// prompts, so this stays adaptive rather than hardcoding an exact
// sequence.
func acceptMenusUntil(t *testing.T, console *expect.Console, target string) {
	t.Helper()
	pattern := regexp.MustCompile(`Greetings, what can we do for you today\?|Choose a deployment profile|Review:|Final review:|Optional services|` + regexp.QuoteMeta(target))
	targetPattern := regexp.MustCompile(regexp.QuoteMeta(target))
	for {
		// No explicit per-call timeout here — this falls back to the
		// console's own configured default (see startLive), which is
		// generous specifically because a real image pull plus health
		// checks can genuinely take minutes. An earlier draft hardcoded
		// 60s here, silently shadowing that default and causing a real
		// CI failure on a resource-constrained runner even though the
		// install had, in fact, not failed — just hadn't finished yet.
		result, err := console.Expect(expect.Regexp(pattern))
		if err != nil {
			t.Fatalf("waiting for %q or a menu: %v", target, err)
		}
		if targetPattern.MatchString(result) {
			return
		}
		if _, err := console.Send("\r"); err != nil {
			t.Fatalf("accept menu default: %v", err)
		}
	}
}

func dockerComposeDown(projectName string) {
	_ = exec.Command("docker", "compose", "-p", projectName, "down", "-v").Run()
}

// TestLive_InstallHealthyEndpointThenRemove is the real virtualized
// live test: a real Install (issue #19 — real Docker, real health
// checks, real HTTP response) followed by a real Remove against that
// same live deployment (issue #23 — real cleanup assertions). Kept as
// one test rather than two so Remove always runs against a deployment
// this same run actually installed, and so the (slow: real image
// pulls) setup cost is paid once.
func TestLive_InstallHealthyEndpointThenRemove(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	binPath := binaryPath(t)
	dir := t.TempDir()
	// install.sh derives the Compose project name from the target
	// directory's basename when none is set — t.TempDir() gives a
	// unique one per run, so concurrent/repeated CI runs never collide.
	projectName := filepath.Base(dir)
	t.Cleanup(func() { dockerComposeDown(projectName) })

	appURL := fmt.Sprintf("https://orbit-live-test-%d.internal", time.Now().UnixNano())

	t.Run("Install", func(t *testing.T) {
		console := startLive(t, binPath, dir)

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
		sendLine := func(s string) { send(s + "\r") }

		must("Install")
		sendLine("") // Install selected by default
		must("Choose a deployment profile")
		sendLine("") // Standard selected by default
		must("Ready to install")
		sendLine("") // confirm — the mission console's piped engine run starts

		// The piped, terminal-less first attempt cannot prompt, so on a
		// fresh target it ends in the engine's non-interactive
		// configuration refusal (target rolled back). A contract-era
		// engine (orbit develop) reports it as an event and lands on
		// the styled configuration prompt; a legacy engine (orbit main)
		// reports nothing and lands on the failure screen. Both
		// screens' default option is the same guided interactive
		// installer, so this test — which runs against whichever
		// install.sh the job points at — accepts either.
		if _, err := console.Expect(expect.Regexp(regexp.MustCompile(
			`Orbit needs your configuration|Installation stopped`))); err != nil {
			t.Fatalf("expected the configuration prompt or failure screen after the piped attempt: %v", err)
		}
		sendLine("") // Continue — guided configuration / Open the guided installer

		acceptMenusUntil(t, console, "Public Orbit origin")
		sendLine(appURL)
		must("OIDC issuer URL")
		sendLine(testOIDCIssuer)
		must("OIDC client ID")
		sendLine("orbit-launcher-live-ci-test")
		must("OIDC client secret (input hidden)")
		sendLine("ci-live-test-fake-secret-value")

		// Wait for the launcher's own success screen, never for the
		// installer's completion prose — orbit main's v1.2.0 release
		// reworded it ("Orbit is deployed from …" now; "Orbit is
		// ready." before), which is precisely why prose is unstable by
		// design and outcomes key off exit codes (learned the hard way:
		// this exact assertion timed out against a fully successful
		// install). The stacked success menu is this repo's own
		// contract. A completion-prose line mentioning "Optional
		// services" can match the adaptive menu pattern and cost one
		// stray Enter on the success screen — which lands on "Get into
		// Orbit", a deliberate no-quit action, so it is benign.
		acceptMenusUntil(t, console, "Get into Orbit")

		var lastStatus int
		var lastErr error
		healthy := false
		for i := 0; i < 60; i++ {
			resp, err := http.Get("http://localhost:3000/")
			if err == nil {
				lastStatus = resp.StatusCode
				resp.Body.Close()
				if lastStatus == http.StatusOK {
					healthy = true
					break
				}
			} else {
				lastErr = err
			}
			time.Sleep(2 * time.Second)
		}
		if !healthy {
			t.Fatalf("deployed Orbit app never answered 200 on :3000 (last status %d, last error %v)", lastStatus, lastErr)
		}

		send("\x1b[B") // Terminal (Get into Orbit, Terminal, Menu)
		send("\r")     // quits this orbit-launcher instance cleanly
	})

	t.Run("Remove", func(t *testing.T) {
		console := startLive(t, binPath, dir)

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

		must("Install")
		// A detected deployment preselects Update, so Remove is two rows
		// down (Update, Repair, Remove). The health probe (enabled here —
		// this is a real deployment) resolves alive and never moves the
		// caret; the first keypress pins it regardless.
		must("▸ Update")
		for i := 0; i < 2; i++ {
			send("\x1b[B") // Repair, Remove
		}
		send("\r") // select Remove
		must("This stops Orbit and removes its containers")
		must(appURL) // proves deploy.Detect read the real .env-orbit this Install wrote
		send("\r")   // Stand down Orbit selected by default
		must("Orbit has been stood down")
		send("\r") // Exit

		out, err := exec.Command("docker", "compose", "-p", projectName, "ps", "--format", "{{.Name}}").CombinedOutput()
		if err != nil {
			t.Fatalf("docker compose ps: %v\n%s", err, out)
		}
		if len(out) != 0 {
			t.Errorf("expected no containers after Remove, got:\n%s", out)
		}

		// Remove's own design promise: data volumes and files are
		// preserved (Remove is the reversible half; the copy-pasteable
		// command it shows, but never runs, is the destructive half —
		// see internal/deploy/removal_property_test.go, which already
		// asserts as a fast unit-test property that this package never
		// calls that command itself).
		if _, err := os.Stat(filepath.Join(dir, ".env-orbit")); err != nil {
			t.Errorf("expected .env-orbit to survive Remove, stat error: %v", err)
		}
		volOut, err := exec.Command("docker", "volume", "ls", "--filter", "name="+projectName, "--format", "{{.Name}}").CombinedOutput()
		if err != nil {
			t.Fatalf("docker volume ls: %v\n%s", err, volOut)
		}
		if len(volOut) == 0 {
			t.Error("expected the deployment's data volumes to survive Remove, found none")
		}
	})
}
