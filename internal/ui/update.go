package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
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
	star          starfield.Model
	targetDir     string
	version       string
	deployment    *deploy.Deployment
	state         updateState
	confirm       menu // updateConfirmRows

	// left is Esc, Cancel or Menu on the flow's first screen: back to the
	// main menu with nothing started (#210).
	left bool

	// installedAt and lookupInstalledAt: see RemoveModel.
	installedAt       time.Time
	lookupInstalledAt func(context.Context, *deploy.Deployment) time.Time

	run engineRun

	seams engineRunSeams

	// send is the run's way back into the event loop, for the engine
	// stream reader; set by AppModel from the program's sender.
	send func(tea.Msg)
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
	return UpdateModel{deployment: d, targetDir: targetDir, version: version, state: state, confirm: newMenu(updateConfirmRows...)}
}

var updateConfirmRows = []string{"Update Orbit", "Cancel"}

// deadEndRows are a dead end's one row, Update's and Remove's not-found
// screens: back to the main menu (#228).
var deadEndRows = []string{"Menu"}

// Outcome surfaces the flow's result to AppModel: back to the menu when
// the person left on the first screen, otherwise the engine run's.
func (m UpdateModel) Outcome() flowOutcome {
	if m.left {
		return backToMenu
	}
	return outcomeOf(m.run)
}

// Init implements tea.Model. On the confirm screen it starts the
// read-only install-date lookup, as RemoveModel.Init does.
func (m UpdateModel) Init() tea.Cmd {
	if m.state != updateStateConfirm {
		return nil
	}
	return installedAtCmd(m.lookupInstalledAt, m.deployment)
}

// Update implements tea.Model.
func (m UpdateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// See InstallModel.Update: neither message is consumed here, a resize
	// arms no tick chain, and the engine run re-arms the one chain while
	// it owns the screen.
	if resized, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = resized.Width, resized.Height
		m.star = newSky(resized.Width, resized.Height)
	}
	if _, ok := msg.(tickMsg); ok {
		m.star = m.star.Advance()
		if m.state != updateStateRunning {
			return m, tick()
		}
	}

	// The lookup may answer after the run has started; it is this
	// model's message, never the engine run's.
	if at, ok := msg.(installedAtMsg); ok {
		m.installedAt = at.at
		return m, nil
	}

	if m.state == updateStateRunning {
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
		return m, cmd
	}

	if key, ok := msg.(tea.KeyPressMsg); ok {
		return m.handleKey(key)
	}
	return m, nil
}

func (m UpdateModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlC(msg) {
		return m, tea.Quit
	}
	switch m.state {
	case updateStateNotFound:
		// The dead end: its one row and Esc both go back to the menu,
		// and no other key does anything (#228).
		if _, chosen, back := newMenu(deadEndRows...).onScreen(msg); back || chosen == "Menu" {
			m.left = true
		}
		return m, nil
	case updateStateConfirm:
		return m.handleConfirmKey(msg)
	}
	return m, nil
}

func (m UpdateModel) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.confirm, chosen, back = m.confirm.onScreen(msg)
	switch {
	case back, chosen == "Cancel":
		m.left = true
	case chosen == "Update Orbit":
		m.state = updateStateRunning
		title := "Update"
		if m.deployment != nil {
			title = "Update — " + deploymentIdentity(m.deployment)
		}
		m.run = newEngineRun("update", m.targetDir, title, m.version).withSeams(m.seams).withSend(m.send)
		var cmd tea.Cmd
		m.run, cmd = m.run.start(m.width, m.height)
		return m, cmd
	}
	return m, nil
}

// View implements tea.Model.
func (m UpdateModel) View() tea.View { return tea.NewView(m.view()) }

// view renders the screen's content.
func (m UpdateModel) view() string {
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
	return viewNoDeployment(m.star, m.width, m.height, m.targetDir,
		"There's nothing to update in this directory —",
		"Install is the way to get into Orbit first.")
}

func (m UpdateModel) viewConfirm() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), "Pull the latest Orbit and update this deployment")

	fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(style.Text).Render(identityLine(m.deployment, m.installedAt)))
	if image := m.deploymentImage(); image != "" {
		fmt.Fprintln(&b, style.Tagline.Render(image))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("The update runs inside the mission console, right here."))
	fmt.Fprintln(&b, style.MutedText.Render("Your existing configuration is preserved. Nothing is deleted."))
	fmt.Fprintln(&b)
	m.confirm.write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
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
