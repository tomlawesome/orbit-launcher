package ui

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// fakeHandoff returns a runHandoff stand-in that never touches a real
// terminal or process — it just synchronously reports the given error,
// so Install/Update tests can exercise the full prepare -> handoff ->
// done/failed state machine deterministically.
func fakeHandoff(err error) func(*exec.Cmd) tea.Cmd {
	return func(*exec.Cmd) tea.Cmd {
		return func() tea.Msg { return installFinishedMsg{err: err} }
	}
}

func newTestInstallModel(prepare func(context.Context, string) (*exec.Cmd, func() error, error), handoff func(*exec.Cmd) tea.Cmd) InstallModel {
	m := NewInstallModel("/opt/orbit")
	m.prepareInstall = prepare
	m.runHandoff = handoff
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	return updated.(InstallModel)
}

func fakePrepare(cmd *exec.Cmd, cleanupCalled *bool, err error) func(context.Context, string) (*exec.Cmd, func() error, error) {
	return func(context.Context, string) (*exec.Cmd, func() error, error) {
		if err != nil {
			return nil, nil, err
		}
		return cmd, func() error { *cleanupCalled = true; return nil }, nil
	}
}

func TestInstallModel_SelectingStandardMovesToConfirm(t *testing.T) {
	m := newTestInstallModel(nil, nil)
	updated, _ := m.Update(key(tea.KeyEnter))
	m = updated.(InstallModel)
	if m.state != installStateConfirm {
		t.Errorf("state = %v, want installStateConfirm", m.state)
	}
}

func TestInstallModel_SelectingAIOrFullShowsUnavailableNotFakeProgress(t *testing.T) {
	for _, sel := range []int{1, 2} { // AI, Full
		m := newTestInstallModel(nil, nil)
		m.profileSel = sel
		updated, _ := m.Update(key(tea.KeyEnter))
		m = updated.(InstallModel)
		if m.state != installStateUnavailableProfile {
			t.Errorf("profile %d: state = %v, want installStateUnavailableProfile", sel, m.state)
		}
	}
}

func TestInstallModel_EscapeFromConfirmReturnsToProfile(t *testing.T) {
	m := newTestInstallModel(nil, nil)
	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> confirm
	m = updated.(InstallModel)
	updated, _ = m.Update(key(tea.KeyEsc))
	m = updated.(InstallModel)
	if m.state != installStateProfile {
		t.Errorf("state = %v, want installStateProfile", m.state)
	}
}

func TestInstallModel_ConfirmNeverPreparesOrHandsOffAutomatically(t *testing.T) {
	prepareCalled := false
	prepare := func(context.Context, string) (*exec.Cmd, func() error, error) {
		prepareCalled = true
		return nil, nil, errors.New("should not be called")
	}
	m := newTestInstallModel(prepare, nil)
	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> confirm
	m = updated.(InstallModel)
	_ = m.View() // rendering alone must never trigger a side effect

	if prepareCalled {
		t.Error("prepareInstall must only run after an explicit confirm, never as a side effect of rendering")
	}
}

func TestInstallModel_ConfirmEnterPreparesThenHandsOffAndReachesDone(t *testing.T) {
	cleanupCalled := false
	fakeCmd := exec.Command("true")
	prepare := fakePrepare(fakeCmd, &cleanupCalled, nil)
	m := newTestInstallModel(prepare, fakeHandoff(nil))

	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> confirm
	m = updated.(InstallModel)
	updated, cmd := m.Update(key(tea.KeyEnter)) // confirm -> running
	m = updated.(InstallModel)
	if m.state != installStateRunning {
		t.Fatalf("state = %v, want installStateRunning", m.state)
	}
	if cmd == nil {
		t.Fatal("expected a command to prepare install.sh")
	}

	msg := cmd() // installPreparedMsg
	updated, cmd = m.Update(msg)
	m = updated.(InstallModel)
	if cmd == nil {
		t.Fatal("expected a command to run the handoff after preparing")
	}

	msg = cmd() // installFinishedMsg
	updated, _ = m.Update(msg)
	m = updated.(InstallModel)

	if m.state != installStateDone {
		t.Errorf("state = %v, want installStateDone", m.state)
	}
	if !cleanupCalled {
		t.Error("expected cleanup to be called after the handoff finished")
	}
}

func TestInstallModel_PrepareFailureReachesFailedWithoutHandoff(t *testing.T) {
	handoffCalled := false
	handoff := func(*exec.Cmd) tea.Cmd {
		handoffCalled = true
		return nil
	}
	prepare := func(context.Context, string) (*exec.Cmd, func() error, error) {
		return nil, nil, errors.New("could not fetch install.sh")
	}
	m := newTestInstallModel(prepare, handoff)

	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> confirm
	m = updated.(InstallModel)
	updated, cmd := m.Update(key(tea.KeyEnter)) // confirm -> running
	m = updated.(InstallModel)

	msg := cmd()
	updated, _ = m.Update(msg)
	m = updated.(InstallModel)

	if handoffCalled {
		t.Error("the handoff must never run when preparing install.sh fails")
	}
	if m.state != installStateFailed {
		t.Errorf("state = %v, want installStateFailed", m.state)
	}
	if m.installErr == nil {
		t.Error("expected installErr to be set")
	}
}

func TestInstallModel_HandoffFailureReachesFailedAndStillCleansUp(t *testing.T) {
	cleanupCalled := false
	fakeCmd := exec.Command("false")
	prepare := fakePrepare(fakeCmd, &cleanupCalled, nil)
	m := newTestInstallModel(prepare, fakeHandoff(errors.New("install.sh exited 1")))

	updated, _ := m.Update(key(tea.KeyEnter)) // Standard -> confirm
	m = updated.(InstallModel)
	updated, cmd := m.Update(key(tea.KeyEnter)) // confirm -> running
	m = updated.(InstallModel)

	msg := cmd()
	updated, cmd = m.Update(msg)
	m = updated.(InstallModel)

	msg = cmd()
	updated, _ = m.Update(msg)
	m = updated.(InstallModel)

	if m.state != installStateFailed {
		t.Errorf("state = %v, want installStateFailed", m.state)
	}
	if !cleanupCalled {
		t.Error("expected cleanup to still be called after a handoff failure")
	}
}
