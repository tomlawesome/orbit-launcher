package ui

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

func newTestUpdateModel(d *deploy.Deployment, prepare func(context.Context, string) (*exec.Cmd, func() error, error), handoff func(*exec.Cmd) tea.Cmd) UpdateModel {
	m := NewUpdateModel(d)
	m.prepareInstall = prepare
	m.runHandoff = handoff
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
	m := newTestUpdateModel(nil, nil, nil)
	if m.state != updateStateNotFound {
		t.Fatalf("state = %v, want updateStateNotFound", m.state)
	}
	_, cmd := m.Update(key(tea.KeyEnter))
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected any key on the not-found screen to quit")
	}
}

func TestUpdateModel_CancelFromConfirmNeverPrepares(t *testing.T) {
	prepareCalled := false
	prepare := func(context.Context, string) (*exec.Cmd, func() error, error) {
		prepareCalled = true
		return nil, nil, errors.New("should not be called")
	}
	m := newTestUpdateModel(testDeployment(), prepare, nil)

	updated, _ := m.Update(key(tea.KeyDown)) // move to Cancel
	m = updated.(UpdateModel)
	_, cmd := m.Update(key(tea.KeyEnter))

	if prepareCalled {
		t.Error("prepareInstall must never be called when Cancel is chosen")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Cancel to issue tea.Quit")
	}
}

func TestUpdateModel_EscapeAtConfirmQuitsWithoutPreparing(t *testing.T) {
	prepareCalled := false
	prepare := func(context.Context, string) (*exec.Cmd, func() error, error) {
		prepareCalled = true
		return nil, nil, errors.New("should not be called")
	}
	m := newTestUpdateModel(testDeployment(), prepare, nil)

	_, cmd := m.Update(key(tea.KeyEsc))

	if prepareCalled {
		t.Error("prepareInstall must never be called on Escape")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Escape to issue tea.Quit")
	}
}

func TestUpdateModel_ConfirmPreparesThenHandsOffAndReachesDone(t *testing.T) {
	cleanupCalled := false
	fakeCmd := exec.Command("true")
	var gotTargetDir string
	prepare := func(_ context.Context, targetDir string) (*exec.Cmd, func() error, error) {
		gotTargetDir = targetDir
		return fakeCmd, func() error { cleanupCalled = true; return nil }, nil
	}
	m := newTestUpdateModel(testDeployment(), prepare, fakeHandoff(nil))

	updated, cmd := m.Update(key(tea.KeyEnter)) // Update Orbit is selected by default
	m = updated.(UpdateModel)
	if m.state != updateStateRunning {
		t.Fatalf("state = %v, want updateStateRunning", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a command to prepare install.sh")
	}

	msg := cmd()
	updated, cmd = m.Update(msg)
	m = updated.(UpdateModel)
	if cmd == nil {
		t.Fatal("expected a command to run the handoff after preparing")
	}

	msg = cmd()
	updated, _ = m.Update(msg)
	m = updated.(UpdateModel)

	if gotTargetDir != "/opt/orbit" {
		t.Errorf("prepareInstall called with targetDir = %q, want /opt/orbit", gotTargetDir)
	}
	if m.state != updateStateDone {
		t.Errorf("state = %v, want updateStateDone", m.state)
	}
	if !cleanupCalled {
		t.Error("expected cleanup to be called after the handoff finished")
	}
}

func TestUpdateModel_PrepareFailureReachesFailedWithoutHandoff(t *testing.T) {
	handoffCalled := false
	handoff := func(*exec.Cmd) tea.Cmd {
		handoffCalled = true
		return nil
	}
	prepare := func(context.Context, string) (*exec.Cmd, func() error, error) {
		return nil, nil, errors.New("could not fetch install.sh")
	}
	m := newTestUpdateModel(testDeployment(), prepare, handoff)

	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(UpdateModel)
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(UpdateModel)

	if handoffCalled {
		t.Error("the handoff must never run when preparing install.sh fails")
	}
	if m.state != updateStateFailed {
		t.Errorf("state = %v, want updateStateFailed", m.state)
	}
	if m.updateErr == nil {
		t.Error("expected updateErr to be set")
	}
}

func TestUpdateModel_HandoffFailureReachesFailedState(t *testing.T) {
	cleanupCalled := false
	fakeCmd := exec.Command("false")
	prepare := func(context.Context, string) (*exec.Cmd, func() error, error) {
		return fakeCmd, func() error { cleanupCalled = true; return nil }, nil
	}
	m := newTestUpdateModel(testDeployment(), prepare, fakeHandoff(errors.New("docker compose up failed")))

	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(UpdateModel)
	msg := cmd()
	updated, cmd = m.Update(msg)
	m = updated.(UpdateModel)
	msg = cmd()
	updated, _ = m.Update(msg)
	m = updated.(UpdateModel)

	if m.state != updateStateFailed {
		t.Errorf("state = %v, want updateStateFailed", m.state)
	}
	if !cleanupCalled {
		t.Error("expected cleanup to still be called after a handoff failure")
	}
}

func TestUpdateModel_NeverPreparesAutomatically(t *testing.T) {
	prepareCalled := false
	prepare := func(context.Context, string) (*exec.Cmd, func() error, error) {
		prepareCalled = true
		return nil, nil, errors.New("should not be called")
	}
	m := newTestUpdateModel(testDeployment(), prepare, nil)
	_ = m.View() // rendering alone must never trigger a side effect
	if prepareCalled {
		t.Error("prepareInstall must only run after an explicit confirm, never as a side effect of rendering")
	}
}
