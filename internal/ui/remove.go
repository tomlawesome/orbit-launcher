package ui

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

type removeState int

const (
	removeStateConfirm removeState = iota
	removeStateStandingDown
	removeStateDone
	removeStateFailed
	removeStateCancelled
	removeStateNotFound
)

// standDownResultMsg carries the outcome of the async StandDown call back
// into the bubbletea event loop.
type standDownResultMsg struct{ err error }

// RemoveModel is the Remove flow: confirm, stand down (automated,
// reversible), then an exact copy-pasteable, irreversible removal
// command that this program never runs itself — see
// design/mockups.html sections 09-11 and
// internal/deploy/removal_property_test.go for the enforced half of that
// promise. With no deployment detected it stops at the start: there is
// nothing to stand down, and a removal command only ever names a
// directory the launcher actually found (#205).
type RemoveModel struct {
	width, height int
	star          starfield.Model
	deployment    *deploy.Deployment
	state         removeState
	confirm       menu // removeConfirmRows
	after         menu // removeDoneRows or removeFailedRows, once the stand-down answers
	standDownErr  error
	copied        bool

	// left is going back to the main menu: Esc, Cancel or Menu on the
	// flow's first screen, with nothing stood down (#210), or Esc or Menu
	// on the removed and failed screens after the stand-down (#225).
	left bool

	// targetDir is where AppModel looked; the no-deployment screen names
	// it. With a deployment, every command uses deployment.TargetDir.
	targetDir string

	// standDown is overridable in tests so they don't need a real Docker
	// daemon — production code always leaves this nil and gets
	// deploy.StandDown.
	standDown func(context.Context, string) error

	// installedAt is when Docker created the deployment's database
	// volume, zero until the lookup Init starts answers (or if it
	// cannot). lookupInstalledAt is the same kind of seam as standDown:
	// nil in production, which means deploy.InstalledAt.
	installedAt       time.Time
	lookupInstalledAt func(context.Context, *deploy.Deployment) time.Time

	// clipboard is where the OSC 52 copy sequence is written: the same kind
	// of seam as standDown, nil in production, which means os.Stdout. Tests
	// set it so they neither write to the real terminal nor go unchecked.
	clipboard io.Writer
}

// NewRemoveModel constructs the Remove flow for a detected deployment. A
// nil deployment means nothing was found, and the flow says so and
// offers only the way back.
func NewRemoveModel(d *deploy.Deployment) RemoveModel {
	m := RemoveModel{deployment: d, confirm: newMenu(removeConfirmRows...)}
	if d == nil {
		m.state = removeStateNotFound
	}
	return m
}

const removeCopy = "Copy command"

var (
	removeConfirmRows = []string{"Stand down Orbit", "Cancel"}
	removeDoneRows    = []string{removeCopy, "Menu", "Exit"}
	removeFailedRows  = []string{"Menu", "Exit"}
)

// Outcome surfaces the flow result to AppModel: leaving, on the first
// screen or after the stand-down, returns to the menu; the Exit row and
// Ctrl-C quit instead.
func (m RemoveModel) Outcome() flowOutcome {
	if m.left {
		return backToMenu
	}
	return flowOutcome{}
}

func (m RemoveModel) standDownFunc() func(context.Context, string) error {
	if m.standDown != nil {
		return m.standDown
	}
	return deploy.StandDown
}

// Init implements tea.Model. It starts the read-only install-date
// lookup; the confirm screen shows the date only once Docker answers.
func (m RemoveModel) Init() tea.Cmd { return installedAtCmd(m.lookupInstalledAt, m.deployment) }

// Update implements tea.Model.
func (m RemoveModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// Build the sky, arm nothing: the one app-wide tick chain
		// SplashModel.Init started is still running and reaches this model
		// the moment it owns the screen. See AppModel.refreshSplash.
		m.width, m.height = msg.Width, msg.Height
		m.star = newSky(msg.Width, msg.Height)
		return m, nil

	case tickMsg:
		m.star = m.star.Advance()
		return m, tick()

	case installedAtMsg:
		m.installedAt = msg.at
		return m, nil

	case standDownResultMsg:
		if msg.err != nil {
			m.standDownErr = msg.err
			m.state = removeStateFailed
			m.after = newMenu(removeFailedRows...)
		} else {
			m.state = removeStateDone
			m.after = newMenu(removeDoneRows...)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m RemoveModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlC(msg) {
		m.state = removeStateCancelled
		return m, tea.Quit
	}

	switch m.state {
	case removeStateNotFound:
		// The dead end: its one row and Esc both go back to the menu
		// (#205, 4a; #228).
		if _, chosen, back := newMenu(deadEndRows...).onScreen(msg); back || chosen == "Menu" {
			m.left = true
		}
		return m, nil
	case removeStateConfirm:
		return m.handleConfirmKey(msg)
	case removeStateDone, removeStateFailed:
		return m.handleDoneKey(msg)
	}
	return m, nil
}

func (m RemoveModel) handleConfirmKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.confirm, chosen, back = m.confirm.onScreen(msg)
	switch {
	case back, chosen == "Cancel":
		m.left = true
	case chosen == "Stand down Orbit":
		m.state = removeStateStandingDown
		targetDir := m.deployment.TargetDir
		standDown := m.standDownFunc()
		return m, func() tea.Msg {
			return standDownResultMsg{err: standDown(context.Background(), targetDir)}
		}
	}
	return m, nil
}

// handleDoneKey drives the two screens after the stand-down. Esc and
// the Menu row go back to the main menu, as after Repair (#225, #228);
// the Exit row quits.
func (m RemoveModel) handleDoneKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.after, chosen, back = m.after.onScreen(msg)
	switch {
	case back, chosen == "Menu":
		m.left = true
		return m, nil
	case chosen == removeCopy:
		words, err := deploy.RemovalCommandWords(m.deployment.TargetDir)
		if err != nil {
			return m, nil
		}
		m.copied = true
		return m, copyToClipboard(m.clipboardWriter(), strings.Join(words, " "))
	case chosen == "Exit":
		return m, tea.Quit
	}
	return m, nil
}

// clipboardWriter is where the copy sequence goes: os.Stdout unless a test
// supplied a writer.
func (m RemoveModel) clipboardWriter() io.Writer {
	if m.clipboard != nil {
		return m.clipboard
	}
	return os.Stdout
}

// copyToClipboard writes an OSC 52 escape sequence, which modern
// terminals (including over SSH) interpret as a clipboard-set request.
// Terminals that don't support it simply ignore the sequence. Written
// directly to stdout rather than via tea.Printf, which is documented as a
// no-op under the alt screen — the mode this program always runs in.
func copyToClipboard(w io.Writer, text string) tea.Cmd {
	encoded := base64.StdEncoding.EncodeToString([]byte(text))
	return func() tea.Msg {
		fmt.Fprintf(w, "\x1b]52;c;%s\x07", encoded)
		return nil
	}
}

// View implements tea.Model.
func (m RemoveModel) View() tea.View { return tea.NewView(m.view()) }

// view renders the screen's content.
func (m RemoveModel) view() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case removeStateNotFound:
		return m.viewNotFound()
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

func (m RemoveModel) viewNotFound() string {
	var b strings.Builder
	// The path is the one word here whose length nobody chose.
	writeHeader(&b, style.AccentText.Render(style.SymbolMark),
		wrapWords("No Orbit deployment found in "+m.targetDir, min(m.width-4, proseWidth))...)
	fmt.Fprintln(&b, style.MutedText.Render("Remove acts only on a deployment the launcher has found."))
	fmt.Fprintln(&b, style.MutedText.Render("Start it in the directory that holds "+deploy.EnvFile+"."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, deadEndRows, 0)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m RemoveModel) viewConfirm() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), "This stops Orbit and removes its containers")

	fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(style.Text).Render(identityLine(m.deployment, m.installedAt)))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("Your mail, documents, and configuration are not deleted"))
	fmt.Fprintln(&b, style.MutedText.Render("by this step — they stay on disk, and the next screen"))
	fmt.Fprintln(&b, style.MutedText.Render("shows exactly how to remove them if you choose to."))
	fmt.Fprintln(&b)
	m.confirm.write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m RemoveModel) viewStandingDown() string {
	return skyBlock(m.star, m.width, m.height, style.WarmText.Render(string(style.SpinnerFrames[0]))+" "+style.MutedText.Render("standing down Orbit…"))
}

func (m RemoveModel) viewDone() string {
	var b strings.Builder
	writeInlineHeader(&b, style.SuccessText.Render(style.SymbolSuccess), "Orbit has been stood down")

	targetDir := m.deployment.TargetDir
	fmt.Fprintln(&b, style.MutedText.Render("Containers and networks are stopped. Your files and data"))
	// The path is the one word here whose length nobody chose.
	writeWrapped(&b, style.MutedText, "volumes are still on disk at "+targetDir+" —", min(m.width-4, proseWidth))
	fmt.Fprintln(&b, style.MutedText.Render("nothing has been deleted."))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.Tagline.Render("to fully remove Orbit from this machine, copy and run"))

	// The command is over 100 cells for a short path, and the box adds
	// four; a terminal cuts whatever is wider than it, and the cut end is
	// the half that deletes everything. Wrapped, it reads whole at 80
	// columns and still runs as typed; Copy command keeps the one-line
	// form. Both are laid out from the same words.
	words, _ := deploy.RemovalCommandWords(targetDir)
	lines := wrapShellCommand(words, m.width-4)
	boxW := 0
	for _, l := range lines {
		boxW = max(boxW, lipgloss.Width(l))
	}
	for _, l := range drawBox(lipgloss.NewStyle().Foreground(style.Border), boxW+4, lines) {
		fmt.Fprintln(&b, l)
	}
	fmt.Fprintln(&b, style.DegradedText.Render("this deletes all mail, documents, and configuration —"))
	fmt.Fprintln(&b, style.DegradedText.Render("it cannot be undone"))
	fmt.Fprintln(&b)

	// The copy row says so once it has copied; the menu's own label,
	// which Enter answers with, stays the same.
	done := m.after
	if m.copied {
		done = done.over(append([]string{removeCopy + " · copied"}, removeDoneRows[1:]...))
	}
	done.write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m RemoveModel) viewFailed() string {
	var b strings.Builder
	writeInlineHeader(&b, style.ErrorText.Render(style.SymbolFailure), "Could not stand down Orbit")
	if m.standDownErr != nil {
		// Docker's words, at Docker's length: wrapped so the reason is
		// never cut at the screen's edge.
		writeWrapped(&b, style.Tagline, m.standDownErr.Error(), min(m.width-4, errorWidth))
		fmt.Fprintln(&b)
	}
	m.after.write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}
