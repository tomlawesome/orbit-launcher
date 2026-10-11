package ui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// inputLine is one line of typed text, and the keys every typing
// surface shares: the development notice's phrase box, the
// configuration prompt and the rotation prompt (#211). It edits only;
// what Enter and Esc mean, and when typing is allowed at all, stay
// with each caller.
type inputLine struct{ runes []rune }

// lineEdit says what a key did to the line, so a caller with gates (the
// notice) can decide whether to keep the edit.
type lineEdit int

const (
	lineUntouched lineEdit = iota // not an editing key
	lineTyped                     // text was added
	lineErased                    // backspace (on an empty line, a no-op)
)

// edit applies one key press. Text is what bubbletea reports the key
// typed: it is empty for special keys and under Ctrl, Alt and the other
// non-Shift modifiers, so it is taken as it comes, with no modifier
// test of its own. The Space key is matched by its code, so it alone
// needs the modifier rule: Ctrl+Space (which bubbletea delivers with no
// Text) is a shortcut, not a space.
func (l inputLine) edit(msg tea.KeyPressMsg) (inputLine, lineEdit) {
	switch {
	case msg.Code == tea.KeyBackspace:
		if len(l.runes) > 0 {
			l.runes = l.runes[:len(l.runes)-1]
		}
		return l, lineErased
	case msg.Text != "":
		l.runes = append(l.runes, []rune(msg.Text)...)
		return l, lineTyped
	case msg.Code == tea.KeySpace && msg.Mod&^typingMods == 0:
		l.runes = append(l.runes, ' ')
		return l, lineTyped
	}
	return l, lineUntouched
}

// paste adds a bracketed paste (tea.PasteMsg) at the end of the line
// (#222). Line breaks and other control characters are removed, not
// replaced: a pasted line break must never act as Enter, and one line
// has nowhere to show a tab or an escape.
func (l inputLine) paste(text string) inputLine {
	for _, r := range text {
		if !unicode.IsControl(r) {
			l.runes = append(l.runes, r)
		}
	}
	return l
}

// typingMods are the modifiers that leave a key typing: Shift and the
// lock keys change what is typed, never whether.
const typingMods = tea.ModShift | tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock

// String is the line as typed.
func (l inputLine) String() string { return string(l.runes) }

// empty reports whether nothing is typed.
func (l inputLine) empty() bool { return len(l.runes) == 0 }

// tail is the last n characters, so a narrow box keeps the cursor end
// in view.
func (l inputLine) tail(n int) []rune {
	if len(l.runes) > n {
		return l.runes[len(l.runes)-n:]
	}
	return l.runes
}
