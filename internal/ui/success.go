package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// SuccessModel is the quiet hero screen after a completed install or
// update (design/mockups-v5.html section 03, handover 06): the splash's
// own scene — sky, planetary systems, pixel wordmark — with the
// wordmark and mark in alive-green, the deployment URL in the splash's
// identity slot as the hero line, and a stacked menu in the splash's
// caret grammar. The footer carries "Orbit achieved in Nm NNs" (the
// mission console's real clock) left and the version right.
type SuccessModel struct {
	width, height int
	star          starfield.Model
	starReady     bool
	noAnimation   bool

	appURL  string
	elapsed time.Duration
	version string

	selected int

	// Chosen is which action the user picked: "terminal" quits, "menu"
	// returns to the splash (AppModel reads it). "Get into Orbit" stays
	// on this screen — the URL opens, the screen remains.
	Chosen string

	// openURL launches the deployment URL in a browser, best effort.
	// Overridable in tests; nil means the default (xdg-open/open).
	openURL func(url string) error

	// openFailed notes a failed browser launch — on a headless server
	// there's often no browser, and the honest response is a hint to
	// copy the URL, not an error screen.
	openFailed bool
}

var successMenu = []string{"Get into Orbit", "Terminal", "Menu"}

// NewSuccessModel constructs the success screen. elapsed is the mission
// console's real wall-clock duration; zero renders no footer figure
// (handover 06: omitted after flows with no meaningful clock).
func NewSuccessModel(appURL string, elapsed time.Duration, version string) SuccessModel {
	return SuccessModel{appURL: appURL, elapsed: elapsed, version: version}
}

// Init implements tea.Model.
func (m SuccessModel) Init() tea.Cmd {
	if m.noAnimation {
		return nil
	}
	return tick()
}

// Update implements tea.Model.
func (m SuccessModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.star = starfield.New(m.width, m.height, 1)
		m.starReady = true
		return m, nil

	case tickMsg:
		if m.starReady {
			m.star = m.star.Advance()
		}
		if m.noAnimation {
			return m, nil
		}
		return m, tick()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m SuccessModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.Chosen = "terminal"
		return m, tea.Quit

	case tea.KeyUp:
		m.selected = (m.selected - 1 + len(successMenu)) % len(successMenu)
		return m, nil

	case tea.KeyDown:
		m.selected = (m.selected + 1) % len(successMenu)
		return m, nil

	case tea.KeyEnter:
		switch m.selected {
		case 0: // Get into Orbit
			m.openFailed = m.launchURL() != nil
			return m, nil
		case 1: // Terminal
			m.Chosen = "terminal"
			return m, tea.Quit
		case 2: // Menu
			m.Chosen = "menu"
			return m, nil
		}
	}

	if msg.Type == tea.KeyRunes {
		for _, r := range msg.Runes {
			if r == 'q' {
				m.Chosen = "terminal"
				return m, tea.Quit
			}
		}
	}
	return m, nil
}

func (m SuccessModel) launchURL() error {
	open := m.openURL
	if open == nil {
		open = defaultOpenURL
	}
	if m.appURL == "" {
		return fmt.Errorf("no deployment URL")
	}
	return open(m.appURL)
}

// defaultOpenURL launches url with the platform opener. Start (not Run):
// the browser owns its own lifetime, and the TUI must not block on it.
func defaultOpenURL(url string) error {
	for _, opener := range []string{"xdg-open", "open"} {
		if _, err := exec.LookPath(opener); err == nil {
			return exec.Command(opener, url).Start()
		}
	}
	return fmt.Errorf("no opener available")
}

// View implements tea.Model.
func (m SuccessModel) View() string {
	if m.width == 0 {
		return ""
	}

	contentLines := strings.Split(strings.TrimRight(m.renderCentreBlock(), "\n"), "\n")
	bodyHeight := m.height - 1
	topOffset := int(0.42 * float64(bodyHeight-len(contentLines)))

	rows := compositeScene(m.star, m.starReady, m.width, bodyHeight, contentLines, topOffset)

	achieved := ""
	if m.elapsed > 0 {
		achieved = "Orbit achieved in " + formatAchieved(m.elapsed)
	}
	return strings.Join(rows, "\n") + "\n" + footerRow(m.width, achieved, "", m.version)
}

func (m SuccessModel) renderCentreBlock() string {
	var b strings.Builder

	fmt.Fprintln(&b, style.SuccessText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	for _, row := range style.BigText("ORBIT") {
		fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Success).Render(row))
	}

	// The identity slot: the deployment URL is the hero line, with the
	// status word beneath — exactly where the splash puts its FQDN and
	// state, so success reads as the same being, now alive.
	if m.appURL != "" {
		fmt.Fprintln(&b, style.AccentText.Render(m.appURL))
	}
	fmt.Fprintln(&b, style.SuccessText.Render("alive"))
	fmt.Fprintln(&b)

	writeStackedMenu(&b, successMenu, m.selected)
	if m.openFailed {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, style.Tagline.Render("no browser here — copy the URL above"))
	}

	return b.String()
}
