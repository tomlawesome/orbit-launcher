package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

func newTestUpdateModel(d *deploy.Deployment, install func(context.Context, string, func(string)) error) UpdateModel {
	m := NewUpdateModel(d)
	m.install = install
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
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
	m := newTestUpdateModel(nil, nil)
	if m.state != updateStateNotFound {
		t.Fatalf("state = %v, want updateStateNotFound", m.state)
	}
	_, cmd := m.Update(key(tea.KeyEnter))
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected any key on the not-found screen to quit")
	}
}

func TestUpdateModel_CancelFromConfirmNeverCallsInstall(t *testing.T) {
	called := false
	m := newTestUpdateModel(testDeployment(), func(context.Context, string, func(string)) error {
		called = true
		return nil
	})

	updated, _ := m.Update(key(tea.KeyDown)) // move to Cancel
	m = updated.(UpdateModel)
	_, cmd := m.Update(key(tea.KeyEnter))

	if called {
		t.Error("install must never be called when Cancel is chosen")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Cancel to issue tea.Quit")
	}
}

func TestUpdateModel_EscapeAtConfirmQuitsWithoutCallingInstall(t *testing.T) {
	called := false
	m := newTestUpdateModel(testDeployment(), func(context.Context, string, func(string)) error {
		called = true
		return nil
	})

	_, cmd := m.Update(key(tea.KeyEsc))

	if called {
		t.Error("install must never be called on Escape")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Escape to issue tea.Quit")
	}
}

func TestUpdateModel_ConfirmRunsInstallWithoutWritingConfigAndReachesDone(t *testing.T) {
	var gotTargetDir string
	installCalled := false
	m := newTestUpdateModel(testDeployment(), func(_ context.Context, targetDir string, onLine func(string)) error {
		installCalled = true
		gotTargetDir = targetDir
		onLine("resolving image")
		onLine("starting services")
		return nil
	})

	updated, cmd := m.Update(key(tea.KeyEnter)) // Update Orbit is selected by default
	m = updated.(UpdateModel)
	if m.state != updateStateUpdating {
		t.Fatalf("state = %v, want updateStateUpdating", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a command to start waiting for update events")
	}

	for i := 0; i < 10; i++ {
		msg := cmd()
		updated, cmd = m.Update(msg)
		m = updated.(UpdateModel)
		if m.state != updateStateUpdating {
			break
		}
	}

	if !installCalled {
		t.Error("expected install to be called")
	}
	if gotTargetDir != "/opt/orbit" {
		t.Errorf("install called with targetDir = %q, want /opt/orbit", gotTargetDir)
	}
	if m.state != updateStateDone {
		t.Errorf("state = %v, want updateStateDone", m.state)
	}
	if len(m.lines) == 0 {
		t.Error("expected streamed lines to have been recorded")
	}
}

func TestUpdateModel_InstallFailureReachesFailedState(t *testing.T) {
	m := newTestUpdateModel(testDeployment(), func(context.Context, string, func(string)) error {
		return errors.New("docker compose up failed")
	})

	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(UpdateModel)
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(UpdateModel)

	if m.state != updateStateFailed {
		t.Errorf("state = %v, want updateStateFailed", m.state)
	}
	if m.updateErr == nil {
		t.Error("expected updateErr to be set")
	}
}

func TestUpdateModel_NeverInvokesInstallAutomatically(t *testing.T) {
	called := false
	m := newTestUpdateModel(testDeployment(), func(context.Context, string, func(string)) error {
		called = true
		return nil
	})
	_ = m.View() // rendering alone must never trigger a side effect
	if called {
		t.Error("install must only run after an explicit confirm, never as a side effect of rendering")
	}
}
