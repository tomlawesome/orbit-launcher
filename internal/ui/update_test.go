package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

func newTestUpdateModel(d *deploy.Deployment, seams engineRunSeams) UpdateModel {
	m := NewUpdateModel(d, "/opt/orbit", "v9.9.9")
	m.seams = seams
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	return updated.(UpdateModel)
}

func testDeployment() *deploy.Deployment {
	return &deploy.Deployment{
		TargetDir:   "/opt/orbit",
		AppURL:      "https://mail.example.com",
		Image:       "ghcr.io/tomlawesome/orbit@sha256:abc",
		InstalledAt: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
	}
}

func TestUpdateModel_NoDeploymentReachesNotFoundAndOnlyQuits(t *testing.T) {
	m := newTestUpdateModel(nil, engineRunSeams{})
	if m.state != updateStateNotFound {
		t.Fatalf("state = %v, want updateStateNotFound", m.state)
	}
	_, cmd := m.Update(key(tea.KeyEnter))
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected any key on the not-found screen to quit")
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
	m := newTestUpdateModel(testDeployment(), seams)

	updated, _ := m.Update(key(tea.KeyDown)) // move to Cancel
	m = updated.(UpdateModel)
	_, cmd := m.Update(key(tea.KeyEnter))

	if engineCalled {
		t.Error("the engine must never start when Cancel is chosen")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Cancel to issue tea.Quit")
	}
}

func TestUpdateModel_EscapeAtConfirmQuitsWithoutStarting(t *testing.T) {
	engineCalled := false
	seams := engineRunSeams{
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			engineCalled = true
			return nil, nil, errors.New("should not be called")
		},
	}
	m := newTestUpdateModel(testDeployment(), seams)

	_, cmd := m.Update(key(tea.KeyEsc))

	if engineCalled {
		t.Error("the engine must never start on Escape")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Escape to issue tea.Quit")
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
	m := newTestUpdateModel(testDeployment(), seams)
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
	m := newTestUpdateModel(testDeployment(), seams)

	updated, cmd := m.Update(key(tea.KeyEnter)) // Update Orbit is selected by default
	m = updated.(UpdateModel)
	if m.state != updateStateRunning {
		t.Fatalf("state = %v, want updateStateRunning", m.state)
	}
	m = drive(t, m, cmd).(UpdateModel)

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

func TestUpdateModel_ConfigurationRefusalReachesThePromptToo(t *testing.T) {
	// A migration can surface missing fields on update as well; the
	// same handoff stretch applies.
	m := newTestUpdateModel(testDeployment(), engineRunSeams{
		prepareEngine: fakeEngine(nil, configRefusalStream()...),
	})
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = drive(t, updated, cmd).(UpdateModel)

	if m.run.state != runConfigPrompt {
		t.Fatalf("run state = %v, want runConfigPrompt", m.run.state)
	}
}

func TestUpdateModel_EngineFailureReachesFailedState(t *testing.T) {
	m := newTestUpdateModel(testDeployment(), engineRunSeams{
		prepareEngine: fakeEngine(nil,
			ev("database", "database", "failed", "database-auth-migration", "repair"),
			engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1},
		),
	})
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = drive(t, updated, cmd).(UpdateModel)

	if m.run.state != runFailed {
		t.Errorf("run state = %v, want runFailed", m.run.state)
	}
	if o := m.Outcome(); o.Succeeded {
		t.Error("a failed engine run must never read as success")
	}
}
