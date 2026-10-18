// Package ansi holds the one terminal escape-sequence stripper the
// product and its tests share (#214, #199 SU-2).
package ansi

import "strings"

const esc = 0x1b

// Strip drops every terminal escape sequence from s and returns valid
// UTF-8: CSI (colours, but also erase, cursor and private-mode
// sequences, to any final byte), OSC (hyperlinks, titles; ended by BEL
// or ST), the DCS, APC, PM and SOS control strings (ended by ST), and
// every other ESC sequence.
//
// Malformed input loses only the escape itself, never the text: an ESC
// before a byte that cannot follow one (another ESC, a control, a
// non-ASCII character) is dropped alone; a CSI or other sequence that
// meets a byte that cannot be part of it ends there, and that byte
// (a newline, a character) is kept; an ESC inside a control string
// that is not its ST starts the next sequence. A sequence's bytes are
// all ASCII, so no character is ever cut in half; bytes that were not
// valid UTF-8 to begin with become U+FFFD.
func Strip(s string) string {
	if strings.IndexByte(s, esc) < 0 {
		return strings.ToValidUTF8(s, "�")
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		j := strings.IndexByte(s[i:], esc)
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+j])
		i = skipEscape(s, i+j)
	}
	return strings.ToValidUTF8(b.String(), "�")
}

// skipEscape returns the index just past the escape sequence that
// starts with the ESC at s[i].
func skipEscape(s string, i int) int {
	i++ // the ESC
	if i >= len(s) {
		return i
	}
	switch c := s[i]; {
	case c == '[': // CSI: parameter and intermediate bytes, then a final byte
		i++
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
			i++
		}
		if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
			i++
		}
		return i
	case c == ']', c == 'P', c == '_', c == '^', c == 'X': // OSC, DCS, APC, PM, SOS
		bel := c == ']' // only OSC may end at BEL
		for i++; i < len(s); i++ {
			switch {
			case bel && s[i] == 0x07:
				return i + 1
			case s[i] == esc && i+1 < len(s) && s[i+1] == '\\':
				return i + 2
			case s[i] == esc:
				return i // not ST: the next sequence starts here
			}
		}
		return i
	case c >= 0x20 && c <= 0x2f: // intermediates, then a final byte
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
			i++
		}
		return i
	case c >= 0x30 && c <= 0x7e: // a two-byte sequence: ESC 7, ESC =, ESC c
		return i + 1
	default: // ESC, a control, DEL or a non-ASCII byte: drop the ESC alone
		return i
	}
}
