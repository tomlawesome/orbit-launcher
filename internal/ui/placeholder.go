package ui

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PlaceholderModel is the Wave 0 promotion-gate screen: proof the full
// pipeline (build, test, CI, release) works before any real screen from
// design/mockups.html exists. Real screens land in Wave 1.
type PlaceholderModel struct {
	quitting bool
}

// NewPlaceholderModel constructs the Wave 0 placeholder screen.
func NewPlaceholderModel() PlaceholderModel {
	return PlaceholderModel{}
}

// Init implements tea.Model.
func (m PlaceholderModel) Init() tea.Cmd { return nil }

// Update implements tea.Model. Any key quits.
func (m PlaceholderModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.KeyMsg:
		m.quitting = true
		return m, tea.Quit
	}
	return m, nil
}

var placeholderStyle = lipgloss.NewStyle().Bold(true)

// View implements tea.Model.
func (m PlaceholderModel) View() string {
	if m.quitting {
		return ""
	}
	return placeholderStyle.Render("hello, orbit-launcher") + "\n\npress any key to exit\n"
}
