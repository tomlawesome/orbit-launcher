package ui

import (
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// newTestUpdateModel mirrors newTestInstallModel: the model gets the
// sender AppModel would give it, and the sink it pushes into (#159).
func newTestUpdateModel(d *deploy.Deployment, seams engineRunSeams) (UpdateModel, *sink) {
	m := NewUpdateModel(d, "/opt/orbit", "v9.9.9")
	m.seams = seams
	s := newSink()
	m.send = s.Send
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	return updated.(UpdateModel), s
}

func testDeployment() *deploy.Deployment {
	return &deploy.Deployment{
		TargetDir: "/opt/orbit",
		AppURL:    "https://mail.example.com",
		Image:     "ghcr.io/tomlawesome/orbit@sha256:abc",
	}
}

// The no-deployment screen is a one-row menu: Menu goes back to the main
// menu, and no other key quits (#228 C2; #226).
func TestUpdateModel_NoDeploymentReachesNotFoundAndOffersMenuOnly(t *testing.T) {
	m, _ := newTestUpdateModel(nil, engineRunSeams{})
	if m.state != updateStateNotFound {
		t.Fatalf("state = %v, want updateStateNotFound", m.state)
	}
	if got := screenLabels(screen(m)); !slices.Equal(got, []string{"Menu"}) {
		t.Fatalf("not-found rows = %q, want just Menu", got)
	}
	m2, cmd := m.Update(key(tea.KeyEnter))
	if isQuit(cmd) {
		t.Fatal("Enter on the not-found screen quit; its one row is Menu")
	}
	if o := m2.(UpdateModel).Outcome(); !leftForMenu(o) {
		t.Fatalf("outcome after Enter = %+v, want back to the menu", o)
	}
}

// The no-deployment screen is the flow's first: Esc is back to the main
// menu (#210, owner 4a); no other key quits (#228).
func TestUpdateModel_EscOnNotFoundGoesBackToTheMenu(t *testing.T) {
	m, _ := newTestUpdateModel(nil, engineRunSeams{})
	updated, cmd := m.Update(key(tea.KeyEsc))
	if cmd != nil {
		t.Fatal("Esc on the not-found screen should run nothing")
	}
	if o := updated.(UpdateModel).Outcome(); !leftForMenu(o) {
		t.Fatalf("outcome after Esc = %+v, want back to the menu", o)
	}
}

func TestUpdateModel_CancelFromConfirmNeverStartsTheEngine(t *testing.T) {
	engineCalled := false
	seams := engineRunSeams{
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			engineCalled = true
			return nil, nil, errors.New("should not be called")
		},
	}
	m, _ := newTestUpdateModel(testDeployment(), seams)

	updated, _ := m.Update(key(tea.KeyDown)) // move to Cancel
	m = updated.(UpdateModel)
	updated, cmd := m.Update(key(tea.KeyEnter))
	if cmd != nil {
		cmd()
	}

	if engineCalled {
		t.Error("the engine must never start when Cancel is chosen")
	}
	// Cancel is the same act as Esc: back to the main menu (#210).
	if o := updated.(UpdateModel).Outcome(); !leftForMenu(o) {
		t.Errorf("outcome after Cancel = %+v, want back to the menu", o)
	}
}

func TestUpdateModel_EscapeAtConfirmGoesBackToTheMenuWithoutStarting(t *testing.T) {
	engineCalled := false
	seams := engineRunSeams{
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			engineCalled = true
			return nil, nil, errors.New("should not be called")
		},
	}
	m, _ := newTestUpdateModel(testDeployment(), seams)

	updated, cmd := m.Update(key(tea.KeyEsc))
	if cmd != nil {
		cmd()
	}

	if engineCalled {
		t.Error("the engine must never start on Escape")
	}
	// The flow's first screen: Esc is back to the main menu (#210).
	if o := updated.(UpdateModel).Outcome(); !leftForMenu(o) {
		t.Errorf("outcome after Escape = %+v, want back to the menu", o)
	}
}

func TestUpdateModel_NeverStartsTheEngineAutomatically(t *testing.T) {
	engineCalled := false
	seams := engineRunSeams{
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			engineCalled = true
			return nil, nil, errors.New("should not be called")
		},
	}
	m, _ := newTestUpdateModel(testDeployment(), seams)
	_ = m.View() // rendering alone must never trigger a side effect
	if engineCalled {
		t.Error("the engine must only start after an explicit confirm, never as a side effect of rendering")
	}
}

func TestUpdateModel_ConfirmRunsTheEngineWithUpdateAction(t *testing.T) {
	var gotAction, gotTargetDir string
	seams := engineRunSeams{
		prepareEngine: func(_ context.Context, targetDir, action string) (*engine.Stream, func() error, error) {
			gotAction, gotTargetDir = action, targetDir
			ch := make(chan any, 2)
			ch <- ev("complete", "installer", "completed", "deployment-ready", "complete")
			ch <- engine.DoneMsg{ExitCode: 0}
			close(ch)
			return &engine.Stream{C: ch}, func() error { return nil }, nil
		},
		detect: fakeDetect("https://mail.example.com"),
	}
	m, s := newTestUpdateModel(testDeployment(), seams)

	updated, cmd := m.Update(key(tea.KeyEnter)) // Update Orbit is selected by default
	m = updated.(UpdateModel)
	if m.state != updateStateRunning {
		t.Fatalf("state = %v, want updateStateRunning", m.state)
	}
	m = drive(t, m, cmd, s)

	if gotAction != "update" {
		t.Errorf("engine action = %q, want update", gotAction)
	}
	if gotTargetDir != "/opt/orbit" {
		t.Errorf("engine targetDir = %q, want /opt/orbit", gotTargetDir)
	}
	if o := m.Outcome(); !o.Done || !o.Succeeded {
		t.Errorf("outcome = %+v, want success", o)
	}
}

func TestUpdateModel_ConfigurationRefusalGoesStraightIntoGuidedConfigurationToo(t *testing.T) {
	// A migration can surface missing fields on update as well; the
	// same straight-in stretch applies (#251).
	prepared := false
	m, s := newTestUpdateModel(testDeployment(), engineRunSeams{
		prepareEngine: fakeEngine(nil, configRefusalStream()...),
		prepareConfig: planned(true, false),
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			prepared = true
			return exec.Command("true"), func() error { return nil }, nil
		},
	})
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = drive(t, updated, cmd, s).(UpdateModel)

	if m.run.state != runConfigSignInMode {
		t.Fatalf("run state = %v, want the guided session's sign-in-mode screen with no key pressed", m.run.state)
	}
	if prepared {
		t.Fatal("the terminal handoff was prepared although the in-console session is available")
	}
	v := screen(m)
	if !strings.Contains(v, "How will people sign in?") {
		t.Fatalf("not in guided configuration:\n%s", v)
	}
	for _, gone := range []string{"The engine stopped before touching anything", "Continue — guided configuration"} {
		if strings.Contains(v, gone) {
			t.Errorf("the removed configuration screen is still shown (%q):\n%s", gone, v)
		}
	}
}

func TestUpdateModel_EngineFailureReachesFailedState(t *testing.T) {
	m, s := newTestUpdateModel(testDeployment(), engineRunSeams{
		prepareEngine: fakeEngine(nil,
			ev("database", "database", "failed", "database-auth-migration", "repair"),
			engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1},
		),
	})
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = drive(t, updated, cmd, s).(UpdateModel)

	if m.run.state != runFailed {
		t.Errorf("run state = %v, want runFailed", m.run.state)
	}
	if o := m.Outcome(); o.Succeeded {
		t.Error("a failed engine run must never read as success")
	}
}

// The install date is Docker's to give: until the lookup answers, the
// confirm screen names the deployment and says nothing about its age.
func TestUpdateModel_ConfirmLeavesTheDateOutUntilDockerSaysIt(t *testing.T) {
	m, _ := newTestUpdateModel(testDeployment(), engineRunSeams{})

	before := screen(m)
	if !strings.Contains(before, "mail.example.com") || strings.Contains(before, "installed") {
		t.Fatalf("before the lookup answers, want the host and no date:\n%s", before)
	}

	updated, _ := m.Update(installedAtMsg{at: time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)})
	after := ansi.Strip(updated.(UpdateModel).View().Content)
	if !strings.Contains(after, "mail.example.com · installed 2026-06-14") {
		t.Fatalf("after the lookup answers, want the date beside the host:\n%s", after)
	}
}

func TestUpdateModel_InitAsksForTheInstallDate(t *testing.T) {
	d := testDeployment()
	want := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	var seen *deploy.Deployment
	m := NewUpdateModel(d, "/opt/orbit", "v9.9.9")
	m.lookupInstalledAt = func(_ context.Context, got *deploy.Deployment) time.Time {
		seen = got
		return want
	}

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must start the install-date lookup")
	}
	msg, ok := cmd().(installedAtMsg)
	if !ok {
		t.Fatalf("Init's command did not answer with the install date")
	}
	if !msg.at.Equal(want) {
		t.Errorf("install date = %v, want %v", msg.at, want)
	}
	if seen != d {
		t.Errorf("the lookup was asked about %+v, want this deployment", seen)
	}

	// Nothing installed here: there is no date to look up.
	if cmd := NewUpdateModel(nil, "/opt/orbit", "v9.9.9").Init(); cmd != nil {
		t.Error("with no deployment, Init must not start a lookup")
	}
}
