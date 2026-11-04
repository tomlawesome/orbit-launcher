package ui

// #332: Orbit v0.3.6's install.sh --update refuses an install that is
// too old to update to directly. Its output, in order: on stdout the
// terminal event (reason=update-path action=abort), then on stderr
// exactly one line "Orbit installer: <refusal>", exit 1. The launcher
// stops with no rerun and shows the refusal word for word, without the
// "Orbit installer: " prefix: the last stderr line that starts with it.
//
// Written from the issue alone, without the code. startedRun, runUpdate,
// runScreen, ev, screenLabels and flat are in the helpers.

import (
	"errors"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const (
	updatePathRefusal = "This install is on v0.3.1. Update to v0.3.4 first, then to v0.3.6."
	installerPrefix   = "Orbit installer: "
)

// updatePathStop runs an Update that ends as v0.3.6's refusal does,
// with stderr lines as the stream's tail, and returns the stopped screen.
func updatePathStop(t *testing.T, stderr ...string) string {
	t.Helper()
	r := startedRun(t, "update", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: stoppableStream(t, "echo ready; sleep 30"), cleanup: func() error { return nil }})
	if r.state != runStreaming {
		t.Fatalf("setup: state = %v, want runStreaming", r.state)
	}
	r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("identity", "image", "failed", "update-path", "abort")})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{
		Err: errors.New("exit status 1"), ExitCode: 1, StderrTail: stderr,
	}})
	if r.state != runFailed {
		t.Fatalf("state = %v, want runFailed (the stop screen):\n%s", r.state, runScreen(r))
	}
	return runScreen(r)
}

func wantNoRerun(t *testing.T, screen string) {
	t.Helper()
	for _, label := range screenLabels(screen) {
		switch label {
		case "Update again", "Install again", "Update", "Update Orbit", "Open Repair":
			t.Errorf("the stop screen offers %q, but an update-path refusal has no rerun:\n%s", label, screen)
		}
	}
	low := strings.ToLower(screen)
	for _, word := range []string{"retry", "run again", "try again"} {
		if strings.Contains(low, word) {
			t.Errorf("the stop screen offers %q:\n%s", word, screen)
		}
	}
}

func TestEngineRun_UpdatePathRefusalShowsTheInstallersLineExactly(t *testing.T) {
	screen := updatePathStop(t, installerPrefix+updatePathRefusal)

	if !strings.Contains(flat(screen), updatePathRefusal) {
		t.Errorf("the stop screen lacks the refusal %q:\n%s", updatePathRefusal, screen)
	}
	if strings.Contains(screen, strings.TrimSpace(installerPrefix)) {
		t.Errorf("the stop screen shows the %q prefix:\n%s", installerPrefix, screen)
	}
	wantNoRerun(t, screen)
}

func TestEngineRun_UpdatePathRefusalAfterOtherStderrShowsOnlyTheLastInstallerLine(t *testing.T) {
	screen := updatePathStop(t,
		"docker: some unrelated warning",
		installerPrefix+"an earlier installer note",
		"another line the installer did not prefix",
		installerPrefix+updatePathRefusal,
	)

	if !strings.Contains(flat(screen), updatePathRefusal) {
		t.Errorf("the stop screen lacks the refusal %q:\n%s", updatePathRefusal, screen)
	}
	for _, other := range []string{"unrelated warning", "earlier installer note", "did not prefix", strings.TrimSpace(installerPrefix)} {
		if strings.Contains(screen, other) {
			t.Errorf("the stop screen shows %q, which is not the last %q line:\n%s", other, installerPrefix, screen)
		}
	}
	wantNoRerun(t, screen)
}

func TestEngineRun_UpdatePathRefusalAfterEarlierProgressEvents(t *testing.T) {
	r := startedRun(t, "update", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: stoppableStream(t, "echo ready; sleep 30"), cleanup: func() error { return nil }})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("preflight", "docker", "running", "docker-check", "check")})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("identity", "image", "failed", "update-path", "abort")})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{
		Err: errors.New("exit status 1"), ExitCode: 1,
		StderrTail: []string{"progress note", installerPrefix + updatePathRefusal},
	}})
	screen := runScreen(r)

	if r.state != runFailed || !strings.Contains(flat(screen), updatePathRefusal) {
		t.Errorf("state = %v; the stop screen lacks the refusal %q:\n%s", r.state, updatePathRefusal, screen)
	}
	wantNoRerun(t, screen)
}
