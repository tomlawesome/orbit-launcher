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

// healthProbeTimeout bounds the optional deployment health probe the
// same way.
const healthProbeTimeout = 2 * time.Second

// updateAvailableMsg carries a newer stable release's tag back into the
// bubbletea event loop once checkForUpdate resolves. It is never sent
// on error or when already current — see SplashModel.checkForUpdateCmd.
type updateAvailableMsg struct{ version string }

// healthResultMsg carries the deployment health probe's verdict back
// into the event loop — see SplashModel.probeHealthCmd.
type healthResultMsg struct{ healthy bool }

// deployState is the splash's three-valued status vocabulary
// (design/mockups-v5.html): dormant (nothing installed), alive
// (deployment answers), degraded (deployment exists but is not
// answering healthily). stateUnknown covers a detected deployment whose
// probe hasn't resolved (or was disabled) — rendered as the FQDN with no
// status word, never as a guess.
type deployState int

const (
	stateDormant deployState = iota
	stateUnknown
	stateAlive
	stateDegraded
)

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

// Menu indices used by the state-based preselection.
const (
	menuInstall = 0
	menuUpdate  = 1
	menuRepair  = 2
)

const tickInterval = 120 * time.Millisecond

type tickMsg time.Time

func tick() tea.Cmd {
	return tea.Tick(tickInterval, func(t time.Time) tea.Msg { return tickMsg(t) })
}

// SplashModel is the entry-point screen: the identity block (⟡ mark,
// big ORBIT wordmark, FQDN and status word) over the rotating starfield
// and its planetary systems, with the main menu beneath
// (design/mockups-v5.html).
type SplashModel struct {
	width, height int
	selected      int
	star          starfield.Model
	starReady     bool
	quitting      bool
	noAnimation   bool
	updateNotice  string

	// Identity — populated by AppModel.WithDeploymentStatus before the
	// program starts, so preselection is deterministic (no race between
	// a detection message and the user's first keypress).
	fqdn   string
	appURL string
	state  deployState

	// userNavigated flips on the first Up/Down/number key and permanently
	// stops the async health probe from moving the caret — state may
	// change what's displayed, but it never fights the user's hands.
	userNavigated bool

	// version is shown bottom-right, e.g. "v0.1.0" — set via
	// AppModel.WithVersion; empty renders nothing.
	version string

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

	// healthProbe is nil by default for the same reason; only the real
	// entry point opts in (AppModel.WithDeploymentStatus + deploy.
	// ProbeHealth), and ORBIT_LAUNCHER_NO_HEALTH_PROBE gates it, so
	// tests stay deterministic and offline.
	healthProbe func(ctx context.Context, appURL string) bool
}

// NewSplashModel constructs the splash/main-menu screen.
func NewSplashModel() SplashModel {
	return SplashModel{state: stateDormant}
}

// NewSplashModelNoAnimation constructs a splash/main-menu screen frozen at
// its initial frame: no tick command, so the sky never advances.
// Two real, independent reasons to want this, not just one convenience
// hack: a reduced-motion accessibility mode for a screen that otherwise
// animates continuously, and deterministic screenshots for visual
// regression (see test/visual) — an always-turning sky would otherwise
// make every baseline comparison flaky by construction.
func NewSplashModelNoAnimation() SplashModel {
	return SplashModel{noAnimation: true, state: stateDormant}
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
	if m.healthProbe != nil && m.appURL != "" {
		cmds = append(cmds, m.probeHealthCmd())
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

// probeHealthCmd asks the detected deployment whether it's answering.
// Like the update check it runs in the background and never surfaces an
// error — an unreachable deployment simply reads as degraded.
func (m SplashModel) probeHealthCmd() tea.Cmd {
	probe, appURL := m.healthProbe, m.appURL
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), healthProbeTimeout)
		defer cancel()
		return healthResultMsg{healthy: probe(ctx, appURL)}
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

	case healthResultMsg:
		if msg.healthy {
			m.state = stateAlive
		} else {
			m.state = stateDegraded
			// A degraded deployment preselects Repair — the mark says
			// what's wrong, the caret already points at the fix — but
			// only while the user hasn't taken over.
			if !m.userNavigated {
				m.selected = menuRepair
			}
		}
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
		m.userNavigated = true
		m.selected = (m.selected - 1 + len(MainMenu)) % len(MainMenu)
		return m, nil

	case tea.KeyDown:
		m.userNavigated = true
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
					m.userNavigated = true
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

// markStyle returns the ⟡ mark's style for the current state — the mark
// carries the deployment's state colour (design/mockups-v5.html).
func (m SplashModel) markStyle() lipgloss.Style {
	switch m.state {
	case stateAlive:
		return style.SuccessText
	case stateDegraded:
		return style.DegradedText
	default:
		return style.MarkStyle
	}
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
	bodyHeight := m.height - 1 // the last row is reserved for the footer
	topOffset := int(0.42 * float64(bodyHeight-len(contentLines)))
	if topOffset < 0 {
		topOffset = 0
	}

	// The sky and the content block are composited per row in styled
	// segments: a content row keeps the sky visible in its margins
	// (that's where the planetary systems live — they are placed clear
	// of the centre column), and every segment is a whole Render() call,
	// so no ANSI escape sequence is ever split mid-sequence.
	sky := m.skyGrid(bodyHeight)
	rows := make([]string, bodyHeight)
	for y := 0; y < bodyHeight; y++ {
		if i := y - topOffset; i >= 0 && i < len(contentLines) {
			line := contentLines[i]
			lineWidth := lipgloss.Width(line)
			start := (m.width - lineWidth) / 2
			if start < 0 {
				start = 0
			}
			rows[y] = renderSkyCells(sky[y][:start]) + line + renderSkyCells(sky[y][min(start+lineWidth, m.width):])
		} else {
			rows[y] = renderSkyCells(sky[y])
		}
	}

	return strings.Join(rows, "\n") + "\n" + m.renderFooter()
}

// skyCell is one background cell: a glyph plus which style class draws
// it. Class 0 is empty, 1 is a star; higher classes are planet kinds.
type skyCell struct {
	glyph rune
	class int
}

var planetClassStyles = map[int]lipgloss.Style{
	1: style.StarField,
	2: style.PlanetIceText,
	3: style.PlanetRoseText,
	4: style.PlanetPaleText,
	5: style.PlanetEmberText,
}

// skyGrid rasterises the current sky (stars then planets, planets on
// top) into a cell grid.
func (m SplashModel) skyGrid(height int) [][]skyCell {
	grid := make([][]skyCell, height)
	for y := range grid {
		grid[y] = make([]skyCell, m.width)
		for x := range grid[y] {
			grid[y][x] = skyCell{glyph: ' '}
		}
	}
	if !m.starReady {
		return grid
	}
	for _, c := range m.star.StarCells() {
		if c.Y >= 0 && c.Y < height {
			grid[c.Y][c.X] = skyCell{glyph: c.Glyph, class: 1}
		}
	}
	for _, p := range m.star.Planets() {
		if p.Y >= 0 && p.Y < height {
			grid[p.Y][p.X] = skyCell{glyph: p.Glyph, class: 2 + int(p.Kind)}
		}
	}
	return grid
}

// renderSkyCells renders a run of sky cells, batching consecutive cells
// of the same class into a single Render() call.
func renderSkyCells(cells []skyCell) string {
	if len(cells) == 0 {
		return ""
	}
	var b strings.Builder
	runStart := 0
	flush := func(end int) {
		if end == runStart {
			return
		}
		chunk := make([]rune, 0, end-runStart)
		for _, c := range cells[runStart:end] {
			chunk = append(chunk, c.glyph)
		}
		if s, ok := planetClassStyles[cells[runStart].class]; ok {
			b.WriteString(s.Render(string(chunk)))
		} else {
			b.WriteString(string(chunk))
		}
		runStart = end
	}
	for i := 1; i < len(cells); i++ {
		if cells[i].class != cells[runStart].class {
			flush(i)
		}
	}
	flush(len(cells))
	return b.String()
}

// renderFooter builds the last row: the keybind hint centred, the
// version bottom-right. The hint yields ground rather than colliding on
// narrow terminals, and the version disappears before the hint does.
func (m SplashModel) renderFooter() string {
	const hint = "↑↓ navigate · ↵ select · esc quit"
	hintWidth := lipgloss.Width(hint)
	verWidth := lipgloss.Width(m.version)

	if m.width < hintWidth {
		return lipgloss.PlaceHorizontal(m.width, lipgloss.Center, style.Tagline.Render(hint))
	}
	pad := (m.width - hintWidth) / 2
	row := strings.Repeat(" ", pad) + style.Tagline.Render(hint)
	right := m.width - pad - hintWidth
	if m.version != "" && right >= verWidth+2 {
		row += strings.Repeat(" ", right-verWidth-1) + style.Tagline.Render(m.version) + " "
	}
	return row
}

func (m SplashModel) renderCentreBlock() string {
	var b strings.Builder

	fmt.Fprintln(&b, m.markStyle().Render(style.SymbolMark))
	fmt.Fprintln(&b)
	for _, row := range style.BigText("ORBIT") {
		fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render(row))
	}

	// The identity block replaces the old static tagline: who this
	// deployment is (FQDN), and how it is (status word, in its state
	// colour). With no deployment there is only "dormant"; with an
	// unresolved or disabled probe there is only the FQDN — the status
	// word is never a guess (design/mockups-v5.html section 01).
	switch m.state {
	case stateDormant:
		fmt.Fprintln(&b, style.Tagline.Render("dormant"))
	case stateUnknown:
		fmt.Fprintln(&b, style.MutedText.Render(m.fqdn))
	case stateAlive:
		fmt.Fprintln(&b, style.MutedText.Render(m.fqdn))
		fmt.Fprintln(&b, style.SuccessText.Render("alive"))
	case stateDegraded:
		fmt.Fprintln(&b, style.MutedText.Render(m.fqdn))
		fmt.Fprintln(&b, style.DegradedText.Render("degraded"))
	}
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

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
