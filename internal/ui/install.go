package ui

import (
	"context"
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type installState int

const (
	installStateProfile installState = iota
	installStateUnavailableProfile
	installStateConfig
	installStateReview
	installStateProgress
	installStateDone
	installStateFailed
)

const (
	fieldAppURL = iota
	fieldOIDCIssuer
	fieldOIDCClientID
	fieldOIDCClientSecret
	fieldCount
)

var installProgressLinesShown = 10 // last N lines kept on the progress screen

// installEvent is one item from the channel connecting Install's
// goroutine (running the real, blocking install.sh process) to the
// bubbletea event loop, which only ever runs on its own goroutine.
// Either Line is set (a streamed output line) or Done is true (the
// terminal event, Err nil on success).
type installEvent struct {
	Line string
	Done bool
	Err  error
}

// installEventMsg wraps an installEvent as a tea.Msg.
type installEventMsg installEvent

// waitForInstallEvent returns a tea.Cmd that blocks for exactly one event
// from ch. Update() re-issues this after every non-terminal event, so the
// program keeps listening — the standard bubbletea pattern for draining
// an external channel without polling.
func waitForInstallEvent(ch <-chan installEvent) tea.Cmd {
	return func() tea.Msg {
		return installEventMsg(<-ch)
	}
}

// startInstall runs writeConfig then install in a background goroutine,
// emitting one installEvent per output line plus a final Done event, and
// returns the channel to listen on.
func startInstall(
	targetDir string,
	cfg deploy.Config,
	writeConfig func(string, deploy.Config) error,
	install func(context.Context, string, func(string)) error,
) <-chan installEvent {
	ch := make(chan installEvent, 32)
	go func() {
		defer close(ch)
		if err := writeConfig(targetDir, cfg); err != nil {
			ch <- installEvent{Done: true, Err: err}
			return
		}
		err := install(context.Background(), targetDir, func(line string) {
			ch <- installEvent{Line: line}
		})
		ch <- installEvent{Done: true, Err: err}
	}()
	return ch
}

// InstallModel is the Install flow: profile, guided configuration, final
// review, live progress, completion/failure — see
// design/mockups.html sections 03-08 and docs/implementation-plan.md
// section 5 Wave 2. Only the Standard profile is wired to
// internal/deploy; AI/Full are visible but honestly say they aren't
// available yet rather than silently doing the wrong thing.
type InstallModel struct {
	width, height int
	state         installState
	targetDir     string

	profileSel int // 0 = Standard, 1 = AI, 2 = Full

	inputs   []textinput.Model
	focusIdx int

	lines      []string
	installErr error
	events     <-chan installEvent

	// Overridable in tests so they don't need real network/Docker access.
	writeConfig func(targetDir string, cfg deploy.Config) error
	install     func(ctx context.Context, targetDir string, onLine func(string)) error
}

// NewInstallModel constructs the Install flow for targetDir.
func NewInstallModel(targetDir string) InstallModel {
	labels := []string{"Public address (e.g. https://mail.example.com)", "OIDC issuer URL", "OIDC client ID", "OIDC client secret"}
	inputs := make([]textinput.Model, fieldCount)
	for i, label := range labels {
		ti := textinput.New()
		ti.Placeholder = label
		ti.CharLimit = 256
		ti.Width = 50
		if i == fieldOIDCClientSecret {
			ti.EchoMode = textinput.EchoPassword
			ti.EchoCharacter = '●'
		}
		inputs[i] = ti
	}
	inputs[0].Focus()

	return InstallModel{
		targetDir:   targetDir,
		inputs:      inputs,
		writeConfig: deploy.WriteConfig,
		install:     deploy.Install,
	}
}

func (m InstallModel) config() deploy.Config {
	return deploy.Config{
		AppURL:           strings.TrimSpace(m.inputs[fieldAppURL].Value()),
		OIDCIssuer:       strings.TrimSpace(m.inputs[fieldOIDCIssuer].Value()),
		OIDCClientID:     strings.TrimSpace(m.inputs[fieldOIDCClientID].Value()),
		OIDCClientSecret: m.inputs[fieldOIDCClientSecret].Value(),
	}
}

func (m InstallModel) configComplete() bool {
	c := m.config()
	return c.AppURL != "" && c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCClientSecret != ""
}

// Init implements tea.Model.
func (m InstallModel) Init() tea.Cmd { return textinput.Blink }

// Update implements tea.Model.
func (m InstallModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case installEventMsg:
		if msg.Done {
			if msg.Err != nil {
				m.installErr = msg.Err
				m.state = installStateFailed
			} else {
				m.state = installStateDone
			}
			return m, nil
		}
		m.lines = append(m.lines, msg.Line)
		if len(m.lines) > installProgressLinesShown {
			m.lines = m.lines[len(m.lines)-installProgressLinesShown:]
		}
		return m, waitForInstallEvent(m.events)

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
	case installStateConfig:
		return m.handleConfigKey(msg)
	case installStateReview:
		return m.handleReviewKey(msg)
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
		m.state = installStateConfig
		return m, textinput.Blink
	}
	return m, nil
}

func (m InstallModel) handleConfigKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.state = installStateProfile
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		return m.focusField(m.focusIdx + 1)
	case tea.KeyShiftTab, tea.KeyUp:
		return m.focusField(m.focusIdx - 1)
	case tea.KeyEnter:
		if m.focusIdx < fieldCount-1 {
			return m.focusField(m.focusIdx + 1)
		}
		if m.configComplete() {
			m.state = installStateReview
			return m, nil
		}
		return m, nil
	}

	updated, cmd := m.inputs[m.focusIdx].Update(msg)
	m.inputs[m.focusIdx] = updated
	return m, cmd
}

func (m InstallModel) focusField(idx int) (tea.Model, tea.Cmd) {
	idx = ((idx % fieldCount) + fieldCount) % fieldCount
	m.inputs[m.focusIdx].Blur()
	m.focusIdx = idx
	cmd := m.inputs[m.focusIdx].Focus()
	return m, cmd
}

func (m InstallModel) handleReviewKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.state = installStateConfig
		return m, nil
	case tea.KeyEnter:
		m.state = installStateProgress
		m.events = startInstall(m.targetDir, m.config(), m.writeConfig, m.install)
		return m, waitForInstallEvent(m.events)
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
	case installStateConfig:
		return m.viewConfig()
	case installStateReview:
		return m.viewReview()
	case installStateProgress:
		return m.viewProgress()
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

func (m InstallModel) viewConfig() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.MenuSelected.Render("ORBIT · Install"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Render("Core configuration"))
	fmt.Fprintln(&b)
	for _, input := range m.inputs {
		fmt.Fprintln(&b, input.View())
		fmt.Fprintln(&b)
	}
	if !m.configComplete() {
		fmt.Fprintln(&b, style.Tagline.Render("All four fields are required to continue."))
	}
	fmt.Fprintln(&b, style.Tagline.Render("⇥ next field · ↵ continue · esc back"))
	return m.frame(b.String())
}

func (m InstallModel) viewReview() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.MenuSelected.Render("ORBIT · Install"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Render("Review before install"))
	fmt.Fprintln(&b)
	cfg := m.config()
	fmt.Fprintf(&b, "  %s      Standard\n", style.MenuUnselected.Render("Profile"))
	fmt.Fprintf(&b, "  %s      %s\n", style.MenuUnselected.Render("Address"), cfg.AppURL)
	fmt.Fprintf(&b, "  %s         %s\n", style.MenuUnselected.Render("Auth"), cfg.OIDCIssuer)
	fmt.Fprintf(&b, "  %s   %s\n", style.MenuUnselected.Render("Client ID"), cfg.OIDCClientID)
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("Nothing has been installed yet. This is the last chance to change"))
	fmt.Fprintln(&b, style.Tagline.Render("anything before Orbit touches the target."))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MenuCaret.Render(style.SymbolSelected)+" "+style.MenuSelected.Render("Install now"))
	fmt.Fprintln(&b, style.Tagline.Render("esc back"))
	return m.frame(b.String())
}

func (m InstallModel) viewProgress() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.MenuSelected.Render("ORBIT · Installing"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.WarmText.Render("⠋")+" Running install.sh…")
	fmt.Fprintln(&b)
	for _, line := range m.lines {
		fmt.Fprintln(&b, style.Tagline.Render(line))
	}
	return m.frame(b.String())
}

func (m InstallModel) viewDone() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.SuccessText.Render(style.SymbolSuccess)+" "+lipgloss.NewStyle().Bold(true).Render("Orbit is ready"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.AccentText.Render(m.config().AppURL))
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
	for _, line := range m.lines {
		fmt.Fprintln(&b, style.Tagline.Render(line))
	}
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, "  "+style.MenuUnselected.Render("Exit"))
	return m.frame(b.String())
}
