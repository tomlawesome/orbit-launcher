package ui

// #336 review defects: a held Enter must not outlive the refusal that
// replaced the confirm screen, and the pin must reach the engine's
// environment, not only the run's channel field.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/release"
)

// Enter on Update Orbit while the lookup is pending holds an Enter. The
// running check then turns the screen into the gone-folder refusal;
// carrying on re-enters confirm and looks up again. A known answer to
// that lookup must not start the run: only a new Enter does.
func TestUpdateConfirm_HeldEnterDoesNotSurviveRefusalAndCarryOn(t *testing.T) {
	called := false
	m := pendingTargetModel(t, &called)
	u, _ := m.Update(key(tea.KeyEnter))
	if !u.(UpdateModel).awaiting {
		t.Fatal("setup: Enter while pending did not hold")
	}
	u, _ = u.Update(runningResultMsg{dir: "/opt/orbit", mismatch: goneMismatch(false)})
	if got := u.(UpdateModel); got.state != updateStateOtherFolder {
		t.Fatalf("setup: state = %v, want the refusal", got.state)
	}
	u, _ = u.Update(key(tea.KeyEnter)) // Carry on from this folder
	if got := u.(UpdateModel); got.state != updateStateConfirm {
		t.Fatalf("setup: state = %v, want confirm after carrying on", got.state)
	}
	u, _ = u.Update(targetMsg{target: release.Target{Version: "v0.3.6", Pin: true}})
	got := u.(UpdateModel)
	if got.state == updateStateRunning {
		t.Fatalf("the run started on the lookup's answer with no fresh Enter")
	}
	if got.state != updateStateConfirm {
		t.Fatalf("state = %v, want the confirm screen", got.state)
	}
	if s := screen(got); strings.Contains(s, "checking which Orbit release") {
		t.Errorf("working line still showing after carry-on:\n%s", s)
	}
	u, _ = got.Update(key(tea.KeyEnter))
	got = u.(UpdateModel)
	if got.state != updateStateRunning || got.run.channel != "v0.3.6" {
		t.Errorf("fresh Enter: state %v channel %q, want running pinned to v0.3.6", got.state, got.run.channel)
	}
}

// runPinnedProbe starts an engine run through start() with a script that
// records what ORBIT_CHANNEL the engine saw ("<unset>" when none), using
// the run's script seam (prepareEngine nil, script set: start() goes to
// startEngine without a network fetch).
func runPinnedProbe(t *testing.T, channel string) string {
	t.Helper()
	t.Setenv("ORBIT_CHANNEL", "latest") // inherited, as a person might have it
	if channel == "" {
		t.Setenv("ORBIT_CHANNEL", "x")
		os.Unsetenv("ORBIT_CHANNEL")
	}
	out := filepath.Join(t.TempDir(), "channel")
	t.Setenv("ORBIT_TEST_PROBE_OUT", out)
	dir := t.TempDir()

	r := newEngineRun(deploy.ActionUpdate, dir, "Update", "v9.9.9").withChannel(channel)
	r.script = []byte("printf '%s' \"${ORBIT_CHANNEL-<unset>}\" > \"$ORBIT_TEST_PROBE_OUT\"\n")
	_, cmd := r.start(80, 26)
	if cmd == nil {
		t.Fatal("start returned no command")
	}
	var msg tea.Msg
	done := make(chan struct{})
	go func() { msg = cmd(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("start's command did not return")
	}
	ready, ok := msg.(engineReadyMsg)
	if !ok || ready.err != nil || ready.stream == nil {
		t.Fatalf("start's command gave %#v, want a started engine", msg)
	}
	defer func() {
		if ready.cleanup != nil {
			_ = ready.cleanup()
		}
	}()
	select {
	case <-ready.stream.Done():
	case <-time.After(10 * time.Second):
		ready.stream.Kill()
		t.Fatal("probe engine did not exit")
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("probe wrote nothing: %v", err)
	}
	return string(data)
}

func TestUpdateRun_PinnedChannelReachesTheEngineEnvironment(t *testing.T) {
	if got := runPinnedProbe(t, "v0.3.6"); got != "v0.3.6" {
		t.Errorf("engine saw ORBIT_CHANNEL=%q, want v0.3.6 (over the inherited latest)", got)
	}
}

func TestUpdateRun_NoPinAddsNoChannelToTheEngineEnvironment(t *testing.T) {
	if got := runPinnedProbe(t, ""); got != "<unset>" {
		t.Errorf("engine saw ORBIT_CHANNEL=%q, want it unset", got)
	}
}
