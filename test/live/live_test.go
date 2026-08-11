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
	// real Docker before trusting the number.
	opts := []expect.ConsoleOpt{expect.WithDefaultTimeout(420 * time.Second)}
	if rawLogPath := os.Getenv("ORBIT_LAUNCHER_LIVE_RAW_LOG"); rawLogPath != "" {
		rawLog, err := os.Create(rawLogPath)
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
		sendLine("") // confirm the handoff to install.sh

		acceptMenusUntil(t, console, "Public Orbit origin")
		sendLine(appURL)
		must("OIDC issuer URL")
		sendLine(testOIDCIssuer)
		must("OIDC client ID")
		sendLine("orbit-launcher-live-ci-test")
		must("OIDC client secret (input hidden)")
		sendLine("ci-live-test-fake-secret-value")

		// install.sh's own plain "Orbit is ready." and orbit-launcher's
		// resumed Done screen (which says the same thing) both land in
		// the same buffered read within milliseconds of each other —
		// waiting for this text a second time here is redundant (and,
		// discovered by actually running this, unreliable: nothing
		// guarantees a second distinct match event across that boundary)
		// and the resumed-screen rendering is already covered by
		// internal/ui's own InstallModel tests.
		acceptMenusUntil(t, console, "Orbit is ready")

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

		send("\r") // Exit — quits this orbit-launcher instance
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
		for i := 0; i < 3; i++ {
			send("\x1b[B") // Install, Update, Repair, Remove
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
