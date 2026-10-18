package ui

import (
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	xansi "github.com/charmbracelet/x/ansi"
)

// inputLine is one line of typed text, and the keys every typing
// surface shares: the development notice's phrase box, the
// configuration prompt and the rotation prompt (#211). It edits only;
// what Enter and Esc mean, and when typing is allowed at all, stay
// with each caller.
//
// The cursor (#258) moves, erases and inserts by grapheme cluster, so a
// combined character or emoji sequence is never split (#223).
type inputLine struct {
	runes []rune
	// back counts the runes after the cursor. Counting from the end
	// keeps the zero value, and a line built from its text alone, with
	// the cursor at the end, where typing has always gone.
	back int
}

// lineEdit says what a key did to the line, so a caller with gates (the
// notice) can decide whether to keep the edit.
type lineEdit int

const (
	lineUntouched lineEdit = iota // not an editing key
	lineTyped                     // text was added
	lineErased                    // backspace or delete (at a line end, a no-op)
	lineMoved                     // the cursor moved (or was already there)
)

// edit applies one key press. Text is what bubbletea reports the key
// typed: it is empty for special keys and under Ctrl, Alt and the other
// non-Shift modifiers, so it is taken as it comes, with no modifier
// test of its own. The Space key is matched by its code, so it alone
// needs the modifier rule: Ctrl+Space (which bubbletea delivers with no
// Text) is a shortcut, not a space. Home and End are here for the
// prompts; the notice keeps them for scrolling and never passes them
// on, which is why Ctrl+A and Ctrl+E jump too.
func (l inputLine) edit(msg tea.KeyPressMsg) (inputLine, lineEdit) {
	at := l.at()
	switch {
	case msg.Code == tea.KeyBackspace:
		if to, ok := l.stop(at, -1); ok {
			l = l.splice(to, at, nil)
		}
		return l, lineErased
	case msg.Code == tea.KeyDelete:
		if to, ok := l.stop(at, +1); ok {
			l = l.splice(at, to, nil)
		}
		return l, lineErased
	case msg.Code == tea.KeyLeft:
		if to, ok := l.stop(at, -1); ok {
			l.back = len(l.runes) - to
		}
		return l, lineMoved
	case msg.Code == tea.KeyRight:
		if to, ok := l.stop(at, +1); ok {
			l.back = len(l.runes) - to
		}
		return l, lineMoved
	case msg.Code == tea.KeyHome || ctrlChord(msg, 'a'):
		l.back = len(l.runes)
		return l, lineMoved
	case msg.Code == tea.KeyEnd || ctrlChord(msg, 'e'):
		l.back = 0
		return l, lineMoved
	case msg.Text != "":
		return l.splice(at, at, []rune(msg.Text)), lineTyped
	case msg.Code == tea.KeySpace && msg.Mod&^typingMods == 0:
		return l.splice(at, at, []rune{' '}), lineTyped
	}
	return l, lineUntouched
}

// ctrlChord reports whether msg is Ctrl plus the letter c (lock keys
// and Shift aside, as typingMods sets them aside for typing).
func ctrlChord(msg tea.KeyPressMsg, c rune) bool {
	return msg.Code == c && msg.Mod&^typingMods == tea.ModCtrl
}

// paste inserts a bracketed paste (tea.PasteMsg) at the cursor (#222).
// Line breaks and other control characters are removed, not replaced:
// a pasted line break must never act as Enter, and one line has
// nowhere to show a tab or an escape.
func (l inputLine) paste(text string) inputLine {
	var kept []rune
	for _, r := range text {
		if !unicode.IsControl(r) {
			kept = append(kept, r)
		}
	}
	at := l.at()
	return l.splice(at, at, kept)
}

// typingMods are the modifiers that leave a key typing: Shift and the
// lock keys change what is typed, never whether.
const typingMods = tea.ModShift | tea.ModCapsLock | tea.ModNumLock | tea.ModScrollLock

// String is the line as typed, wherever the cursor is.
func (l inputLine) String() string { return string(l.runes) }

// empty reports whether nothing is typed.
func (l inputLine) empty() bool { return len(l.runes) == 0 }

// at is the cursor as a rune index from the start.
func (l inputLine) at() int { return len(l.runes) - min(max(l.back, 0), len(l.runes)) }

// splice replaces runes [from, to) with ins and leaves the cursor after
// ins. It builds a new slice: a caller may drop the result (the notice
// does, while its gates are shut), and the old line must not change
// under it.
func (l inputLine) splice(from, to int, ins []rune) inputLine {
	out := make([]rune, 0, len(l.runes)-(to-from)+len(ins))
	out = append(append(append(out, l.runes[:from]...), ins...), l.runes[to:]...)
	l.runes, l.back = out, len(l.runes)-to
	// Inserted text can join the cluster after it (a letter before a
	// combining mark); step past the joined cluster rather than sit
	// inside it.
	if at := l.at(); !l.isStop(at) {
		to, _ := l.stop(at, +1)
		l.back = len(l.runes) - to
	}
	return l
}

// stops are the rune indexes between grapheme clusters, both ends
// included: the only places the cursor may sit.
func (l inputLine) stops() []int {
	s := string(l.runes)
	stops := []int{0}
	for i, r := 0, 0; i < len(s); {
		cluster, _ := xansi.FirstGraphemeCluster(s[i:], xansi.GraphemeWidth)
		i += len(cluster)
		r += utf8.RuneCountInString(cluster)
		stops = append(stops, r)
	}
	return stops
}

func (l inputLine) isStop(at int) bool {
	for _, s := range l.stops() {
		if s == at {
			return true
		}
	}
	return false
}

// stop is the nearest stop before (dir -1) or after (dir +1) at; ok is
// false at that end of the line.
func (l inputLine) stop(at, dir int) (int, bool) {
	stops := l.stops()
	if dir < 0 {
		for k := len(stops) - 1; k >= 0; k-- {
			if stops[k] < at {
				return stops[k], true
			}
		}
		return at, false
	}
	for _, s := range stops {
		if s > at {
			return s, true
		}
	}
	return at, false
}

// around is the part of the line that fits in n cells, split at the
// cursor, so a narrow row keeps the cursor in view (#223): as much
// before the cursor as fits, less some room kept for the text after it,
// then what still fits after it. With the
// cursor at the end it is the line's tail. It cuts between grapheme
// clusters and measures them as lipgloss.Width does, the walk
// truncateStyled makes from the other end: a wide character takes two
// cells and is never split.
func (l inputLine) around(n int) (before, after string) {
	s := string(l.runes)
	cut := len(string(l.runes[:l.at()])) // the cursor as a byte offset
	var starts, widths []int
	for i := 0; i < len(s); {
		cluster, w := xansi.FirstGraphemeCluster(s[i:], xansi.GraphemeWidth)
		starts, widths = append(starts, i), append(widths, w)
		i += len(cluster)
	}
	k0 := len(starts) // the first cluster after the cursor
	for k, st := range starts {
		if st >= cut {
			k0 = k
			break
		}
	}
	// With text after the cursor, keep room for some of it (up to a
	// quarter of the row), so moving left on a wide line shows what
	// follows the cursor instead of pinning it to the right edge.
	room := n
	if k0 < len(starts) {
		afterCells := 0
		for _, w := range widths[k0:] {
			afterCells += w
		}
		room = n - min(afterCells, n/4)
	}
	from, cells := cut, 0
	for k := k0 - 1; k >= 0 && cells+widths[k] <= room; k-- {
		cells += widths[k]
		from = starts[k]
	}
	to := cut
	for k := k0; k < len(starts) && cells+widths[k] <= n; k++ {
		cells += widths[k]
		to = len(s)
		if k+1 < len(starts) {
			to = starts[k+1]
		}
	}
	return s[from:cut], s[cut:to]
}
