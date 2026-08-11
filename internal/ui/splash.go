package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// updateCheckTimeout bounds how long the splash screen's optional
// self-update check may run before it's abandoned — the check is
// entirely non-blocking (a background tea.Cmd; the screen renders
// immediately either way), but a hung request should still eventually
// give up rather than leak forever.
const updateCheckTimeout = 3 * time.Second

// updateAvailableMsg carries a newer stable release's tag back into the
// bubbletea event loop once checkForUpdate resolves. It is never sent
// on error or when already current — see SplashModel.checkForUpdateCmd.
type updateAvailableMsg struct{ version string }

// MenuItem is one row of the splash screen's main menu. Gap requests a
// blank line before this item, used once (before Remove) to visually
// separate "manage the deployment" from "leave" — see
// design/mockups.html section 02.
type MenuItem struct {
	Label string
	Gap   bool
}

// MainMenu is the fixed set of top-level choices, in display order.
var MainMenu = []MenuItem{
	{Label: "Install"},
	{Label: "Update"},
	{Label: "Repair"},
	{Label: "Remove", Gap: true},
	{Label: "Exit"},
}

const tickInterval = 120 * time.Millisecond

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// SplashModel is the entry-point screen: the ⟡ ORBIT mark over a starfield,
// with the main menu beneath it.
type SplashModel struct {
	width, height int
	selected      int
	star          starfield.Model
	starReady     bool
	quitting      bool
	noAnimation   bool
	updateNotice  string

	// Chosen is set to the selected MenuItem's Label once Enter picks one,
	// and stays "" while the user is still navigating or after quitting
	// via Escape/Ctrl-C. A caller (once other screens exist) reads this
	// after the program exits to decide what to launch next.
	Chosen string

	// checkForUpdate is nil by default — every existing constructor
	// leaves the splash screen free of network side effects, matching
	// every other flow in this program (fetches only ever happen after
	// an explicit user confirmation, never automatically on render).
	// Only cmd/orbit-launcher's real entry point opts in, via
	// AppModel.WithUpdateCheck — see internal/release.CheckForUpdate.
	checkForUpdate func(context.Context) (version string, hasUpdate bool, err error)
}

// NewSplashModel constructs the splash/main-menu screen.
func NewSplashModel() SplashModel {
	return SplashModel{}
}

// NewSplashModelNoAnimation constructs a splash/main-menu screen frozen at
// its initial frame: no tick command, so the star field never advances.
// Two real, independent reasons to want this, not just one convenience
// hack: a reduced-motion accessibility mode for a screen that otherwise
// animates continuously, and deterministic screenshots for visual
// regression (see test/visual) — an always-drifting starfield would
// otherwise make every baseline comparison flaky by construction.
func NewSplashModelNoAnimation() SplashModel {
	return SplashModel{noAnimation: true}
}

// Init implements tea.Model.
func (m SplashModel) Init() tea.Cmd {
	var cmds []tea.Cmd
	if !m.noAnimation {
		cmds = append(cmds, tick())
	}
	if m.checkForUpdate != nil {
		cmds = append(cmds, m.checkForUpdateCmd())
	}
	return tea.Batch(cmds...)
}

// checkForUpdateCmd runs checkForUpdate in the background and reports a
// newer stable release, if any. Any error (network failure, GitHub
// unreachable, no stable release published yet) is silently treated as
// "nothing to report" — a failed update check must never surface as a
// user-facing error on the one screen that renders unconditionally on
// every launch.
func (m SplashModel) checkForUpdateCmd() tea.Cmd {
	check := m.checkForUpdate
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), updateCheckTimeout)
		defer cancel()
		version, hasUpdate, err := check(ctx)
		if err != nil || !hasUpdate {
			return nil
		}
		return updateAvailableMsg{version: version}
	}
}

// Update implements tea.Model.
func (m SplashModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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
		return m, tick()

	case updateAvailableMsg:
		m.updateNotice = msg.version
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m SplashModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		m.quitting = true
		return m, tea.Quit

	case tea.KeyUp:
		m.selected = (m.selected - 1 + len(MainMenu)) % len(MainMenu)
		return m, nil

	case tea.KeyDown:
		m.selected = (m.selected + 1) % len(MainMenu)
		return m, nil

	case tea.KeyEnter:
		m.Chosen = MainMenu[m.selected].Label
		m.quitting = true
		return m, tea.Quit
	}

	if msg.Type == tea.KeyRunes {
		for _, r := range msg.Runes {
			if r >= '1' && r <= '9' {
				idx := int(r - '1')
				if idx < len(MainMenu) {
					m.selected = idx
					m.Chosen = MainMenu[idx].Label
					m.quitting = true
					return m, tea.Quit
				}
			}
			switch r {
			case 'q':
				m.quitting = true
				return m, tea.Quit
			}
		}
	}

	return m, nil
}

// View implements tea.Model.
func (m SplashModel) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return ""
	}

	contentLines := strings.Split(strings.TrimRight(m.renderCentreBlock(), "\n"), "\n")
	bodyHeight := m.height - 1 // the last row is reserved for the footer hint
	topOffset := int(0.42 * float64(bodyHeight-len(contentLines)))
	if topOffset < 0 {
		topOffset = 0
	}

	// The starfield and the content block are composited row-by-row
	// rather than character-by-character: each is a single Render() call
	// per row, so no ANSI escape sequence ever sits mid-row where a
	// naive per-cell overlay could corrupt it. The tradeoff is that a
	// content row fully replaces a star row rather than letting stars
	// show in the margins beside text on the same line — an acceptable
	// simplification since the mockup's starfield already reads as
	// sparse background atmosphere, not a dense field brushing the text.
	starRows := m.renderStarRows(bodyHeight)
	rows := make([]string, bodyHeight)
	for y := 0; y < bodyHeight; y++ {
		if i := y - topOffset; i >= 0 && i < len(contentLines) {
			rows[y] = lipgloss.PlaceHorizontal(m.width, lipgloss.Center, contentLines[i])
		} else {
			rows[y] = starRows[y]
		}
	}

	footer := lipgloss.PlaceHorizontal(m.width, lipgloss.Center, style.Tagline.Render("↑↓ navigate · ↵ select · esc quit"))
	return strings.Join(rows, "\n") + "\n" + footer
}

// renderStarRows renders the current star field as height full-width,
// already-styled rows, one Render() call per row.
func (m SplashModel) renderStarRows(height int) []string {
	grid := make([][]rune, height)
	for y := range grid {
		row := make([]rune, m.width)
		for x := range row {
			row[x] = ' '
		}
		grid[y] = row
	}
	for _, s := range m.star.Stars {
		x, y := int(s.X), int(s.Y)
		if y >= 0 && y < height && x >= 0 && x < m.width {
			grid[y][x] = s.Glyph()
		}
	}
	rows := make([]string, height)
	for y, r := range grid {
		rows[y] = style.StarField.Render(string(r))
	}
	return rows
}

func (m SplashModel) renderCentreBlock() string {
	var b strings.Builder

	fmt.Fprintln(&b, style.MarkStyle.Render(style.SymbolMark))
	fmt.Fprintln(&b, style.Wordmark("ORBIT"))
	fmt.Fprintln(&b, style.Tagline.Render("personal server launcher"))
	if m.updateNotice != "" {
		fmt.Fprintln(&b, style.WarmText.Render("update available: "+m.updateNotice))
	}
	fmt.Fprintln(&b)

	for i, item := range MainMenu {
		if item.Gap {
			fmt.Fprintln(&b)
		}
		if i == m.selected {
			fmt.Fprintln(&b, style.MenuCaret.Render(style.SymbolSelected)+" "+style.MenuSelected.Render(item.Label))
		} else {
			fmt.Fprintln(&b, "  "+style.MenuUnselected.Render(item.Label))
		}
	}

	return b.String()
}
