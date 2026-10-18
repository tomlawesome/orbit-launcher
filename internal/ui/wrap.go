package ui

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"

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
// Breaks fall only between the words, never inside a quoted path, and
// an option stays with its value ("--env-file /opt/orbit/.env-orbit") so
// no line ends on a flag waiting for its argument. A single word wider
// than limit is still placed whole — a path that long cannot be helped.
func wrapShellCommand(words []string, limit int) []string {
	if cmd := strings.Join(words, " "); lipgloss.Width(cmd) <= limit {
		return []string{cmd}
	}
	units := commandUnits(words)
	const indent, cont = "  ", " \\"
	var lines []string
	line := ""
	flush := func() {
		if line != "" {
			lines = append(lines, line+cont)
			line = indent
		}
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
