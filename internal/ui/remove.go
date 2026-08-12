package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type removeState int

const (
	removeStateConfirm removeState = iota
	removeStateStandingDown
	removeStateDone
	removeStateFailed
	removeStateCancelled
)

// standDownResultMsg carries the outcome of the async StandDown call back
// into the bubbletea event loop.
type standDownResultMsg struct{ err error }

// RemoveModel is the Remove flow: confirm, stand down (automated,
// reversible), then an exact copy-pasteable, irreversible removal
// command that this program never runs itself — see
// design/mockups.html sections 09-11 and
// internal/deploy/removal_property_test.go for the enforced half of that
// promise.
type RemoveModel struct {
	width, height int
	deployment    *deploy.Deployment
	state         removeState
	confirmSel    int // 0 = Stand down Orbit, 1 = Cancel
	doneSel       int // 0 = Copy command, 1 = Exit
	standDownErr  error
	copied        bool

	// standDown is overridable in tests so they don't need a real Docker
	// daemon — production code always leaves this nil and gets
	// deploy.StandDown.
	standDown func(context.Context, string) error
}

// NewRemoveModel constructs the Remove flow for a detected deployment.
func NewRemoveModel(d *deploy.Deployment) RemoveModel {
	return RemoveModel{deployment: d}
}

func (m RemoveModel) standDownFunc() func(context.Context, string) error {
	if m.standDown != nil {
		return m.standDown
	}
	return deploy.StandDown
}

// Init implements tea.Model.
func (m RemoveModel) Init() tea.Cmd { return nil }

// Update implements tea.Model.
func (m RemoveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case standDownResultMsg:
		if msg.err != nil {
			m.standDownErr = msg.err
			m.state = removeStateFailed
		} else {
			m.state = removeStateDone
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m RemoveModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		m.state = removeStateCancelled
		return m, tea.Quit
	}

	switch m.state {
	case removeStateConfirm:
		return m.handleConfirmKey(msg)
	case removeStateDone, removeStateFailed:
		return m.handleDoneKey(msg)
	}
	return m, nil
}

func (m RemoveModel) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.state = removeStateCancelled
		return m, tea.Quit
	case tea.KeyUp, tea.KeyDown:
		m.confirmSel = 1 - m.confirmSel
		return m, nil
	case tea.KeyEnter:
		if m.confirmSel == 1 {
			m.state = removeStateCancelled
			return m, tea.Quit
		}
		m.state = removeStateStandingDown
		targetDir := ""
		if m.deployment != nil {
			targetDir = m.deployment.TargetDir
		}
		standDown := m.standDownFunc()
		return m, func() tea.Msg {
			return standDownResultMsg{err: standDown(context.Background(), targetDir)}
		}
	}
	return m, nil
}

func (m RemoveModel) handleDoneKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		return m, tea.Quit
	case tea.KeyEnter:
		if m.state == removeStateDone && m.doneSel == 0 {
			m.copied = true
			return m, copyToClipboard(deploy.RemovalCommand(targetDirOrPlaceholder(m.deployment)))
		}
		return m, tea.Quit
	case tea.KeyUp, tea.KeyDown:
		if m.state == removeStateDone {
			m.doneSel = 1 - m.doneSel
		}
		return m, nil
	}
	return m, nil
}

// copyToClipboard writes an OSC 52 escape sequence, which modern
// terminals (including over SSH) interpret as a clipboard-set request.
// Terminals that don't support it simply ignore the sequence. Written
// directly to stdout rather than via tea.Printf, which is documented as a
// no-op under the alt screen — the mode this program always runs in.
func copyToClipboard(text string) tea.Cmd {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return func() tea.Msg {
		fmt.Fprintf(os.Stdout, "\x1b]52;c;%s\x07", encoded)
		return nil
	}
}

// View implements tea.Model.
func (m RemoveModel) View() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case removeStateConfirm:
		return m.viewConfirm()
	case removeStateStandingDown:
		return m.viewStandingDown()
	case removeStateDone:
		return m.viewDone()
	case removeStateFailed:
		return m.viewFailed()
	default:
		return ""
	}
}

// The flow screens speak the same starchart grammar as every other
// screen (design/DECISIONS.md): centred block, ⟡ mark and bold title,
// muted prose, individually-centred stacked menu, no keybind hints.

func (m RemoveModel) viewConfirm() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.AccentText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("This stops Orbit and removes its containers"))
	fmt.Fprintln(&b)

	identity := "no deployment details found"
	if m.deployment != nil && m.deployment.AppURL != "" {
		identity = displayHost(m.deployment.AppURL) + " · installed " + m.deployment.InstalledAt.Format("2006-01-02")
	}
	fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(style.Text).Render(identity))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("Your mail, documents, and configuration are not deleted"))
	fmt.Fprintln(&b, style.MutedText.Render("by this step — they stay on disk, and the next screen"))
	fmt.Fprintln(&b, style.MutedText.Render("shows exactly how to remove them if you choose to."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, []string{"Stand down Orbit", "Cancel"}, m.confirmSel)
	return centreBlock(m.width, m.height, b.String())
}

func (m RemoveModel) viewStandingDown() string {
	return centreBlock(m.width, m.height, style.WarmText.Render("⠋")+" "+style.MutedText.Render("standing down Orbit…"))
}

func (m RemoveModel) viewDone() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.SuccessText.Render(style.SymbolSuccess)+" "+lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Orbit has been stood down"))
	fmt.Fprintln(&b)

	targetDir := "the deployment directory"
	if m.deployment != nil && m.deployment.TargetDir != "" {
		targetDir = m.deployment.TargetDir
	}
	fmt.Fprintln(&b, style.MutedText.Render("Containers and networks are stopped. Your files and data"))
	fmt.Fprintln(&b, style.MutedText.Render("volumes are still on disk at "+targetDir+" —"))
	fmt.Fprintln(&b, style.MutedText.Render("nothing has been deleted."))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("to fully remove Orbit from this machine, copy and run"))

	cmd := deploy.RemovalCommand(targetDirOrPlaceholder(m.deployment))
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(style.BorderSoft).Padding(0, 1).Render(cmd)
	fmt.Fprintln(&b, box)
	fmt.Fprintln(&b, style.DegradedText.Render("this deletes all mail, documents, and configuration — it cannot be undone"))
	fmt.Fprintln(&b)

	copyLabel := "Copy command"
	if m.copied {
		copyLabel = "Copy command · copied"
	}
	writeStackedMenu(&b, []string{copyLabel, "Exit"}, m.doneSel)
	return centreBlock(m.width, m.height, b.String())
}

func targetDirOrPlaceholder(d *deploy.Deployment) string {
	if d != nil && d.TargetDir != "" {
		return d.TargetDir
	}
	return "/opt/orbit"
}

func (m RemoveModel) viewFailed() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Could not stand down Orbit"))
	fmt.Fprintln(&b)
	if m.standDownErr != nil {
		fmt.Fprintln(&b, style.Tagline.Render(m.standDownErr.Error()))
		fmt.Fprintln(&b)
	}
	writeStackedMenu(&b, []string{"Exit"}, 0)
	return centreBlock(m.width, m.height, b.String())
}
