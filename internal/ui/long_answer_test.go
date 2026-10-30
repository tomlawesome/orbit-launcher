package ui

// Pins #223 (a long answer stays on screen). The configuration and
// rotation prompts printed the whole answer on the input row, so a
// pasted URL or secret ran past the screen edge with the cursor out of
// sight. At any width the row must fit the screen: an answer too long to
// show is shown by its end, cursor visible. A secret still shows
// nothing, and Enter still sends the whole answer.

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// longRig is one engine prompt drawn at a given width, as pasteRig but
// with the width chosen by the test.
type longRig struct {
	pasteRig
	width int
}

// longRigs builds the rotation and configuration prompts for one engine
// prompt line at one screen width, keyed by surface name.
func longRigs(t *testing.T, promptLine string, width int) map[string]longRig {
	t.Helper()

	m := sizedRepair(t)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 30})
	m.state = repairRotating
	rStdin := &recordingStdin{}
	m, _ = updateAs(t, m, repairRotateReadyMsg{stream: &engine.Stream{C: make(chan any)}, stdin: rStdin})
	m = repairLines(t, m, promptLine)
	rotation := pasteRig{
		typingRig: typingRig{
			press: func(k tea.KeyPressMsg) { m, _ = updateAs(t, m, k) },
			typed: func() string { return inputRowText(screen(m)) },
			sent:  rStdin.String,
		},
		pasteIn: func(s string) { m, _ = updateAs(t, m, paste(s)) },
		screen:  func() string { return screen(m) },
	}

	r := collectingRun(t, engineRunSeams{})
	r.width, r.height = width, 26
	r.console = r.console.setSize(width, 26)
	cStdin := &recordingStdin{}
	r.cfg.stream = sleepingStream(t)
	r.cfg.stdin = cStdin
	r, _ = r.update(configStreamMsg{msg: engine.RawLineMsg{Text: promptLine}})
	screenOf := func() string { return ansi.Strip(r.view(width, 26)) }
	configuration := pasteRig{
		typingRig: typingRig{
			press: func(k tea.KeyPressMsg) { r, _ = r.update(k) },
			typed: func() string { return inputRowText(screenOf()) },
			sent:  cStdin.String,
		},
		pasteIn: func(s string) { r, _ = r.update(paste(s)) },
		screen:  screenOf,
	}

	return map[string]longRig{
		"rotation":      {rotation, width},
		"configuration": {configuration, width},
	}
}

// longAnswerSurfaces runs body once per width and engine prompt.
func longAnswerSurfaces(t *testing.T, promptLine string, body func(t *testing.T, rig longRig)) {
	t.Helper()
	for _, width := range []int{80, 60} {
		for _, name := range []string{"rotation", "configuration"} {
			t.Run(fmt.Sprintf("%s at %d columns", name, width), func(t *testing.T) {
				t.Parallel() // each case builds its own rig and shares nothing
				body(t, longRigs(t, promptLine, width)[name])
			})
		}
	}
}

// requireFits fails if any line of the screen is wider than the screen,
// measured in cells (not bytes or runes).
func requireFits(t *testing.T, rig longRig, what string) {
	t.Helper()
	scr := rig.screen()
	for i, line := range strings.Split(scr, "\n") {
		if w := lipgloss.Width(line); w > rig.width {
			t.Fatalf("%s: line %d is %d cells wide on a %d-column screen: %q", what, i, w, rig.width, line)
		}
	}
}

// longAnswer is 303 characters (the size the #222 review measured) whose
// head and end can be told apart: it starts "HEAD" and finishes "TAIL".
func longAnswer() string {
	var b strings.Builder
	b.WriteString("HEAD")
	for i := 0; b.Len() < 299; i++ {
		b.WriteString(fmt.Sprintf("-%02d", i%100))
	}
	s := b.String()[:299] + "TAIL"
	return s
}

// A short answer fits and is shown whole, with the cursor. This is the
// control for every refusal below: the screen can draw an answer, and
// the width check is not failing for another reason.
func TestLongAnswer_ShortAnswerIsShownWhole(t *testing.T) {
	longAnswerSurfaces(t, plainPrompt, func(t *testing.T, rig longRig) {
		rig.pasteIn("https://orbit.example.org")
		requireFits(t, rig, "a short answer")
		if got := rig.typed(); got != "https://orbit.example.org" {
			t.Fatalf("a short answer shows %q, want it whole", got)
		}
	})
}

// A pasted answer wider than the row keeps the row on screen, and what
// shows is the end of the answer with the cursor after it.
func TestLongAnswer_PastedAnswerFitsAndShowsItsEnd(t *testing.T) {
	answer := longAnswer()
	if len(answer) != 303 {
		t.Fatalf("test answer is %d characters, want 303", len(answer))
	}
	longAnswerSurfaces(t, plainPrompt, func(t *testing.T, rig longRig) {
		rig.pasteIn(answer)
		requireFits(t, rig, "a pasted 303-character answer")

		shown := rig.typed()
		if !strings.Contains(rig.screen(), "▏") {
			t.Fatalf("the cursor is not on screen:\n%s", rig.screen())
		}
		if !strings.HasSuffix(answer, shown) {
			t.Fatalf("the row shows %q, which is not the end of the answer", shown)
		}
		if !strings.HasSuffix(shown, "TAIL") {
			t.Fatalf("the row shows %q, want it to finish with the answer's last characters", shown)
		}
		// Shown by its end, not squeezed to a stub: a 60-column row has
		// room for a good deal more than this.
		if len(shown) < 20 {
			t.Fatalf("the row shows only %q (%d characters)", shown, len(shown))
		}
		if strings.Contains(shown, "HEAD") {
			t.Fatalf("the row still shows the start of the answer: %q", shown)
		}
	})
}

// Typing does the same as pasting, one key at a time: at every length
// the screen fits, the row ends with the last character typed, and an
// answer that is short enough to show is shown whole (no early
// clipping).
func TestLongAnswer_TypingKeepsTheEndInViewAtEveryLength(t *testing.T) {
	answer := longAnswer()
	longAnswerSurfaces(t, plainPrompt, func(t *testing.T, rig longRig) {
		for i, r := range answer {
			rig.press(runeKey(r))
			n := i + 1
			requireFits(t, rig, fmt.Sprintf("after typing %d characters", n))
			shown := rig.typed()
			if !strings.HasSuffix(answer[:n], shown) || !strings.HasSuffix(shown, string(r)) {
				t.Fatalf("after %d characters the row shows %q, want the end of %q", n, shown, answer[:n])
			}
			if n <= 30 && shown != answer[:n] {
				t.Fatalf("after %d characters the row shows %q, want it whole", n, shown)
			}
		}
	})
}

// Backspacing a long answer back to a short one shows the whole short
// answer again: the clipping follows the answer, it does not stick.
func TestLongAnswer_BackspaceBringsTheStartBack(t *testing.T) {
	longAnswerSurfaces(t, plainPrompt, func(t *testing.T, rig longRig) {
		rig.pasteIn("HEAD")
		rig.pasteIn(strings.Repeat("x", 200))
		requireFits(t, rig, "a long answer")
		if strings.Contains(rig.typed(), "HEAD") {
			t.Fatalf("the row shows the start of a long answer: %q", rig.typed())
		}
		for i := 0; i < 200; i++ {
			rig.press(key(tea.KeyBackspace))
		}
		requireFits(t, rig, "after backspacing")
		if got := rig.typed(); got != "HEAD" {
			t.Fatalf("after backspacing the row shows %q, want %q", got, "HEAD")
		}
	})
}

// Width is measured in screen cells: a long answer of double-width
// characters (two cells each) must fit too, which a count of characters
// would not.
func TestLongAnswer_WideCharactersAreMeasuredInCells(t *testing.T) {
	answer := strings.Repeat("日本語", 60) + "終"
	longAnswerSurfaces(t, plainPrompt, func(t *testing.T, rig longRig) {
		rig.pasteIn(answer)
		requireFits(t, rig, "a long answer of wide characters")
		shown := rig.typed()
		if !strings.HasSuffix(answer, shown) || !strings.HasSuffix(shown, "終") {
			t.Fatalf("the row shows %q, want the end of the answer", shown)
		}
		if len([]rune(shown)) < 10 {
			t.Fatalf("the row shows only %q", shown)
		}
	})
}

// Enter sends the whole answer, not the part on screen, whether it was
// pasted or typed.
func TestLongAnswer_EnterSendsTheWholeAnswer(t *testing.T) {
	answer := longAnswer()
	for _, how := range []string{"pasted", "typed"} {
		t.Run(how, func(t *testing.T) {
			longAnswerSurfaces(t, plainPrompt, func(t *testing.T, rig longRig) {
				if how == "pasted" {
					rig.pasteIn(answer)
				} else {
					for _, r := range answer {
						rig.press(runeKey(r))
					}
				}
				// The width is checked by the tests above; this one only
				// guards what Enter sends, so it must hold on its own.
				if got := rig.sent(); got != "" {
					t.Fatalf("the engine received %q before Enter", got)
				}
				rig.press(key(tea.KeyEnter))
				if got := rig.sent(); got != answer+"\n" {
					t.Fatalf("Enter sent %d bytes (%q), want the whole %d-character answer and a newline",
						len(got), got, len(answer))
				}
			})
		})
	}
}

// A long secret still shows nothing of itself, the screen still fits,
// and Enter sends all of it. The send proves the prompt took the secret,
// so a prompt that dropped it could not pass the hiding check.
func TestLongAnswer_ASecretStaysHidden(t *testing.T) {
	var b strings.Builder
	for i := 0; b.Len() < 303; i++ {
		fmt.Fprintf(&b, "k%03dq", i)
	}
	secret := b.String()[:303]
	longAnswerSurfaces(t, secretPrompt, func(t *testing.T, rig longRig) {
		if !strings.Contains(rig.screen(), "input hidden") {
			t.Fatalf("the prompt does not say its input is hidden:\n%s", rig.screen())
		}
		rig.pasteIn(secret)
		requireFits(t, rig, "a long secret")
		scr := rig.screen()
		if !strings.Contains(scr, "input hidden") {
			t.Fatalf("the prompt stopped saying its input is hidden:\n%s", scr)
		}
		// No window of the secret, at either end, may appear.
		for _, leak := range []string{secret[:8], secret[len(secret)-8:], secret[150:158], "k001q"} {
			if strings.Contains(scr, leak) {
				t.Fatalf("the screen shows %q of a secret:\n%s", leak, scr)
			}
		}
		rig.press(key(tea.KeyEnter))
		if got := rig.sent(); got != secret+"\n" {
			t.Fatalf("Enter sent %q, want the whole secret and a newline", got)
		}
	})
}
