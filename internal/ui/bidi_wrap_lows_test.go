package ui

// #277: engine text drawn through the wrapping path -- the installer's
// own lines on the stopped screen, Repair's manual steps and the
// console's raw lines -- shows bidirectional controls (U+202A-U+202E,
// U+2066-U+2069) as their escape text, `\u202e`, the way every other
// screen does (screen_escape_test.go), never as the raw character that
// would turn the rest of the row around.

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// bidiControls are the embedding, override and isolate controls.
var bidiControls = []rune{
	'\u202a', '\u202b', '\u202c', '\u202d', '\u202e',
	'\u2066', '\u2067', '\u2068', '\u2069',
}

// bidiToken is a word holding the control, and how it must read on
// screen.
func bidiToken(r rune) (raw, shown string) {
	return "port" + string(r) + "EVIL", "port" + fmt.Sprintf(`\u%04x`, r) + "EVIL"
}

// checkBidiShown fails if s holds the raw control (or any other control
// or format rune) or lacks the escaped token.
func checkBidiShown(t *testing.T, where string, r rune, s string) {
	t.Helper()
	_, shown := bidiToken(r)
	if strings.ContainsRune(s, r) {
		t.Errorf("%s: the raw %U reached the screen:\n%q", where, r, s)
	} else if stray := identityStray(s); stray != 0 {
		t.Errorf("%s: %U reached the screen:\n%q", where, stray, s)
	}
	if !strings.Contains(s, shown) {
		t.Errorf("%s: the screen lacks the escaped text %s:\n%s", where, shown, s)
	}
}

func TestLows_BidiControlsInTheInstallersLinesShowEscapedOnTheStoppedScreen(t *testing.T) {
	for _, r := range bidiControls {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			raw, _ := bidiToken(r)
			line := "Orbit installer: Could not bind " + raw + " on the host."
			m := stoppedInstall(t, folderWith(t), hostDone, rollbackRunning, appAborted, exit1(line))
			s := screen(m)
			if !strings.Contains(s, "Could not bind") {
				t.Fatalf("setup: the installer's line is not on the stopped screen:\n%s", s)
			}
			checkBidiShown(t, "stopped screen", r, s)
		})
	}
}

func TestLows_BidiControlsInAManualStepShowEscaped(t *testing.T) {
	for _, r := range bidiControls {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			raw, _ := bidiToken(r)
			m := NewRepairModel(t.TempDir(), "v0.6.0")
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 60})
			m = repairLines(t, m,
				"plan action=manual resolves=database-unreachable mutation=none backup=not-required",
				"plan result=manual-required actions=0 manual=1",
			)
			m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, StderrTail: []string{
				"manual step: check the database " + raw + " is reachable (resolves=database-unreachable)",
			}})
			s := screen(m)
			if !strings.Contains(s, "Needs your attention") || !strings.Contains(s, "reachable") {
				t.Fatalf("setup: the manual step is not on screen:\n%s", s)
			}
			checkBidiShown(t, "manual step", r, s)
		})
	}
}

func TestLows_BidiControlsInAConsoleLineShowEscaped(t *testing.T) {
	for _, r := range bidiControls {
		t.Run(fmt.Sprintf("%U", r), func(t *testing.T) {
			raw, _ := bidiToken(r)
			c := testConsole().observeRaw("pulling image " + raw + " now")
			s := ansi.Strip(c.view(80, 26))
			if !strings.Contains(s, "pulling image") {
				t.Fatalf("setup: the console line is not on screen:\n%s", s)
			}
			checkBidiShown(t, "console line", r, s)
		})
	}
}
