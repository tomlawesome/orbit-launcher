package ui

import (
	"context"
	"fmt"
	"os/exec"
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
	installStateDone
	installStateFailed
)

// InstallModel is the Install flow: profile choice, then a full
// handoff of the real terminal to Orbit's own install.sh — see
// design/mockups.html sections 03-08 and docs/implementation-plan.md
// section 5 Wave 2. Only the Standard profile is wired to
// internal/deploy; AI/Full are visible but honestly say they aren't
// available yet rather than silently doing the wrong thing.
//
// orbit-launcher deliberately never collects or writes Orbit's
// configuration itself: install.sh's own scripts/configure.sh is the
// single source of truth for what fields it needs and how to collect
// them (including the OIDC client secret, which configure.sh only
// ever reads from a real controlling terminal, by design). Requiring
// this program to reimplement that field list would tie its
// correctness to orbit's config schema, needing a recompile of
// orbit-launcher every time that schema changes — see issue #51.
type InstallModel struct {
	width, height int
	state         installState
	targetDir     string

	profileSel int // 0 = Standard, 1 = AI, 2 = Full

	installErr error
	cleanup    func() error

	// Overridable in tests so they don't need real network/terminal access.
	prepareInstall func(ctx context.Context, targetDir string) (*exec.Cmd, func() error, error)
	runHandoff     func(cmd *exec.Cmd) tea.Cmd
}

// NewInstallModel constructs the Install flow for targetDir.
func NewInstallModel(targetDir string) InstallModel {
	return InstallModel{
		targetDir:      targetDir,
		prepareInstall: defaultPrepareInstall,
		runHandoff:     defaultRunHandoff,
	}
}

// Init implements tea.Model.
func (m InstallModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m InstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case installPreparedMsg:
		if msg.err != nil {
			m.installErr = msg.err
			m.state = installStateFailed
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
			m.installErr = msg.err
			m.state = installStateFailed
		} else {
			m.state = installStateDone
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
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
	case installStateDone, installStateFailed:
		return m, tea.Quit
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
		return m, prepareInstallCmd(m.prepareInstall, m.targetDir)
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
		return m.viewRunning()
	case installStateDone:
		return m.viewDone()
	case installStateFailed:
		return m.viewFailed()
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
	fmt.Fprintln(&b, "This hands control of your terminal to Orbit's own installer")
	fmt.Fprintln(&b, "(install.sh) — it will guide you through any configuration it")
	fmt.Fprintln(&b, "needs, pull the image, and start the containers. You'll return")
	fmt.Fprintln(&b, "here automatically once it finishes.")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "  %s  %s\n", style.MenuUnselected.Render("Target"), m.targetDir)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MenuCaret.Render(style.SymbolSelected)+" "+style.MenuSelected.Render("Install now"))
	fmt.Fprintln(&b, style.Tagline.Render("esc back"))
	return m.frame(b.String())
}

func (m InstallModel) viewRunning() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.MenuSelected.Render("ORBIT · Install"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.WarmText.Render("⠋")+" Handing off to install.sh…")
	return m.frame(b.String())
}

func (m InstallModel) viewDone() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.SuccessText.Render(style.SymbolSuccess)+" "+lipgloss.NewStyle().Bold(true).Render("Orbit is ready"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.AccentText.Render(m.targetDir))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  "+style.MenuUnselected.Render("Exit"))
	return m.frame(b.String())
}

func (m InstallModel) viewFailed() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+lipgloss.NewStyle().Bold(true).Render("Installation stopped"))
	fmt.Fprintln(&b)
	if m.installErr != nil {
		fmt.Fprintln(&b, style.Tagline.Render(m.installErr.Error()))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  "+style.MenuUnselected.Render("Exit"))
	return m.frame(b.String())
}
