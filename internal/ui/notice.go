package ui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// This file is the development notice (#175): the screen between "Ready
// to install" and the mission console that says, in the owner's own
// words, what Orbit is and is not, and asks for a typed acknowledgement
// before an install starts. The copy, emphasis, countdown, phrase and
// layout were approved in design/mockups-v7-notice.html; its
// noticeScreen() is the reference renderer this file follows.
//
// noticeModel is a value type with pure methods. InstallModel owns one,
// supplies the clock and the duration, and only wires keys and ticks.

const (
	// noticeDefaultDuration is the reading countdown: 257 words at 238
	// words a minute is 64.8 s, rounded up to the next ten.
	noticeDefaultDuration = 70 * time.Second

	// noticeTickInterval redraws the countdown. The sky's own tick chain
	// stops under ORBIT_LAUNCHER_NO_ANIMATION and the countdown is not a
	// motion beat, so it keeps its own.
	noticeTickInterval = time.Second

	noticeMeasure    = 56 // prose wrap width, in cells
	noticeGutter     = 2  // cells either side of the measure
	noticeHeaderRows = 6  // blank, mark, blank, title, state, blank
	noticePinnedRows = 8  // countdown, prompt, box ×3, feedback, Go back, blank
	noticeWheelRows  = 3

	noticeTitle       = "A note before you install"
	noticeStateLabel  = "Project state: "
	noticeStateWord   = "alpha"
	noticePhrase      = "I've read this and I understand"
	noticePromptLabel = "To install, type  "
	noticeRefusal     = "Please type the phrase as shown above"
	noticeGoBack      = "Go back"
)

// noticeParagraphs is the approved copy, verbatim (owner, #175).
var noticeParagraphs = []string{
	"I designed Orbit. Claude, an AI model, wrote the code.",
	"I'm a mechatronics engineer by trade, not a professional software engineer. I built Orbit for myself, because keeping track of so many life admin tasks is hard. I've released it under the GNU AGPL v3.0 so anyone can use it for free.",
	"If you find it useful, that's great. Please understand though that you use it at your own risk: it comes with no warranty, and there's no one on call to support it.",
	"Everything Orbit stores stays on your own machine, and backing it up is up to you.",
	"Because Orbit is in alpha, upgrading it is not guaranteed to keep your data. Always back up before you upgrade.",
	"Orbit sends no data or telemetry about you or your installation anywhere. It does reach the internet for a few things, and sends none of your data when it does: installing and upgrading downloads files from GitHub and Docker Hub, the launcher checks GitHub for a newer version when it starts, and the virus scanner downloads new signatures.",
	"I've poured significant effort and resources into trying to make it as robust and secure as I can, but you should not trust that it is safe to expose to the internet.",
	"Never expose Orbit directly to the internet. Keep it on your own network, and reach it from outside through a VPN back to that network.",
	"If you find a security problem, please report it privately on GitHub at github.com/tomlawesome/orbit/security, not in a public issue.",
}

// noticeEmphasis is the owner-chosen emphasis: exactly these substrings,
// matched before wrapping so a span that breaks across rows keeps its
// emphasis on every word.
var noticeEmphasis = []string{
	"I'm a mechatronics engineer by trade, not a professional software engineer",
	"use it at your own risk",
	"Never expose Orbit directly to the internet",
}

// noticeSeg is a run of prose text that is either emphasised or not.
type noticeSeg struct {
	text string
	em   bool
}

// noticeRow is one document row: wrapped prose, or (no segments) the
// blank gap between paragraphs.
type noticeRow struct{ segs []noticeSeg }

func (r noticeRow) blank() bool { return len(r.segs) == 0 }

func (r noticeRow) plain() string {
	var b strings.Builder
	for _, s := range r.segs {
		b.WriteString(s.text)
	}
	return b.String()
}

func (r noticeRow) width() int { return len([]rune(r.plain())) }

type noticeFocus int

const (
	noticeFocusBack noticeFocus = iota
	noticeFocusField
)

// noticeOutcome is what a key press asks the owning flow to do.
type noticeOutcome int

const (
	noticeStay noticeOutcome = iota
	noticeBack
	noticeAccept
)

// noticeTickMsg redraws the countdown. It carries the start of the
// notice that armed it, so a chain left over from an earlier visit
// (Go back, then Install now again) is recognised and dropped.
type noticeTickMsg struct{ start time.Time }

func noticeTick(start time.Time) tea.Cmd {
	return tea.Tick(noticeTickInterval, func(time.Time) tea.Msg { return noticeTickMsg{start: start} })
}

type noticeModel struct {
	start   time.Time
	offset  int
	endSeen bool // sticky: the whole notice has been on screen
	focus   noticeFocus
	typed   []rune
	refused bool

	doc           []noticeRow
	measure       int
	width, height int
}

// newNotice builds a fresh notice: the countdown starts at start, the
// scroll gate is closed unless everything fits.
func newNotice(start time.Time, width, height int) noticeModel {
	return noticeModel{start: start}.resize(width, height)
}

// noticeMeasureFor is the prose measure for a terminal width: 56, or
// width-4 on a terminal narrower than the 60-cell block (never below
// 20). The house minimum is 80×26, so the narrow case only has to not
// panic.
func noticeMeasureFor(width int) int {
	if width >= noticeMeasure+2*noticeGutter {
		return noticeMeasure
	}
	return max(width-2*noticeGutter, 20)
}

// resize rebuilds the document when the measure changes, re-clamps the
// offset and re-runs the scroll-gate check.
func (n noticeModel) resize(width, height int) noticeModel {
	n.width, n.height = width, height
	measure := noticeMeasureFor(width)
	if n.doc == nil || measure != n.measure {
		n.measure = measure
		n.doc = buildNoticeDoc(measure)
	}
	return n.scrollTo(n.offset)
}

func (n noticeModel) viewport() int { return max(n.height-noticeHeaderRows-noticePinnedRows, 1) }

func (n noticeModel) maxOffset() int { return max(len(n.doc)-n.viewport(), 0) }

// scrollTo clamps the offset and opens the scroll gate the first time
// the end is on screen. The gate never closes again.
func (n noticeModel) scrollTo(offset int) noticeModel {
	n.offset = min(max(offset, 0), n.maxOffset())
	if n.layout().endOnScreen {
		n.endSeen = true
	}
	return n
}

func (n noticeModel) scrollBy(delta int) noticeModel { return n.scrollTo(n.offset + delta) }

func (n noticeModel) page() int { return max(n.viewport()-1, 1) }

func (n noticeModel) timerDone(now time.Time, d time.Duration) bool {
	return now.Sub(n.start) >= d
}

// awake: both gates passed, so the box takes typing.
func (n noticeModel) awake(now time.Time, d time.Duration) bool {
	return n.timerDone(now, d) && n.endSeen
}

// noticeLayout is the visible slice of the document after the gap rule,
// with the rows to draw faded (-1 for none).
type noticeLayout struct {
	rows             []noticeRow
	fadeTop, fadeBot int
	endOnScreen      bool
}

func (n noticeModel) layout() noticeLayout {
	l := noticeLayout{fadeTop: -1, fadeBot: -1}
	vp := n.viewport()
	if len(n.doc) <= vp {
		l.rows = n.doc
		l.endOnScreen = true
		return l
	}
	off := min(max(n.offset, 0), n.maxOffset())
	l.rows = append([]noticeRow(nil), n.doc[off:off+vp]...)
	last := off + vp - 1
	// A paragraph gap is never the last row while more follows: the
	// next line takes its place, so the bottom cue is always text.
	if last < len(n.doc)-1 && l.rows[len(l.rows)-1].blank() {
		last++
		l.rows[len(l.rows)-1] = n.doc[last]
	}
	l.endOnScreen = last >= len(n.doc)-1
	if off > 0 {
		for i, r := range l.rows {
			if !r.blank() {
				l.fadeTop = i
				break
			}
		}
	}
	if !l.endOnScreen {
		for i := len(l.rows) - 1; i >= 0; i-- {
			if !l.rows[i].blank() {
				l.fadeBot = i
				break
			}
		}
	}
	return l
}

// handleKey applies one key press. now is called only where a gate
// matters, so a key that needs no gate reads no clock.
func (n noticeModel) handleKey(msg tea.KeyPressMsg, now func() time.Time, d time.Duration) (noticeModel, noticeOutcome) {
	n.refused = false
	awake := func() bool { return n.awake(now(), d) }
	switch msg.Code {
	case tea.KeyEsc:
		return n, noticeBack
	case tea.KeyPgDown:
		return n.scrollBy(n.page()), noticeStay
	case tea.KeyPgUp:
		return n.scrollBy(-n.page()), noticeStay
	case tea.KeyHome:
		return n.scrollTo(0), noticeStay
	case tea.KeyEnd:
		return n.scrollTo(n.maxOffset()), noticeStay
	case tea.KeyDown:
		if n.focus == noticeFocusField {
			n.focus = noticeFocusBack
			return n, noticeStay
		}
		return n.scrollBy(1), noticeStay
	case tea.KeyUp:
		if n.focus == noticeFocusBack && awake() {
			n.focus = noticeFocusField
			return n, noticeStay
		}
		return n.scrollBy(-1), noticeStay
	case tea.KeySpace:
		if !awake() {
			return n.scrollBy(n.page()), noticeStay
		}
		n.focus = noticeFocusField
		n.typed = append(n.typed, ' ')
	case tea.KeyBackspace:
		if n.focus == noticeFocusField && len(n.typed) > 0 {
			n.typed = n.typed[:len(n.typed)-1]
		}
	case tea.KeyEnter:
		if n.focus == noticeFocusBack {
			return n, noticeBack
		}
		if len(n.typed) == 0 {
			return n, noticeStay
		}
		if phraseMatches(string(n.typed)) {
			return n, noticeAccept
		}
		n.refused = true
	default:
		// Typing is what claims the box; while either gate is open it
		// is ignored.
		if msg.Text != "" && !msg.Mod.Contains(tea.ModCtrl) && awake() {
			n.focus = noticeFocusField
			n.typed = append(n.typed, []rune(msg.Text)...)
		}
	}
	return n, noticeStay
}

// phraseMatches reports whether typed is the acknowledgement phrase,
// under the owner's rulings (#175): capitals and apostrophes don't
// matter, "I have" equals "I've", other words do.
func phraseMatches(typed string) bool {
	return normalisePhrase(typed) == normalisePhrase(noticePhrase)
}

func normalisePhrase(s string) string {
	s = strings.ToLower(s)
	s = strings.Map(func(r rune) rune {
		switch r {
		case '\'', '’', '‘', '´', '`':
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ") // collapse runs, trim
	s = strings.TrimSuffix(s, ".")
	if rest, ok := strings.CutPrefix(s, "i have "); ok {
		s = "ive " + rest
	}
	return s
}

// buildNoticeDoc wraps every paragraph at measure, with a blank row
// between paragraphs.
func buildNoticeDoc(measure int) []noticeRow {
	var doc []noticeRow
	for i, p := range noticeParagraphs {
		if i > 0 {
			doc = append(doc, noticeRow{})
		}
		doc = append(doc, wrapNotice(p, measure)...)
	}
	return doc
}

// wrapNotice greedily wraps one paragraph on spaces; a word is never
// split, so the security address never breaks. Emphasis is marked per
// character before wrapping, so a span keeps it across a row break and
// the punctuation after a span (the full stop after the second
// paragraph's first sentence, the colon after "your own risk") stays
// plain, as the build instructions specify.
func wrapNotice(p string, measure int) []noticeRow {
	text := []rune(p)
	em := make([]bool, len(text))
	for _, span := range noticeEmphasis {
		for from := 0; ; {
			i := strings.Index(string(text[from:]), span)
			if i < 0 {
				break
			}
			start := from + len([]rune(string(text[from:])[:i]))
			end := start + len([]rune(span))
			for k := start; k < end; k++ {
				em[k] = true
			}
			from = end
		}
	}

	type word struct{ start, end int } // rune range in text
	var words []word
	start := 0
	for i := 0; i <= len(text); i++ {
		if i == len(text) || text[i] == ' ' {
			if i > start {
				words = append(words, word{start, i})
			}
			start = i + 1
		}
	}

	var rows []noticeRow
	var cur []word
	length := 0
	flush := func() {
		if len(cur) == 0 {
			return
		}
		rows = append(rows, noticeRowFrom(text, em, cur[0].start, cur[len(cur)-1].end))
		cur, length = nil, 0
	}
	for _, w := range words {
		wl := w.end - w.start
		if len(cur) > 0 && length+1+wl > measure {
			flush()
		}
		if len(cur) > 0 {
			length++
		}
		cur = append(cur, w)
		length += wl
	}
	flush()
	return rows
}

// noticeRowFrom turns text[start:end] into runs of equal emphasis. A
// space takes emphasis only when both neighbours have it, so a span's
// interior reads as one run.
func noticeRowFrom(text []rune, em []bool, start, end int) noticeRow {
	var row noticeRow
	for i := start; i < end; i++ {
		e := em[i]
		if text[i] == ' ' {
			e = i > start && i+1 < end && em[i-1] && em[i+1]
		}
		if n := len(row.segs); n > 0 && row.segs[n-1].em == e {
			row.segs[n-1].text += string(text[i])
			continue
		}
		row.segs = append(row.segs, noticeSeg{text: string(text[i]), em: e})
	}
	return row
}

// view renders the notice block. Every line is padded to the same width
// (measure plus a two-cell gutter each side) so the block has one left
// edge and the sky stays out of its rectangle; skyBlock places it.
func (n noticeModel) view(now time.Time, d time.Duration) string {
	blockW := n.measure + 2*noticeGutter
	pad := func(s string, w int) string { return s + strings.Repeat(" ", max(blockW-w, 0)) }
	centred := func(s string, w int) string {
		left := max(blockW/2-w/2, 0)
		return pad(strings.Repeat(" ", left)+s, left+w)
	}
	gutter := strings.Repeat(" ", noticeGutter)
	blank := pad("", 0)
	textStyle := lipgloss.NewStyle().Foreground(style.Text)
	mutedStyle := lipgloss.NewStyle().Foreground(style.TextMuted)
	faintStyle := lipgloss.NewStyle().Foreground(style.TextFaint)

	awake := n.awake(now, d)
	focused := n.focus == noticeFocusField
	var lines []string

	// Header.
	lines = append(lines,
		blank,
		centred(style.DegradedText.Render(style.SymbolMark), 1),
		blank,
		centred(lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render(noticeTitle), len(noticeTitle)),
		centred(mutedStyle.Render(noticeStateLabel)+style.DegradedText.Render(noticeStateWord), len(noticeStateLabel)+len(noticeStateWord)),
		blank,
	)

	// Prose.
	l := n.layout()
	for i, r := range l.rows {
		if r.blank() {
			lines = append(lines, blank)
			continue
		}
		lines = append(lines, pad(gutter+renderNoticeRow(r, i == l.fadeTop || i == l.fadeBot), noticeGutter+r.width()))
	}

	// Countdown: blank once the gate is passed — the gate is gone, so
	// its object is gone.
	if elapsed := now.Sub(n.start); elapsed < d {
		elapsed = max(elapsed, 0)
		cells := n.measure - 6
		fill := min(max(int(int64(elapsed)*int64(cells)/int64(d)), 0), cells)
		left := (d - elapsed + time.Second - 1) / time.Second // whole seconds, rounded up
		clock := formatClock(left * time.Second)
		bar := ""
		if fill > 0 {
			bar += style.AccentText.Render(strings.Repeat("─", fill))
		}
		if cells-fill > 0 {
			bar += lipgloss.NewStyle().Foreground(style.BorderSoft).Render(strings.Repeat("─", cells-fill))
		}
		lines = append(lines, pad(gutter+bar+"  "+style.Tagline.Render(clock), noticeGutter+cells+2+len(clock)))
	} else {
		lines = append(lines, blank)
	}

	// Prompt: one step brighter once awake.
	labelStyle, phraseStyle := faintStyle, mutedStyle
	if awake || focused {
		labelStyle, phraseStyle = mutedStyle, textStyle
	}
	lines = append(lines, pad(gutter+labelStyle.Render(noticePromptLabel)+phraseStyle.Render(noticePhrase),
		noticeGutter+len(noticePromptLabel)+len([]rune(noticePhrase))))

	// The box, two cells in from the block's edge so the caret at the
	// edge shares Go back's column.
	boxW := n.measure - 2
	inner := boxW - 4
	border := lipgloss.NewStyle().Foreground(style.Border)
	if awake || focused {
		border = faintStyle
	}
	boxIndent := strings.Repeat(" ", 2*noticeGutter)
	lines = append(lines, pad(boxIndent+border.Render("╭"+strings.Repeat("─", boxW-2)+"╮"), 2*noticeGutter+boxW))

	var content string
	contentW := 0
	phrase := []rune(noticePhrase)
	switch {
	case focused && len(n.typed) > 0:
		shown := n.typed
		if len(shown) > inner-1 {
			shown = shown[len(shown)-(inner-1):] // the tail, so the cursor stays visible
		}
		content = textStyle.Render(string(shown)) + style.AccentText.Render("▏")
		contentW = len(shown) + 1
	case focused:
		example := phrase[:min(len(phrase), inner-1)]
		content = style.AccentText.Render("▏") + faintStyle.Render(string(example))
		contentW = 1 + len(example)
	default:
		example := phrase[:min(len(phrase), inner)]
		content = faintStyle.Render(string(example))
		contentW = len(example)
	}
	lead := boxIndent
	if focused {
		lead = gutter + style.MenuCaret.Render(style.SymbolSelected) + " "
	}
	lines = append(lines, pad(lead+border.Render("│")+" "+content+strings.Repeat(" ", max(inner-contentW, 0))+" "+border.Render("│"), 2*noticeGutter+boxW))
	lines = append(lines, pad(boxIndent+border.Render("╰"+strings.Repeat("─", boxW-2)+"╯"), 2*noticeGutter+boxW))

	// Feedback, aligned with the box's text.
	if n.refused {
		lines = append(lines, pad(strings.Repeat(" ", 2*noticeGutter+2)+style.DegradedText.Render(noticeRefusal), 2*noticeGutter+2+len(noticeRefusal)))
	} else {
		lines = append(lines, blank)
	}

	// Go back.
	if focused {
		lines = append(lines, pad(boxIndent+mutedStyle.Render(noticeGoBack), 2*noticeGutter+len(noticeGoBack)))
	} else {
		lines = append(lines, pad(gutter+style.MenuCaret.Render(style.SymbolSelected)+" "+textStyle.Render(noticeGoBack), 2*noticeGutter+len(noticeGoBack)))
	}
	lines = append(lines, blank)

	return strings.Join(lines, "\n")
}

// renderNoticeRow draws one prose row. Emphasis is bold plus one ink
// step up from its row: Text on a muted row, Muted on a faded one.
func renderNoticeRow(r noticeRow, faded bool) string {
	ink, emInk := style.TextMuted, style.Text
	if faded {
		ink, emInk = style.TextFaint, style.TextMuted
	}
	plain := lipgloss.NewStyle().Foreground(ink)
	strong := lipgloss.NewStyle().Bold(true).Foreground(emInk)
	var b strings.Builder
	for _, s := range r.segs {
		if s.em {
			b.WriteString(strong.Render(s.text))
		} else {
			b.WriteString(plain.Render(s.text))
		}
	}
	return b.String()
}
