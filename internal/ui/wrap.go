package ui

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
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
// error from Docker. Each line of the text is wrapped on its own, so
// its line breaks stay. A line that fits is kept exactly as given; one
// that does not breaks at spaces, the spaces at a break being the
// break, while its indent and runs of spaces elsewhere stay. A single
// word wider than limit stays whole. Empty text is no lines.
func wrapWords(text string, limit int) []string {
	if text == "" {
		return nil
	}
	var rows []string
	for _, line := range strings.Split(text, "\n") {
		rows = append(rows, wrapLine(line, limit)...)
	}
	return rows
}

// wrapLine is wrapWords for one line of text.
func wrapLine(line string, limit int) []string {
	if lipgloss.Width(line) <= limit {
		return []string{line}
	}
	var rows []string
	words := strings.Split(line, " ") // a run of spaces is empty words
	cur := words[0]
	for _, word := range words[1:] {
		if word != "" && strings.TrimSpace(cur) != "" && lipgloss.Width(cur)+1+lipgloss.Width(word) > limit {
			rows = append(rows, strings.TrimRight(cur, " "))
			cur = word
			continue
		}
		cur += " " + word
	}
	return append(rows, strings.TrimRight(cur, " "))
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
