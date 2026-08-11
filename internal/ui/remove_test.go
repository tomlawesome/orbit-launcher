package ui

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

func newTestRemoveModel(standDown func(context.Context, string) error) RemoveModel {
	d := &deploy.Deployment{
		TargetDir:   "/opt/orbit",
		AppURL:      "https://mail.example.com",
		InstalledAt: time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC),
	}
	m := NewRemoveModel(d)
	m.standDown = standDown
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(RemoveModel)
}

func TestRemoveModel_CancelFromConfirmNeverCallsStandDown(t *testing.T) {
	called := false
	m := newTestRemoveModel(func(context.Context, string) error {
		called = true
		return nil
	})

	updated, _ := m.Update(key(tea.KeyDown)) // move to Cancel
	m = updated.(RemoveModel)
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(RemoveModel)

	if called {
		t.Error("StandDown must never be called when Cancel is chosen")
	}
	if m.state != removeStateCancelled {
		t.Errorf("state = %v, want removeStateCancelled", m.state)
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Cancel to issue tea.Quit")
	}
}

func TestRemoveModel_EscapeAtConfirmCancelsWithoutCallingStandDown(t *testing.T) {
	called := false
	m := newTestRemoveModel(func(context.Context, string) error {
		called = true
		return nil
	})

	updated, cmd := m.Update(key(tea.KeyEsc))
	m = updated.(RemoveModel)

	if called {
		t.Error("StandDown must never be called on Escape")
	}
	if m.state != removeStateCancelled {
		t.Errorf("state = %v, want removeStateCancelled", m.state)
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Escape to issue tea.Quit")
	}
}

func TestRemoveModel_ConfirmStandsDownAndReachesDoneOnSuccess(t *testing.T) {
	var gotTargetDir string
	m := newTestRemoveModel(func(_ context.Context, targetDir string) error {
		gotTargetDir = targetDir
		return nil
	})

	updated, cmd := m.Update(key(tea.KeyEnter)) // Stand down Orbit is selected by default
	m = updated.(RemoveModel)
	if m.state != removeStateStandingDown {
		t.Fatalf("state = %v, want removeStateStandingDown", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a command to run StandDown")
	}

	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(RemoveModel)

	if gotTargetDir != "/opt/orbit" {
		t.Errorf("StandDown called with targetDir = %q, want /opt/orbit", gotTargetDir)
	}
	if m.state != removeStateDone {
		t.Errorf("state = %v, want removeStateDone", m.state)
	}
}

func TestRemoveModel_ConfirmReachesFailedOnError(t *testing.T) {
	m := newTestRemoveModel(func(context.Context, string) error {
		return errors.New("docker daemon not running")
	})

	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(RemoveModel)
	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(RemoveModel)

	if m.state != removeStateFailed {
		t.Errorf("state = %v, want removeStateFailed", m.state)
	}
	if m.standDownErr == nil {
		t.Error("expected standDownErr to be set")
	}
}

func TestRemoveModel_DoneScreenCopyThenExit(t *testing.T) {
	m := newTestRemoveModel(func(context.Context, string) error { return nil })
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(RemoveModel)
	updated, _ = m.Update(cmd())
	m = updated.(RemoveModel)
	if m.state != removeStateDone {
		t.Fatalf("state = %v, want removeStateDone", m.state)
	}

	// Copy command is selected by default (doneSel == 0); Enter copies and
	// stays on screen rather than quitting.
	updated, copyCmd := m.Update(key(tea.KeyEnter))
	m = updated.(RemoveModel)
	if !m.copied {
		t.Error("expected copied to be true after selecting Copy command")
	}
	if copyCmd == nil {
		t.Fatal("expected a command to write the OSC 52 sequence")
	}
	if msg := copyCmd(); msg != nil {
		t.Errorf("expected the copy command to return a nil message, got %#v", msg)
	}

	// Move to Exit and confirm it quits.
	updated, _ = m.Update(key(tea.KeyDown))
	m = updated.(RemoveModel)
	_, quitCmd := m.Update(key(tea.KeyEnter))
	if quitCmd == nil || quitCmd() != tea.Quit() {
		t.Error("expected Exit to issue tea.Quit")
	}
}

func TestRemoveModel_NeverInvokesStandDownAutomatically(t *testing.T) {
	called := false
	m := newTestRemoveModel(func(context.Context, string) error {
		called = true
		return nil
	})
	_ = m.View() // rendering alone must never trigger a side effect
	if called {
		t.Error("StandDown must only run after an explicit confirm, never as a side effect of rendering")
	}
}
