package ui

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	xansi "github.com/charmbracelet/x/ansi"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

// proseWidth is how wide the flow screens' hand-broken prose runs (the
// confirm screen's lines are 56 cells, and the install notice's prose
// measure is the same); errorWidth is the most an
// unbroken error line gets before it wraps, so a long one reads as a
// paragraph rather than a ribbon.
const (
	proseWidth = 56
	errorWidth = 72
)

// measure is the wrap width of a screen's prose: the terminal's width
// less the two-cell margin each side, capped at limit (proseWidth or
// errorWidth).
func measure(width, limit int) int {
	return min(width-4, limit)
}

// wrapShellCommand lays a shell command, given as its already-quoted
// words, out on lines of at most limit cells that a shell reads back as
// those words joined by spaces: every line that continues ends in " \",
// "&&" always starts its own line, and continuation lines are indented
// two cells (design/mockups.html §11). A command that fits is one line.
//
// Breaks fall between the words, and an option stays with its value
// ("--env-file /opt/orbit/.env-orbit") so no line ends on a flag waiting
// for its argument. A unit wider than limit starts a row of its own and
// is cut across rows the way a POSIX shell is written across lines
// (§2.2.1: a backslash-newline continues a line; §2.2.2: adjacent quoted
// strings join into one word; see cutUnit). The next unit starts a fresh row.
func wrapShellCommand(words []string, limit int) []string {
	if cmd := strings.Join(words, " "); lipgloss.Width(cmd) <= limit {
		return []string{cmd}
	}
	units := commandUnits(words)
	const indent, cont = "  ", " \\"
	var lines []string
	line := ""
	sealed := false // line is a cut unit's last fragment: the next unit starts a fresh row
	flush := func() {
		if line != "" {
			lines = append(lines, line+cont)
			line = indent
		}
		sealed = false
	}
	for _, u := range units {
		if u == "&&" {
			flush()
			if line == "" {
				line = indent
			}
			line += u
			continue
		}
		if sealed {
			flush()
		}
		if lipgloss.Width(indent+u)+len(cont) > limit {
			if line != "" && line != indent {
				flush()
			}
			rows, last := cutUnit(u, limit)
			lines = append(lines, rows...)
			line, sealed = last, true
			continue
		}
		if line != "" && line != indent && lipgloss.Width(line)+1+lipgloss.Width(u)+len(cont) > limit {
			flush()
		}
		if line != "" && line != indent {
			line += " "
		}
		line += u
	}
	if line != "" && line != indent {
		lines = append(lines, line)
	}
	return lines
}

// endCut keeps text to one row of at most limit cells, ending in "…"
// when anything is cut, as wrapHard's last row does (#271 decision 3).
// A limit of 0 means no cut.
func endCut(text string, limit int) string {
	if limit <= 0 || lipgloss.Width(text) <= limit {
		return text
	}
	return xansi.Truncate(text, limit-1, "") + "…"
}

// escapedQuote is how shellQuote writes an apostrophe inside a quoted
// word: close the quote, a backslash-quote, reopen the quote.
const escapedQuote = `'\''`

// cutUnit cuts a unit too wide for one row (an option with its value, or
// a word) into rows that read back as it. It returns every row but the
// last, each ending in a backslash that continues the line, and the last
// fragment, which the caller continues as it does any row. The first row
// has the two-cell indent and carries the option; the rest start flush
// left. A cut inside a single-quoted word closes the quote and reopens
// it: a row ends in `'\` and the next starts with `'`. A cut inside a
// bare word ends the row in a backslash glued to the text. A cut falls
// between grapheme clusters, never leaves an empty fragment, and never
// falls beside or inside an escaped apostrophe. A unit with no place to
// cut is returned whole.
func cutUnit(u string, limit int) (rows []string, last string) {
	const indent, cont = "  ", " \\"
	lead := ""
	if strings.HasPrefix(u, "-") {
		if i := strings.IndexByte(u, ' '); i >= 0 {
			lead, u = u[:i+1], u[i+1:]
		}
	}
	open, closing := "", ""
	if len(u) >= 2 && u[0] == '\'' && u[len(u)-1] == '\'' {
		open, closing, u = "'", "'", u[1:len(u)-1]
	}
	var atoms []string
	for rest := u; rest != ""; {
		a := escapedQuote
		if !strings.HasPrefix(rest, a) {
			a, _ = xansi.FirstGraphemeCluster(rest, xansi.GraphemeWidth)
		}
		atoms = append(atoms, a)
		rest = rest[len(a):]
	}
	width := func(as []string) int { return lipgloss.Width(strings.Join(as, "")) }
	canCut := func(as []string, n int) bool {
		return n > 0 && n < len(as) && as[n-1] != escapedQuote && as[n] != escapedQuote
	}
	head := indent + lead
	for len(atoms) > 0 {
		room := limit - lipgloss.Width(head) - len(open) - len(closing)
		if width(atoms) <= room-len(cont) {
			break
		}
		n, w := 0, 0
		for n < len(atoms)-1 && w+lipgloss.Width(atoms[n]) <= room-1 {
			w += lipgloss.Width(atoms[n])
			n++
		}
		for n > 0 && !canCut(atoms, n) {
			n--
		}
		if n == 0 { // nothing fits: the first fragment runs on to the next place a cut may fall
			for n = 1; n < len(atoms) && !canCut(atoms, n); n++ {
			}
			if n == len(atoms) { // nowhere to cut
				break
			}
		}
		rows = append(rows, head+open+strings.Join(atoms[:n], "")+closing+"\\")
		atoms, head = atoms[n:], ""
	}
	return rows, head + open + strings.Join(atoms, "") + closing
}

// commandUnits groups a command's words into the pieces a line may end
// after: each word, with an option glued to the value that follows it.
func commandUnits(words []string) []string {
	var units []string
	for i := 0; i < len(words); i++ {
		u := words[i]
		if strings.HasPrefix(u, "-") && i+1 < len(words) && !strings.HasPrefix(words[i+1], "-") && words[i+1] != "&&" {
			u += " " + words[i+1]
			i++
		}
		units = append(units, u)
	}
	return units
}

// wrapWords breaks text into lines of at most limit cells, for text
// whose length is not known when the screen is written — a path or an
// error from Docker. The text is made safe to draw first: escape
// sequences go and each line is cleanLine'd, so no control character
// reaches the screen. Each line is then wrapped on its own, so its
// line breaks stay. A line that fits is kept as given; one
// that does not breaks at spaces, the spaces at a break being the
// break, while its indent and runs of spaces elsewhere stay. A single
// word wider than limit stays whole. Trailing line breaks are dropped,
// and empty text is no lines.
func wrapWords(text string, limit int) []string {
	// Trailing line breaks are not rows: command output ends in one.
	text = strings.TrimRight(ansi.Strip(text), "\r\n")
	if text == "" {
		return nil
	}
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		rows = append(rows, wrapLine(cleanLine(line), limit)...)
	}
	// Trailing rows that clean to nothing (a NUL, a tab, spaces) are
	// not rows either; a blank row inside the text stays.
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 0 {
		return nil
	}
	return rows
}

// cleanLine makes one line of unsized text safe to draw. A carriage
// return keeps the last non-empty segment after it: what a terminal
// shows once the progress it overwrote has finished, and a CRLF line
// end loses nothing. A tab is one space (it measures 0 cells but
// renders as four), and any other control character is dropped.
func cleanLine(line string) string {
	if strings.Contains(line, "\r") {
		segs := strings.Split(line, "\r")
		line = ""
		for i := len(segs) - 1; i >= 0; i-- {
			if segs[i] != "" {
				line = segs[i]
				break
			}
		}
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20, r == 0x7f, r >= 0x80 && r < 0xa0:
			return -1
		}
		return r
	}, line)
}

// wrapLine is wrapWords for one line of text, in one pass. It breaks
// at any run of Unicode spaces (unicode.IsSpace, as strings.Fields
// does: a no-break or ideographic space too); the run is the break,
// and elsewhere it is kept as written. The row's width is a running
// count (a space never joins a grapheme cluster, so widths add), and a
// run is written only when a word follows it on the same row. An
// indent stays while it and the first word fit.
func wrapLine(line string, limit int) []string {
	if lipgloss.Width(line) <= limit {
		return []string{line}
	}
	var rows []string
	var row strings.Builder
	rowW, hasWord := 0, false
	for rest := line; rest != ""; {
		end := strings.IndexFunc(rest, func(r rune) bool { return !unicode.IsSpace(r) })
		if end < 0 {
			break // trailing spaces
		}
		gap := rest[:end]
		rest = rest[end:]
		end = strings.IndexFunc(rest, unicode.IsSpace)
		if end < 0 {
			end = len(rest)
		}
		word := rest[:end]
		rest = rest[end:]

		gapW, w := lipgloss.Width(gap), lipgloss.Width(word)
		switch {
		case hasWord && rowW+gapW+w > limit: // the gap is the break
			rows = append(rows, row.String())
			row.Reset()
			rowW, gap, gapW = 0, "", 0
		case !hasWord && gapW+w > limit: // an indent that does not fit goes
			gap, gapW = "", 0
		}
		row.WriteString(gap)
		row.WriteString(word)
		rowW += gapW + w
		hasWord = true
	}
	return append(rows, row.String())
}

// writeWrapped writes text that nobody sized — a path, Docker's own
// words — in ink, as wrapWords lines, so it is never cut at the
// screen's edge. Blank text is one blank line.
func writeWrapped(b *strings.Builder, ink lipgloss.Style, text string, limit int) {
	lines := wrapWords(text, limit)
	if len(lines) == 0 {
		lines = []string{""}
	}
	for _, line := range lines {
		fmt.Fprintln(b, ink.Render(line))
	}
}

// folderRows is the most rows a folder's sentence takes, so a huge path
// never pushes a screen's menu off a 24-row terminal.
const folderRows = 4

// visibleLines is visibleText for error text, which may span lines
// (Docker's, the installer's): each LF stays a row break, and every
// other non-graphic rune on each line is shown escaped (#272). The rows
// it adds stay inside the error block, so they forge nothing.
func visibleLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = visibleText(line)
	}
	return strings.Join(lines, "\n")
}

// visibleText shows text nobody vetted, such as a folder name, as the
// terminal should draw it: every rune that is not graphic is written as
// its Go escape (\r, \n, \t, \x1b, \u202e, \xff for an invalid
// byte), so a CR or LF cannot forge or add a row, an escape sequence
// cannot restyle the screen, and a bidi override cannot reorder it.
// Printable text, spaces and non-ASCII letters stay as they are.
func visibleText(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && n == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case unicode.IsGraphic(r):
			b.WriteRune(r)
		default:
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		}
		i += n
	}
	return b.String()
}

// wrapHard lays out a sentence holding untrusted text (a folder name) in
// at most maxRows rows. The text is visibleText'd first, then wrapped at
// runs of spaces to limit cells (the prose width). A word wider than
// limit that still fits edge (the screen's text area) gets a row of its
// own, whole, since what fits the terminal is never cut. A word wider
// than edge joins the row and is cut at limit on grapheme clusters, so
// the whole path reads across rows. Rows past maxRows are dropped, the
// last kept one ending in "…".
func wrapHard(text string, limit, edge, maxRows int) []string {
	text = visibleText(text)
	var rows []string
	row, rowW := "", 0
	for rest := text; rest != ""; {
		end := strings.IndexFunc(rest, func(r rune) bool { return !unicode.IsSpace(r) })
		if end < 0 {
			break // trailing spaces
		}
		gap := rest[:end]
		rest = rest[end:]
		end = strings.IndexFunc(rest, unicode.IsSpace)
		if end < 0 {
			end = len(rest)
		}
		word := rest[:end]
		rest = rest[end:]

		if row == "" {
			gap = ""
		}
		w := lipgloss.Width(gap + word)
		switch {
		case rowW+w <= limit:
			row, rowW = row+gap+word, rowW+w
		case lipgloss.Width(word) <= limit:
			rows = append(rows, row)
			row, rowW = word, lipgloss.Width(word)
		case lipgloss.Width(word) <= edge:
			if row != "" {
				rows = append(rows, row)
			}
			rows = append(rows, word)
			row, rowW = "", 0
		default:
			parts := strings.Split(xansi.Hardwrap(row+gap+word, limit, true), "\n")
			rows = append(rows, parts[:len(parts)-1]...)
			row = parts[len(parts)-1]
			rowW = lipgloss.Width(row)
		}
	}
	if row != "" {
		rows = append(rows, row)
	}
	if maxRows > 0 && len(rows) > maxRows {
		rows = rows[:maxRows]
		rows[maxRows-1] = xansi.Truncate(rows[maxRows-1], limit-1, "") + "…"
	}
	return rows
}
