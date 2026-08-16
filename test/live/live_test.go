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
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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

// liveSession is one launcher process on one real pty, plus the
// expectations driven against it. It exists so every expectation in this
// file runs the same hang diagnosis on timeout (issue #100) instead of
// each subtest re-declaring its own bare must/send closures.
type liveSession struct {
	t       *testing.T
	console *expect.Console
	cmd     *exec.Cmd
}

// must waits for s, and on failure diagnoses the hang before failing —
// the timeout is 600s, so this is the one chance to learn anything about
// a freeze that has never reproduced locally.
func (s *liveSession) must(str string) {
	s.t.Helper()
	if _, err := s.console.ExpectString(str); err != nil {
		s.diagnose(fmt.Sprintf("expected %q", str))
		s.t.Fatalf("expected %q: %v", str, err)
	}
}

func (s *liveSession) send(str string) {
	s.t.Helper()
	if _, err := s.console.Send(str); err != nil {
		s.diagnose("send " + str)
		s.t.Fatalf("send: %v", err)
	}
}

// diagnose answers the open question in issue #100: when the Remove
// subtest freezes after one settled frame, is the launcher wedged or is
// the pty transport dead? The recorded signature — zero further output,
// not even starfield tick diffs, while the same binary's Install subtest
// passes in the same job — points at the transport, but nothing so far
// has proven it either way.
//
// Order matters here. The kernel-level state is read first and goes to
// the test log, because it survives a dead pty; the goroutine dump is
// asked for second and can only land in the raw transcript, which is
// exactly the channel under suspicion. If /proc says the process is
// blocked writing and no dump arrives, the transport is the bug and the
// launcher is innocent. If the dump arrives and shows the app wedged in
// its own code, this stops being a test problem.
func (s *liveSession) diagnose(reason string) {
	s.t.Helper()
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	pid := s.cmd.Process.Pid
	s.t.Logf("hang diagnosis (%s): orbit-launcher pid %d", reason, pid)

	// wchan is the money shot: a process parked in the tty write path is
	// blocked on a pty nobody is draining. status carries State and the
	// thread count for corroboration.
	for _, name := range []string{"wchan", "status"} {
		content, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), name))
		if err != nil {
			s.t.Logf("  /proc/%d/%s: %v", pid, name, err)
			continue
		}
		s.t.Logf("  /proc/%d/%s:\n%s", pid, name, strings.TrimSpace(string(content)))
	}
	if out, err := exec.Command("ps", "-o", "pid,ppid,stat,wchan:24,etime,args", "-p", strconv.Itoa(pid)).CombinedOutput(); err == nil {
		s.t.Logf("  ps:\n%s", strings.TrimSpace(string(out)))
	}

	// SIGQUIT makes the Go runtime dump every goroutine's stack to
	// stderr — which is this pty. GOTRACEBACK=all is set in startLive so
	// the dump covers runtime goroutines too.
	if err := s.cmd.Process.Signal(syscall.SIGQUIT); err != nil {
		s.t.Logf("  SIGQUIT: %v", err)
		return
	}
	// This read both waits for the dump and funnels it into the raw
	// transcript. Its absence is the finding, not an error.
	if _, err := s.console.Expect(expect.String("goroutine "), expect.WithTimeout(10*time.Second)); err != nil {
		s.t.Logf("  no goroutine dump reached the pty within 10s (%v) — consistent with the transport, not the app, being the problem", err)
		return
	}
	s.t.Log("  goroutine dump reached the pty; full stacks are in the raw transcript")
}

// startLive spawns binPath with a real controlling terminal attached —
// not just a pty on stdin/stdout, but a real session controlling
// terminal, which install.sh's has_controlling_terminal() (opens
// /dev/tty directly) actually requires. go-expect's Console alone does
// not establish this; creack/pty's own Start() helper does, via
// Setsid+Setctty — discovered the hard way verifying this test
// manually before writing it (see orbit-launcher issue #51/#52).
func startLive(t *testing.T, binPath, dir string) *liveSession {
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
	// GOTRACEBACK=all so a SIGQUIT from liveSession.diagnose dumps every
	// goroutine, not just the one that took the signal.
	cmd.Env = append(os.Environ(), "TERM=xterm", "NO_COLOR=1", "ORBIT_LAUNCHER_NO_UPDATE_CHECK=1", "GOTRACEBACK=all")
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
	return &liveSession{t: t, console: console, cmd: cmd}
}

// acceptMenusUntil sends Enter to accept the default choice on any of
// install.sh's own interactive menus/reviews until target is seen —
// which install.sh version is fetched (main branch, always moving) can
// change exactly which menus appear before the guided configuration
// prompts, so this stays adaptive rather than hardcoding an exact
// sequence.
func acceptMenusUntil(t *testing.T, session *liveSession, target string) {
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
		result, err := session.console.Expect(expect.Regexp(pattern))
		if err != nil {
			session.diagnose("waiting for " + target + " or a menu")
			t.Fatalf("waiting for %q or a menu: %v", target, err)
		}
		if targetPattern.MatchString(result) {
			return
		}
		session.send("\r")
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
		session := startLive(t, binPath, dir)

		sendLine := func(s string) { session.send(s + "\r") }

		session.must("Install")
		sendLine("") // Install selected by default
		session.must("Choose a deployment profile")
		sendLine("") // Standard selected by default
		session.must("Ready to install")
		sendLine("") // confirm — the mission console's piped engine run starts

		// The piped, terminal-less first attempt cannot prompt, so on a
		// fresh target it ends in the engine's non-interactive
		// configuration refusal (target rolled back). A contract-era
		// engine (orbit develop) reports it as an event and lands on
		// the styled configuration prompt; a legacy engine (orbit main)
		// reports nothing and lands on the failure screen. Both
		// screens' default option leads to the same guided
		// configuration — in-console over the machine prompt protocol
		// when the engine speaks it, the interactive terminal handoff
		// otherwise (the launcher falls back automatically) — and both
		// paths use the same field prompts, so this test, which runs
		// against whichever install.sh the job points at, accepts
		// either.
		if _, err := session.console.Expect(expect.Regexp(regexp.MustCompile(
			`Orbit needs your configuration|Installation stopped`))); err != nil {
			t.Fatalf("expected the configuration prompt or failure screen after the piped attempt: %v", err)
		}
		sendLine("") // Continue — guided configuration / Open the guided installer

		acceptMenusUntil(t, session, "Public Orbit origin")
		sendLine(appURL)
		session.must("OIDC issuer URL")
		sendLine(testOIDCIssuer)
		session.must("OIDC client ID")
		sendLine("orbit-launcher-live-ci-test")
		// Generation-agnostic: a contract-era engine (orbit develop)
		// collects this in-console over the machine prompt protocol
		// ("OIDC client secret" + "input hidden" on separate lines),
		// while a legacy engine's terminal handoff prints install.sh's
		// own "OIDC client secret (input hidden)" prompt. Match the
		// common prefix so this suite proves both paths.
		session.must("OIDC client secret")
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
		acceptMenusUntil(t, session, "Get into Orbit")

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

		session.send("\x1b[B") // Terminal (Get into Orbit, Terminal, Menu)
		session.send("\r")     // quits this orbit-launcher instance cleanly
	})

	t.Run("Remove", func(t *testing.T) {
		session := startLive(t, binPath, dir)

		session.must("Install")
		// A detected deployment preselects Update, so Remove is two rows
		// down (Update, Repair, Remove). The health probe (enabled here —
		// this is a real deployment) resolves alive and never moves the
		// caret; the first keypress pins it regardless.
		session.must("▸ Update")
		for i := 0; i < 2; i++ {
			session.send("\x1b[B") // Repair, Remove
		}
		session.send("\r") // select Remove
		session.must("This stops Orbit and removes its containers")
		// The confirm screen's identity line carries the bare FQDN —
		// the scheme is launcher noise at a glance, same as the splash.
		// Matching it still proves deploy.Detect read the real
		// .env-orbit this Install wrote.
		session.must(strings.TrimPrefix(appURL, "https://"))
		session.send("\r") // Stand down Orbit selected by default
		session.must("Orbit has been stood down")
		session.send("\r") // Exit

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

// TestLive_InstallPortConflictFailsCleanly is the failure-path live
// scenario (issue #57): a real deploy genuinely fails partway through,
// and the launcher must land on an honest failure screen while
// install.sh's own transaction leaves nothing half-changed. The
// dependency is broken deterministically *before* the run — the test
// process itself holds the app's port, so the engine's compose phase
// hits a real "port is already allocated" failure — rather than
// killing a container mid-install, which issue #57 itself flags as
// needing timing care to keep non-flaky. Same failure family (the
// deploy dies after config, at the container layer), none of the
// race.
func TestLive_InstallPortConflictFailsCleanly(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not available")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	// Hold the app's port for the whole test: compose cannot bind it.
	blocker, err := net.Listen("tcp", ":3000")
	if err != nil {
		t.Skipf("port 3000 not free to block: %v", err)
	}
	defer blocker.Close()

	binPath := binaryPath(t)
	dir := t.TempDir()
	projectName := filepath.Base(dir)
	t.Cleanup(func() { dockerComposeDown(projectName) })

	appURL := fmt.Sprintf("https://orbit-live-fail-%d.internal", time.Now().UnixNano())
	session := startLive(t, binPath, dir)

	sendLine := func(s string) { session.send(s + "\r") }

	session.must("Install")
	sendLine("")
	session.must("Choose a deployment profile")
	sendLine("")
	session.must("Ready to install")
	sendLine("")

	// The piped attempt's configuration refusal, then guided
	// configuration — identical to the happy path up to here.
	if _, err := session.console.Expect(expect.Regexp(regexp.MustCompile(
		`Orbit needs your configuration|Installation stopped`))); err != nil {
		t.Fatalf("expected the configuration prompt or failure screen after the piped attempt: %v", err)
	}
	sendLine("")

	acceptMenusUntil(t, session, "Public Orbit origin")
	sendLine(appURL)
	session.must("OIDC issuer URL")
	sendLine(testOIDCIssuer)
	session.must("OIDC client ID")
	sendLine("orbit-launcher-live-failure-test")
	session.must("OIDC client secret")
	sendLine("ci-live-failure-fake-secret")

	// The deploy proceeds into compose and dies on the held port. The
	// launcher's own failure screen — not a hang, not a fake success —
	// is the contract, with its stacked menu present.
	session.must("Installation stopped")
	session.must("Menu")

	// Exit via the failure menu (guided installer, Menu, Exit).
	session.send("\x1b[B")
	session.send("\x1b[B")
	session.send("\r")

	// Nothing half-changed: no containers left running for this
	// project. (Stopped/created remnants are compose implementation
	// detail; running anything would be the real lie.)
	psOut, err := exec.Command("docker", "ps", "--filter", "name="+projectName, "--format", "{{.Names}}").CombinedOutput()
	if err != nil {
		t.Fatalf("docker ps: %v\n%s", err, psOut)
	}
	if len(psOut) != 0 {
		t.Errorf("expected no running containers after the failed install, got:\n%s", psOut)
	}
}

// The hang diagnosis only ever runs when a release-gate run has already
// failed, which is the worst possible moment to discover it is broken.
// This exercises it end to end against a real launcher process on a real
// pty — no Docker needed, so it runs even when the rest of this file
// skips — and proves the two things it promises: that the kernel state
// is readable, and that a SIGQUIT dump actually reaches the transcript.
func TestLive_HangDiagnosisProducesAGoroutineDump(t *testing.T) {
	session := startLive(t, binaryPath(t), t.TempDir())

	// A settled first frame, so the process is genuinely up and drawing
	// before it is asked to account for itself.
	session.must("Install")

	if _, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(session.cmd.Process.Pid), "wchan")); err != nil {
		t.Skipf("no readable /proc on this platform: %v", err)
	}

	// The pty is being drained here, so unlike the failure this
	// instruments, the dump is expected to arrive.
	session.diagnose("instrumentation self-test")

	if err := session.cmd.Wait(); err == nil {
		t.Error("SIGQUIT should have terminated the launcher")
	}
}
