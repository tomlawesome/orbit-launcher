package ui

import (
	"context"
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

// The arrival (design/mockups-v6-starchart.html panel 04, after orbit's
// POL-1): Get, Into and Orbit fade at the screen's vertical centre; the
// third word is the wordmark, which takes a gold sweep and slides up to
// its resting row; then the identity and menu fade in and the foot
// arrives last. All timings in seconds of tick-time (120ms ticks). Any
// key skips; NO_ANIMATION never plays it; it runs once per process.
const (
	introGetStart   = 0.0
	introIntoStart  = 1.7
	introOrbitStart = 3.4
	introSweepStart = 3.9
	introSweepLen   = 2.0
	introSlideStart = 5.9
	introSlideStep  = 0.2 // seconds per row of upward slide
	introSettle     = 6.7
	introMenuStart  = 7.0
	introMenuStep   = 0.15
	introMenuFade   = 0.3
	introFootAt     = 8.2
	introEnd        = 8.6
)

// wordFadeIn/hold/out shape the Get and Into envelopes.
const (
	wordFadeIn  = 0.5
	wordHold    = 0.6
	wordFadeOut = 0.4
)

// inkRamp is the fade ladder from near-background to full ink — colour
// steps are the terminal-honest way to fade (glyphs never move; only
// brightness changes). Index by alpha.
var inkRamp = []lipgloss.Style{
	lipgloss.NewStyle().Foreground(lipgloss.Color("#232837")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#3c4557")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#6b7488")),
	lipgloss.NewStyle().Foreground(lipgloss.Color("#a9b1c1")),
	lipgloss.NewStyle().Bold(true).Foreground(style.Text),
}

// sweepRamp lifts a wordmark cell toward gold-white as the sweep
// highlight passes it.
var sweepRamp = []lipgloss.Style{
	lipgloss.NewStyle().Bold(true).Foreground(style.Text),
	lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#f2e7c8")),
	lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#ffeec4")),
}

func rampStyle(ramp []lipgloss.Style, a float64) lipgloss.Style {
	if a < 0 {
		a = 0
	}
	if a > 1 {
		a = 1
	}
	i := int(a * float64(len(ramp)-1) * 0.999)
	return ramp[i]
}

// envelope is a fade-in / hold / fade-out alpha curve starting at start.
func envelope(s, start float64) float64 {
	u := s - start
	switch {
	case u < 0:
		return 0
	case u < wordFadeIn:
		return u / wordFadeIn
	case u < wordFadeIn+wordHold:
		return 1
	case u < wordFadeIn+wordHold+wordFadeOut:
		return 1 - (u-wordFadeIn-wordHold)/wordFadeOut
	default:
		return 0
	}
}

// SplashModel is the entry-point screen: the identity block (⟡ mark,
// letter-spaced ORBIT wordmark, FQDN and status word) over the rotating
// starfield and its planetary systems, with the main menu beneath and a
// single centred version foot (design/mockups-v6-starchart.html).
type SplashModel struct {
	width, height int
	selected      int
	star          starfield.Model
	starReady     bool
	quitting      bool
	noAnimation   bool
	updateNotice  string

	// The arrival plays once per process; introTick advances with the
	// shared UI tick and introDone latches when it finishes (or is
	// skipped by any key, or never starts under NO_ANIMATION).
	introTick int
	introDone bool

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

	// version is the launcher's own version ("v0.1.0", "dev");
	// orbitVersion is the detected deployment's applied orbit version.
	// Both render in the single centred foot; orbitVersion may be empty.
	version      string
	orbitVersion string

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
// its initial frame: no tick command, so the sky never advances and the
// arrival never plays. Two real, independent reasons to want this, not
// just one convenience hack: a reduced-motion accessibility mode for a
// screen that otherwise animates continuously, and deterministic
// screenshots for visual regression (see test/visual).
func NewSplashModelNoAnimation() SplashModel {
	return SplashModel{noAnimation: true, state: stateDormant, introDone: true}
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

// introSeconds converts the intro's tick counter to seconds.
func (m SplashModel) introSeconds() float64 {
	return float64(m.introTick) * tickInterval.Seconds()
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
		if !m.introDone {
			m.introTick++
			if m.introSeconds() >= introEnd {
				m.introDone = true
			}
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
	}
	if msg.Type == tea.KeyRunes {
		for _, r := range msg.Runes {
			if r == 'q' {
				m.quitting = true
				return m, tea.Quit
			}
		}
	}

	// Any other key during the arrival skips straight to the lit room —
	// nobody should ever wait for a splash — and is swallowed, so a
	// hurried Enter doesn't also select a menu item.
	if !m.introDone {
		m.introDone = true
		return m, nil
	}

	switch msg.Type {
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
		}
	}

	return m, nil
}

// markStyle returns the ⟡ mark's style for the current state — the mark
// carries the deployment's state colour; dormant wears the gold accent
// (design/mockups-v6-starchart.html).
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

// footText is the splash's whole foot: the launcher's version, plus the
// deployment's orbit version once one is known.
func (m SplashModel) footText() string {
	launcher := m.version
	if launcher == "" {
		launcher = "dev"
	}
	text := "orbit-launcher " + launcher
	if m.orbitVersion != "" {
		text += " · orbit " + m.orbitVersion
	}
	return text
}

// View implements tea.Model.
func (m SplashModel) View() string {
	if m.quitting {
		return ""
	}
	if m.width == 0 {
		return ""
	}
	if !m.introDone {
		return m.viewIntro()
	}

	contentLines := m.centreBlockLines()
	bodyHeight := m.height - 1 // the last row is reserved for the foot
	topOffset := int(0.42 * float64(bodyHeight-len(contentLines)))

	rows := compositeScene(m.star, m.starReady, m.width, bodyHeight, contentLines, topOffset)
	return strings.Join(rows, "\n") + "\n" + footerRow(m.width, m.footText())
}

// centreBlockLines builds the settled centre block: mark, blank,
// wordmark, identity tight beneath it, optional update notice, blank,
// then the menu in the centred grammar.
func (m SplashModel) centreBlockLines() []string {
	var lines []string

	lines = append(lines, m.markStyle().Render(style.SymbolMark))
	lines = append(lines, "")
	lines = append(lines, style.Wordmark("ORBIT"))

	// The identity block sits directly beneath the wordmark: FQDN then
	// status word, no floating gap. With no deployment there is only
	// "dormant"; with an unresolved or disabled probe only the FQDN —
	// the status word is never a guess.
	switch m.state {
	case stateDormant:
		lines = append(lines, style.Tagline.Render("dormant"))
	case stateUnknown:
		lines = append(lines, style.MutedText.Render(m.fqdn))
	case stateAlive:
		lines = append(lines, style.MutedText.Render(m.fqdn))
		lines = append(lines, style.SuccessText.Render("alive"))
	case stateDegraded:
		lines = append(lines, style.MutedText.Render(m.fqdn))
		lines = append(lines, style.DegradedText.Render("degraded"))
	}
	if m.updateNotice != "" {
		lines = append(lines, style.WarmText.Render("update available: "+m.updateNotice))
	}
	lines = append(lines, "")

	for i, item := range MainMenu {
		if item.Gap {
			lines = append(lines, "")
		}
		lines = append(lines, menuRow(item.Label, i == m.selected))
	}
	return lines
}

// viewIntro renders the arrival. Content is assembled as a full-height
// line list (composited at offset 0) so words can sit at the true
// vertical centre and the wordmark can slide to exactly the row the
// settled view will hold it at — no jump at the handover.
func (m SplashModel) viewIntro() string {
	bodyHeight := m.height - 1
	s := m.introSeconds()

	settledLines := m.centreBlockLines()
	settledTop := int(0.42 * float64(bodyHeight-len(settledLines)))
	if settledTop < 0 {
		settledTop = 0
	}
	wordmarkRest := settledTop + 2 // mark, blank, wordmark
	centreRow := bodyHeight/2 - 1
	if centreRow < 0 {
		centreRow = 0
	}

	lines := make([]string, bodyHeight)

	if a := envelope(s, introGetStart); a > 0.06 {
		lines[centreRow] = rampStyle(inkRamp, a).Render("Get")
	}
	if a := envelope(s, introIntoStart); a > 0.06 {
		lines[centreRow] = rampStyle(inkRamp, a).Render("Into")
	}

	if s >= introOrbitStart {
		fade := (s - introOrbitStart) / 0.5
		row := centreRow
		if s >= introSlideStart {
			up := int((s - introSlideStart) / introSlideStep)
			row = centreRow - up
			if row < wordmarkRest {
				row = wordmarkRest
			}
		}
		if row >= 0 && row < bodyHeight {
			lines[row] = m.introWordmark(fade, s)
		}
	}

	if s >= introSettle {
		// The mark and identity are simply there; the menu fades in one
		// item at a time, top to bottom.
		if settledTop < bodyHeight {
			lines[settledTop] = m.markStyle().Render(style.SymbolMark)
		}
		identity := settledLines[3 : len(settledLines)-menuLineCount()-1]
		for i, l := range identity {
			if r := settledTop + 3 + i; r < bodyHeight {
				lines[r] = l
			}
		}
		menuTop := settledTop + len(settledLines) - menuLineCount()
		item := 0
		for i, mi := range MainMenu {
			r := menuTop + item
			if mi.Gap {
				item++
				r++
			}
			a := (s - introMenuStart - float64(i)*introMenuStep) / introMenuFade
			if a > 0.06 && r >= 0 && r < bodyHeight {
				if a >= 1 {
					lines[r] = menuRow(mi.Label, i == m.selected)
				} else {
					lines[r] = rampStyle(inkRamp[:4], a).Render(mi.Label)
				}
			}
			item++
		}
	}

	rows := compositeScene(m.star, m.starReady, m.width, bodyHeight, lines, 0)
	foot := ""
	if s >= introFootAt {
		foot = footerRow(m.width, m.footText())
	}
	return strings.Join(rows, "\n") + "\n" + foot
}

// introWordmark renders the letter-spaced wordmark mid-arrival: fading
// in, then lifted cell by cell as the gold-white sweep crosses it.
func (m SplashModel) introWordmark(fade, s float64) string {
	word := "O R B I T"
	if fade < 1 {
		return rampStyle(inkRamp, fade).Render(word)
	}
	sweep := (s - introSweepStart) / introSweepLen
	if sweep < 0 || sweep > 1 {
		return inkRamp[len(inkRamp)-1].Render(word)
	}
	pos := sweep * float64(len(word))
	var b strings.Builder
	for i, r := range word {
		if r == ' ' {
			b.WriteRune(' ')
			continue
		}
		d := pos - float64(i)
		if d < 0 {
			d = -d
		}
		lift := 1 - d/2.2
		b.WriteString(rampStyle(sweepRamp, lift).Render(string(r)))
	}
	return b.String()
}

// menuLineCount is the menu's row count including its one gap line.
func menuLineCount() int {
	n := 0
	for _, item := range MainMenu {
		if item.Gap {
			n++
		}
		n++
	}
	return n
}
