package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }
func runeKey(r rune) tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}} }

func TestSplashModel_ArrowNavigationWraps(t *testing.T) {
	m := NewSplashModel()

	if m.selected != 0 {
		t.Fatalf("initial selection = %d, want 0", m.selected)
	}

	updated, _ := m.Update(key(tea.KeyUp))
	m = updated.(SplashModel)
	if m.selected != len(MainMenu)-1 {
		t.Errorf("Up from index 0 = %d, want wrap to %d", m.selected, len(MainMenu)-1)
	}

	updated, _ = m.Update(key(tea.KeyDown))
	m = updated.(SplashModel)
	if m.selected != 0 {
		t.Errorf("Down from the last index = %d, want wrap to 0", m.selected)
	}
}

func TestSplashModel_EnterChoosesTheSelectedItemAndQuits(t *testing.T) {
	m := NewSplashModel()
	m.selected = 2 // Repair

	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(SplashModel)

	if m.Chosen != "Repair" {
		t.Errorf("Chosen = %q, want %q", m.Chosen, "Repair")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Enter to issue tea.Quit")
	}
}

func TestSplashModel_NumberKeyJumpsAndChooses(t *testing.T) {
	m := NewSplashModel()

	updated, cmd := m.Update(runeKey('4')) // 1-indexed: Remove
	m = updated.(SplashModel)

	if m.selected != 3 {
		t.Errorf("selected = %d, want 3 (Remove)", m.selected)
	}
	if m.Chosen != "Remove" {
		t.Errorf("Chosen = %q, want %q", m.Chosen, "Remove")
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected a number key to issue tea.Quit")
	}
}

func TestSplashModel_NumberKeyBeyondMenuLengthIsIgnored(t *testing.T) {
	m := NewSplashModel()

	updated, cmd := m.Update(runeKey('9'))
	m = updated.(SplashModel)

	if m.Chosen != "" {
		t.Errorf("Chosen = %q, want empty (out-of-range number key)", m.Chosen)
	}
	if cmd != nil {
		t.Error("expected no command for an out-of-range number key")
	}
}

func TestSplashModel_EscapeAndCtrlCQuitWithoutChoosing(t *testing.T) {
	for _, k := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		m := NewSplashModel()
		m.selected = 1

		updated, cmd := m.Update(key(k))
		m = updated.(SplashModel)

		if m.Chosen != "" {
			t.Errorf("%v: Chosen = %q, want empty", k, m.Chosen)
		}
		if !m.quitting {
			t.Errorf("%v: expected quitting to be true", k)
		}
		if cmd == nil || cmd() != tea.Quit() {
			t.Errorf("%v: expected tea.Quit", k)
		}
	}
}

func TestSplashModel_QLowercaseQuitsWithoutChoosing(t *testing.T) {
	m := NewSplashModel()
	updated, cmd := m.Update(runeKey('q'))
	m = updated.(SplashModel)

	if m.Chosen != "" {
		t.Errorf("Chosen = %q, want empty", m.Chosen)
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected 'q' to issue tea.Quit")
	}
}

func TestNewSplashModelNoAnimation_InitIssuesNoTickCommand(t *testing.T) {
	m := NewSplashModelNoAnimation()
	if cmd := m.Init(); cmd != nil {
		t.Error("expected Init() to issue no command in no-animation mode")
	}
}

func TestSplashModel_InitIssuesATickCommand(t *testing.T) {
	m := NewSplashModel()
	if cmd := m.Init(); cmd == nil {
		t.Error("expected Init() to issue a tick command by default")
	}
}

func TestSplashModel_ViewIsEmptyBeforeFirstWindowSize(t *testing.T) {
	m := NewSplashModel()
	if view := m.View(); view != "" {
		t.Errorf("View() before any WindowSizeMsg = %q, want empty", view)
	}
}

func TestSplashModel_ViewIsEmptyAfterQuitting(t *testing.T) {
	m := NewSplashModel()
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = updated.(SplashModel)
	updated, _ = m.Update(key(tea.KeyEsc))
	m = updated.(SplashModel)

	if view := m.View(); view != "" {
		t.Errorf("View() after quitting = %q, want empty", view)
	}
}
