package ui

// Pins #252 (secret prompts show a fixed eight-dot mask once they hold
// input). A secret prompt used to look dead while you typed or pasted:
// the input row stayed "▸ ▏" whatever the answer held. The row now shows
// a fixed-width mask, eight "●", whenever the answer holds anything, and
// none when it holds nothing. The width never follows the answer's
// length, so the screen still gives away neither the text nor its size.
//
// Every test reads the rendered screen only, on both engine prompt
// surfaces (Repair's rotation prompt and the in-console configuration
// prompt). Bullets are counted on the input row, and across the whole
// screen as a change against the same prompt's fresh screen, so other
// chrome that happens to use the glyph cannot confuse the count.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const (
	maskGlyph = "●"
	maskEight = "●●●●●●●●"
)

// bullets counts the mask glyph on a whole screen.
func bullets(scr string) int { return strings.Count(scr, maskGlyph) }

// requireMask fails unless the input row reads exactly eight bullets
// before the cursor, nothing after it, and the whole screen has gained
// exactly eight bullets over the fresh prompt's screen.
func requireMask(t *testing.T, rig pasteRig, freshBullets int, what string) {
	t.Helper()
	scr := rig.screen()
	before, after := cursorRowParts(t, scr)
	if before != maskEight || after != "" {
		t.Fatalf("%s: the input row reads %q▏%q, want %q▏\n%s", what, before, after, maskEight, scr)
	}
	if got := bullets(scr) - freshBullets; got != 8 {
		t.Fatalf("%s: the screen gained %d %q, want exactly 8\n%s", what, got, maskGlyph, scr)
	}
}

// requireNoMask fails if the row holds any bullet or the screen has
// gained any over the fresh prompt's screen.
func requireNoMask(t *testing.T, rig pasteRig, freshBullets int, what string) {
	t.Helper()
	scr := rig.screen()
	before, after := cursorRowParts(t, scr)
	if strings.Contains(before, maskGlyph) || strings.Contains(after, maskGlyph) {
		t.Fatalf("%s: the input row shows a mask %q▏%q\n%s", what, before, after, scr)
	}
	if got := bullets(scr); got != freshBullets {
		t.Fatalf("%s: the screen has %d %q, a fresh prompt has %d\n%s", what, got, maskGlyph, freshBullets, scr)
	}
}

// secretMaskSurfaces runs body once per engine prompt, on a secret
// prompt, with the bullet count of the fresh screen.
func secretMaskSurfaces(t *testing.T, body func(t *testing.T, rig pasteRig, fresh int)) {
	t.Helper()
	for _, name := range []string{"rotation", "configuration"} {
		t.Run(name, func(t *testing.T) {
			rig := pasteRigs(t, secretPrompt)[name]
			body(t, rig, bullets(rig.screen()))
		})
	}
}

// 1. A secret prompt that has just been asked shows no mask: the row is
// the bare "▸ ▏" and the hint still says the input is hidden.
func TestSecretMask_FreshPromptShowsNoMask(t *testing.T) {
	secretMaskSurfaces(t, func(t *testing.T, rig pasteRig, _ int) {
		scr := rig.screen()
		if !strings.Contains(scr, "input hidden") {
			t.Fatalf("the hint no longer says the input is hidden:\n%s", scr)
		}
		before, after := cursorRowParts(t, scr)
		if before != "" || after != "" {
			t.Fatalf("a fresh secret prompt's row reads %q▏%q, want the bare cursor", before, after)
		}
		if strings.Contains(inputRowOf(scr), maskGlyph) {
			t.Fatalf("a fresh secret prompt's row shows a mask:\n%s", scr)
		}
		// The row is "▸ ▏" exactly (cursor straight after the marker).
		found := false
		for _, line := range strings.Split(scr, "\n") {
			if strings.TrimSpace(line) == "▸ ▏" {
				found = true
			}
		}
		if !found {
			t.Fatalf("no row of a fresh secret prompt reads exactly %q:\n%s", "▸ ▏", scr)
		}
	})
}

// 2. One typed character shows exactly eight bullets and none of the
// typed text, with the cursor straight after them.
func TestSecretMask_OneTypedCharacterShowsEightBullets(t *testing.T) {
	secretMaskSurfaces(t, func(t *testing.T, rig pasteRig, fresh int) {
		rig.press(runeKey('Q'))
		requireMask(t, rig, fresh, "after one typed character")
		if scr := rig.screen(); strings.Contains(inputRowOf(scr), "Q") {
			t.Fatalf("the typed character shows on the row:\n%s", scr)
		}
		if got := rig.typed(); got != maskEight {
			t.Fatalf("the answer reads %q, want %q", got, maskEight)
		}
		// Typing more does not change the mask.
		rig.press(runeKey('r'))
		rig.press(runeKey('7'))
		requireMask(t, rig, fresh, "after three typed characters")
	})
}

// inputRowOf is the line of the screen that carries the cursor.
func inputRowOf(scr string) string {
	for _, line := range strings.Split(scr, "\n") {
		if strings.Contains(line, "▏") {
			return line
		}
	}
	return ""
}

// 3. A paste shows exactly eight bullets; neither the pasted text nor
// any four-character slice of it is on the screen; Enter still sends
// the whole text.
func TestSecretMask_PasteShowsEightBulletsAndNoneOfTheText(t *testing.T) {
	const pasted = "Zq7!vK2#mX9wRt4"
	secretMaskSurfaces(t, func(t *testing.T, rig pasteRig, fresh int) {
		rig.pasteIn(pasted)
		requireMask(t, rig, fresh, "after a paste")
		scr := rig.screen()
		for i := 0; i+4 <= len(pasted); i++ {
			if slice := pasted[i : i+4]; strings.Contains(scr, slice) {
				t.Fatalf("the screen shows the slice %q of the pasted text:\n%s", slice, scr)
			}
		}
		if got := rig.sent(); got != "" {
			t.Fatalf("the engine received %q before Enter", got)
		}
		rig.press(key(tea.KeyEnter))
		if got := rig.sent(); got != pasted+"\n" {
			t.Fatalf("Enter sent %q, want %q", got, pasted+"\n")
		}
	})
}

// A paste of spaces is content and shows the mask; a paste that is only
// control characters is dropped and shows none.
func TestSecretMask_PasteOfSpacesShowsMaskAndControlOnlyPasteDoesNot(t *testing.T) {
	secretMaskSurfaces(t, func(t *testing.T, rig pasteRig, fresh int) {
		rig.pasteIn("\x01\x02\x07")
		requireNoMask(t, rig, fresh, "after a paste of control characters only")
		rig.pasteIn("   ")
		requireMask(t, rig, fresh, "after a paste of spaces")
	})
}

// 4. A long answer, wider than a 40-column screen, still shows exactly
// eight bullets; the count never grows with the answer.
func TestSecretMask_LongAnswerAtWidth40StillShowsEightBullets(t *testing.T) {
	for _, name := range []string{"rotation", "configuration"} {
		t.Run(name, func(t *testing.T) {
			lr := longRigs(t, secretPrompt, 40)[name]
			fresh := bullets(lr.screen())
			answer := longAnswer()
			if len(answer) <= 40 {
				t.Fatalf("the long answer is only %d characters", len(answer))
			}
			lr.pasteIn(answer)
			requireMask(t, lr.pasteRig, fresh, "after a long paste at width 40")
			for _, c := range "abcdefghijklmnopqrstuvwxyz" {
				lr.press(runeKey(c))
			}
			requireMask(t, lr.pasteRig, fresh, "after typing on top of a long paste")
			lr.press(key(tea.KeyEnter))
			if got := lr.sent(); got != answer+"abcdefghijklmnopqrstuvwxyz\n" {
				t.Fatalf("Enter sent %q, want the whole long answer", got)
			}
		})
	}
}

// 5. Backspace down to empty removes every bullet; typing again brings
// back eight.
func TestSecretMask_BackspaceToEmptyRemovesTheMaskAndTypingRestoresIt(t *testing.T) {
	secretMaskSurfaces(t, func(t *testing.T, rig pasteRig, fresh int) {
		rig.press(runeKey('a'))
		rig.press(runeKey('b'))
		requireMask(t, rig, fresh, "after two characters")
		rig.press(bsKey)
		requireMask(t, rig, fresh, "after one Backspace (one character left)")
		rig.press(bsKey)
		requireNoMask(t, rig, fresh, "after erasing back to empty")
		if got := rig.typed(); got != "" {
			t.Fatalf("an emptied secret answer reads %q, want nothing", got)
		}
		rig.press(runeKey('c'))
		requireMask(t, rig, fresh, "after typing again")

		// The same after a paste: erase it all, mask gone; paste, mask back.
		rig.press(bsKey)
		requireNoMask(t, rig, fresh, "after erasing the last character")
		rig.pasteIn("xyz")
		requireMask(t, rig, fresh, "after a paste into the emptied answer")
		for range 3 {
			rig.press(bsKey)
		}
		requireNoMask(t, rig, fresh, "after erasing a pasted answer")
	})
}

// 6. Cursor movement and mid-line edits leave the screen byte-for-byte
// unchanged, and Enter still sends the edited answer.
func TestSecretMask_CursorMovesAndMidLineEditsLeaveTheScreenUnchanged(t *testing.T) {
	secretMaskSurfaces(t, func(t *testing.T, rig pasteRig, fresh int) {
		// On an empty answer, moving changes nothing and shows no mask.
		empty := rig.screen()
		for _, k := range []tea.KeyPressMsg{leftKey, rightKey, homeKey, endKey, ctrlA, ctrlE} {
			rig.press(k)
			if scr := rig.screen(); scr != empty {
				t.Fatalf("a cursor key changed the screen of an empty secret prompt:\nbefore:\n%s\nafter:\n%s", empty, scr)
			}
		}

		for _, c := range "abcd" {
			rig.press(runeKey(c))
		}
		atEnd := rig.screen()
		requireMask(t, rig, fresh, "after typing abcd")
		for _, mv := range []struct {
			name string
			k    tea.KeyPressMsg
		}{
			{"Left", leftKey}, {"Left again", leftKey}, {"Home", homeKey}, {"Right", rightKey},
			{"End", endKey}, {"Ctrl+A", ctrlA}, {"Ctrl+E", ctrlE},
		} {
			rig.press(mv.k)
			if scr := rig.screen(); scr != atEnd {
				t.Fatalf("%s changed the screen:\nbefore:\n%s\nafter:\n%s", mv.name, atEnd, scr)
			}
		}

		// Mid-line edits: a b ▏ c d, erase, delete, insert, then both ends.
		rig.press(leftKey)
		rig.press(leftKey)
		for _, step := range []struct {
			name string
			k    tea.KeyPressMsg
		}{
			{"Backspace mid-line", bsKey}, {"Delete mid-line", delKey}, {"typing mid-line", runeKey('X')},
			{"Home", homeKey}, {"typing at the start", runeKey('<')},
			{"End", endKey}, {"typing at the end", runeKey('>')},
		} {
			rig.press(step.k)
			if scr := rig.screen(); scr != atEnd {
				t.Fatalf("%s changed the screen:\nbefore:\n%s\nafter:\n%s", step.name, atEnd, scr)
			}
		}
		rig.press(key(tea.KeyEnter))
		if got := rig.sent(); got != "<aXd>\n" {
			t.Fatalf("Enter sent %q, want %q", got, "<aXd>\n")
		}
	})
}

// 7. A text-kind prompt never shows the mask: typed and pasted text
// shows as itself.
func TestSecretMask_TextPromptShowsNoMask(t *testing.T) {
	for _, name := range []string{"rotation", "configuration"} {
		t.Run(name, func(t *testing.T) {
			rig := pasteRigs(t, plainPrompt)[name]
			fresh := bullets(rig.screen())
			requireNoMask(t, rig, fresh, "on a fresh text prompt")
			rig.press(runeKey('h'))
			rig.press(runeKey('i'))
			requireNoMask(t, rig, fresh, "after typing into a text prompt")
			if got := rig.typed(); got != "hi" {
				t.Fatalf("the text prompt shows %q, want %q", got, "hi")
			}
			rig.pasteIn(" there")
			requireNoMask(t, rig, fresh, "after a paste into a text prompt")
			if got := rig.typed(); got != "hi there" {
				t.Fatalf("the text prompt shows %q, want %q", got, "hi there")
			}
		})
	}
}

// 8. The prompt session itself, drawn directly at every width including
// width 0 (not yet sized): the row is "▸ " + eight bullets + cursor
// whether the answer is one character or far wider than the screen,
// never truncated; an empty answer is the bare "▸ ▏"; and a second
// render pass is identical to the first. (The three secret goldens under
// testdata/c9 pin the colours; the builder rewrites them.)
func TestSecretMask_PromptSessionRowIsElevenCellsAtEveryWidthAndRendersTwiceTheSame(t *testing.T) {
	p := engine.Prompt{Field: "OIDC_CLIENT_SECRET", Kind: "secret", Attempt: 1, Required: true}
	render := func(s promptSession, w int) string {
		var b strings.Builder
		s.view(&b, "https://orbit.example.com", w, 0)
		return b.String()
	}
	rowOf := func(t *testing.T, raw string) string {
		t.Helper()
		for _, line := range strings.Split(ansi.Strip(raw), "\n") {
			if strings.Contains(line, "▸") {
				return strings.TrimRight(line, " ")
			}
		}
		t.Fatalf("no input row in:\n%s", ansi.Strip(raw))
		return ""
	}
	answers := map[string]string{
		"one character": "k",
		"a long answer": strings.Repeat("https://orbit.example.com/with/a/long/path/", 5),
		"spaces":        "    ",
	}
	for _, w := range []int{0, 20, 40, 80} {
		for name, answer := range answers {
			s := promptSession{prompt: &p, input: inputLine{runes: []rune(answer)}}
			first := render(s, w)
			if got := rowOf(t, first); got != "▸ "+maskEight+"▏" {
				t.Fatalf("width %d, %s: the row reads %q, want %q", w, name, got, "▸ "+maskEight+"▏")
			}
			if again := render(s, w); again != first {
				t.Fatalf("width %d, %s: a second render differs:\nfirst:\n%q\nsecond:\n%q", w, name, first, again)
			}
			if plain := ansi.Strip(first); strings.Count(plain, maskGlyph) != 8 {
				t.Fatalf("width %d, %s: the screen shows %d bullets, want 8:\n%s", w, name, strings.Count(plain, maskGlyph), plain)
			}
		}
		empty := promptSession{prompt: &p}
		first := render(empty, w)
		if got := rowOf(t, first); got != "▸ ▏" {
			t.Fatalf("width %d: an empty secret answer's row reads %q, want %q", w, got, "▸ ▏")
		}
		if strings.Contains(ansi.Strip(first), maskGlyph) {
			t.Fatalf("width %d: an empty secret answer shows a mask:\n%s", w, ansi.Strip(first))
		}
		if again := render(empty, w); again != first {
			t.Fatalf("width %d: a second render of the empty prompt differs", w)
		}
	}

	// A text prompt with the same answer never shows the mask.
	text := engine.Prompt{Field: "APP_URL", Kind: "url", Attempt: 1, Required: true}
	s := promptSession{prompt: &text, input: inputLine{runes: []rune("https://orbit.example.com")}}
	if got := render(s, 80); strings.Contains(ansi.Strip(got), maskGlyph) {
		t.Fatalf("a text prompt shows a mask:\n%s", ansi.Strip(got))
	}
}
