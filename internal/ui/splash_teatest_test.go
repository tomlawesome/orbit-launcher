package ui

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

func TestSplashModel_TeaTest_RendersMarkAndMenu(t *testing.T) {
	tm := teatest.NewTestModel(t, NewSplashModel(), teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		// The wordmark is deliberately letter-spaced ("O R B I T"), not a
		// literal "ORBIT" substring — see style.Wordmark.
		return bytes.Contains(out, []byte("O R B I T")) && bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestSplashModel_TeaTest_NavigateAndSelectRemove(t *testing.T) {
	tm := teatest.NewTestModel(t, NewSplashModel(), teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Install"))
	}, teatest.WithDuration(2*time.Second))

	// Down x3 from Install lands on Remove (Install, Update, Repair, Remove).
	for i := 0; i < 3; i++ {
		tm.Send(tea.KeyMsg{Type: tea.KeyDown})
	}
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	finalModel := tm.FinalModel(t, teatest.WithFinalTimeout(2*time.Second))
	splash, ok := finalModel.(SplashModel)
	if !ok {
		t.Fatalf("final model has unexpected type %T", finalModel)
	}
	if splash.Chosen != "Remove" {
		t.Errorf("Chosen = %q, want %q", splash.Chosen, "Remove")
	}
}
