package ui

// Pins #258 (text fields edit at a cursor). Every text field built on
// the shared input line — the development notice's phrase box, the
// in-console configuration prompt and Repair's rotation prompt — edits
// like a shell prompt: Left/Right move one character (one grapheme
// cluster, never half of one), Home/End and Ctrl+A/Ctrl+E jump to the
// ends, Backspace erases before the cursor and Delete at it, typing and
// pasting insert at the cursor, and Enter submits the whole line
// wherever the cursor is. The cursor is drawn where it is, except in a
// secret prompt, which shows neither the text nor where the cursor sits.
//
// Decisions for this work: on the notice screen Up/Down and Home/End
// keep scrolling the notice, so in the phrase box Left/Right move and
// Ctrl+A/Ctrl+E jump; everywhere else Home/End and Ctrl+A/Ctrl+E both
// jump. Secret prompts edit at the cursor blind.
//
// The tests read the field off the rendered screen: the text before and
// after the "▏" cursor on the input row. A cursor in the middle of the
// line is drawn between the two characters it sits between ("ab▏c").

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// Keys this file presses, spelled once.
var (
	leftKey  = key(tea.KeyLeft)
	rightKey = key(tea.KeyRight)
	homeKey  = key(tea.KeyHome)
	endKey   = key(tea.KeyEnd)
	bsKey    = key(tea.KeyBackspace)
	delKey   = key(tea.KeyDelete)
	ctrlA    = tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
	ctrlE    = tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}
)

// Grapheme clusters of more than one rune: "e" plus a combining acute
// accent, and a woman-technologist emoji (woman, zero-width joiner,
// laptop). Each must move, erase and draw as one character.
const (
	combinedE = "e\u0301"
	zwjEmoji  = "\U0001F469\u200D\U0001F4BB"
)

// cursorRowParts reads the input row of a rendered screen: the text
// between the row's marker and the "▏" cursor, and the text after the
// cursor up to the box border or the blank space that follows the
// answer. It fails the test if no row carries a cursor.
func cursorRowParts(t *testing.T, scr string) (before, after string) {
	t.Helper()
	for _, line := range strings.Split(scr, "\n") {
		at := strings.Index(line, "▏")
		if at < 0 {
			continue
		}
		before = inputRowText(line)
		after = line[at+len("▏"):]
		if i := strings.Index(after, "│"); i >= 0 {
			after = after[:i]
		}
		if i := strings.Index(after, "  "); i >= 0 {
			after = after[:i]
		}
		return before, strings.TrimRight(after, " ")
	}
	t.Fatalf("no row of the screen carries the cursor:\n%s", scr)
	return "", ""
}

// cursorRig is one text field seen from outside: keys go in, the screen
// comes out.
type cursorRig struct {
	press  func(tea.KeyPressMsg)
	screen func() string
}

func (r cursorRig) keys(ks ...tea.KeyPressMsg) {
	for _, k := range ks {
		r.press(k)
	}
}

// typeRunes types s one key per rune (ASCII test text only).
func (r cursorRig) typeRunes(s string) {
	for _, c := range s {
		r.press(runeKey(c))
	}
}

// expectRow fails unless the input row reads before▏after.
func (r cursorRig) expectRow(t *testing.T, before, after, what string) {
	t.Helper()
	gotB, gotA := cursorRowParts(t, r.screen())
	if gotB != before || gotA != after {
		t.Fatalf("%s: the row reads %q▏%q, want %q▏%q", what, gotB, gotA, before, after)
	}
}

func asCursorRig(p pasteRig) cursorRig { return cursorRig{press: p.press, screen: p.screen} }

// noticeCursorRig is the notice's phrase box, awake so letters type.
func noticeCursorRig(t *testing.T) cursorRig {
	m := awakeNotice(t, engineRunSeams{})
	return cursorRig{
		press:  func(k tea.KeyPressMsg) { m, _ = updateAs(t, m, k) },
		screen: func() string { return screen(m) },
	}
}

// everyField runs body on the notice's phrase box and on both engine
// prompts (plain text).
func everyField(t *testing.T, body func(t *testing.T, rig cursorRig)) {
	t.Helper()
	t.Run("notice", func(t *testing.T) { body(t, noticeCursorRig(t)) })
	for _, name := range []string{"rotation", "configuration"} {
		t.Run(name, func(t *testing.T) { body(t, asCursorRig(pasteRigs(t, plainPrompt)[name])) })
	}
}

// ---------------------------------------------------------------------------
// Moving.
// ---------------------------------------------------------------------------

// Left and Right move one character, the cursor is drawn where it is,
// and moving past either end is harmless.
func TestInputCursor_LeftRightMoveOneCharacter(t *testing.T) {
	everyField(t, func(t *testing.T, rig cursorRig) {
		rig.typeRunes("abc")
		rig.expectRow(t, "abc", "", "after typing abc")

		rig.press(leftKey)
		rig.expectRow(t, "ab", "c", "one Left")
		rig.press(leftKey)
		rig.expectRow(t, "a", "bc", "two Lefts")
		rig.press(rightKey)
		rig.expectRow(t, "ab", "c", "Right after two Lefts")

		rig.keys(rightKey, rightKey, rightKey)
		rig.expectRow(t, "abc", "", "Right past the end")
		rig.keys(leftKey, leftKey, leftKey, leftKey, leftKey)
		rig.expectRow(t, "", "abc", "Left past the start")
	})
}

// Left and Right step over a combined character and an emoji joiner
// sequence whole, never stopping inside one.
func TestInputCursor_MovementNeverSplitsAGraphemeCluster(t *testing.T) {
	everyField(t, func(t *testing.T, rig cursorRig) {
		rig.press(runeKey('a'))
		rig.press(tea.KeyPressMsg{Text: combinedE})
		rig.press(tea.KeyPressMsg{Text: zwjEmoji})
		rig.press(runeKey('b'))
		rig.expectRow(t, "a"+combinedE+zwjEmoji+"b", "", "after typing")

		rig.press(leftKey)
		rig.expectRow(t, "a"+combinedE+zwjEmoji, "b", "Left over b")
		rig.press(leftKey)
		rig.expectRow(t, "a"+combinedE, zwjEmoji+"b", "Left over the emoji sequence")
		rig.press(leftKey)
		rig.expectRow(t, "a", combinedE+zwjEmoji+"b", "Left over the combined e")
		rig.press(rightKey)
		rig.expectRow(t, "a"+combinedE, zwjEmoji+"b", "Right over the combined e")
		rig.press(rightKey)
		rig.expectRow(t, "a"+combinedE+zwjEmoji, "b", "Right over the emoji sequence")
	})
}

// Backspace and Delete erase a whole grapheme cluster, as movement steps
// over one.
func TestInputCursor_BackspaceAndDeleteEraseAWholeGraphemeCluster(t *testing.T) {
	everyField(t, func(t *testing.T, rig cursorRig) {
		rig.press(runeKey('a'))
		rig.press(tea.KeyPressMsg{Text: combinedE})
		rig.press(tea.KeyPressMsg{Text: zwjEmoji})
		rig.press(runeKey('b'))

		rig.press(leftKey) // a é 👩‍💻 ▏ b
		rig.press(bsKey)
		rig.expectRow(t, "a"+combinedE, "b", "Backspace after the emoji sequence")

		rig.press(leftKey) // a ▏ é b
		rig.press(delKey)
		rig.expectRow(t, "a", "b", "Delete on the combined e")
	})
}

// On the engine prompts Home/End and Ctrl+A/Ctrl+E all jump to the
// start and end of the line. (The notice keeps Home/End for scrolling;
// see the notice test below.)
func TestInputCursor_HomeEndAndCtrlAEJumpOnThePrompts(t *testing.T) {
	for _, name := range []string{"rotation", "configuration"} {
		t.Run(name, func(t *testing.T) {
			rig := asCursorRig(pasteRigs(t, plainPrompt)[name])
			rig.typeRunes("abc")
			rig.press(leftKey)

			rig.press(homeKey)
			rig.expectRow(t, "", "abc", "Home")
			rig.press(endKey)
			rig.expectRow(t, "abc", "", "End")
			rig.press(ctrlA)
			rig.expectRow(t, "", "abc", "Ctrl+A")
			rig.press(ctrlE)
			rig.expectRow(t, "abc", "", "Ctrl+E")
		})
	}
}

// ---------------------------------------------------------------------------
// Editing in the middle of the line.
// ---------------------------------------------------------------------------

// Backspace erases the character before the cursor and Delete the one
// at it; at the start and the end of the line respectively they do
// nothing.
func TestInputCursor_BackspaceAndDeleteEditMidLine(t *testing.T) {
	everyField(t, func(t *testing.T, rig cursorRig) {
		rig.typeRunes("abcd")
		rig.keys(leftKey, leftKey)
		rig.expectRow(t, "ab", "cd", "two Lefts")

		rig.press(bsKey)
		rig.expectRow(t, "a", "cd", "Backspace mid-line")
		rig.press(delKey)
		rig.expectRow(t, "a", "d", "Delete mid-line")

		rig.press(leftKey)
		rig.press(bsKey)
		rig.expectRow(t, "", "ad", "Backspace at the start")

		rig.keys(rightKey, rightKey)
		rig.press(delKey)
		rig.expectRow(t, "ad", "", "Delete at the end")
	})
}

// Typing in the middle of the line inserts at the cursor, which stays
// after what was typed; Space and a multi-rune Text do the same.
func TestInputCursor_TypingMidLineInserts(t *testing.T) {
	everyField(t, func(t *testing.T, rig cursorRig) {
		rig.typeRunes("ad")
		rig.press(leftKey)
		rig.typeRunes("bc")
		rig.expectRow(t, "abc", "d", "typing b, c before d")

		rig.press(key(tea.KeySpace))
		rig.expectRow(t, "abc ", "d", "Space mid-line")

		rig.press(tea.KeyPressMsg{Text: "xy"})
		rig.expectRow(t, "abc xy", "d", "a multi-rune Text mid-line")
	})
}

// A paste into the middle of an answer inserts there, with line breaks
// and control characters removed, and never submits. (The notice
// ignores pastes; #222.)
func TestInputCursor_PasteMidLineInserts(t *testing.T) {
	pasteSurfaces(t, func(t *testing.T, p pasteRig) {
		rig := asCursorRig(p)
		p.pasteIn("https://.org")
		rig.keys(leftKey, leftKey, leftKey, leftKey)
		rig.expectRow(t, "https://", ".org", "four Lefts")

		p.pasteIn("orbit\r\n.exam\tple\x1b\n")
		rig.expectRow(t, "https://orbit.example", ".org", "a paste mid-line")
		if got := p.sent(); got != "" {
			t.Fatalf("a paste mid-line sent %q to the engine", got)
		}

		rig.press(key(tea.KeyEnter))
		if got := p.sent(); got != "https://orbit.example.org\n" {
			t.Fatalf("Enter sent %q, want %q", got, "https://orbit.example.org\n")
		}
	})
}

// ---------------------------------------------------------------------------
// Enter.
// ---------------------------------------------------------------------------

// Enter submits the whole line, wherever the cursor is.
func TestInputCursor_EnterSubmitsTheWholeLineWhereverTheCursorIs(t *testing.T) {
	for _, where := range []struct {
		name string
		keys []tea.KeyPressMsg
	}{
		{"cursor at the start", []tea.KeyPressMsg{homeKey}},
		{"cursor mid-line", []tea.KeyPressMsg{leftKey, leftKey}},
		{"cursor at the end", nil},
	} {
		t.Run(where.name, func(t *testing.T) {
			pasteSurfaces(t, func(t *testing.T, p pasteRig) {
				rig := asCursorRig(p)
				rig.typeRunes("ok 1")
				rig.keys(where.keys...)
				rig.press(key(tea.KeyEnter))
				if got := p.sent(); got != "ok 1\n" {
					t.Fatalf("Enter sent %q, want %q", got, "ok 1\n")
				}
			})
		})
	}

	t.Run("notice with the cursor mid-phrase", func(t *testing.T) {
		calls := 0
		m := awakeNotice(t, engineRunSeams{
			prepareEngine: func(ctx context.Context, dir, action string) (*engine.Stream, func() error, error) {
				calls++
				return fakeEngine(nil, successStream()...)(ctx, dir, action)
			},
		})
		m = typeText(t, m, noticePhrase)
		for range 5 {
			m, _ = updateAs(t, m, leftKey)
		}
		if b, a := cursorRowParts(t, screen(m)); b+a != noticePhrase || a == "" {
			t.Fatalf("the box reads %q▏%q, want the phrase with the cursor inside it", b, a)
		}
		updated, cmd := m.Update(key(tea.KeyEnter))
		m = updated.(InstallModel)
		if m.state != installStateRunning || cmd == nil {
			t.Fatalf("Enter with the cursor mid-phrase left state %v, want installStateRunning", m.state)
		}
		cmd()
		if calls != 1 {
			t.Fatalf("the engine was prepared %d times, want once", calls)
		}
	})
}

// ---------------------------------------------------------------------------
// Drawing.
// ---------------------------------------------------------------------------

// The cursor is drawn at its position in a visible field, on the row's
// own marker: "▸ " on the prompts, the box border on the notice.
func TestInputCursor_IsDrawnAtItsPosition(t *testing.T) {
	cases := map[string]struct {
		rig  func(t *testing.T) cursorRig
		want string
	}{
		"notice":        {noticeCursorRig, "│ ab▏c"},
		"rotation":      {func(t *testing.T) cursorRig { return asCursorRig(pasteRigs(t, plainPrompt)["rotation"]) }, "▸ ab▏c"},
		"configuration": {func(t *testing.T) cursorRig { return asCursorRig(pasteRigs(t, plainPrompt)["configuration"]) }, "▸ ab▏c"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rig := c.rig(t)
			rig.typeRunes("abc")
			rig.press(leftKey)
			if scr := rig.screen(); !strings.Contains(scr, c.want) {
				t.Fatalf("the screen does not draw %q:\n%s", c.want, scr)
			}
		})
	}
}

// A secret prompt edits at the cursor (Enter sends the edited answer)
// but the screen gives away neither the text nor where the cursor is:
// moving the cursor changes nothing on screen.
func TestInputCursor_SecretPromptShowsNeitherTextNorCursorPosition(t *testing.T) {
	for _, name := range []string{"rotation", "configuration"} {
		t.Run(name, func(t *testing.T) {
			p := pasteRigs(t, secretPrompt)[name]
			rig := asCursorRig(p)
			rig.typeRunes("abcd")
			atEnd := rig.screen()

			for _, mv := range []struct {
				name string
				k    tea.KeyPressMsg
			}{
				{"Left", leftKey}, {"Left again", leftKey}, {"Home", homeKey}, {"Right", rightKey},
				{"End", endKey}, {"Ctrl+A", ctrlA}, {"Ctrl+E", ctrlE},
			} {
				rig.press(mv.k)
				if scr := rig.screen(); scr != atEnd {
					t.Fatalf("%s changed the screen of a secret prompt (the cursor's place shows):\nbefore:\n%s\nafter:\n%s", mv.name, atEnd, scr)
				}
			}

			// Now at the end: a b c d ▏
			rig.keys(leftKey, leftKey) // a b ▏ c d
			rig.press(bsKey)           // a ▏ c d
			rig.press(delKey)          // a ▏ d
			rig.press(runeKey('X'))    // a X ▏ d
			rig.press(homeKey)
			rig.press(runeKey('<'))
			rig.press(endKey)
			rig.press(runeKey('>'))

			scr := rig.screen()
			for _, leak := range []string{"<aXd>", "aXd", "abcd", "X"} {
				if strings.Contains(scr, leak) {
					t.Fatalf("the secret prompt shows %q:\n%s", leak, scr)
				}
			}
			if got := p.sent(); got != "" {
				t.Fatalf("the engine received %q before Enter", got)
			}
			rig.press(key(tea.KeyEnter))
			if got := p.sent(); got != "<aXd>\n" {
				t.Fatalf("Enter sent %q, want %q (the edits were made at the cursor)", got, "<aXd>\n")
			}
		})
	}
}

// ---------------------------------------------------------------------------
// The notice: Home/End scroll, Ctrl+A/Ctrl+E and Left/Right edit.
// ---------------------------------------------------------------------------

// With the phrase box in focus, Home and End still scroll the notice and
// leave the cursor alone; Ctrl+A/Ctrl+E jump and Left/Right move in the
// box and leave the scroll alone.
func TestInputCursor_NoticeHomeEndScrollWhileCtrlAEAndArrowsEdit(t *testing.T) {
	m := awakeNotice(t, engineRunSeams{})
	maxOff := m.notice.maxOffset()
	if maxOff == 0 {
		t.Fatal("the notice does not scroll at 80×26, so Home/End cannot be told apart")
	}
	m = typeText(t, m, "abc")
	row := func(before, after, what string) {
		t.Helper()
		b, a := cursorRowParts(t, screen(m))
		if b != before || a != after {
			t.Fatalf("%s: the box reads %q▏%q, want %q▏%q", what, b, a, before, after)
		}
	}
	row("abc", "", "after typing abc")

	m, _ = updateAs(t, m, homeKey)
	if m.notice.offset != 0 {
		t.Fatalf("Home with the box in focus: offset = %d, want 0 (Home scrolls the notice)", m.notice.offset)
	}
	row("abc", "", "Home")

	m, _ = updateAs(t, m, endKey)
	if m.notice.offset != maxOff {
		t.Fatalf("End with the box in focus: offset = %d, want %d (End scrolls the notice)", m.notice.offset, maxOff)
	}
	row("abc", "", "End")

	m, _ = updateAs(t, m, ctrlA)
	row("", "abc", "Ctrl+A")
	m, _ = updateAs(t, m, rightKey)
	row("a", "bc", "Right")
	m, _ = updateAs(t, m, ctrlE)
	row("abc", "", "Ctrl+E")
	m, _ = updateAs(t, m, leftKey)
	row("ab", "c", "Left")
	if m.notice.offset != maxOff {
		t.Fatalf("editing keys moved the notice: offset = %d, want %d", m.notice.offset, maxOff)
	}

	m = typeText(t, m, "X")
	if got := m.notice.typed.String(); got != "abXc" {
		t.Fatalf("typing after the moves left %q, want %q", got, "abXc")
	}
}
