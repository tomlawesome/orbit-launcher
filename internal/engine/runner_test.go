package engine

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// startScript runs an inline bash script the way the mission console
// runs the engine: session-detached, stdout piped.
func startScript(t *testing.T, script string) *Stream {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	s, err := Start(cmd)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s
}

// collect drains the stream with a timeout so a hung subprocess fails
// the test instead of hanging the suite.
func collect(t *testing.T, s *Stream) (events []Event, raws []string, done DoneMsg) {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case msg, ok := <-s.C:
			if !ok {
				return events, raws, done
			}
			switch m := msg.(type) {
			case EventMsg:
				events = append(events, m.Event)
			case RawLineMsg:
				raws = append(raws, m.Text)
			case DoneMsg:
				done = m
			}
		case <-timeout:
			t.Fatal("stream did not finish in time")
		}
	}
}

func TestStream_EventsRawLinesAndExitCode(t *testing.T) {
	s := startScript(t, `
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=1s"
echo "Some human prose the parser must pass through raw."
echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=9s"
echo "guidance on stderr" >&2
exit 0
`)
	events, raws, done := collect(t, s)

	if len(events) != 2 || !events[1].IsSuccess() {
		t.Fatalf("events = %+v, want 2 ending in success", events)
	}
	if len(raws) != 1 || raws[0] != "Some human prose the parser must pass through raw." {
		t.Fatalf("raws = %q", raws)
	}
	if done.Err != nil || done.ExitCode != 0 {
		t.Fatalf("done = %+v, want clean exit", done)
	}
	if len(done.StderrTail) != 1 || done.StderrTail[0] != "guidance on stderr" {
		t.Fatalf("StderrTail = %q", done.StderrTail)
	}
}

func TestStream_ConfigurationRefusalOutcome(t *testing.T) {
	// The shape of a real non-interactive refusal: one failed event,
	// guidance on stderr, exit 1 (verified against orbit develop's
	// install.sh — see docs/engine-events.md "Non-interactive contract").
	s := startScript(t, `
echo "phase=configuration component=configuration state=failed reason=configuration-failure action=retry elapsed=12s"
echo "Orbit installer: configuration fields requiring attention: APP_URL OIDC_ISSUER." >&2
exit 1
`)
	events, _, done := collect(t, s)

	if len(events) != 1 || !events[0].NeedsConfiguration() {
		t.Fatalf("events = %+v, want one configuration refusal", events)
	}
	if done.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", done.ExitCode)
	}
}

func TestStream_LegacyEngineEmitsNoEvents(t *testing.T) {
	// orbit main's install.sh predates the stream entirely: prose only.
	// The console must still see its lines and key the outcome off the
	// exit code alone.
	s := startScript(t, `
echo "Pulling ghcr.io/tomlawesome/orbit:latest"
echo "Orbit is ready."
exit 0
`)
	events, raws, done := collect(t, s)

	if len(events) != 0 {
		t.Fatalf("events = %+v, want none from a legacy engine", events)
	}
	if len(raws) != 2 {
		t.Fatalf("raws = %q, want both prose lines", raws)
	}
	if done.ExitCode != 0 {
		t.Fatalf("ExitCode = %d, want 0", done.ExitCode)
	}
}

func TestStream_StderrTailIsBounded(t *testing.T) {
	s := startScript(t, `
for i in $(seq 1 40); do echo "stderr line $i" >&2; done
exit 2
`)
	_, _, done := collect(t, s)

	if len(done.StderrTail) != stderrTailLines {
		t.Fatalf("len(StderrTail) = %d, want %d", len(done.StderrTail), stderrTailLines)
	}
	if done.StderrTail[stderrTailLines-1] != "stderr line 40" {
		t.Fatalf("tail should keep the end, got last = %q", done.StderrTail[stderrTailLines-1])
	}
	if done.ExitCode != 2 {
		t.Fatalf("ExitCode = %d, want 2", done.ExitCode)
	}
}

func TestStream_KillTerminatesPromptly(t *testing.T) {
	s := startScript(t, `
echo "phase=application component=application state=starting reason=application-health action=start elapsed=1s"
sleep 60
`)
	// Wait for the first event so the process is definitely up.
	select {
	case <-s.C:
	case <-time.After(5 * time.Second):
		t.Fatal("no first event")
	}

	start := time.Now()
	s.Kill()
	_, _, done := collect(t, s)
	if time.Since(start) > 5*time.Second {
		t.Fatal("kill did not terminate the stream promptly")
	}
	if done.Err == nil {
		t.Fatal("expected a non-nil error after kill")
	}
}
