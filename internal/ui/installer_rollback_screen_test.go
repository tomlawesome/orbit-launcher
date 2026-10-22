package ui

// #261 screen rulings (items 1, 2): written from the rulings alone.
//
//  1. At 80x26 an unfinished rollback's failed screen keeps its Menu and
//     Exit rows: the installer's message gets only the rows left, newest
//     lines kept, with one "…" row first when lines are dropped. The
//     recovery command is shown once (the box shows it; a stderr line
//     equal to it is not repeated) and "Open the guided installer" is not
//     offered beside Install again.
//  2. The command is framed like Remove's box, broken only between words
//     (never inside a word of printf %q quoting), and reads whole.

import (
	"fmt"
	"strings"
	"testing"
)

// screenRows is the number of rows a screen takes.
func screenRows(s string) int { return strings.Count(strings.TrimRight(s, "\n"), "\n") + 1 }

// ellipsisRows are the indexes of the lines that are the "…" row alone.
func ellipsisRows(s string) []int {
	var at []int
	for i, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(screenRow(line), "…") && len([]rune(screenRow(line))) <= 1 {
			at = append(at, i)
		}
	}
	return at
}

// lineOf is the index of the first screen line holding text, or -1.
func lineOf(s, text string) int {
	for i, line := range strings.Split(s, "\n") {
		if strings.Contains(line, text) {
			return i
		}
	}
	return -1
}

// (a) retry with a recovery command, 80x26: leaving rows stay on screen.
func TestRollbackScreen_RetryAt80x26KeepsMenuAndExit(t *testing.T) {
	for name, command := range map[string]string{
		"engine command":              engineCommand,
		"engine command with volumes": engineCommandWithVolumes,
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := installApp(t, failedScript(
				[]string{"recovery-command: " + command}, retryEvent, retryStderr(command)), 80, 26)
			s := screen(m)
			if !strings.Contains(s, "Install stopped") {
				t.Fatalf("not the failed screen:\n%s", s)
			}
			for _, want := range []string{"Copy command", "Install again", "Menu", "Exit"} {
				if !hasRow(s, want) {
					t.Errorf("no %q row at 80x26:\n%s", want, s)
				}
			}
			if hasRow(s, "Open the guided installer") {
				t.Errorf("Open the guided installer is offered beside Install again:\n%s", s)
			}
			if n := strings.Count(flat(s), command); n != 1 {
				t.Errorf("the recovery command is shown %d times, want once:\n%s", n, s)
			}
			if n := screenRows(s); n > 26 {
				t.Errorf("the screen takes %d rows, more than the 26 there are:\n%s", n, s)
			}
		})
	}
}

// (b) abort with a long stderr block, 80x26: the newest lines, a "…"
// row first, and the way out.
func TestRollbackScreen_AbortWithLongStderrAt80x26ShowsEllipsisMenuAndExit(t *testing.T) {
	var lines []string
	for i := 1; i <= 30; i++ {
		lines = append(lines, fmt.Sprintf("Orbit installer: abort line %02d", i))
	}
	m, _ := installApp(t, failedScript(nil, abortEvent, lines), 80, 26)
	s := screen(m)
	if !strings.Contains(s, "Install stopped") {
		t.Fatalf("not the failed screen:\n%s", s)
	}
	dots := ellipsisRows(s)
	if len(dots) != 1 {
		t.Fatalf("want exactly one … row for the dropped lines, got %d:\n%s", len(dots), s)
	}
	last := lineOf(s, "abort line 30")
	if last < 0 {
		t.Fatalf("the newest stderr line is not shown:\n%s", s)
	}
	if first := lineOf(s, "abort line 01"); first >= 0 {
		t.Errorf("the oldest stderr line is shown though the screen has no room:\n%s", s)
	}
	if kept := lineOf(s, "abort line"); kept < dots[0] {
		t.Errorf("the … row is not first in the stderr block:\n%s", s)
	}
	for _, want := range []string{"Menu", "Exit"} {
		if !hasRow(s, want) {
			t.Errorf("no %s row at 80x26:\n%s", want, s)
		}
	}
	if hasRow(s, "Open the guided installer") || hasRow(s, "Install again") {
		t.Errorf("an abort offers a retry row:\n%s", s)
	}
	if n := screenRows(s); n > 26 {
		t.Errorf("the screen takes %d rows, more than the 26 there are:\n%s", n, s)
	}
}

// With room (no overflow) nothing is dropped and no … row shows.
func TestRollbackScreen_AbortThatFitsHasNoEllipsis(t *testing.T) {
	m, _ := installApp(t, failedScript(nil, abortEvent, abortStderr()), 80, 40)
	s := screen(m)
	if dots := ellipsisRows(s); len(dots) != 0 {
		t.Errorf("a … row though every line fits:\n%s", s)
	}
}

// longQuotedCommand is a recovery command the way printf %q writes it:
// spaces inside a word are backslash-escaped.
const longQuotedCommand = `cd /srv/orbit/my\ dir/orbit-install-backups/` + rollbackStamp +
	`/engine && docker compose --project-name orbit --env-file .env-orbit down --remove-orphans` +
	` && docker volume rm -- orbit_orbit-db-data orbit_orbit-documents-data orbit_orbit\ search\ data`

// legalBreak reports whether text may be broken just before index i: a
// space preceded by an even run of backslashes.
func legalBreak(text string, i int) bool {
	if i <= 0 || i >= len(text) || text[i] != ' ' {
		return false
	}
	run := 0
	for j := i - 1; j >= 0 && text[j] == '\\'; j-- {
		run++
	}
	return run%2 == 0
}

// (c) the command reads whole in a framed box at 80 columns, every row
// but the last ends " \", and no row ends inside a word.
func TestRollbackScreen_TheCommandIsFramedWholeAndBrokenOnlyBetweenWords(t *testing.T) {
	m, _ := installApp(t, failedScript(
		[]string{"recovery-command: " + longQuotedCommand}, retryEvent, retryStderr(longQuotedCommand)), 80, 40)
	s := screen(m)
	if !strings.Contains(s, "Install stopped") {
		t.Fatalf("not the failed screen:\n%s", s)
	}
	if !strings.Contains(flat(s), longQuotedCommand) {
		t.Errorf("the command does not read whole:\nwant %q in\n%s", longQuotedCommand, flat(s))
	}
	if n := strings.Count(flat(s), longQuotedCommand); n != 1 {
		t.Errorf("the command is shown %d times, want once:\n%s", n, s)
	}
	inside := boxInside(t, s)
	if len(inside) < 2 {
		t.Fatalf("a %d-character command fits one box row at 80 columns?\n%s", len(longQuotedCommand), s)
	}
	for i, r := range boxRowsOf(t, s) {
		if n := len([]rune(r)); n > 80 {
			t.Errorf("box row %d is %d columns wide: %q", i, n, r)
		}
	}
	if got := joinBox(inside); got != longQuotedCommand {
		t.Fatalf("the box text, joined, is not the command:\n got %q\nwant %q", got, longQuotedCommand)
	}
	pos := 0
	for i, row := range inside {
		if i == len(inside)-1 {
			if strings.HasSuffix(row, `\`) && !strings.HasSuffix(longQuotedCommand, `\`) {
				t.Errorf("the last box row %q ends with a continuation", row)
			}
			break
		}
		if !strings.HasSuffix(row, ` \`) {
			t.Errorf("box row %d %q does not end with the continuation \" \\\"", i, row)
		}
		text := strings.TrimSuffix(row, ` \`)
		pos += len(text)
		if !legalBreak(longQuotedCommand, pos) {
			t.Errorf("box row %d breaks the command inside a word, after %q", i, text)
		}
		pos++ // the space the break stands for
	}
	if !hasRow(s, "Copy command") {
		t.Errorf("no Copy command row:\n%s", s)
	}
}

// A word wider than the box cannot be broken: the screen still shows the
// command whole (the plain wrap), with its Copy command row.
func TestRollbackScreen_AWordWiderThanTheBoxStillReadsWhole(t *testing.T) {
	wide := "cd /srv/" + strings.Repeat("deep-folder-name/", 8) + "engine && docker compose down"
	m, _ := installApp(t, failedScript(
		[]string{"recovery-command: " + wide}, retryEvent, retryStderr(wide)), 80, 40)
	s := screen(m)
	if !strings.Contains(flat(s), wide) {
		t.Errorf("a command with a word wider than the box does not read whole:\n%s", s)
	}
	if !hasRow(s, "Copy command") {
		t.Errorf("no Copy command row:\n%s", s)
	}
}
