package ui

// #267 "engine field name and rejection reason drawn without removing
// control codes" and #236 item c12 (SP2-5) for console.go and
// configcollect.go: text the launcher did not write reaches the screen
// as visibleText, never raw (the same rule as screen_escape_test.go).
//
// The tests use U+202E (right-to-left override), a format rune that the
// engine's line parser passes through and that ansi.Strip cannot hide.
// ESC would be removed by ansi.Strip on today's code too, so only the
// absence of the escaped text would show it.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

const (
	lowsRLO     = "\u202e"
	lowsRLOText = `\u202e`
)

// The engine's field name is drawn as the prompt's label when this build
// has no words for it; the rejection reason when this build has none.
// Both go through the shared prompt screen, in Configuration and in
// Repair's rotation.
func TestLows_PromptFieldNameAndRejectionReasonShowControlCodesAsText(t *testing.T) {
	for name, mk := range map[string]func(*testing.T) promptDriver{
		"configuration": configDriver,
		"rotation":      rotationDriver,
	} {
		t.Run(name, func(t *testing.T) {
			d := mk(t)
			d.line("prompt field=FUTURE" + lowsRLO + "FIELD kind=text required=true attempt=1")
			s := d.screen()
			if !strings.Contains(s, "FUTURE"+lowsRLOText+"FIELD") {
				t.Errorf("the engine's field name is not drawn as escaped text:\n%s", s)
			}
			if r := identityStray(s); r != 0 {
				t.Errorf("%U reached the screen through the field name:\n%q", r, s)
			}

			d.line("prompt-reject field=FUTURE" + lowsRLO + "FIELD reason=odd" + lowsRLO + "reason")
			d.line("prompt field=FUTURE" + lowsRLO + "FIELD kind=text required=true attempt=2")
			s = d.screen()
			if !strings.Contains(s, "odd"+lowsRLOText+"reason") {
				t.Errorf("the engine's rejection reason is not drawn as escaped text:\n%s", s)
			}
			if r := identityStray(s); r != 0 {
				t.Errorf("%U reached the screen through the rejection reason:\n%q", r, s)
			}
		})
	}
}

// The same field name on the screen that says the engine stopped asking
// for it (giveUpWords).
func TestLows_TheGiveUpScreenShowsTheFieldNameWithControlCodesAsText(t *testing.T) {
	const script = `
printf 'prompt field=FUTURE\xe2\x80\xaeFIELD kind=text required=true attempt=1\n'
printf 'prompt-abort field=FUTURE\xe2\x80\xaeFIELD\n'
exit 1`
	for _, name := range straightInFlows {
		t.Run(name, func(t *testing.T) {
			f := startStraightFlow(t, name, engineRunSeams{
				prepareConfig: planned(true, false),
				startConfig:   scriptedConfig(script, machineSecretScript),
			})
			f.pressUntil(key(tea.KeyEnter), "the failure screen", f.failed) // sign-in mode
			s := f.screen()
			if f.runState() != runFailed {
				t.Fatalf("setup: run state = %v, want the failure screen:\n%s", f.runState(), s)
			}
			if !strings.Contains(s, "FUTURE"+lowsRLOText+"FIELD") {
				t.Errorf("the failure screen does not show the field name as escaped text:\n%s", s)
			}
			if r := identityStray(s); r != 0 {
				t.Errorf("%U reached the failure screen:\n%q", r, s)
			}
		})
	}
}

// 236-c12: the console draws a phase word this build does not know
// verbatim, in the stage bar and on the event line.
func TestLows_TheConsoleShowsAnUnknownPhaseWordWithControlCodesAsText(t *testing.T) {
	c := testConsole()
	c = c.observeEvent(ev("hyper"+lowsRLO+"space", "installer", "running", "unknown", "continue").Event)
	for _, width := range []int{80, 120} {
		c = c.setSize(width, 26)
		s := ansi.Strip(c.view(width, 26))
		if !strings.Contains(s, "hyper"+lowsRLOText+"space") {
			t.Errorf("at %d columns the unknown phase word is not drawn as escaped text:\n%s", width, s)
		}
		if r := identityStray(s); r != 0 {
			t.Errorf("at %d columns %U reached the screen through the phase word:\n%q", width, r, s)
		}
	}
}
