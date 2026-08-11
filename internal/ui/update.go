package ui

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type updateState int

const (
	updateStateNotFound updateState = iota
	updateStateConfirm
	updateStateRunning
	updateStateDone
	updateStateFailed
)

// UpdateModel is the Update flow: confirm, then the same full terminal
// handoff to install.sh that Install uses — see internal/ui/install.go
// and internal/deploy.BuildInstallCommand for why. install.sh is
// idempotent: run again against a directory that already holds a
// recognised deployment, it pulls the latest image, refreshes
// deployment assets, and runs its own configuration migration, all
// while preserving the existing .env-orbit values (configure.sh's own
// "Existing values were preserved" behaviour, verified directly
// against orbit's develop branch).
type UpdateModel struct {
	width, height int
	deployment    *deploy.Deployment
	state         updateState
	confirmSel    int // 0 = Update Orbit, 1 = Cancel

	updateErr error
	cleanup   func() error

	// Overridable in tests so they don't need real network/terminal
	// access; production code leaves these nil and gets the real
	// implementations.
	prepareInstall func(ctx context.Context, targetDir string) (*exec.Cmd, func() error, error)
	runHandoff     func(cmd *exec.Cmd) tea.Cmd
}

// NewUpdateModel constructs the Update flow for a detected deployment. A
// nil deployment means Update was chosen with nothing installed here —
// the model honestly says so rather than pretending there's something to
// update.
func NewUpdateModel(d *deploy.Deployment) UpdateModel {
	state := updateStateConfirm
	if d == nil {
		state = updateStateNotFound
	}
	return UpdateModel{
		deployment:     d,
		state:          state,
		prepareInstall: defaultPrepareInstall,
		runHandoff:     defaultRunHandoff,
	}
}

// Init implements tea.Model.
func (m UpdateModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m UpdateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case installPreparedMsg:
		if msg.err != nil {
			m.updateErr = msg.err
			m.state = updateStateFailed
			return m, nil
		}
		m.cleanup = msg.cleanup
		return m, m.runHandoff(msg.cmd)

	case installFinishedMsg:
		if m.cleanup != nil {
			m.cleanup()
			m.cleanup = nil
		}
		if msg.err != nil {
			m.updateErr = msg.err
			m.state = updateStateFailed
		} else {
			m.state = updateStateDone
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m UpdateModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	switch m.state {
	case updateStateNotFound:
		return m, tea.Quit
	case updateStateConfirm:
		return m.handleConfirmKey(msg)
	case updateStateDone, updateStateFailed:
		return m, tea.Quit
	}
	return m, nil
}

func (m UpdateModel) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyUp, tea.KeyDown:
		m.confirmSel = 1 - m.confirmSel
		return m, nil
	case tea.KeyEnter:
		if m.confirmSel == 1 {
			return m, tea.Quit
		}
		m.state = updateStateRunning
		return m, prepareInstallCmd(m.prepareInstall, targetDirOrPlaceholder(m.deployment))
	}
	return m, nil
}

// View implements tea.Model.
func (m UpdateModel) View() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case updateStateNotFound:
		return m.viewNotFound()
	case updateStateConfirm:
		return m.viewConfirm()
	case updateStateRunning:
		return m.viewRunning()
	case updateStateDone:
		return m.viewDone()
	case updateStateFailed:
		return m.viewFailed()
	}
	return ""
}

func (m UpdateModel) frame(body string) string {
	return lipgloss.NewStyle().Padding(1, 2).Render(body)
}

func (m UpdateModel) viewNotFound() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+lipgloss.NewStyle().Bold(true).Render("No existing Orbit deployment found here"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "There's nothing to update in this directory. Use Install")
	fmt.Fprintln(&b, "first if you haven't deployed Orbit yet.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  "+style.MenuUnselected.Render("Exit"))
	return m.frame(b.String())
}

func (m UpdateModel) viewConfirm() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.MenuSelected.Render("ORBIT · Update"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Render("This pulls the latest Orbit image and updates your deployment"))
	fmt.Fprintln(&b)

	appURL, installed, image := "unknown", "unknown", "unknown"
	if m.deployment != nil {
		if m.deployment.AppURL != "" {
			appURL = m.deployment.AppURL
		}
		if m.deployment.Image != "" {
			image = m.deployment.Image
		}
		installed = m.deployment.InstalledAt.Format("2006-01-02")
	}
	fmt.Fprintf(&b, "  %s  %s\n", style.MenuUnselected.Render("Deployment"), appURL)
	fmt.Fprintf(&b, "  %s   %s\n", style.MenuUnselected.Render("Installed"), installed)
	fmt.Fprintf(&b, "  %s       %s\n", style.MenuUnselected.Render("Image"), image)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("This hands control of your terminal to Orbit's own installer."))
	fmt.Fprintln(&b, style.Tagline.Render("Your existing configuration is preserved. Nothing is deleted."))
	fmt.Fprintln(&b)

	options := []string{"Update Orbit", "Cancel"}
	for i, opt := range options {
		if i == m.confirmSel {
			fmt.Fprintln(&b, style.MenuCaret.Render(style.SymbolSelected)+" "+style.MenuSelected.Render(opt))
		} else {
			fmt.Fprintln(&b, "  "+style.MenuUnselected.Render(opt))
		}
	}
	return m.frame(b.String())
}

func (m UpdateModel) viewRunning() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.MenuSelected.Render("ORBIT · Updating"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.WarmText.Render("⠋")+" Handing off to install.sh…")
	return m.frame(b.String())
}

func (m UpdateModel) viewDone() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.SuccessText.Render(style.SymbolSuccess)+" "+lipgloss.NewStyle().Bold(true).Render("Orbit is up to date"))
	fmt.Fprintln(&b)
	if m.deployment != nil && m.deployment.AppURL != "" {
		fmt.Fprintln(&b, style.AccentText.Render(m.deployment.AppURL))
		fmt.Fprintln(&b)
	}
	fmt.Fprintln(&b, "  "+style.MenuUnselected.Render("Exit"))
	return m.frame(b.String())
}

func (m UpdateModel) viewFailed() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+lipgloss.NewStyle().Bold(true).Render("Update stopped"))
	fmt.Fprintln(&b)
	if m.updateErr != nil {
		fmt.Fprintln(&b, style.Tagline.Render(m.updateErr.Error()))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  "+style.MenuUnselected.Render("Exit"))
	return m.frame(b.String())
}
