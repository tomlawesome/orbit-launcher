package ui

// v0.4.3 group B, L4 (ratified design, issue #277): copyable text on a
// narrow screen is cut visibly, every character kept, never a marker.
//
//   - On the stopped, success and diagnosis screens no row is wider than
//     width-4 at 50, 80 and 120 columns (measured as a row of text with its
//     margin and indent taken off, as the mockups count it).
//   - A word of at most width-4 cells is never cut. A longer one is cut
//     across rows with every character present and in order, and no "…".
//   - A shell command (the recovery command) is always in a box, with the
//     shell's own continuation: a backslash ends each row but the last, a
//     quoted word is closed and reopened. Read back as a shell reads it, the
//     box's rows give the command's words.
//   - The stderr tail still keeps its newest lines behind one "…" row when
//     short of room (unchanged, pinned by earlier tests).
//
// Not covered here: that Copy command still copies the raw line (unchanged
// behaviour, pinned where the clipboard is observed).
//
// Written from the specification alone. The screens are built the way the
// c9 harness and the Repair, success and stopped-screen tests build them.

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

var gbStWidths = []int{50, 80, 120}

// gbStLongFolder is a path with no space in it, wider than any of the
// three terminals: a word that has to be cut at every width.
var gbStLongFolder = "/srv/" + strings.Repeat("segment-of-a-long-path/", 7) + "orbit"

// gbStSpacedFolder has spaces, so the shell quotes it as one word.
const gbStSpacedFolder = "/srv/containers/mail stack/orbit production/with several/long directories/under the data volume/orbit"

func gbStRecoveryCommand(folder string) string {
	return "cd " + shQuoteIfSpaced(folder+"/orbit-install-backups/"+rollbackStamp+"/engine") +
		" && docker compose --project-name orbit --env-file .env-orbit down --remove-orphans"
}

func shQuoteIfSpaced(s string) string {
	if strings.ContainsAny(s, " \t") {
		return "'" + s + "'"
	}
	return s
}

// gbStRetryStream is an Install that stopped after a rollback that left
// a recovery command, the installer's lines naming the backup folder.
func gbStRetryStream(t *testing.T, folder string, extra ...string) []any {
	t.Helper()
	command := gbStRecoveryCommand(folder)
	stderr := []string{
		"Orbit installer: This install could not be fully rolled back: the containers could not be stopped.",
		"Orbit installer: Install again to finish putting the folder back. If the containers still will not stop, run this in a terminal first, then install again:",
		command,
		"Orbit installer: .env-orbit and .orbit-secrets will be put back exactly as they were before this install; anything changed in them while it ran is replaced. A copy of this folder's original files is kept in " + folder + "/orbit-install-backups/" + rollbackStamp + "/originals.",
	}
	stderr = append(stderr, extra...)
	return []any{
		hostDone, rollbackRunning,
		engine.RawLineMsg{Text: "recovery-command: " + command},
		parsedEvent(t, retryEvent),
		exit1(stderr...),
	}
}

// gbStStoppedView is an Install's engine run fed msgs and drawn at
// w x h on the screen it stops on, escapes stripped.
func gbStStoppedView(t *testing.T, w, h int, msgs ...any) string {
	t.Helper()
	run := newEngineRun(deploy.ActionInstall, rollbackFolder, "Install — Standard", "v1").
		withSeams(engineRunSeams{now: func() time.Time { return time.Time{} }})
	run, _ = run.start(w, h)
	run.state = runStreaming
	for _, msg := range msgs {
		run, _ = run.update(engineStreamMsg{msg: msg})
	}
	if run.state != runFailed {
		t.Fatalf("the run did not stop (state %v)", run.state)
	}
	return ansi.Strip(run.view(w, h))
}

// gbStBoxLine reports whether a screen line is part of a framed box.
func gbStBoxLine(line string) bool {
	trimmed := strings.TrimLeft(line, " ▸.·✦●⟡")
	return strings.HasPrefix(trimmed, "│") || strings.HasPrefix(trimmed, "╭") || strings.HasPrefix(trimmed, "╰")
}

// gbStNoRowWiderThan fails for each row (sky, caret, margin and indent
// off) wider than width-4.
func gbStNoRowWiderThan(t *testing.T, screenText string, width int) {
	t.Helper()
	for i, line := range strings.Split(screenText, "\n") {
		row := trimSky(line)
		if got := lipgloss.Width(row); got > width-4 {
			t.Errorf("at %d columns row %d is %d cells, want at most %d: %q", width, i, got, width-4, row)
		}
	}
}

// gbStOutsideBoxes is the screen's text without any boxed row,
// whitespace removed, so text cut across rows reads as one run.
func gbStOutsideBoxes(screenText string) string {
	var b strings.Builder
	for _, line := range strings.Split(screenText, "\n") {
		if gbStBoxLine(line) {
			continue
		}
		b.WriteString(squeeze(trimSky(line)))
	}
	return b.String()
}

// gbStNoEllipsisNear fails for a row holding a fragment of word that
// also holds a "…": the cut must show every character, not a marker.
func gbStNoEllipsisNear(t *testing.T, screenText, fragment string) {
	t.Helper()
	for _, line := range strings.Split(screenText, "\n") {
		if strings.Contains(line, fragment) && strings.Contains(line, "…") {
			t.Errorf("a cut row carries a …: %q", strings.TrimSpace(line))
		}
	}
}

// gbStRowHolding reports whether some row holds word whole.
func gbStRowHolding(screenText, word string) bool {
	for _, line := range strings.Split(screenText, "\n") {
		if strings.Contains(line, word) {
			return true
		}
	}
	return false
}

// gbStOwnRow reports whether some row is exactly word.
func gbStOwnRow(screenText, word string) bool {
	for _, line := range strings.Split(screenText, "\n") {
		if trimSky(line) == word {
			return true
		}
	}
	return false
}

// --- the stopped screen: installer lines ------------------------------------

// The installer's line naming the backup folder: no row is wider than
// width-4, the path is cut in order with every character kept, no "…".
func TestGroupB_L4_StoppedScreenCutsALongPathInOrderAndKeepsEveryCharacter(t *testing.T) {
	want := gbStLongFolder + "/orbit-install-backups/" + rollbackStamp + "/originals."
	for _, w := range gbStWidths {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			s := gbStStoppedView(t, w, 70, gbStRetryStream(t, gbStLongFolder)...)
			gbStNoRowWiderThan(t, s, w)
			if !strings.Contains(gbStOutsideBoxes(s), want) {
				t.Errorf("at %d columns the path is not on the screen whole and in order:\n%s", w, s)
			}
			gbStNoEllipsisNear(t, s, "segment-of")
		})
	}
}

// A word that fits (exactly width-4 cells) stays whole on a row of its own;
// a wider one is cut, every character present and in order, no "…".
func TestGroupB_L4_StoppedScreenKeepsAFittingWordWholeAndCutsALongerOne(t *testing.T) {
	for _, w := range gbStWidths {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			fit := strings.Repeat("f", w-4)
			over := strings.Repeat("o", w-4+37)
			s := gbStStoppedView(t, w, 70, hostDone, composeRunning, rollbackRunning, rollbackDone, composeFailed,
				exit1("Orbit installer: see "+fit+" for the folder", "Orbit installer: and "+over+" too"))
			gbStNoRowWiderThan(t, s, w)
			if !gbStOwnRow(s, fit) {
				t.Errorf("the %d-cell word is not whole on a row of its own at %d columns:\n%s", w-4, w, s)
			}
			if !strings.Contains(gbStOutsideBoxes(s), over) {
				t.Errorf("the %d-cell word lost characters or order at %d columns:\n%s", len(over), w, s)
			}
			gbStNoEllipsisNear(t, s, "ooooo")
			gbStNoEllipsisNear(t, s, "fffff")
		})
	}
}

// --- the stopped screen: the recovery command's box -------------------------

// gbStBox returns the inside of the first framed box on a screen, one
// string per row, the border and its one cell of padding taken off; ok is
// false when the screen has no box.
func gbStBox(screenText string) (rows []string, ok bool) {
	in := false
	for _, line := range strings.Split(screenText, "\n") {
		trimmed := strings.TrimLeft(line, " ▸.·✦●⟡")
		switch {
		case strings.HasPrefix(trimmed, "╭"):
			in, ok = true, true
		case strings.HasPrefix(trimmed, "╰"):
			return rows, ok
		case in && strings.HasPrefix(trimmed, "│"):
			first, last := strings.Index(line, "│"), strings.LastIndex(line, "│")
			inner := line[first+len("│") : last]
			inner = strings.TrimPrefix(inner, " ")
			rows = append(rows, strings.TrimRight(inner, " "))
		}
	}
	return rows, ok
}

// gbStShellWords reads text as a POSIX shell reads words: a backslash
// before a newline is a continuation (both vanish), single quotes keep
// everything literal, double quotes keep it with \" and \\ , a backslash
// outside quotes quotes the next character, and unquoted blanks end a
// word. Adjacent quoted and unquoted pieces make one word.
func gbStShellWords(text string) []string {
	var words []string
	var cur strings.Builder
	inWord := false
	end := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	rs := []rune(text)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case r == '\\' && i+1 < len(rs) && rs[i+1] == '\n':
			i++
		case r == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			inWord = true
		case r == '\'':
			inWord = true
			for i++; i < len(rs) && rs[i] != '\''; i++ {
				cur.WriteRune(rs[i])
			}
		case r == '"':
			inWord = true
			for i++; i < len(rs) && rs[i] != '"'; i++ {
				if rs[i] == '\\' && i+1 < len(rs) && (rs[i+1] == '"' || rs[i+1] == '\\') {
					i++
				}
				cur.WriteRune(rs[i])
			}
		case unicode.IsSpace(r):
			end()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	end()
	return words
}

// The reader of gbStShellWords is itself checked, so a mistake in it
// cannot make the box tests pass or fail for the wrong reason.
func TestGroupB_L4_TheShellReaderReadsContinuationAndQuoteFragments(t *testing.T) {
	text := "cd \\\n  '/srv/mail stack/orbit p'\\\n'roduction/x' \\\n  && ls"
	want := []string{"cd", "/srv/mail stack/orbit production/x", "&&", "ls"}
	if got := gbStShellWords(text); !slices.Equal(got, want) {
		t.Errorf("gbStShellWords = %q, want %q", got, want)
	}
}

// The recovery command is always in a box, at every width, even when a
// word of it is wider than the terminal; its rows read back as a shell
// reads them give the command's words, and no row is wider than width-4.
func TestGroupB_L4_TheRecoveryCommandIsAlwaysBoxedAndReadsBackAsTheCommand(t *testing.T) {
	for name, folder := range map[string]string{"a long path": gbStLongFolder, "a path with spaces": gbStSpacedFolder} {
		for _, w := range gbStWidths {
			t.Run(fmt.Sprintf("%s/%d", name, w), func(t *testing.T) {
				s := gbStStoppedView(t, w, 70, gbStRetryStream(t, folder)...)
				gbStNoRowWiderThan(t, s, w)
				rows, ok := gbStBox(s)
				if !ok || len(rows) == 0 {
					t.Fatalf("no box at %d columns:\n%s", w, s)
				}
				for i, row := range rows[:len(rows)-1] {
					if !strings.HasSuffix(row, `\`) {
						t.Errorf("box row %d %q does not end in a backslash, so the shell would end the command there", i, row)
					}
				}
				got := gbStShellWords(strings.Join(rows, "\n"))
				want := gbStShellWords(gbStRecoveryCommand(folder))
				if !slices.Equal(got, want) {
					t.Errorf("the box reads back as\n  %q\nwant the command's words\n  %q\nrows: %q", got, want, rows)
				}
				for _, row := range rows {
					if strings.Contains(row, "…") {
						t.Errorf("box row %q carries a …", row)
					}
				}
			})
		}
	}
}

// --- the success screen ------------------------------------------------------

// The success screen's installer lines follow the same rule.
func TestGroupB_L4_SuccessScreenCutsLongInstallerLinesAndKeepsFittingWords(t *testing.T) {
	longPath := gbStLongFolder + "/logs/scanner.log"
	for _, w := range gbStWidths {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			fit := strings.Repeat("f", w-4)
			m := NewSuccessModel("https://mail.example.com", 0, "v9.9.9")
			m.serviceNotes = []string{
				"Scanner: still starting; its log is " + longPath + ".",
				"Documents: see " + fit + " for the folder.",
			}
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: w, Height: 60})
			s := screen(m)
			gbStNoRowWiderThan(t, s, w)
			if !strings.Contains(gbStOutsideBoxes(s), longPath) {
				t.Errorf("at %d columns the path is not on the screen whole and in order:\n%s", w, s)
			}
			if !gbStOwnRow(s, fit) {
				t.Errorf("the %d-cell word is not whole on a row of its own at %d columns:\n%s", w-4, w, s)
			}
			gbStNoEllipsisNear(t, s, "segment-of")
		})
	}
}

// --- the diagnosis screen ----------------------------------------------------

// Repair's manual steps follow the same rule. The step text is indented on
// this screen, so the fitting word leaves a few cells of slack.
func TestGroupB_L4_DiagnosisScreenCutsLongManualStepsAndKeepsFittingWords(t *testing.T) {
	longPath := gbStLongFolder + "/orbit-install-backups/" + rollbackStamp + "/originals/.env-orbit"
	for _, w := range gbStWidths {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			fit := strings.Repeat("f", w-4-6)
			step := "manual step: restore your settings from " + longPath + " then check " + fit + " is right (resolves=secret-stale)"
			m := NewRepairModel(t.TempDir(), "v0.6.0")
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: w, Height: 60})
			m = repairLines(t, m,
				"plan action=manual resolves=secret-stale mutation=none backup=required",
				"plan result=manual-required actions=0 manual=1",
			)
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, StderrTail: []string{step}})
			s := screen(m)
			if !strings.Contains(s, "Needs your attention") {
				t.Fatalf("setup: not the diagnosis screen:\n%s", s)
			}
			gbStNoRowWiderThan(t, s, w)
			if !strings.Contains(gbStOutsideBoxes(s), longPath) {
				t.Errorf("at %d columns the path is not on the screen whole and in order:\n%s", w, s)
			}
			if !gbStRowHolding(s, fit) {
				t.Errorf("the %d-cell word was cut at %d columns:\n%s", len(fit), w, s)
			}
			gbStNoEllipsisNear(t, s, "segment-of")
		})
	}
}
