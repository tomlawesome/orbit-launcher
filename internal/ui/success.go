package ui

import (
	"errors"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

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

	menu menu // successMenu

	// Chosen is which action the user picked: "terminal" quits, "menu"
	// returns to the splash (AppModel reads it through Outcome). "Get
	// into Orbit" stays on this screen — the URL opens, the screen
	// remains.
	Chosen string

	// openURL launches the deployment URL in a browser, best effort.
	// Overridable in tests; nil means the default (xdg-open/open).
	openURL func(url string) error

	// openFailed notes a failed browser launch — on a headless server
	// there's often no browser, and the honest response is a hint to
	// copy the URL, not an error screen.
	openFailed bool

	// readClaim reads the claim link Orbit prints while nobody has
	// claimed it (#259); nil reads nothing, which is every screen but
	// the one after a fresh Install. claimLink is its answer: with one,
	// the address links to the claim page and Copy claim link appears.
	// The link reaches the screen and the clipboard, nothing else.
	readClaim func() (string, error)
	claimLink string
	copied    bool

	// clipboard is where Copy claim link writes its OSC 52 sequence:
	// nil in production, which means os.Stdout, as for Remove's Copy
	// command.
	clipboard io.Writer
}

const successCopyClaim = "Copy claim link"

var (
	successMenu      = []string{"Get into Orbit", "Terminal", "Menu"}
	successClaimMenu = []string{"Get into Orbit", successCopyClaim, "Terminal", "Menu"}
)

// claimLinkMsg carries readClaim's answer back into the event loop; an
// empty link leaves the screen as it is.
type claimLinkMsg struct{ link string }

// NewSuccessModel constructs the success screen. elapsed is the mission
// console's real wall-clock duration; zero renders no footer figure
// (handover 06: omitted after flows with no meaningful clock).
func NewSuccessModel(appURL string, elapsed time.Duration, version string) SuccessModel {
	return SuccessModel{appURL: appURL, elapsed: elapsed, version: version, menu: newMenu(successMenu...)}
}

// Outcome surfaces the screen's result to AppModel the way every flow
// does: the Menu row or Esc (#225) leaves for the menu; Terminal and
// Ctrl-C quit instead.
func (m SuccessModel) Outcome() flowOutcome {
	if m.Chosen == "menu" {
		return backToMenu
	}
	return flowOutcome{}
}

// Init implements tea.Model.
func (m SuccessModel) Init() tea.Cmd {
	read := m.claimCmd()
	if m.noAnimation {
		return read
	}
	if read == nil {
		return tick()
	}
	return tea.Batch(tick(), read)
}

// claimCmd is the one claim-link read, or nil without a reader. An
// error is no link: the screen stays as it would have been.
func (m SuccessModel) claimCmd() tea.Cmd {
	readClaim := m.readClaim
	if readClaim == nil {
		return nil
	}
	return func() tea.Msg {
		link, err := readClaim()
		if err != nil {
			return claimLinkMsg{}
		}
		return claimLinkMsg{link: link}
	}
}

// Update implements tea.Model.
func (m SuccessModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.star = newSky(m.width, m.height)
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

	case claimLinkMsg:
		if msg.link != "" {
			m.claimLink = msg.link
			m.menu = m.menu.over(successClaimMenu)
		}
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m SuccessModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlC(msg) {
		m.Chosen = "terminal"
		return m, tea.Quit
	}

	// Esc is one step back, and back from a finished flow is the
	// menu, as after Repair (#225).
	var chosen string
	var back bool
	m.menu, chosen, back = m.menu.onScreen(msg)
	if back {
		m.Chosen = "menu"
		return m, nil
	}
	switch chosen {
	case "Get into Orbit":
		m.openFailed = m.launchURL() != nil
		return m, nil
	case successCopyClaim:
		m.copied = true
		return m, copyToClipboard(m.clipboardWriter(), m.claimLink)
	case "Terminal":
		m.Chosen = "terminal"
		return m, tea.Quit
	case "Menu":
		m.Chosen = "menu"
		return m, nil
	}

	return m, nil
}

func (m SuccessModel) launchURL() error {
	open := m.openURL
	if open == nil {
		open = defaultOpenURL
	}
	if m.claimLink != "" {
		return open(m.claimLink)
	}
	if m.appURL == "" {
		return errors.New("no deployment URL")
	}
	return open(m.appURL)
}

// clipboardWriter is where the copy sequence goes: os.Stdout unless a test
// supplied a writer.
func (m SuccessModel) clipboardWriter() io.Writer {
	if m.clipboard != nil {
		return m.clipboard
	}
	return os.Stdout
}

// defaultOpenURL launches url with the platform opener. Start (not Run):
// the browser owns its own lifetime, and the TUI must not block on it.
func defaultOpenURL(url string) error {
	for _, opener := range []string{"xdg-open", "open"} {
		if _, err := exec.LookPath(opener); err == nil {
			return exec.Command(opener, url).Start()
		}
	}
	return errors.New("no opener available")
}

// View implements tea.Model.
func (m SuccessModel) View() tea.View { return tea.NewView(m.view()) }

// view renders the screen's content.
func (m SuccessModel) view() string {
	if m.width == 0 {
		return ""
	}

	contentLines := m.centreBlockLines()
	bodyHeight := m.height - 1
	rows := compositeScene(m.star, m.starReady, m.width, bodyHeight, contentLines, topOffsetFor(bodyHeight, len(contentLines)))

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
		// Escaped for the screen only: open() above still gets the raw
		// address, as the Remove box shows escaped and runs raw.
		address := style.AccentText.Render(visibleText(m.appURL))
		if m.claimLink != "" {
			// The address a person reads, opening the claim page a
			// terminal that knows OSC 8 lets them click.
			address = xansi.SetHyperlink(m.claimLink) + address + xansi.ResetHyperlink()
		}
		lines = append(lines, address)
	}
	status := style.SuccessText.Render("alive")
	if m.claimLink != "" {
		status += style.Separator + style.DegradedText.Render("unclaimed")
	}
	lines = append(lines, status)
	lines = append(lines, "")

	// The copy row says so once it has copied; the menu's own label,
	// which Enter answers with, stays the same.
	rows := m.menu.items
	if m.copied {
		rows = make([]string, len(m.menu.items))
		for i, label := range m.menu.items {
			if label == successCopyClaim {
				label += style.Separator + "copied"
			}
			rows[i] = label
		}
	}
	for i, label := range rows {
		lines = append(lines, menuRow(label, i == m.menu.sel))
	}
	if m.copied {
		lines = append(lines, "")
		for _, row := range claimLinkRows(m.claimLink, measure(m.width, errorWidth)) {
			lines = append(lines, style.MutedText.Render(row))
		}
	}
	if m.openFailed {
		lines = append(lines, "")
		lines = append(lines, style.Tagline.Render("no browser here — copy the URL above"))
	}
	return lines
}

// claimLinkRows lays the claim link out in rows of at most limit cells:
// whole when it fits, otherwise in its two units, the address and then
// the #claim= fragment, each on its own row. The address is written
// without a bare "/" path, which names the same page, so it reads as
// the address above it. A unit wider than limit is cut at the measure,
// the one place a unit is broken. deploy.ParseClaimLink admits printable
// ASCII only, so a byte is a cell.
func claimLinkRows(link string, limit int) []string {
	if lipgloss.Width(link) <= limit || limit <= 0 {
		return []string{link}
	}
	address, fragment := link, ""
	if i := strings.IndexByte(link, '#'); i >= 0 {
		address, fragment = link[:i], link[i:]
	}
	if u, err := url.Parse(link); err == nil && u.Path == "/" && !u.ForceQuery && u.RawQuery == "" {
		address = strings.TrimSuffix(address, "/")
	}
	var rows []string
	for _, unit := range []string{address, fragment} {
		for len(unit) > limit {
			rows = append(rows, unit[:limit])
			unit = unit[limit:]
		}
		if unit != "" {
			rows = append(rows, unit)
		}
	}
	return rows
}
