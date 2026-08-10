package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPlaceholderModel_QuitsOnAnyKey(t *testing.T) {
	m := NewPlaceholderModel()

	if !strings.Contains(m.View(), "hello, orbit-launcher") {
		t.Fatalf("initial view missing greeting: %q", m.View())
	}

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	next, ok := updated.(PlaceholderModel)
	if !ok {
		t.Fatalf("Update returned unexpected model type %T", updated)
	}
	if cmd == nil {
		t.Fatal("expected a quit command after a keypress")
	}
	if got := cmd(); got != tea.Quit() {
		t.Fatalf("expected tea.Quit, got %#v", got)
	}
	if view := next.View(); view != "" {
		t.Fatalf("expected empty view once quitting, got %q", view)
	}
}

func TestPlaceholderModel_IgnoresNonKeyMessages(t *testing.T) {
	m := NewPlaceholderModel()

	updated, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	if cmd != nil {
		t.Fatal("expected no command for a non-key message")
	}
	next, ok := updated.(PlaceholderModel)
	if !ok {
		t.Fatalf("Update returned unexpected model type %T", updated)
	}
	if !strings.Contains(next.View(), "hello, orbit-launcher") {
		t.Fatalf("view should still greet after a non-key message: %q", next.View())
	}
}
