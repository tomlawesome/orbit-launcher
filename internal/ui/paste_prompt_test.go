package ui

// Pins #222 (pasting into the configuration and rotation prompts).
// bubbletea v2 delivers a bracketed paste as one tea.PasteMsg, not as key
// presses, and the launcher handled none, so pasted text vanished. The
// two engine prompts (in-console configuration, rotation in Repair) must
// add the pasted text at the end of the answer, drop line breaks and
// other control characters, never submit (only Enter does), and still
// draw nothing of a secret. The install notice's phrase is meant to be
// typed, so a paste there stays ignored.

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

func paste(s string) tea.PasteMsg { return tea.PasteMsg{Content: s} }

// pasteRig is one engine prompt seen from outside, as in typingRig, with
// a way to deliver a paste and to read the whole screen.
type pasteRig struct {
	typingRig
	pasteIn func(string)
	screen  func() string
}

// pasteRigs builds the rotation and configuration prompts for one
// engine prompt line, keyed by surface name.
func pasteRigs(t *testing.T, promptLine string) map[string]pasteRig {
	t.Helper()

	m := sizedRepair(t)
	m.state = repairRotating
	rStdin := &recordingStdin{}
	m, _ = repairUpdate(t, m, repairRotateReadyMsg{stream: &engine.Stream{C: make(chan any)}, stdin: rStdin})
	m = repairLines(t, m, promptLine)
	rotation := pasteRig{
		typingRig: typingRig{
			press: func(k tea.KeyPressMsg) { m, _ = repairUpdate(t, m, k) },
			typed: func() string { return inputRowText(repairScreen(m)) },
			sent:  rStdin.String,
		},
		pasteIn: func(s string) { m, _ = repairUpdate(t, m, paste(s)) },
		screen:  func() string { return repairScreen(m) },
	}

	r := collectingRun(t, engineRunSeams{})
	cStdin := &recordingStdin{}
	r.cfg.stream = sleepingStream(t)
	r.cfg.stdin = cStdin
	r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: promptLine}})
	configuration := pasteRig{
		typingRig: typingRig{
			press: func(k tea.KeyPressMsg) { r, _ = r.update(k) },
			typed: func() string { return inputRowText(collectScreen(r)) },
			sent:  cStdin.String,
		},
		pasteIn: func(s string) { r, _ = r.update(paste(s)) },
		screen:  func() string { return collectScreen(r) },
	}

	return map[string]pasteRig{"rotation": rotation, "configuration": configuration}
}

const (
	plainPrompt  = "prompt field=OIDC_CLIENT_ID kind=text required=true attempt=1"
	secretPrompt = "prompt field=checkpoint-passphrase kind=secret required=true attempt=1"
)

// pasteSurfaces runs body once per engine prompt, on a plain-text prompt.
func pasteSurfaces(t *testing.T, body func(t *testing.T, rig pasteRig)) {
	t.Helper()
	for _, name := range []string{"rotation", "configuration"} {
		t.Run(name, func(t *testing.T) { body(t, pasteRigs(t, plainPrompt)[name]) })
	}
}

// A paste into an empty answer shows on the input row, and a paste after
// typing lands at the end; typing carries on after it.
func TestPaste_IsAddedAtTheEndOfTheAnswer(t *testing.T) {
	pasteSurfaces(t, func(t *testing.T, rig pasteRig) {
		rig.pasteIn("https://orbit.example.org")
		if got := rig.typed(); got != "https://orbit.example.org" {
			t.Fatalf("a paste into an empty answer left %q", got)
		}

		rig.press(runeKey('/'))
		rig.pasteIn("auth")
		if got := rig.typed(); got != "https://orbit.example.org/auth" {
			t.Fatalf("a paste after typing left %q, want it at the end", got)
		}

		rig.press(runeKey('!'))
		if got := rig.typed(); got != "https://orbit.example.org/auth!" {
			t.Fatalf("typing after a paste left %q", got)
		}
		rig.press(key(tea.KeyBackspace))
		if got := rig.typed(); got != "https://orbit.example.org/auth" {
			t.Fatalf("backspace after a paste left %q", got)
		}
	})
}

// A paste keeps spaces and non-ASCII text (so the control-character
// filter cannot be "drop everything unusual"), and drops line breaks and
// other control characters wherever they sit. Removed, not replaced by
// spaces.
func TestPaste_DropsLineBreaksAndControlCharacters(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"kept as is", "a b é ✓", "a b é ✓"},
		{"newline", "exa\nmple", "example"},
		{"carriage return and newline", "exa\r\nmple\r\n", "example"},
		{"trailing newline", "example\n", "example"},
		{"tab", "ex\tample", "example"},
		{"escape and bell", "ex\x1b\x07ample", "example"},
		{"nul and delete", "ex\x00ample\x7f", "example"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pasteSurfaces(t, func(t *testing.T, rig pasteRig) {
				rig.press(runeKey('>'))
				rig.pasteIn(c.in)
				if got := rig.typed(); got != ">"+c.want {
					t.Fatalf("pasting %q left %q, want %q", c.in, got, ">"+c.want)
				}
			})
		})
	}
}

// A paste of nothing but line breaks changes nothing and sends nothing.
func TestPaste_OfOnlyLineBreaksChangesNothing(t *testing.T) {
	pasteSurfaces(t, func(t *testing.T, rig pasteRig) {
		rig.press(runeKey('a'))
		rig.pasteIn("\r\n\n")
		if got := rig.typed(); got != "a" {
			t.Fatalf("a paste of line breaks left %q, want %q", got, "a")
		}
		if got := rig.sent(); got != "" {
			t.Fatalf("a paste of line breaks sent %q to the engine", got)
		}
	})
}

// A paste never submits, even with a newline inside or at the end:
// nothing reaches the engine until Enter, and then exactly the cleaned
// text goes, once, with its single newline.
func TestPaste_NeverSubmitsOnlyEnterDoes(t *testing.T) {
	for _, in := range []string{"first\nsecond", "first\r\nsecond\n"} {
		t.Run(strings.NewReplacer("\r", "CR", "\n", "LF").Replace(in), func(t *testing.T) {
			pasteSurfaces(t, func(t *testing.T, rig pasteRig) {
				rig.pasteIn(in)
				if got := rig.sent(); got != "" {
					t.Fatalf("the engine received %q from a paste alone", got)
				}
				if got := rig.typed(); got != "firstsecond" {
					t.Fatalf("the answer reads %q after the paste, want %q", got, "firstsecond")
				}
				rig.press(key(tea.KeyEnter))
				if got := rig.sent(); got != "firstsecond\n" {
					t.Fatalf("Enter sent %q, want %q", got, "firstsecond\n")
				}
			})
		})
	}
}

// A secret prompt takes the paste (Enter sends it) and shows nothing of
// it. The send proves the paste was accepted, so a prompt that ignores
// pastes cannot pass the hiding check.
func TestPaste_IntoASecretPromptIsAcceptedAndNeverShown(t *testing.T) {
	const secret = "s3cr3t-Pa55phrase"
	for name, rig := range pasteRigs(t, secretPrompt) {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(rig.screen(), "input hidden") {
				t.Fatalf("the prompt does not say its input is hidden:\n%s", rig.screen())
			}
			rig.pasteIn(secret[:6])
			rig.pasteIn(secret[6:] + "\n")
			scr := rig.screen()
			for _, leak := range []string{secret, secret[:6], secret[6:], "s3cr"} {
				if strings.Contains(scr, leak) {
					t.Fatalf("the screen shows %q after a paste into a secret prompt:\n%s", leak, scr)
				}
			}
			if got := rig.sent(); got != "" {
				t.Fatalf("the engine received %q before Enter", got)
			}
			rig.press(key(tea.KeyEnter))
			if got := rig.sent(); got != secret+"\n" {
				t.Fatalf("Enter sent %q, want %q", got, secret+"\n")
			}
		})
	}
}

// The install notice's phrase is meant to be typed: a paste there is
// ignored, whatever it holds, and pasting the whole phrase does not let
// Enter start the run. The same phrase typed does, so the ignoring is
// the paste's, not a dead box.
func TestPaste_IntoTheInstallNoticeIsIgnored(t *testing.T) {
	start := func(t *testing.T) (InstallModel, *int) {
		calls := new(int)
		m := awakeNotice(t, engineRunSeams{
			prepareEngine: func(ctx context.Context, dir, action string) (*engine.Stream, func() error, error) {
				*calls++
				return fakeEngine(nil, successStream()...)(ctx, dir, action)
			},
		})
		return m, calls
	}

	t.Run("a paste adds nothing to what was typed", func(t *testing.T) {
		m, _ := start(t)
		m = typeText(t, m, "I")
		m = update(t, m, paste("'ve read this"))
		if got := inputRowText(screen(m)); got != "I" {
			t.Fatalf("the box reads %q after a paste, want %q", got, "I")
		}
		if m.notice.typed.String() != "I" {
			t.Fatalf("the notice holds %q after a paste, want %q", m.notice.typed.String(), "I")
		}
	})

	t.Run("a pasted phrase does not start the run", func(t *testing.T) {
		m, calls := start(t)
		// As in TestNotice_EnterChecksThePhrase, a key first puts the
		// box in focus; the paste then lands on an empty, focused box.
		m = typeText(t, m, "x")
		m = update(t, m, key(tea.KeyBackspace))
		m = update(t, m, paste(noticePhrase))
		updated, cmd := m.Update(key(tea.KeyEnter))
		m = updated.(InstallModel)
		if m.state != installStateNotice {
			t.Fatalf("state = %v after a pasted phrase and Enter, want installStateNotice", m.state)
		}
		if cmd != nil {
			cmd()
		}
		if *calls != 0 {
			t.Fatalf("the engine was prepared %d times from a pasted phrase", *calls)
		}
	})

	t.Run("the typed phrase does start the run", func(t *testing.T) {
		m, calls := start(t)
		m = typeText(t, m, noticePhrase)
		updated, cmd := m.Update(key(tea.KeyEnter))
		m = updated.(InstallModel)
		if m.state != installStateRunning || cmd == nil {
			t.Fatalf("state = %v after the typed phrase and Enter, want installStateRunning", m.state)
		}
		cmd()
		if *calls != 1 {
			t.Fatalf("the engine was prepared %d times, want once", *calls)
		}
	})
}
