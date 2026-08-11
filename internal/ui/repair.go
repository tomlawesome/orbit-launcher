package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// RepairModel is a deliberately honest, non-mutating placeholder. orbit's
// own install.sh keeps Repair as a non-mutating dispatch seam until a real
// repair engine exists (tracked separately) — this does the same: it
// never touches Docker, the filesystem, or any deployment, and says so
// plainly rather than pretending to repair anything.
type RepairModel struct {
	width, height int
}

// NewRepairModel constructs the Repair stub.
func NewRepairModel() RepairModel { return RepairModel{} }

// Init implements tea.Model.
func (m RepairModel) Init() tea.Cmd { return nil }

// Update implements tea.Model. The only possible outcome is quitting —
// there is nothing here that reads input to decide on an action, because
// there is no action to take yet.
func (m RepairModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.KeyMsg:
		return m, tea.Quit
	}
	return m, nil
}

// View implements tea.Model.
func (m RepairModel) View() string {
	if m.width == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintln(&b, style.MenuSelected.Render("ORBIT · Repair"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Render("Repair isn't available yet"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "This is an honest placeholder, not a partial repair engine:")
	fmt.Fprintln(&b, style.Tagline.Render("nothing on your deployment has been touched, and nothing"))
	fmt.Fprintln(&b, style.Tagline.Render("here has the ability to touch it."))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  "+style.MenuUnselected.Render("Exit"))
	return lipgloss.NewStyle().Padding(1, 2).Render(b.String())
}
