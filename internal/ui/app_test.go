package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func TestAppModel_SelectingRemoveLaunchesTheRemoveFlow(t *testing.T) {
	m := NewAppModel()
	m.targetDir = t.TempDir() // no .env-orbit here — a nil-deployment Remove flow

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	for i := 0; i < 3; i++ { // Install, Update, Repair, Remove
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("This stops Orbit and removes its containers"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEsc}) // Cancel out of Remove
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_WithUpdateCheckSetsItOnTheSplashScreenOnly(t *testing.T) {
	m := NewAppModel()
	m = m.WithUpdateCheck(func(context.Context) (string, bool, error) { return "", false, nil })
	if m.splash.checkForUpdate == nil {
		t.Error("expected WithUpdateCheck to set the splash screen's checkForUpdate")
	}
}

func TestAppModel_SelectingUpdateWithNoDeploymentShowsNotFound(t *testing.T) {
	m := NewAppModel()
	m.targetDir = t.TempDir() // no .env-orbit here
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyDown}) // Update
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("No existing Orbit deployment found here"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_SelectingInstallLaunchesTheInstallFlow(t *testing.T) {
	m := NewAppModel()
	m.targetDir = t.TempDir()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Choose a deployment profile")) || bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Install is selected by default

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Choose a deployment profile"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Standard profile is selected by default

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Ready to install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEsc}) // confirm -> profile
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc}) // profile -> quit
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_SelectingUpdateWithAnExistingDeploymentShowsTheConfirmScreen(t *testing.T) {
	dir := t.TempDir()
	envContent := "APP_URL=https://mail.example.com\nORBIT_IMAGE=ghcr.io/tomlawesome/orbit@sha256:abc\n"
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte(envContent), 0o600); err != nil {
		t.Fatalf("failed to write fixture .env-orbit: %v", err)
	}

	m := NewAppModel()
	m.targetDir = dir
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyDown}) // Update
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("This pulls the latest Orbit image and updates your deployment"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEsc}) // Cancel out without ever touching Docker
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_SelectingRepairShowsTheHonestStub(t *testing.T) {
	m := NewAppModel()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	for i := 0; i < 2; i++ { // Install, Update, Repair
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Repair isn't available yet"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}
