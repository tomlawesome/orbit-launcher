package ui

import (
	"fmt"
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
)

// UpdateModel is the Update flow: confirm, then the same mission
// console the Install flow uses (see internal/ui/enginerun.go), with
// the engine run flagged --update. install.sh is idempotent against an
// existing deployment: it pulls the latest image, refreshes deployment
// assets, and runs its own configuration migration, preserving the
// existing .env-orbit values.
type UpdateModel struct {
	width, height int
	targetDir     string
	version       string
	deployment    *deploy.Deployment
	state         updateState
	confirmSel    int // 0 = Update Orbit, 1 = Cancel

	run engineRun

	seams engineRunSeams
}

// NewUpdateModel constructs the Update flow for a detected deployment. A
// nil deployment means Update was chosen with nothing installed here —
// the model honestly says so rather than pretending there's something to
// update.
func NewUpdateModel(d *deploy.Deployment, targetDir, version string) UpdateModel {
	state := updateStateConfirm
	if d == nil {
		state = updateStateNotFound
	}
	return UpdateModel{deployment: d, targetDir: targetDir, version: version, state: state}
}

// Outcome surfaces the engine run's result to AppModel.
func (m UpdateModel) Outcome() flowOutcome { return outcomeOf(m.run) }

// Init implements tea.Model.
func (m UpdateModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m UpdateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if resized, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = resized.Width, resized.Height
	}

	if m.state == updateStateRunning {
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
		return m, cmd
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		return m.handleKey(key)
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
		title := "Update"
		if m.deployment != nil && m.deployment.AppURL != "" {
			title = "Update — " + displayHost(m.deployment.AppURL)
		}
		m.run = newEngineRun("update", m.resolvedTargetDir(), title, m.version).withSeams(m.seams)
		var cmd tea.Cmd
		m.run, cmd = m.run.start(m.width, m.height)
		return m, cmd
	}
	return m, nil
}

func (m UpdateModel) resolvedTargetDir() string {
	if m.targetDir != "" {
		return m.targetDir
	}
	return targetDirOrPlaceholder(m.deployment)
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
		return m.run.view(m.width, m.height)
	}
	return ""
}

// The flow screens speak the same starchart grammar as every other
// screen (design/DECISIONS.md): centred block, ⟡ mark and bold title,
// muted prose, individually-centred stacked menu, no keybind hints.

func (m UpdateModel) viewNotFound() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.AccentText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("No Orbit deployment found here"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("There's nothing to update in this directory —"))
	fmt.Fprintln(&b, style.MutedText.Render("Install is the way to get into Orbit first."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, []string{"Exit"}, 0)
	return centreBlock(m.width, m.height, b.String())
}

func (m UpdateModel) viewConfirm() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.AccentText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Pull the latest Orbit and update this deployment"))
	fmt.Fprintln(&b)

	identity := "no deployment details found"
	if m.deployment != nil && m.deployment.AppURL != "" {
		identity = displayHost(m.deployment.AppURL) + " · installed " + m.deployment.InstalledAt.Format("2006-01-02")
	}
	fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(style.Text).Render(identity))
	if image := m.deploymentImage(); image != "" {
		fmt.Fprintln(&b, style.Tagline.Render(image))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("The update runs inside the mission console, right here."))
	fmt.Fprintln(&b, style.MutedText.Render("Your existing configuration is preserved. Nothing is deleted."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, []string{"Update Orbit", "Cancel"}, m.confirmSel)
	return centreBlock(m.width, m.height, b.String())
}

// deploymentImage is the deployment's image reference without its
// digest pin — the digest is engine bookkeeping, not something a
// person scans a confirmation screen for.
func (m UpdateModel) deploymentImage() string {
	if m.deployment == nil || m.deployment.Image == "" {
		return ""
	}
	image, _, _ := strings.Cut(m.deployment.Image, "@")
	return image
}
