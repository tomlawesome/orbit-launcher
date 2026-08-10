package ui

import (
	"bytes"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"
)

// TestPlaceholderModel_TeaTest drives a real tea.Program in-memory (no PTY,
// no subprocess) — the fast, high-volume layer of the interaction-test
// pyramid described in docs/implementation-plan.md section 3.2. The
// black-box PTY equivalent against the compiled binary lives in test/pty.
func TestPlaceholderModel_TeaTest(t *testing.T) {
	tm := teatest.NewTestModel(t, NewPlaceholderModel(), teatest.WithInitialTermSize(80, 24))

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("hello, orbit-launcher"))
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}
