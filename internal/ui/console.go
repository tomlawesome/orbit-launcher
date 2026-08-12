package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// The mission console (design/mockups-v5.html section 02): the engine
// runs inside a big rounded rectangle in the TUI, its event stream
// rendered as styled lines, with a stage bar (no percentage — its fill
// is the engine's own phase progression, never an estimate), a stage
// word, and an elapsed clock. Everything shown is derived from engine
// event stream v0 enums or is verbatim raw output; success and failure
// are decided elsewhere, off events plus exit codes, never off prose
// (orbit-launcher#73 guardrails).

// consolePhases is the engine's canonical phase order (orbit
// docs/engine-events.md vocabulary) with the stage word each phase
// shows. The bar's fill is the furthest phase reached.
var consolePhases = []struct{ token, word string }{
	{"bootstrap", "Preparing"},
	{"host", "Checking host"},
	{"identity", "Resolving image identity"},
	{"assets", "Fetching deployment assets"},
	{"configuration", "Configuration"},
	{"oidc", "Verifying sign-in provider"},
	{"compose", "Validating services"},
	{"preparation", "Preparing services"},
	{"database", "Waking the database"},
	{"application", "Starting Orbit"},
	{"optional", "Optional services"},
	{"complete", "Orbit achieved"},
}

func phaseIndex(token string) int {
	for i, p := range consolePhases {
		if p.token == token {
			return i
		}
	}
	return -1
}

// stateWords maps known engine states to the console's display words.
// An unknown state renders as its literal token, unstyled — the
// contract's renderable-but-unstyled rule.
var stateWords = map[string]string{
	"waiting":   "waiting",
	"starting":  "starting",
	"running":   "running",
	"healthy":   "healthy",
	"completed": "done",
	"skipped":   "skipped",
	"failed":    "failed",
	"blocked":   "blocked",
}

var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

// consoleEntry is one displayed line: a parsed event or a raw
// (non-event) stdout line shown verbatim.
type consoleEntry struct {
	isEvent bool
	event   engine.Event
	raw     string
}

// ConsoleModel is the shared mission-console component embedded in the
// Install and Update flows. It is presentation only: it observes the
// engine stream and renders; it never decides outcomes.
type ConsoleModel struct {
	title   string // e.g. "Install — Standard"
	version string

	entries  []consoleEntry
	phaseIdx int  // furthest known phase reached; -1 before any event
	rollback bool // a rollback-phase event was seen

	// unknownStage carries the literal token of an unknown phase — the
	// contract's renderable-but-unstyled rule applies to the stage word
	// too. Cleared when a known phase advances past it.
	unknownStage string

	startedAt time.Time
	now       func() time.Time // injectable clock for deterministic tests
	spinner   int

	// The sky shows in the box's margins — the console stays part of
	// the same scene as the splash, not a separate world.
	sky      starfield.Model
	skyReady bool
}

func newConsole(title, version string, now func() time.Time) ConsoleModel {
	if now == nil {
		now = time.Now
	}
	return ConsoleModel{
		title:     title,
		version:   version,
		phaseIdx:  -1,
		now:       now,
		startedAt: now(),
	}
}

// maxConsoleEntries bounds memory on chatty legacy engines; the view
// shows only what fits anyway.
const maxConsoleEntries = 200

// observeEvent records a parsed engine event.
func (c ConsoleModel) observeEvent(e engine.Event) ConsoleModel {
	c.entries = appendEntry(c.entries, consoleEntry{isEvent: true, event: e})
	if e.Phase == "rollback" {
		c.rollback = true
		return c
	}
	idx := phaseIndex(e.Phase)
	switch {
	case idx == -1:
		// An unknown phase is newest information; show its literal
		// token until a known phase moves things forward again.
		c.unknownStage = e.Phase
	case idx >= c.phaseIdx:
		c.phaseIdx = idx
		c.unknownStage = ""
	}
	return c
}

// observeRaw records a non-event stdout line — legacy-engine prose,
// displayed dim and verbatim.
func (c ConsoleModel) observeRaw(line string) ConsoleModel {
	c.entries = appendEntry(c.entries, consoleEntry{raw: line})
	return c
}

func appendEntry(entries []consoleEntry, e consoleEntry) []consoleEntry {
	entries = append(entries, e)
	if len(entries) > maxConsoleEntries {
		entries = entries[len(entries)-maxConsoleEntries:]
	}
	return entries
}

// setSize (re)builds the background sky for the given screen size.
func (c ConsoleModel) setSize(width, height int) ConsoleModel {
	c.sky = starfield.New(width, height, 1)
	c.skyReady = true
	return c
}

// advance moves the spinner and the sky one frame (called on the
// shared UI tick).
func (c ConsoleModel) advance() ConsoleModel {
	c.spinner = (c.spinner + 1) % len(spinnerFrames)
	if c.skyReady {
		c.sky = c.sky.Advance()
	}
	return c
}

// elapsed is the console's own wall clock — also the "Orbit achieved
// in" figure, so the number the user celebrates is the number they
// watched tick.
func (c ConsoleModel) elapsed() time.Duration {
	return c.now().Sub(c.startedAt)
}

// stageWord is the text under the bar. Before any event it is honest
// about what's happening (the engine buffers early events until its UI
// helper loads, and a legacy engine emits none at all).
func (c ConsoleModel) stageWord() string {
	if c.rollback {
		return "Rolling back"
	}
	if c.unknownStage != "" {
		return c.unknownStage
	}
	if c.phaseIdx >= 0 {
		return consolePhases[c.phaseIdx].word
	}
	if len(c.entries) > 0 {
		return "Working" // a legacy engine: output but no telemetry
	}
	return "Contacting the engine"
}

// entryLine renders one console entry to fit interior width w.
func (c ConsoleModel) entryLine(e consoleEntry, latest bool) string {
	if !e.isEvent {
		return style.Tagline.Render(e.raw)
	}

	ev := e.event
	var glyph string
	switch ev.State {
	case "completed", "healthy":
		glyph = style.SuccessText.Render(style.SymbolSuccess)
	case "skipped":
		glyph = style.Tagline.Render(style.SymbolQueued)
	case "failed":
		glyph = style.ErrorText.Render(style.SymbolFailure)
	case "blocked":
		glyph = style.DegradedText.Render(style.SymbolFailure)
	case "waiting", "starting", "running":
		if latest {
			glyph = style.WarmText.Render(string(spinnerFrames[c.spinner]))
		} else {
			glyph = style.Tagline.Render(style.SymbolQueued)
		}
	default:
		// Unknown state: renderable but unstyled, and no glyph guess.
		glyph = " "
	}

	word, known := stateWords[ev.State]
	if !known {
		word = ev.State
	}
	line := glyph + " " + style.MutedText.Render(ev.Component) + " "
	if known {
		switch ev.State {
		case "completed", "healthy":
			line += style.MutedText.Render(word)
		case "failed":
			line += style.ErrorText.Render(word) + style.Tagline.Render(" — "+ev.Reason)
		case "blocked":
			line += style.DegradedText.Render(word) + style.Tagline.Render(" — "+ev.Reason)
		default:
			line += style.Tagline.Render(word)
		}
	} else {
		line += word
	}
	if ev.Simulation {
		line += style.Tagline.Render("  simulation")
	}
	return line
}

// view renders the full console screen at the given size: the content
// block composited over the sky, plus the footer row.
func (c ConsoleModel) view(width, height int) string {
	lines := c.contentLines(width, height)
	rows := compositeScene(c.sky, c.skyReady, width, height-1, lines, 1)
	foot := ""
	if c.version != "" {
		foot = footerRow(width, "orbit-launcher "+c.version)
	}
	return strings.Join(rows, "\n") + "\n" + foot
}

// contentLines builds the console's centred content block.
func (c ConsoleModel) contentLines(width, height int) []string {
	boxWidth := width - 4
	if boxWidth > 76 {
		boxWidth = 76
	}
	if boxWidth < 20 {
		boxWidth = width
		if boxWidth < 2 {
			return nil
		}
	}
	interior := boxWidth - 4 // "│ " and " │"

	// Rows: title, blank, box top, interior…, box bottom, blank, bar,
	// stage word. The footer row is the caller's.
	boxInterior := height - 1 - 8
	if boxInterior < 3 {
		boxInterior = 3
	}

	var lines []string

	// Title row: "ORBIT · <title>" left, elapsed clock right.
	clock := formatClock(c.elapsed())
	titleLeft := style.MutedText.Render("ORBIT") + style.AccentText.Render(" · ") + style.MutedText.Render(c.title)
	titleGap := boxWidth - lipgloss.Width(titleLeft) - lipgloss.Width(clock)
	if titleGap < 1 {
		titleGap = 1
	}
	lines = append(lines, titleLeft+strings.Repeat(" ", titleGap)+style.Tagline.Render(clock))
	lines = append(lines, strings.Repeat(" ", boxWidth))

	border := lipgloss.NewStyle().Foreground(style.Border)
	lines = append(lines, border.Render("╭"+strings.Repeat("─", boxWidth-2)+"╮"))

	visible := c.entries
	if len(visible) > boxInterior {
		visible = visible[len(visible)-boxInterior:]
	}
	pad := boxInterior - len(visible)
	for i := 0; i < pad; i++ {
		lines = append(lines, border.Render("│")+strings.Repeat(" ", boxWidth-2)+border.Render("│"))
	}
	for i, e := range visible {
		latest := i == len(visible)-1
		content := truncateStyled(c.entryLine(e, latest), interior)
		gap := interior - lipgloss.Width(content)
		if gap < 0 {
			gap = 0
		}
		lines = append(lines, border.Render("│")+" "+content+strings.Repeat(" ", gap+1)+border.Render("│"))
	}
	lines = append(lines, border.Render("╰"+strings.Repeat("─", boxWidth-2)+"╯"))
	lines = append(lines, strings.Repeat(" ", boxWidth))

	// The stage bar: fill is phases reached out of the canonical
	// sequence — engine truth, not an estimate, hence no percentage.
	fill := 0
	if c.phaseIdx >= 0 {
		fill = (c.phaseIdx + 1) * boxWidth / len(consolePhases)
	}
	if fill > boxWidth {
		fill = boxWidth
	}
	barStyle := style.AccentText
	if c.rollback {
		barStyle = style.DegradedText
	}
	bar := barStyle.Render(strings.Repeat("─", fill)) + lipgloss.NewStyle().Foreground(style.BorderSoft).Render(strings.Repeat("─", boxWidth-fill))
	lines = append(lines, bar)

	stage := style.MutedText.Render(c.stageWord())
	gap := boxWidth - lipgloss.Width(c.stageWord())
	if gap < 0 {
		gap = 0
	}
	lines = append(lines, stage+strings.Repeat(" ", gap))

	return lines
}

// formatClock renders the elapsed clock as m:ss (h:mm:ss past an hour).
func formatClock(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	if total >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", total/3600, (total%3600)/60, total%60)
	}
	return fmt.Sprintf("%d:%02d", total/60, total%60)
}

// formatAchieved renders the success footer's duration: "Nm NNs", or
// "NNs" under a minute (design handover 06).
func formatAchieved(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	total := int(d.Seconds())
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	return fmt.Sprintf("%dm %02ds", total/60, total%60)
}

// truncateStyled bounds a styled line to w cells. Styled segments make
// exact truncation fiddly; entries are built to fit, so this is a
// guard: over-wide lines fall back to their plain text truncated.
func truncateStyled(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	plain := []rune(stripANSI(s))
	if len(plain) > w {
		plain = plain[:w]
	}
	return string(plain)
}

func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case inEscape:
			if r == 'm' {
				inEscape = false
			}
		case r == '\x1b':
			inEscape = true
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
