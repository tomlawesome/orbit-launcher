package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type installState int

const (
	installStateProfile installState = iota
	installStateUnavailableProfile
	installStateConfirm
	installStateRunning
)

// InstallModel is the Install flow: profile choice, confirmation, then
// the mission console — the engine's event stream rendered natively
// inside the TUI (see internal/ui/enginerun.go and design/mockups-v5.html
// section 02). Only the Standard profile is wired; AI/Full are visible
// but honestly say they aren't available yet rather than silently doing
// the wrong thing.
//
// orbit-launcher still never collects or writes Orbit's configuration
// itself (issue #51): the engine run cannot prompt by construction, and
// when the engine refuses with its configuration-required signal, the
// flow hands the real terminal to install.sh's own guided setup — the
// single source of truth for what fields it needs — exactly as before.
type InstallModel struct {
	width, height int
	state         installState
	targetDir     string
	version       string

	profileSel int // 0 = Standard, 1 = AI, 2 = Full

	run engineRun

	// Test seams, copied into the engine run at start; nil means real.
	seams engineRunSeams
}

// engineRunSeams bundles the overridable dependencies tests inject.
type engineRunSeams struct {
	prepareEngine  prepareEngineFunc
	prepareInstall prepareInstallFunc
	runHandoff     runHandoffFunc
	detect         detectFunc
	now            nowFunc
}

func (r engineRun) withSeams(s engineRunSeams) engineRun {
	r.prepareEngine = s.prepareEngine
	r.prepareInstall = s.prepareInstall
	r.runHandoff = s.runHandoff
	r.detect = s.detect
	r.now = s.now
	return r
}

// NewInstallModel constructs the Install flow for targetDir.
func NewInstallModel(targetDir, version string) InstallModel {
	return InstallModel{targetDir: targetDir, version: version}
}

// Done, Succeeded, WantsMenu, SuccessURL and SuccessElapsed surface the
// engine run's outcome to AppModel.
func (m InstallModel) Outcome() flowOutcome { return outcomeOf(m.run) }

// Init implements tea.Model.
func (m InstallModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m InstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if resized, ok := msg.(tea.WindowSizeMsg); ok {
		m.width, m.height = resized.Width, resized.Height
	}

	if m.state == installStateRunning {
		var cmd tea.Cmd
		m.run, cmd = m.run.update(msg)
		return m, cmd
	}

	if key, ok := msg.(tea.KeyMsg); ok {
		return m.handleKey(key)
	}
	return m, nil
}

func (m InstallModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	switch m.state {
	case installStateProfile:
		return m.handleProfileKey(msg)
	case installStateUnavailableProfile:
		return m, tea.Quit
	case installStateConfirm:
		return m.handleConfirmKey(msg)
	}
	return m, nil
}

func (m InstallModel) handleProfileKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyUp:
		m.profileSel = (m.profileSel - 1 + 3) % 3
		return m, nil
	case tea.KeyDown:
		m.profileSel = (m.profileSel + 1) % 3
		return m, nil
	case tea.KeyEnter:
		if m.profileSel != 0 {
			m.state = installStateUnavailableProfile
			return m, nil
		}
		m.state = installStateConfirm
		return m, nil
	}
	return m, nil
}

func (m InstallModel) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.state = installStateProfile
		return m, nil
	case tea.KeyEnter:
		m.state = installStateRunning
		m.run = newEngineRun("install", m.targetDir, "Install — Standard", m.version).withSeams(m.seams)
		var cmd tea.Cmd
		m.run, cmd = m.run.start(m.width, m.height)
		return m, cmd
	}
	return m, nil
}

// View implements tea.Model.
func (m InstallModel) View() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case installStateProfile:
		return m.viewProfile()
	case installStateUnavailableProfile:
		return m.viewUnavailableProfile()
	case installStateConfirm:
		return m.viewConfirm()
	case installStateRunning:
		return m.run.view(m.width, m.height)
	}
	return ""
}

func (m InstallModel) frame(body string) string {
	return lipgloss.NewStyle().Padding(1, 2).Render(body)
}

func (m InstallModel) viewProfile() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.MenuSelected.Render("ORBIT · Install"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Render("Choose a deployment profile"))
	fmt.Fprintln(&b)

	profiles := []struct{ name, desc string }{
		{"Standard", "Mail, documents, calendar"},
		{"AI", "Adds a local model for search & suggestions"},
		{"Full", "AI features plus every optional service"},
	}
	for i, p := range profiles {
		if i == m.profileSel {
			fmt.Fprintln(&b, style.MenuCaret.Render(style.SymbolSelected)+" "+style.MenuSelected.Render(p.name)+"  "+style.Tagline.Render(p.desc))
		} else {
			fmt.Fprintln(&b, "  "+style.MenuUnselected.Render(p.name)+"  "+style.Tagline.Render(p.desc))
		}
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("Only Standard is wired up so far — AI and Full are visible but not yet available."))
	return m.frame(b.String())
}

func (m InstallModel) viewUnavailableProfile() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+lipgloss.NewStyle().Bold(true).Render("This profile isn't available yet"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "AI and Full profiles need local-model configuration that hasn't")
	fmt.Fprintln(&b, "been built yet. Only Standard is wired up so far.")
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  "+style.MenuUnselected.Render("Exit"))
	return m.frame(b.String())
}

func (m InstallModel) viewConfirm() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.MenuSelected.Render("ORBIT · Install"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Render("Ready to install"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "Orbit's own installer runs inside the mission console — you'll")
	fmt.Fprintln(&b, "watch it validate, pull the image and start the containers right")
	fmt.Fprintln(&b, "here. If it needs configuration only you can provide, it stops")
	fmt.Fprintln(&b, "safely and hands you to its guided setup first.")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "  %s  %s\n", style.MenuUnselected.Render("Target"), m.targetDir)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MenuCaret.Render(style.SymbolSelected)+" "+style.MenuSelected.Render("Install now"))
	fmt.Fprintln(&b, style.Tagline.Render("esc back"))
	return m.frame(b.String())
}
