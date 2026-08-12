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
	skipArrival(tm)

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
	skipArrival(tm)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyDown}) // Update
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("No Orbit deployment found here"))
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
	skipArrival(tm)

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
	skipArrival(tm)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyDown}) // Update
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Pull the latest Orbit and update this deployment"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEsc}) // Cancel out without ever touching Docker
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_SelectingRepairRunsDiagnosisAndMenuReturnsToSplash(t *testing.T) {
	m := NewAppModel()
	m.targetDir = t.TempDir()
	m.flowSeams = engineRunSeams{
		prepareRepair: fakeRepairStream(`echo 'diagnosis result=healthy checked=13 skipped=0'; exit 0`),
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	skipArrival(tm)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	for i := 0; i < 2; i++ { // Install, Update, Repair
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Diagnosis clear"))
	}, teatest.WithDuration(5*time.Second))

	// "Menu" is preselected: back to the splash.
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("O R B I T"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_InstallSuccessReachesSuccessScreenAndMenuReturnsToSplash(t *testing.T) {
	dir := t.TempDir()
	m := NewAppModel()
	m.targetDir = dir
	m = m.WithVersion("v9.9.9")
	m.flowSeams = engineRunSeams{
		prepareEngine: fakeEngine(nil, successStream()...),
		detect:        fakeDetect("https://mail.example.com"),
	}

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 26))
	skipArrival(tm)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Install
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Choose a deployment profile"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Standard
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Ready to install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Install now -> engine runs -> success

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Get into Orbit")) &&
			bytes.Contains(out, []byte("https://mail.example.com")) &&
			bytes.Contains(out, []byte("alive"))
	}, teatest.WithDuration(2*time.Second))

	// Menu (third item) returns to the splash — the launcher is a loop.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Repair")) && bytes.Contains(out, []byte("Remove"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_SuccessScreenTerminalQuitsTheProgram(t *testing.T) {
	m := NewAppModel()
	m.targetDir = t.TempDir()
	m.flowSeams = engineRunSeams{
		prepareEngine: fakeEngine(nil, successStream()...),
		detect:        fakeDetect("https://mail.example.com"),
	}

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 26))
	skipArrival(tm)
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Install
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Standard
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Ready to install"))
	}, teatest.WithDuration(2*time.Second))
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Install now

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Get into Orbit"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyDown}) // Terminal
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	tm.WaitFinished(t, teatest.WithFinalTimeout(2*time.Second))
}

func TestAppModel_WithDeploymentStatusPreselectsUpdateAndSetsFQDN(t *testing.T) {
	dir := t.TempDir()
	envContent := "APP_URL=https://mail.example.com\nORBIT_IMAGE=ghcr.io/tomlawesome/orbit@sha256:abc\n"
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte(envContent), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	m := NewAppModel()
	m.targetDir = dir
	m = m.WithDeploymentStatus(nil)

	if m.splash.selected != menuUpdate {
		t.Errorf("selected = %d, want Update preselected for a detected deployment", m.splash.selected)
	}
	if m.splash.fqdn != "mail.example.com" {
		t.Errorf("fqdn = %q, want the bare host", m.splash.fqdn)
	}
	if m.splash.state != stateUnknown {
		t.Errorf("state = %v, want stateUnknown until a probe resolves", m.splash.state)
	}
}

func TestAppModel_WithDeploymentStatusIsANoOpWithoutADeployment(t *testing.T) {
	m := NewAppModel()
	m.targetDir = t.TempDir()
	m = m.WithDeploymentStatus(nil)

	if m.splash.selected != menuInstall {
		t.Errorf("selected = %d, want Install for a dormant machine", m.splash.selected)
	}
	if m.splash.state != stateDormant {
		t.Errorf("state = %v, want stateDormant", m.splash.state)
	}
}
