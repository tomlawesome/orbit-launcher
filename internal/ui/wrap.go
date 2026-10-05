package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// wrapShellCommand breaks a one-line shell command into lines of at most
// limit cells that a shell reads back as the same command: every line
// that continues ends in " \", "&&" always starts its own line, and
// continuation lines are indented two cells (design/mockups.html §11).
// A command that fits is returned as the one line it is.
//
// Breaks fall only between shell words, never inside a quoted path, and
// an option stays with its value ("--env-file /opt/orbit/.env-orbit") so
// no line ends on a flag waiting for its argument. A single word wider
// than limit is still placed whole — a path that long cannot be helped.
func wrapShellCommand(cmd string, limit int) []string {
	if lipgloss.Width(cmd) <= limit {
		return []string{cmd}
	}
	units := shellUnits(cmd)
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

// shellUnits splits a command into the pieces a line may end after:
// shell words, with single-quoted spans kept whole and an option glued to
// the value that follows it.
func shellUnits(cmd string) []string {
	var words []string
	var w strings.Builder
	quoted, escaped := false, false
	for _, r := range cmd {
		switch {
		case escaped: // the character after a backslash is literal
			escaped = false
			w.WriteRune(r)
		case r == '\\' && !quoted: // shellQuote writes a quote inside a word as '\''
			escaped = true
			w.WriteRune(r)
		case r == '\'':
			quoted = !quoted
			w.WriteRune(r)
		case r == ' ' && !quoted:
			if w.Len() > 0 {
				words = append(words, w.String())
				w.Reset()
			}
		default:
			w.WriteRune(r)
		}
	}
	if w.Len() > 0 {
		words = append(words, w.String())
	}

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

// wrapWords breaks prose into lines of at most limit cells at spaces, for
// text whose length is not known when the screen is written — a path or
// an error from Docker. A single word wider than limit stays whole.
func wrapWords(text string, limit int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		switch {
		case line == "":
			line = word
		case lipgloss.Width(line)+1+lipgloss.Width(word) > limit:
			lines = append(lines, line)
			line = word
		default:
			line += " " + word
		}
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
