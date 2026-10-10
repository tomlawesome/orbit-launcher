package ui

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

func newTestRemoveModel(standDown func(context.Context, string) error) RemoveModel {
	d := &deploy.Deployment{
		TargetDir: "/opt/orbit",
		AppURL:    "https://mail.example.com",
	}
	m := NewRemoveModel(d)
	m.standDown = standDown
	m.clipboard = io.Discard // no test may write OSC 52 to the real terminal
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

func TestRemoveModel_CopyWritesTheRemovalCommandAsOSC52(t *testing.T) {
	m := newTestRemoveModel(func(context.Context, string) error { return nil })
	var buf bytes.Buffer
	m.clipboard = &buf
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(RemoveModel)
	updated, _ = m.Update(cmd())
	m = updated.(RemoveModel)

	_, copyCmd := m.Update(key(tea.KeyEnter)) // Copy command is selected by default
	if copyCmd == nil {
		t.Fatal("expected a command to write the OSC 52 sequence")
	}
	copyCmd()

	const prefix, suffix = "\x1b]52;c;", "\x07"
	got := buf.String()
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("clipboard write = %q, want an OSC 52 sequence", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix))
	if err != nil {
		t.Fatalf("payload is not standard base64: %v", err)
	}
	if want := removalLine(t, "/opt/orbit"); string(decoded) != want {
		t.Errorf("copied %q, want the removal command %q", decoded, want)
	}
}

// In production nothing sets the seam, and the sequence must reach the
// terminal, which is the only thing that can act on it.
func TestRemoveModel_ClipboardDefaultsToStdout(t *testing.T) {
	if w := NewRemoveModel(nil).clipboardWriter(); w != os.Stdout {
		t.Errorf("clipboardWriter() = %v, want os.Stdout", w)
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

// The install date is Docker's to give: until the lookup answers, the
// confirm screen names the deployment and says nothing about its age.
func TestRemoveModel_ConfirmLeavesTheDateOutUntilDockerSaysIt(t *testing.T) {
	m := newTestRemoveModel(func(context.Context, string) error { return nil })

	before := stripANSI(m.View().Content)
	if !strings.Contains(before, "mail.example.com") || strings.Contains(before, "installed") {
		t.Fatalf("before the lookup answers, want the host and no date:\n%s", before)
	}

	updated, _ := m.Update(installedAtMsg{at: time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)})
	after := stripANSI(updated.(RemoveModel).View().Content)
	if !strings.Contains(after, "mail.example.com · installed 2026-06-14") {
		t.Fatalf("after the lookup answers, want the date beside the host:\n%s", after)
	}
}

func TestRemoveModel_InitAsksForTheInstallDate(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"}
	want := time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)
	var seen *deploy.Deployment
	m := NewRemoveModel(d)
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
}
