// Package ansi holds the one terminal escape-sequence stripper the
// product and its tests share (#214, #199 SU-2).
package ansi

import "strings"

// Strip drops every terminal escape sequence from s: CSI (colours, but
// also erase, cursor and private-mode sequences, to any final byte), OSC
// (hyperlinks, titles; ended by BEL or ST) and any other ESC sequence.
// Every byte of an escape sequence is ASCII and every byte of a
// multi-byte UTF-8 character is not, so a byte scan cannot cut a
// character in half.
func Strip(s string) string {
	if !strings.ContainsRune(s, 0x1b) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] != 0x1b {
			b.WriteByte(s[i])
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI: parameters and intermediates, then one final byte in 0x40-0x7e
			for i++; i < len(s) && (s[i] < 0x40 || s[i] > 0x7e); i++ {
			}
		case ']': // OSC: ends at BEL or ST (ESC \)
			for i++; i < len(s); i++ {
				if s[i] == 0x07 {
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i++
					break
				}
			}
		default: // other ESC sequences: intermediates 0x20-0x2f, then a final byte
			for ; i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f; i++ {
			}
		}
	}
	return b.String()
}
