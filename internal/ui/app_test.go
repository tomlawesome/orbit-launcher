package ui

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
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
		tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("This stops Orbit and removes its containers"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEsc}) // Cancel out of Remove
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

	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown}) // Update
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("No Orbit deployment found here"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_SelectingInstallLaunchesTheInstallFlow(t *testing.T) {
	m := NewAppModel()
	m.targetDir = t.TempDir()
	m.flowCheckVolumes = noStaleVolumes
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	skipArrival(tm)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Choose a deployment profile")) || bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // Install is selected by default

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Choose a deployment profile"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // Standard profile is selected by default

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Ready to install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEsc}) // confirm -> profile
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEsc}) // profile -> quit
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

	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown}) // Update
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Pull the latest Orbit and update this deployment"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEsc}) // Cancel out without ever touching Docker
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_SelectingRepairRunsDiagnosisAndMenuReturnsToSplash(t *testing.T) {
	m := NewAppModel()
	m.targetDir = t.TempDir()
	m.flowCheckVolumes = noStaleVolumes
	m.flowSeams = engineRunSeams{
		prepareRepair: fakeRepairStream(`echo 'diagnosis result=healthy checked=13 skipped=0'; exit 0`),
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))
	skipArrival(tm)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	for i := 0; i < 2; i++ { // Install, Update, Repair
		tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Diagnosis clear"))
	}, teatest.WithDuration(5*time.Second))

	// "Menu" is preselected: back to the splash.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("O R B I T"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_InstallSuccessReachesSuccessScreenAndMenuReturnsToSplash(t *testing.T) {
	dir := t.TempDir()
	m := NewAppModel()
	m.targetDir = dir
	m = m.WithVersion("v9.9.9")
	m.flowCheckVolumes = noStaleVolumes
	m.flowSeams = engineRunSeams{
		prepareEngine: fakeEngine(nil, successStream()...),
		detect:        fakeDetect("https://mail.example.com"),
	}

	sender := &deferredSender{}
	m.flowSend = sender.Send
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 26))
	sender.attach(tm.Send)
	skipArrival(tm)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // Install
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Choose a deployment profile"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // Standard
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Ready to install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // Install now -> engine runs -> success

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Get into Orbit")) &&
			bytes.Contains(out, []byte("https://mail.example.com")) &&
			bytes.Contains(out, []byte("alive"))
	}, teatest.WithDuration(2*time.Second))

	// Menu (third item) returns to the splash — the launcher is a loop.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Repair")) && bytes.Contains(out, []byte("Remove"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEsc})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_SuccessScreenTerminalQuitsTheProgram(t *testing.T) {
	m := NewAppModel()
	m.targetDir = t.TempDir()
	m.flowCheckVolumes = noStaleVolumes
	m.flowSeams = engineRunSeams{
		prepareEngine: fakeEngine(nil, successStream()...),
		detect:        fakeDetect("https://mail.example.com"),
	}

	sender := &deferredSender{}
	m.flowSend = sender.Send
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 26))
	sender.attach(tm.Send)
	skipArrival(tm)
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // Install
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // Standard
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Ready to install"))
	}, teatest.WithDuration(2*time.Second))
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // Install now

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Get into Orbit"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown}) // Terminal
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

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

// deferredSender is ProgramSender's teatest twin, and exists for the
// same ordering reason: teatest.NewTestModel takes the model, so the
// sender is built first, handed to the model, and pointed at the test
// model's Send afterwards. Anything sent before that is dropped, which
// is safe here — only a started engine run has a reader to send at all.
//
// The pointer is atomic because the reader goroutine and the test
// goroutine that attaches reach it from different threads.
type deferredSender struct {
	send atomic.Pointer[func(tea.Msg)]
}

// attach points the sender at the running test model.
func (s *deferredSender) attach(send func(tea.Msg)) { s.send.Store(&send) }

// Send delivers one message into the event loop, and does nothing
// before attach.
func (s *deferredSender) Send(msg tea.Msg) {
	if send := s.send.Load(); send != nil {
		(*send)(msg)
	}
}

// noStaleVolumes fakes Install's stale-database-volume pre-flight as a
// clean machine. Without it these tests would reach the real Docker
// daemon, and on any machine that has ever run Orbit the pre-flight
// would legitimately interrupt the profile screen they assert on —
// every other dependency in this file is already faked for the same
// reason.
func noStaleVolumes(context.Context, string) []deploy.DatabaseVolume { return nil }
