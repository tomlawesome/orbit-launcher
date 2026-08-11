package ui

import (
	"bytes"
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

func TestAppModel_UnwiredChoicesJustQuit(t *testing.T) {
	m := NewAppModel()
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // Install is selected by default

	if err := tm.Quit(); err != nil {
		t.Fatalf("expected an unwired choice to quit cleanly, got: %v", err)
	}
}
