package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestWrapShellCommand_FitsOnOneLineUnchanged(t *testing.T) {
	cmd := removalLine(t, "/opt/orbit")
	got := wrapShellCommand(removalWords(t, "/opt/orbit"), len(cmd))
	if len(got) != 1 || got[0] != cmd {
		t.Fatalf("wrapShellCommand(fits) = %q, want the command untouched", got)
	}
}

// Whatever the width, the lines must read back as exactly the command
// (the read-back of #271 criterion g: a row ending " \" joins with a space,
// one ending "'\" joins directly and drops the doubled quote, one ending
// in a bare "\" joins directly),
// no line may be wider than the limit, a single word included, and "&&"
// must lead its own line so the destructive half is a line apart.
func TestWrapShellCommand_ReadsBackAsTheSameCommand(t *testing.T) {
	for _, dir := range []string{"/opt/orbit", "/srv/containers/mail/orbit-production", "/home/sam/my orbit", "/opt/it's"} {
		cmd := removalLine(t, dir)
		for _, limit := range []int{40, 56, 76, 100} {
			lines := wrapShellCommand(removalWords(t, dir), limit)
			if got := narrowReadBack(lines); got != cmd {
				t.Errorf("limit %d, dir %q: lines %q read back as\n  %q\nwant\n  %q", limit, dir, lines, got, cmd)
			}
			for i, l := range lines {
				if lipgloss.Width(l) > limit {
					t.Errorf("limit %d, dir %q: line %d is %d cells: %q", limit, dir, i, lipgloss.Width(l), l)
				}
				if i < len(lines)-1 && !strings.HasSuffix(l, "\\") {
					t.Errorf("limit %d, dir %q: line %d does not continue with a backslash: %q", limit, dir, i, l)
				}
				if strings.Contains(l, "&&") && !strings.HasPrefix(l, "  && ") {
					t.Errorf("limit %d, dir %q: && does not lead its own line: %q", limit, dir, l)
				}
			}
		}
	}
}

// A quoted path with a space in it is cut across lines only as a closed
// and reopened quote: a line ends in "'\" and the next starts with "'",
// so every line holds whole quotes (#271 criteria f and h).
func TestWrapShellCommand_BreaksInsideQuotesOnlyAsClosedAndReopened(t *testing.T) {
	for _, limit := range []int{30, 42, 56} {
		lines := wrapShellCommand(removalWords(t, "/home/sam/my orbit"), limit)
		for i, l := range lines {
			if strings.Count(l, "'")%2 != 0 {
				t.Errorf("limit %d: line breaks a quoted word without closing it: %q", limit, l)
			}
			if strings.HasSuffix(l, "'\\") && !strings.HasSuffix(l, " '\\") {
				if i+1 >= len(lines) || !strings.HasPrefix(lines[i+1], "'") {
					t.Errorf("limit %d: line %q ends in '\\ but the next line does not reopen the quote: %q", limit, l, lines)
				}
			}
		}
	}
}

// An option stays with its value: no line ends on "--env-file \".
func TestWrapShellCommand_KeepsAnOptionWithItsValue(t *testing.T) {
	for _, l := range wrapShellCommand(removalWords(t, "/opt/orbit"), 60) {
		trimmed := strings.TrimSuffix(l, " \\")
		if strings.HasSuffix(trimmed, "--env-file") || strings.HasSuffix(trimmed, "--project-directory") {
			t.Errorf("line ends on an option without its value: %q", l)
		}
	}
}

func TestWrapWords(t *testing.T) {
	got := wrapWords("volumes are still on disk at /srv/containers/mail/orbit-production —", 56)
	want := []string{"volumes are still on disk at", "/srv/containers/mail/orbit-production —"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapWords = %q, want %q", got, want)
	}
	if got := wrapWords("short", 56); len(got) != 1 || got[0] != "short" {
		t.Errorf("wrapWords(short) = %q", got)
	}
	if got := wrapWords("", 56); len(got) != 0 {
		t.Errorf("wrapWords(empty) = %q, want nothing", got)
	}
}

// dashPath is a folder path of exactly n characters, made of short
// segments so that it is one word to wrapHard.
func dashPath(n int) string {
	p := ""
	for i := 0; len(p) < n; i++ {
		p += "/srv/containers/mail/orbit-production-data"[:min(n-len(p), 41)]
	}
	return p
}

func TestWrapHard_LoneDash_TheDashAfterACutPathNeverStandsAloneOnARow(t *testing.T) {
	const limit, edge, maxRows = 56, 76, 4
	for _, n := range []int{80, 81, 82, 83} {
		path := dashPath(n)
		text := "volumes are still on disk at " + path + " —"
		rows := wrapHard(text, limit, edge, maxRows)
		for i, r := range rows {
			if strings.TrimSpace(r) == "—" {
				t.Errorf("path of %d: row %d is the dash alone: %q", n, i, rows)
			}
			if w := lipgloss.Width(r); w > limit {
				t.Errorf("path of %d: row %d is %d cells, over %d: %q", n, i, w, limit, r)
			}
		}
		last := rows[len(rows)-1]
		if !strings.HasSuffix(last, " —") {
			t.Errorf("path of %d: last row %q does not end in \" —\"", n, last)
			continue
		}
		if tail := strings.TrimSuffix(last, " —"); tail == "" || strings.HasSuffix(tail, " ") {
			t.Errorf("path of %d: last row %q holds no path before the dash", n, last)
		}
		// Read-back, asserted exactly: the only breaks the layout may add
		// inside the path are cuts, which lose nothing, so joining the rows
		// with no separator must equal the text with its spaces removed.
		if got, want := strings.Join(rows, ""), strings.ReplaceAll(text, " ", ""); strings.ReplaceAll(got, " ", "") != want {
			t.Errorf("path of %d: rows read back %q, want %q", n, got, want)
		}
	}
}
