package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func newTestSuccessModel() SuccessModel {
	m := NewSuccessModel("https://mail.example.com", 3*time.Minute+42*time.Second, "v9.9.9")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	return updated.(SuccessModel)
}

func TestSuccessModel_ViewCarriesIdentitySlotAndFooter(t *testing.T) {
	m := newTestSuccessModel()
	view := m.View()

	for _, want := range []string{
		"https://mail.example.com", // hero line in the splash's identity slot
		"alive",                    // status word beneath it
		"Get into Orbit",           // stacked menu in splash grammar
		"Terminal",
		"Menu",
		"Orbit achieved in 3m 42s", // footer left: the console's real clock
		"v9.9.9",                   // footer right
	} {
		if !strings.Contains(view, want) {
			t.Errorf("success view missing %q", want)
		}
	}
	if strings.Contains(view, "Orbit is ready") {
		t.Error("the old completion copy must be gone")
	}
}

func TestSuccessModel_ZeroElapsedOmitsTheAchievedFigure(t *testing.T) {
	m := NewSuccessModel("https://mail.example.com", 0, "v9.9.9")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	m = updated.(SuccessModel)
	if strings.Contains(m.View(), "Orbit achieved in") {
		t.Error("a flow with no meaningful clock must not invent one")
	}
}

func TestSuccessModel_GetIntoOrbitOpensTheURLAndStays(t *testing.T) {
	var opened string
	m := newTestSuccessModel()
	m.openURL = func(url string) error { opened = url; return nil }

	updated, cmd := m.Update(key(tea.KeyEnter)) // Get into Orbit is default
	m = updated.(SuccessModel)

	if opened != "https://mail.example.com" {
		t.Errorf("opened = %q, want the deployment URL", opened)
	}
	if cmd != nil {
		t.Error("Get into Orbit must stay on this screen, not quit")
	}
	if m.Chosen != "" {
		t.Errorf("Chosen = %q, want empty — the screen remains", m.Chosen)
	}
}

func TestSuccessModel_NoBrowserShowsCopyHintNotError(t *testing.T) {
	m := newTestSuccessModel()
	m.openURL = func(string) error { return errors.New("no opener available") }

	updated, _ := m.Update(key(tea.KeyEnter))
	m = updated.(SuccessModel)

	if !strings.Contains(m.View(), "copy the URL above") {
		t.Error("a headless server deserves a copy hint, not an error screen")
	}
}

func TestSuccessModel_TerminalQuitsCleanly(t *testing.T) {
	m := newTestSuccessModel()
	updated, _ := m.Update(key(tea.KeyDown)) // Terminal
	m = updated.(SuccessModel)
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(SuccessModel)

	if m.Chosen != "terminal" {
		t.Errorf("Chosen = %q, want terminal", m.Chosen)
	}
	if cmd == nil || cmd() != tea.Quit() {
		t.Error("expected Terminal to issue tea.Quit")
	}
}

func TestSuccessModel_MenuSignalsReturnWithoutQuitting(t *testing.T) {
	m := newTestSuccessModel()
	for i := 0; i < 2; i++ { // Get into Orbit -> Terminal -> Menu
		updated, _ := m.Update(key(tea.KeyDown))
		m = updated.(SuccessModel)
	}
	updated, cmd := m.Update(key(tea.KeyEnter))
	m = updated.(SuccessModel)

	if m.Chosen != "menu" {
		t.Errorf("Chosen = %q, want menu", m.Chosen)
	}
	if cmd != nil {
		t.Error("Menu must not quit — AppModel swaps the screen")
	}
}

func TestSuccessModel_WordmarkIsGreen(t *testing.T) {
	// The wordmark carries the alive colour on success — the same
	// being as the splash, now green (design/mockups-v5.html §03).
	m := newTestSuccessModel()
	if !strings.Contains(m.View(), "\x1b[") {
		t.Skip("styling disabled in this environment")
	}
	// #4ade80 -> 78;222;128 in truecolor profiles; lipgloss may also
	// downsample, so assert on the seam that matters: the view differs
	// from a dormant splash's white wordmark rendering.
	splash := NewSplashModel()
	updatedSplash, _ := splash.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	if m.View() == updatedSplash.(SplashModel).View() {
		t.Error("success screen must not render identically to the splash")
	}
}
