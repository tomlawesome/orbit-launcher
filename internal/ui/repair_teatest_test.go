package ui

import (
	"bytes"
	"context"
	"os/exec"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// fakeRepairStream runs a real subprocess that speaks the repair
// diagnosis contract, so the model consumes a genuine engine.Stream.
func fakeRepairStream(script string) prepareRepairFunc {
	return func(_ context.Context, _ string) (*engine.Stream, error) {
		return engine.Start(exec.Command("bash", "-c", script))
	}
}

func TestRepairModel_TeaTest_DiagnosisRendersAndReturnsToMenu(t *testing.T) {
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m.prepare = fakeRepairStream(`
echo 'finding class=managed-file-missing target=compose-file severity=fail'
echo 'finding class=database-credential-mismatch target=database severity=fail'
echo 'finding class=secret-missing target=session-secret severity=warn'
echo 'diagnosis result=failed checked=15 skipped=0'
exit 4`)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Problems found")) &&
			bytes.Contains(out, []byte("docker-compose.yml")) &&
			bytes.Contains(out, []byte("rejects the stored credentials")) &&
			bytes.Contains(out, []byte("session-secret secret")) &&
			bytes.Contains(out, []byte("15 checked · 0 skipped")) &&
			bytes.Contains(out, []byte("repair actions arrive with a later Orbit release"))
	}, teatest.WithDuration(5*time.Second))

	// "Exit" (one down from "Menu") quits cleanly.
	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestRepairModel_TeaTest_HealthyDiagnosis(t *testing.T) {
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m.prepare = fakeRepairStream(`echo 'diagnosis result=healthy checked=13 skipped=0'; exit 0`)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Diagnosis clear")) &&
			bytes.Contains(out, []byte("13 checked · 0 skipped")) &&
			!bytes.Contains(out, []byte("repair actions arrive"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestRepairModel_UnavailableOrbitLine(t *testing.T) {
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m.prepare = func(_ context.Context, _ string) (*engine.Stream, error) {
		return nil, deploy.ErrRepairUnavailable
	}
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Diagnosis needs a newer Orbit"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestRepairModel_MenuOutcomeWantsMenu(t *testing.T) {
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m.state = repairDiagnosis

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter}) // "Menu" is preselected
	got := updated.(RepairModel)
	if !got.Done || !got.WantsMenu {
		t.Fatalf("expected Done+WantsMenu after choosing Menu, got Done=%v WantsMenu=%v", got.Done, got.WantsMenu)
	}
}

func TestRepairModel_NotAnOrbitInstallation(t *testing.T) {
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m.prepare = fakeRepairStream(`
echo 'finding class=not-orbit-directory target=directory severity=fail'
echo 'diagnosis result=failed checked=1 skipped=12'
exit 5`)
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("No Orbit installation here"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}
