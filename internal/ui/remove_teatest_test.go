package ui

import (
	"bytes"
	"context"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

func TestRemoveModel_TeaTest_FullFlowToDone(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"}
	m := NewRemoveModel(d)
	m.standDown = func(context.Context, string) error { return nil }

	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Stand down Orbit"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("stood down"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyDown})  // Exit
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter}) // quit

	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}
