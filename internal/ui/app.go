package ui

import (
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

type appState int

const (
	appStateSplash appState = iota
	appStateRemove
	appStateRepair
	appStateInstall
	appStateUpdate
)

// AppModel is the root model: it starts at the splash screen and, once a
// choice is made, hands control to that flow. Install (Standard profile
// only), Update, Remove and Repair (a deliberate non-mutating stub) are
// all wired to real flows.
type AppModel struct {
	width, height int
	state         appState
	splash        SplashModel
	remove        RemoveModel
	repair        RepairModel
	install       InstallModel
	update        UpdateModel

	// targetDir is where an existing deployment, if any, would be found.
	// Overridable in tests; production code leaves it empty and gets the
	// working directory.
	targetDir string
}

// NewAppModel constructs the root application model, starting at the
// splash screen.
func NewAppModel() AppModel {
	return AppModel{splash: NewSplashModel(), state: appStateSplash}
}

// NewAppModelNoAnimation constructs the root application model with the
// splash screen's starfield frozen — see NewSplashModelNoAnimation.
func NewAppModelNoAnimation() AppModel {
	return AppModel{splash: NewSplashModelNoAnimation(), state: appStateSplash}
}

func (m AppModel) resolvedTargetDir() string {
	if m.targetDir != "" {
		return m.targetDir
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// Init implements tea.Model.
func (m AppModel) Init() tea.Cmd { return m.splash.Init() }

// Update implements tea.Model.
func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if resized, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = resized.Width, resized.Height
	}

	switch m.state {
	case appStateSplash:
		return m.updateSplash(msg)
	case appStateRemove:
		updated, cmd := m.remove.Update(msg)
		m.remove = updated.(RemoveModel)
		return m, cmd
	case appStateRepair:
		updated, cmd := m.repair.Update(msg)
		m.repair = updated.(RepairModel)
		return m, cmd
	case appStateInstall:
		updated, cmd := m.install.Update(msg)
		m.install = updated.(InstallModel)
		return m, cmd
	case appStateUpdate:
		updated, cmd := m.update.Update(msg)
		m.update = updated.(UpdateModel)
		return m, cmd
	}
	return m, nil
}

func (m AppModel) updateSplash(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.splash.Update(msg)
	m.splash = updated.(SplashModel)

	if !m.splash.quitting || m.splash.Chosen == "" {
		return m, cmd
	}

	sizeCmd := func() tea.Msg { return tea.WindowSizeMsg{Width: m.width, Height: m.height} }

	switch m.splash.Chosen {
	case "Remove":
		// deploy.Detect's error return only ever reflects a real I/O
		// failure reading an existing .env-orbit, never "not installed"
		// (that's a nil Deployment, nil error) — the Remove confirm
		// screen already renders sensibly for a nil Deployment, so
		// there's nothing actionable to do with an error here beyond
		// proceeding with what we have.
		deployment, _ := deploy.Detect(m.resolvedTargetDir())
		m.remove = NewRemoveModel(deployment)
		m.state = appStateRemove
		return m, sizeCmd
	case "Repair":
		m.repair = NewRepairModel()
		m.state = appStateRepair
		return m, sizeCmd
	case "Install":
		m.install = NewInstallModel(m.resolvedTargetDir())
		m.state = appStateInstall
		return m, sizeCmd
	case "Update":
		deployment, _ := deploy.Detect(m.resolvedTargetDir())
		m.update = NewUpdateModel(deployment)
		m.state = appStateUpdate
		return m, sizeCmd
	default:
		return m, tea.Quit
	}
}

// View implements tea.Model.
func (m AppModel) View() string {
	switch m.state {
	case appStateSplash:
		return m.splash.View()
	case appStateRemove:
		return m.remove.View()
	case appStateRepair:
		return m.repair.View()
	case appStateInstall:
		return m.install.View()
	case appStateUpdate:
		return m.update.View()
	}
	return ""
}
