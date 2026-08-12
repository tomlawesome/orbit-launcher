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
// orbit-launcher still never invents Orbit configuration itself
// (issue #51): the engine run cannot prompt by construction, and when
// the engine refuses with its configuration-required signal, the flow
// runs configure.sh's own guided setup — the single source of truth
// for what fields it needs and what answers are valid — in-console
// over the machine prompt protocol when the engine speaks it
// (orbit#297), or via the terminal handoff when it doesn't.
type InstallModel struct {
	width, height int
	state         installState
	targetDir     string
	version       string

	profileSel int // 0 = Standard, 1 = AI, 2 = Full
	confirmSel int // 0 = Install now, 1 = Back

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
	prepareConfig  prepareConfigFunc
	startConfig    startConfigFunc
	adoptConfig    adoptConfigFunc
	prepareRepair  prepareRepairFunc
}

func (r engineRun) withSeams(s engineRunSeams) engineRun {
	r.prepareEngine = s.prepareEngine
	r.prepareInstall = s.prepareInstall
	r.runHandoff = s.runHandoff
	r.detect = s.detect
	r.now = s.now
	r.prepareConfig = s.prepareConfig
	r.startConfig = s.startConfig
	r.adoptConfig = s.adoptConfig
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
		// The only way out of the honest dead end is back to the
		// choice that led in.
		if msg.Type == tea.KeyEnter || msg.Type == tea.KeyEsc {
			m.state = installStateProfile
		}
		return m, nil
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
		m.confirmSel = 0
		return m, nil
	}
	return m, nil
}

func (m InstallModel) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.state = installStateProfile
		return m, nil
	case tea.KeyUp, tea.KeyDown:
		m.confirmSel = 1 - m.confirmSel
		return m, nil
	case tea.KeyEnter:
		if m.confirmSel == 1 {
			m.state = installStateProfile
			return m, nil
		}
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

// The flow screens speak the same starchart grammar as every other
// screen (design/DECISIONS.md): centred block, ⟡ mark and bold title,
// muted prose, individually-centred stacked menu, no keybind hints.

func (m InstallModel) viewProfile() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.AccentText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Choose a deployment profile"))
	fmt.Fprintln(&b)

	profiles := []struct{ name, desc string }{
		{"Standard", "mail, documents, calendar"},
		{"AI", "adds a local model for search & suggestions"},
		{"Full", "AI features plus every optional service"},
	}
	for i, p := range profiles {
		fmt.Fprintln(&b, menuRow(p.name+"  ·  "+p.desc, i == m.profileSel))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("only Standard is available so far"))
	return centreBlock(m.width, m.height, b.String())
}

func (m InstallModel) viewUnavailableProfile() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("This profile isn't available yet"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("AI and Full need local-model configuration that"))
	fmt.Fprintln(&b, style.MutedText.Render("hasn't been built yet."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, []string{"Back"}, 0)
	return centreBlock(m.width, m.height, b.String())
}

func (m InstallModel) viewConfirm() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.AccentText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Ready to install"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("Orbit's own installer runs inside the mission console —"))
	fmt.Fprintln(&b, style.MutedText.Render("watch it validate, pull the image, and start the containers"))
	fmt.Fprintln(&b, style.MutedText.Render("right here. If it needs configuration only you can provide,"))
	fmt.Fprintln(&b, style.MutedText.Render("it stops safely and asks first."))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render(m.targetDir))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, []string{"Install now", "Back"}, m.confirmSel)
	return centreBlock(m.width, m.height, b.String())
}
