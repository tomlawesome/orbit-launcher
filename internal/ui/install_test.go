package ui

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// fakeEngine returns a prepareEngine stand-in whose stream replays the
// given messages — the deterministic, offline equivalent of a real
// piped install.sh run. It records the action flag it was asked for.
func fakeEngine(gotAction *string, msgs ...any) prepareEngineFunc {
	return func(_ context.Context, _ string, action string) (*engine.Stream, func() error, error) {
		if gotAction != nil {
			*gotAction = action
		}
		ch := make(chan any, len(msgs))
		for _, m := range msgs {
			ch <- m
		}
		close(ch)
		return &engine.Stream{C: ch}, func() error { return nil }, nil
	}
}

// fakeEngineStreaming is fakeEngine for a run that has not finished:
// the messages are replayed and the channel stays open, because a live
// engine's pipe does. A closed channel now means the engine stopped,
// which the reader reports as a failed run (#159), so a mid-flight
// stream has to stay open to be one.
func fakeEngineStreaming(t *testing.T, msgs ...any) prepareEngineFunc {
	return func(context.Context, string, string) (*engine.Stream, func() error, error) {
		ch := make(chan any, len(msgs))
		for _, m := range msgs {
			ch <- m
		}
		// The reader lives as long as the channel is open, so the test
		// closes it on the way out rather than leaving one behind.
		t.Cleanup(func() { close(ch) })
		return &engine.Stream{C: ch}, func() error { return nil }, nil
	}
}

// fakeHandoff returns a runHandoff stand-in that never touches a real
// terminal or process — it just synchronously reports the given error.
func fakeHandoff(err error) runHandoffFunc {
	return func(*exec.Cmd) tea.Cmd {
		return func() tea.Msg { return installFinishedMsg{err: err} }
	}
}

func fakeDetect(appURL string) detectFunc {
	return func(string) (*deploy.Deployment, error) {
		return &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: appURL}, nil
	}
}

func ev(phase, component, state, reason, action string) engine.EventMsg {
	return engine.EventMsg{Event: engine.Event{Phase: phase, Component: component, State: state, Reason: reason, Action: action}}
}

// successStream is the honest shape of a completing contract-era run:
// progress events, the success event, then a clean exit.
func successStream() []any {
	return []any{
		ev("host", "host", "completed", "host-tools", "check"),
		ev("application", "application", "healthy", "application-health", "health"),
		ev("complete", "installer", "completed", "deployment-ready", "complete"),
		engine.DoneMsg{ExitCode: 0},
	}
}

// configRefusalStream is the documented non-interactive refusal.
func configRefusalStream() []any {
	return []any{
		ev("host", "host", "completed", "host-tools", "check"),
		ev("configuration", "configuration", "failed", "configuration-failure", "retry"),
		engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1,
			StderrTail: []string{"Orbit installer: configuration fields requiring attention: APP_URL."}},
	}
}

// newTestInstallModel builds the model the way AppModel does, sender
// included: the engine stream reader pushes from outside the command
// chain, so a run without one cannot receive engine output at all
// (#159). Tests that drive a run hand the sink to drive; the rest
// ignore it.
func newTestInstallModel(seams engineRunSeams) (InstallModel, *sink) {
	m := NewInstallModel("/opt/orbit", "v9.9.9")
	m.seams = seams
	s := newSink()
	m.send = s.Send
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	return updated.(InstallModel), s
}

// sink collects the messages a model pushes into the event loop from
// outside the command chain. Only the engine stream reader does that
// (#159); in production the same route is tea.Program.Send.
type sink struct{ msgs chan tea.Msg }

func newSink() *sink { return &sink{msgs: make(chan tea.Msg, 64)} }

func (s *sink) Send(msg tea.Msg) { s.msgs <- msg }

// drive feeds a command's resulting messages back into the model until
// everything goes quiet — how bubbletea itself would run it. A test
// driving an engine run passes that run's sink, which drive drains the
// way bubbletea drains its own message queue: a command result and a
// pushed message are the same thing once they reach Update.
func drive(t *testing.T, model tea.Model, cmd tea.Cmd, pushed ...*sink) tea.Model {
	t.Helper()
	var queue *sink
	if len(pushed) > 0 {
		queue = pushed[0]
	}
	for i := 0; ; i++ {
		if i > 1000 {
			t.Fatal("command chain did not settle")
		}
		var msg tea.Msg
		switch {
		case cmd != nil:
			msg, cmd = cmd(), nil
		case queue != nil:
			select {
			case msg = <-queue.msgs:
			case <-time.After(2 * time.Second):
				return model
			}
		default:
			return model
		}
		if msg == nil {
			continue
		}
		model, cmd = model.Update(msg)
		// Nothing follows the stream ending, so waiting for more would
		// only spend the timeout above.
		if _, ended := msg.(engineStreamEndedMsg); ended && cmd == nil {
			return model
		}
	}
}

func startInstallRun(t *testing.T, m InstallModel) (InstallModel, tea.Cmd) {
	t.Helper()
	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> confirm
	m = updated.(InstallModel)
	updated, cmd := m.Update(key(tea.KeyEnter)) // confirm -> running
	m = updated.(InstallModel)
	if m.state != installStateRunning {
		t.Fatalf("state = %v, want installStateRunning", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a command to start the engine")
	}
	return m, cmd
}

func TestInstallModel_SelectingStandardMovesToConfirm(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	updated, _ := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	if m.state != installStateConfirm {
		t.Errorf("state = %v, want installStateConfirm", m.state)
	}
}

func TestInstallModel_SelectingAIOrFullShowsUnavailableNotFakeProgress(t *testing.T) {
	for _, sel := range []int{1, 2} { // AI, Full
		m, _ := newTestInstallModel(engineRunSeams{})
		m.profileSel = sel
		updated, _ := m.Update(key(tea.KeyEnter))
		m = updated.(InstallModel)
		if m.state != installStateUnavailableProfile {
			t.Errorf("profile %d: state = %v, want installStateUnavailableProfile", sel, m.state)
		}
	}
}

func TestInstallModel_EscapeFromConfirmReturnsToProfile(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> confirm
	m = updated.(InstallModel)
	updated, _ = m.Update(key(tea.KeyEsc))
	m = updated.(InstallModel)
	if m.state != installStateProfile {
		t.Errorf("state = %v, want installStateProfile", m.state)
	}
}

func TestInstallModel_ConfirmNeverStartsTheEngineAutomatically(t *testing.T) {
	engineCalled := false
	seams := engineRunSeams{
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			engineCalled = true
			return nil, nil, errors.New("should not be called")
		},
	}
	m, _ := newTestInstallModel(seams)
	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> confirm
	m = updated.(InstallModel)
	_ = m.View() // rendering alone must never trigger a side effect

	if engineCalled {
		t.Error("the engine must only start after an explicit confirm, never as a side effect of rendering")
	}
}

func TestInstallModel_EngineSuccessConcludesTheFlow(t *testing.T) {
	var gotAction string
	m, s := newTestInstallModel(engineRunSeams{
		prepareEngine: fakeEngine(&gotAction, successStream()...),
		detect:        fakeDetect("https://mail.example.com"),
	})
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd, s).(InstallModel)

	if gotAction != "install" {
		t.Errorf("engine action = %q, want install", gotAction)
	}
	o := m.Outcome()
	if !o.Done || !o.Succeeded {
		t.Fatalf("outcome = %+v, want Done and Succeeded", o)
	}
	if o.URL != "https://mail.example.com" {
		t.Errorf("URL = %q — the success screen's hero line comes from detection, not from prose", o.URL)
	}
}

func TestInstallModel_ConsoleShowsEventsWhileStreaming(t *testing.T) {
	// Replay only progress events with no DoneMsg: the run is mid-
	// flight, and the console must be showing the stream.
	m, s := newTestInstallModel(engineRunSeams{
		prepareEngine: fakeEngineStreaming(t,
			ev("host", "host", "completed", "host-tools", "check"),
			ev("identity", "image", "running", "image-identity", "inspect"),
		),
	})
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd, s).(InstallModel)

	view := m.View().Content
	if !strings.Contains(view, "image") || !strings.Contains(view, "running") {
		t.Error("expected the console to render the streamed events")
	}
	if !strings.Contains(view, "Resolving image identity") {
		t.Error("expected the stage word for the furthest phase reached")
	}
	if strings.Contains(view, "%") {
		t.Error("the stage bar must never show a percentage")
	}
}

func TestInstallModel_ConfigurationRefusalOffersTheGuidedHandoff(t *testing.T) {
	handoffRan := false
	prepared := false
	seams := engineRunSeams{
		prepareEngine: fakeEngine(nil, configRefusalStream()...),
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			prepared = true
			return exec.Command("true"), func() error { return nil }, nil
		},
		runHandoff: func(cmd *exec.Cmd) tea.Cmd {
			handoffRan = true
			return func() tea.Msg { return installFinishedMsg{} }
		},
		detect: fakeDetect("https://mail.example.com"),
	}
	m, s := newTestInstallModel(seams)
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd, s).(InstallModel)

	if m.run.state != runConfigPrompt {
		t.Fatalf("run state = %v, want runConfigPrompt", m.run.state)
	}
	if handoffRan || prepared {
		t.Fatal("the handoff must wait for the user's explicit choice")
	}
	if !strings.Contains(m.View().Content, "Orbit needs your configuration") {
		t.Error("expected the styled configuration prompt")
	}

	// Accept the default: Continue — guided configuration.
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = drive(t, updated, cmd).(InstallModel)

	if !prepared || !handoffRan {
		t.Fatal("expected the interactive handoff to run after Continue")
	}
	o := m.Outcome()
	if !o.Done || !o.Succeeded {
		t.Errorf("outcome after a clean handoff = %+v, want success", o)
	}
}

func TestInstallModel_EngineFailureShowsReasonWordsAndStderrTail(t *testing.T) {
	m, s := newTestInstallModel(engineRunSeams{
		prepareEngine: fakeEngine(nil,
			ev("identity", "image", "failed", "image-registry", "retry"),
			engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1,
				StderrTail: []string{"Orbit installer: Could not pull ghcr.io/tomlawesome/orbit:latest."}},
		),
	})
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd, s).(InstallModel)

	if m.run.state != runFailed {
		t.Fatalf("run state = %v, want runFailed", m.run.state)
	}
	view := m.View().Content
	for _, want := range []string{"Installation stopped", "image-registry", "Could not pull"} {
		if !strings.Contains(view, want) {
			t.Errorf("failure view missing %q", want)
		}
	}

	// Menu is the second option — choosing it asks for the splash back.
	updated, _ := m.Update(key(tea.KeyDown))
	m = updated.(InstallModel)
	updated, _ = m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	if o := m.Outcome(); !o.Done || o.Succeeded || !o.WantsMenu {
		t.Errorf("outcome = %+v, want WantsMenu without success", o)
	}
}

func TestInstallModel_LegacyRefusalFailureScreenOpensGuidedInstaller(t *testing.T) {
	// A legacy engine (orbit main) reports no telemetry: its
	// non-interactive configuration refusal is just prose plus exit 1,
	// which honestly lands on the failure screen — whose first option
	// is the same guided installer the config prompt offers.
	handoffRan := false
	m, s := newTestInstallModel(engineRunSeams{
		prepareEngine: fakeEngine(nil,
			engine.RawLineMsg{Text: "Orbit installer: non-interactive use requires a complete .env-orbit"},
			engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1,
				StderrTail: []string{"Orbit installer: configuration fields requiring attention: APP_URL."}},
		),
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			return exec.Command("true"), func() error { return nil }, nil
		},
		runHandoff: func(*exec.Cmd) tea.Cmd {
			handoffRan = true
			return func() tea.Msg { return installFinishedMsg{} }
		},
		detect: fakeDetect("https://mail.example.com"),
	})
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd, s).(InstallModel)

	if m.run.state != runFailed {
		t.Fatalf("run state = %v, want runFailed — no events means no config prompt", m.run.state)
	}
	if !strings.Contains(m.View().Content, "Open the guided installer") {
		t.Fatal("failure screen must offer the guided installer")
	}

	updated, cmd := m.Update(key(tea.KeyEnter)) // Open the guided installer (default)
	m = drive(t, updated, cmd).(InstallModel)

	if !handoffRan {
		t.Fatal("expected the guided installer handoff to run")
	}
	if o := m.Outcome(); !o.Done || !o.Succeeded {
		t.Errorf("outcome = %+v, want success after a clean interactive install", o)
	}
}

func TestInstallModel_LegacyEngineJudgedByExitCodeAlone(t *testing.T) {
	// orbit main's install.sh emits no events. Its prose is displayed
	// raw; a clean exit is still success — never keyed off the prose.
	m, s := newTestInstallModel(engineRunSeams{
		prepareEngine: fakeEngine(nil,
			engine.RawLineMsg{Text: "Pulling ghcr.io/tomlawesome/orbit:latest"},
			engine.RawLineMsg{Text: "Orbit is ready."},
			engine.DoneMsg{ExitCode: 0},
		),
		detect: fakeDetect("https://mail.example.com"),
	})
	m, cmd := startInstallRun(t, m)

	// Mid-drive the raw prose must be visible; drive fully first, since
	// the fake stream is replayed synchronously.
	m = drive(t, m, cmd, s).(InstallModel)
	if o := m.Outcome(); !o.Done || !o.Succeeded {
		t.Errorf("outcome = %+v, want success on exit 0 with zero events", o)
	}
}

func TestInstallModel_EnginePrepareFailureReachesFailedWithoutHandoff(t *testing.T) {
	handoffCalled := false
	m, _ := newTestInstallModel(engineRunSeams{
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			return nil, nil, errors.New("could not fetch install.sh")
		},
		runHandoff: func(*exec.Cmd) tea.Cmd {
			handoffCalled = true
			return nil
		},
	})
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd).(InstallModel)

	if handoffCalled {
		t.Error("the handoff must never run when the engine cannot start")
	}
	if m.run.state != runFailed {
		t.Errorf("run state = %v, want runFailed", m.run.state)
	}
	if !strings.Contains(m.View().Content, "could not fetch install.sh") {
		t.Error("expected the failure view to carry the error")
	}
}

func TestInstallModel_HandoffFailureReachesFailed(t *testing.T) {
	m, s := newTestInstallModel(engineRunSeams{
		prepareEngine: fakeEngine(nil, configRefusalStream()...),
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			return exec.Command("false"), func() error { return nil }, nil
		},
		runHandoff: fakeHandoff(errors.New("install.sh exited 1")),
	})
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd, s).(InstallModel)

	updated, cmd := m.Update(key(tea.KeyEnter)) // Continue — guided configuration
	m = drive(t, updated, cmd).(InstallModel)

	if m.run.state != runFailed {
		t.Errorf("run state = %v, want runFailed after a failed handoff", m.run.state)
	}
	if o := m.Outcome(); o.Succeeded {
		t.Error("a failed handoff must never read as success")
	}
}

func TestInstallModel_SuccessElapsedComesFromTheConsoleClock(t *testing.T) {
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	current := base
	m, s := newTestInstallModel(engineRunSeams{
		prepareEngine: fakeEngine(nil, successStream()...),
		detect:        fakeDetect("https://mail.example.com"),
		now: func() time.Time {
			// First call stamps the console start; later calls read it
			// 3m42s further on.
			t := current
			current = base.Add(3*time.Minute + 42*time.Second)
			return t
		},
	})
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd, s).(InstallModel)

	if got := m.Outcome().Elapsed; got != 3*time.Minute+42*time.Second {
		t.Errorf("Elapsed = %v, want 3m42s from the injected clock", got)
	}
}

// staleVolumeCheck returns a pre-flight seam reporting exactly volumes.
func staleVolumeCheck(volumes ...deploy.DatabaseVolume) func(context.Context, string) []deploy.DatabaseVolume {
	return func(context.Context, string) []deploy.DatabaseVolume { return volumes }
}

// The dead end issue #105 fixes is being told what is refused and never
// why. The pre-flight names the volume before the engine fails, rather
// than after.
func TestInstallModel_StaleVolumePreFlightNamesTheVolume(t *testing.T) {
	m := NewInstallModel("/opt/orbit", "v0.1.0")
	m.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "old-tree_orbit-db-data", Project: "old-tree"})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	updated, _ = updated.Update(m.Init()())

	view := updated.View().Content
	for _, want := range []string{"old-tree_orbit-db-data", "old-tree", "Continue anyway"} {
		if !strings.Contains(view, want) {
			t.Errorf("pre-flight screen missing %q", want)
		}
	}
}

// The screen explains a refusal; it never offers to carry one out. A
// clearing command is deliberately out of scope (see the issue), and
// nothing on this screen may read as an instruction to delete data.
func TestInstallModel_StaleVolumePreFlightOffersNoDestructiveAction(t *testing.T) {
	m := NewInstallModel("/opt/orbit", "v0.1.0")
	m.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "old-tree_orbit-db-data", Project: "old-tree"})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	updated, _ = updated.Update(m.Init()())

	view := updated.View().Content
	for _, forbidden := range []string{"volume rm", "docker volume", "Copy command", "delete", "Delete"} {
		if strings.Contains(view, forbidden) {
			t.Errorf("pre-flight screen must not offer %q", forbidden)
		}
	}
}

// Advisory means advisory: a detection mistake must never be able to
// stand between someone and an install.
func TestInstallModel_StaleVolumePreFlightAlwaysLetsYouThrough(t *testing.T) {
	m := NewInstallModel("/opt/orbit", "v0.1.0")
	m.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "orbit-db-data"})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	updated, _ = updated.Update(m.Init()())
	updated, _ = updated.Update(key(tea.KeyEnter)) // Continue anyway is preselected

	if got := updated.(InstallModel).state; got != installStateProfile {
		t.Errorf("Continue anyway left the flow in state %v, want the profile screen", got)
	}
	if !strings.Contains(updated.View().Content, "Choose a deployment profile") {
		t.Error("expected the profile screen after continuing")
	}
}

// A clean machine is the overwhelmingly common case, and it must be
// indistinguishable from the flow before this pre-flight existed.
func TestInstallModel_NoStaleVolumeChangesNothing(t *testing.T) {
	m := NewInstallModel("/opt/orbit", "v0.1.0")
	m.checkVolumes = staleVolumeCheck()

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	updated, _ = updated.Update(m.Init()())

	if got := updated.(InstallModel).state; got != installStateProfile {
		t.Errorf("a clean machine moved the flow to state %v", got)
	}
}

// The check is async, so its answer can arrive after a quick hand has
// already moved on. Yanking someone back from the confirm screen to
// report a pre-flight finding is worse than letting the engine say the
// same thing a moment later.
func TestInstallModel_StaleVolumeFindingNeverInterruptsALaterScreen(t *testing.T) {
	m := NewInstallModel("/opt/orbit", "v0.1.0")
	m.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "orbit-db-data"})

	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	updated, _ = updated.Update(key(tea.KeyEnter)) // Standard → confirm
	if got := updated.(InstallModel).state; got != installStateConfirm {
		t.Fatalf("expected the confirm screen, got state %v", got)
	}

	updated, _ = updated.Update(m.Init()())
	if got := updated.(InstallModel).state; got != installStateConfirm {
		t.Errorf("a late pre-flight finding pulled the flow back to state %v", got)
	}
}
