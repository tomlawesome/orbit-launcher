package ui

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// SuccessModel is the quiet hero screen after a completed install or
// update (design/mockups-v6-starchart.html section 03): the splash's
// own scene, the wordmark in plain ink, the ⟡ mark alone carrying
// alive-green, the deployment URL as the one gold object on screen, and
// the centred stacked menu. The foot is the achieved line, centred, and
// nothing else. On entry the binary pair's lead drifts once to a wider,
// calmer orbit — the restored-orbit beat.
type SuccessModel struct {
	width, height int
	star          starfield.Model
	starReady     bool
	noAnimation   bool

	// driftTick drives the one-shot restored-orbit beat.
	driftTick int

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
			// The restored-orbit beat: after a beat of stillness the
			// lead body eases outward over ~1.2s, then stays. One shot.
			m.driftTick++
			const driftStart, driftLen = 8, 10
			drift := float64(m.driftTick-driftStart) / driftLen
			if drift < 0 {
				drift = 0
			}
			if drift > 1 {
				drift = 1
			}
			m.star.Drift = drift
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

	contentLines := m.centreBlockLines()
	bodyHeight := m.height - 1
	topOffset := int(0.42 * float64(bodyHeight-len(contentLines)))

	rows := compositeScene(m.star, m.starReady, m.width, bodyHeight, contentLines, topOffset)

	achieved := ""
	if m.elapsed > 0 {
		achieved = "Orbit achieved in " + formatAchieved(m.elapsed)
	}
	return strings.Join(rows, "\n") + "\n" + footerRow(m.width, achieved)
}

func (m SuccessModel) centreBlockLines() []string {
	var lines []string

	// The mark alone carries alive-green; the wordmark is ink because
	// it is the wordmark — states outrank brand, brand never shouts.
	lines = append(lines, style.SuccessText.Render(style.SymbolMark))
	lines = append(lines, "")
	lines = append(lines, style.Wordmark("ORBIT"))

	// The identity slot, tight beneath: the deployment URL is the one
	// gold object on this screen, with the status word under it.
	if m.appURL != "" {
		lines = append(lines, style.AccentText.Render(m.appURL))
	}
	lines = append(lines, style.SuccessText.Render("alive"))
	lines = append(lines, "")

	for i, label := range successMenu {
		lines = append(lines, menuRow(label, i == m.selected))
	}
	if m.openFailed {
		lines = append(lines, "")
		lines = append(lines, style.Tagline.Render("no browser here — copy the URL above"))
	}
	return lines
}
