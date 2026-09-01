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

	"github.com/tomlawesome/orbit-launcher/internal/ui"
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
	// budget is the wall-clock ceiling used by expectWithin. It exists
	// as a field (rather than expectWithin reading the expectBudget
	// constant directly) so a test can substitute a short budget to
	// exercise the timeout path without waiting out the real one.
	budget time.Duration
}

// expectBudget is the wall-clock ceiling for any single expectation.
//
// go-expect's own timeout cannot serve as this (issue #121): it resets
// the read deadline before every rune (expect.go:82), so it measures
// idleness *between characters*, not total time waiting for a match.
// The starfield repaints continuously, so runes never stop arriving and
// an expectation that will never match never times out — the run dies at
// the job timeout instead, and diagnose below never gets to say anything.
// Matches the console's configured default so a healthy slow run (real
// image pull, real health checks) is unaffected.
const expectBudget = 600 * time.Second

// expectWithin runs one blocking expectation under a real wall-clock
// deadline, diagnosing the hang before failing.
//
// On the timeout path the expectation's goroutine is still parked inside
// go-expect reading runes, so it competes with diagnose for the pty.
// diagnose is told which case it is (consoleBusy) so it never issues its
// own console.Expect on top of the parked reader on the timeout path —
// doing so raced two goroutines against the same bufio.Reader and could
// panic ("slice bounds out of range"), destroying the diagnosis the code
// exists to produce.
func (s *liveSession) expectWithin(what string, expectation func() (string, error)) string {
	s.t.Helper()
	out, err := s.expectWithinErr(what, expectation)
	if err != nil {
		s.t.Fatalf("%s: %v", what, err)
	}
	return out
}

// expectWithinErr holds all of expectWithin's logic but returns the
// failure instead of calling t.Fatalf, so the timeout/diagnose path can
// be exercised and asserted on by a test.
func (s *liveSession) expectWithinErr(what string, expectation func() (string, error)) (string, error) {
	s.t.Helper()
	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := expectation()
		done <- outcome{out: out, err: err}
	}()
	select {
	case got := <-done:
		if got.err != nil {
			s.diagnose(what, false)
			return "", got.err
		}
		return got.out, nil
	case <-time.After(s.budget):
		s.diagnose(what, true)
		return "", fmt.Errorf("no match within %s of wall clock", s.budget)
	}
}

// must waits for str, and on failure diagnoses the hang before failing —
// this is the one chance to learn anything about a freeze that has never
// reproduced locally.
func (s *liveSession) must(str string) {
	s.t.Helper()
	s.expectWithin(fmt.Sprintf("expected %q", str), func() (string, error) {
		return s.console.ExpectString(str)
	})
}

func (s *liveSession) send(str string) {
	s.t.Helper()
	if _, err := s.console.Send(str); err != nil {
		s.diagnose("send "+str, false)
		s.t.Fatalf("send: %v", err)
	}
}

// choose selects a top-level menu row by its digit shortcut rather than by
// counting arrow presses from an assumed caret position (issue #122).
//
// Relative navigation races the splash's async health probe: a failed
// probe marks the deployment degraded and moves the caret to Repair
// (splash.go:308-319), so two Downs intended as Update->Repair->Remove
// instead walk Repair->Remove->Exit and the Enter quits the launcher.
// That is the intermittent "freezes after one settled frame" of #100.
// splash.go:368-380 maps "1"-"9" straight to a menu index, so this picks
// the intended row whenever the probe lands, leaving the probe itself
// enabled and its degraded-state behaviour exercised.
func (s *liveSession) choose(label string) {
	s.t.Helper()
	for i, item := range ui.MainMenu {
		if item.Label == label {
			s.send(strconv.Itoa(i + 1))
			return
		}
	}
	s.t.Fatalf("no top-level menu row labelled %q", label)
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
// consoleBusy tells diagnose whether a reader is already parked on
// s.console (the expectWithinErr timeout path). When it is, diagnose must
// not issue its own console.Expect — two goroutines reading the same
// underlying bufio.Reader at once can panic ("slice bounds out of
// range"), destroying the diagnosis. Call sites where nothing is parked
// (an expectation's own error path, and send) pass false and keep the
// original console.Expect-based dump capture.
func (s *liveSession) diagnose(reason string, consoleBusy bool) {
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

	if consoleBusy {
		// A reader is already parked in console.Expect for the
		// expectation that just timed out — issuing a second Expect here
		// would race that goroutine on the same bufio.Reader and can
		// panic. Give the parked reader a brief grace period to drain the
		// dump instead; it lands in the raw transcript (if configured),
		// not the test log, since nothing here is reading it.
		s.t.Log("  SIGQUIT sent; a reader is already parked on the console, so the goroutine dump (if any) is left for it to drain and will appear only in the raw transcript, not this log")
		time.Sleep(2 * time.Second)
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
	cmd.Env = append(os.Environ(), "TERM=xterm", "NO_COLOR=1", "ORBIT_LAUNCHER_NO_UPDATE_CHECK=1", "GOTRACEBACK=all",
		// The stale-database-volume pre-flight (issue #105) reads the
		// host's volumes, so a volume left behind by a crashed earlier
		// run would interrupt this install with a screen the test never
		// expects — and the gate would sit out its full 600s waiting for
		// a profile screen that isn't coming, which is precisely the
		// hang class issue #100 exists to diagnose. Covered by its own
		// unit and seam tests instead of on machine state.
		"ORBIT_LAUNCHER_NO_VOLUME_CHECK=1",
		// No arrival animation (issue #120). Menu rows render at
		// introMenuStart but keys stay swallowed until introEnd
		// (internal/ui/splash.go:98-102), so a test that matches "Install"
		// and immediately sends a selection can have it eaten as the skip,
		// leaving the menu settled with nothing chosen.
		//
		// Sending a separate skip key first does not fix it: bubbletea
		// batches consecutive printable runes into one KeyRunes message,
		// and handleKey's swallow returns before the digit shortcuts are
		// examined (splash.go:343-380), so "s" and the selection sent
		// back-to-back are discarded together. Measured: that attempt hung
		// in exactly the same place as no skip key at all.
		//
		// This costs no coverage. The arrival's own behaviour — any key
		// skips and is swallowed, it finishes on its own after enough
		// ticks, NO_ANIMATION never plays it — is asserted by
		// internal/ui/splash_test.go, and how it looks belongs to
		// visual-regression.yml. What this suite exists to pin is the
		// install.sh handoff contract, which the animation only ever
		// added nondeterminism to.
		"ORBIT_LAUNCHER_NO_ANIMATION=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start orbit-launcher: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	return &liveSession{t: t, console: console, cmd: cmd, budget: expectBudget}
}

// acceptMenusUntil sends Enter to accept the default choice on any of
// install.sh's own interactive menus/reviews until target is seen —
// which install.sh version is fetched (main branch, always moving) can
// change exactly which menus appear before the guided configuration
// prompts, so this stays adaptive rather than hardcoding an exact
// sequence.
// stopScreenReasonPattern matches the installer's own reason line on its
// failure screen, e.g. "Orbit installer: Could not fetch
// config/tika-config.json from the published revision." (see
// internal/ui/install_test.go around lines 254-288 for real examples).
var stopScreenReasonPattern = regexp.MustCompile(`Orbit installer: .*`)

func acceptMenusUntil(t *testing.T, session *liveSession, target string) {
	t.Helper()
	const stopScreen = "Installation stopped"
	pattern := regexp.MustCompile(`Greetings, what can we do for you today\?|Choose a deployment profile|Review:|Final review:|Optional services|` + regexp.QuoteMeta(stopScreen) + `|` + regexp.QuoteMeta(target))
	targetPattern := regexp.MustCompile(regexp.QuoteMeta(target))
	stopPattern := regexp.MustCompile(regexp.QuoteMeta(stopScreen))
	for {
		// No tighter budget than expectWithin's — that ceiling is
		// deliberately generous because a real image pull plus health
		// checks can genuinely take minutes. An earlier draft hardcoded
		// 60s here, causing a real CI failure on a resource-constrained
		// runner even though the install had, in fact, not failed — just
		// hadn't finished yet.
		result := session.expectWithin("waiting for "+target+" or a menu", func() (string, error) {
			return session.console.Expect(expect.Regexp(pattern))
		})
		if targetPattern.MatchString(result) {
			return
		}
		if stopPattern.MatchString(result) {
			// The install genuinely stopped — sending Enter here would
			// just cycle the menu until the 600s budget expires instead
			// of reporting why. Fail now, with the installer's own
			// reason if it's available.
			if reasons := stopScreenReasonPattern.FindAllString(result, -1); len(reasons) > 0 {
				t.Fatalf("install stopped waiting for %s: %s", target, reasons[len(reasons)-1])
			}
			more, err := session.console.Expect(expect.Regexp(stopScreenReasonPattern), expect.WithTimeout(10*time.Second))
			if err == nil {
				if reasons := stopScreenReasonPattern.FindAllString(more, -1); len(reasons) > 0 {
					t.Fatalf("install stopped waiting for %s: %s", target, reasons[len(reasons)-1])
				}
			}
			t.Fatalf("install stopped waiting for %s, but no reason line was captured", target)
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
		session.choose("Install") // by digit, not by trusting the preselection
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
		session.expectWithin("expected the configuration prompt or failure screen after the piped attempt", func() (string, error) {
			return session.console.Expect(expect.Regexp(regexp.MustCompile(
				`Orbit needs your configuration|Installation stopped`)))
		})
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

		// Nothing is asserted about the caret here (issue #122). The
		// health probe is enabled — this is a real deployment — and this
		// test's APP_URL is deliberately unresolvable, so the probe fails,
		// marks the deployment degraded and moves the caret to Repair
		// (splash.go:308-319); it was observed doing exactly that. Which
		// row is preselected is therefore a race, so the row is chosen by
		// digit instead of by counting arrows from an assumed start.
		//
		// Detection is not asserted on this screen either: the splash's
		// identity line is styled and centred, so escape sequences
		// interleave within the FQDN and ExpectString cannot match it
		// there. The confirm screen below carries the bare FQDN and
		// proves the same thing.
		session.must("Install")
		session.choose("Remove")
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
	session.choose("Install") // by digit, not by trusting the preselection
	session.must("Choose a deployment profile")
	sendLine("")
	session.must("Ready to install")
	sendLine("")

	// The piped attempt's configuration refusal, then guided
	// configuration — identical to the happy path up to here.
	session.expectWithin("expected the configuration prompt or failure screen after the piped attempt", func() (string, error) {
		return session.console.Expect(expect.Regexp(regexp.MustCompile(
			`Orbit needs your configuration|Installation stopped`)))
	})
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
	// instruments, the dump is expected to arrive. Nothing is parked on
	// the console at this point, so consoleBusy is false.
	session.diagnose("instrumentation self-test", false)

	if err := session.cmd.Wait(); err == nil {
		t.Error("SIGQUIT should have terminated the launcher")
	}
}

// TestExpectWithin_TimeoutDiagnosesWithoutPanicking reproduces the race
// fixed above: on the timeout path, the expectation's goroutine is still
// parked inside console.Expect reading runes when diagnose runs. Before
// the fix, diagnose issued its own console.Expect on top of that parked
// reader — two goroutines pulling from the same bufio.Reader at once,
// which panics ("slice bounds out of range") instead of producing a
// diagnosis. This drives expectWithinErr into that exact timeout path
// with a short budget and asserts it returns an error instead of
// panicking.
//
// It needs no Docker, network or the real launcher binary — just any
// process on a real pty that keeps producing output. The child's tight
// echo loop is deliberate and load-bearing: it keeps the expectation's
// reader inside a blocking ReadRune for the whole budget, which is what
// puts two goroutines in the same bufio.Reader. Measured against the
// unfixed code: a tight loop panics on every run, the same loop with a
// 50ms sleep passes every run — enough idle time between ticks and the
// two readers simply take turns. A slower child would make this a test
// that always passes and proves nothing.
//
// Verified by reverting the fix (passing false at the timeout call site
// in expectWithinErr): three of three runs die with the issue's exact
// panic, "slice bounds out of range [:32] with capacity 16" in
// bufio.(*Reader).ReadRune via go-expect's Console.Expect. With the fix
// in place, three of three pass, as does -race.
func TestExpectWithin_TimeoutDiagnosesWithoutPanicking(t *testing.T) {
	console, err := expect.NewConsole()
	if err != nil {
		t.Fatalf("create console: %v", err)
	}
	t.Cleanup(func() { console.Close() })

	cmd := exec.Command("sh", "-c", "while :; do echo tick; done")
	cmd.Stdin = console.Tty()
	cmd.Stdout = console.Tty()
	cmd.Stderr = console.Tty()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start noisy child: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})

	session := &liveSession{t: t, console: console, cmd: cmd, budget: 2 * time.Second}

	_, err = session.expectWithinErr("waiting for something that never arrives", func() (string, error) {
		return session.console.Expect(expect.String("this string never appears"))
	})
	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "no match within") {
		t.Errorf("error = %q, want it to contain %q", err.Error(), "no match within")
	}
}
