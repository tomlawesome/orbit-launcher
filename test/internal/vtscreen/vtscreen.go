// Package vtscreen is the one way test/pty and test/live read a program's
// screen: Charm's vttest runs the program on a pty inside a virtual
// terminal, and this package turns that terminal's rendered cells into
// plain text and waits, against a wall-clock deadline, for text to appear
// on it (#181).
//
// It replaces go-expect, which matched the raw stream of bytes the program
// wrote. A stream carries a frame as a diff of the cells that changed, with
// escape sequences between them, so text could arrive split across writes
// or never be written whole at all; the screen holds it whole wherever it
// is drawn.
//
// What a screen cannot show is text that was drawn and then overdrawn
// between two looks. Every wait here is for something that stays on screen
// until the program is asked to move on, or for a stage the program shows
// for longer than PollInterval; a test that needs to see a brief stage has
// to give it time on screen.
//
// Test support only: nothing in the launcher imports it.
package vtscreen

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/vttest"
)

// PollInterval is how often a wait looks at the screen.
const PollInterval = 50 * time.Millisecond

// Screen is term's screen as it is now, as plain text: one line per row,
// trailing spaces trimmed, styling dropped.
//
// It reads the emulator directly rather than through vttest's
// Terminal.Snapshot, for two reasons found running this suite (#181):
//
//   - Snapshot holds the Terminal's own mutex while it reads cells under
//     the emulator's lock, and the emulator's callbacks (cursor moves,
//     modes) take that same mutex while a write holds the emulator's
//     lock. A snapshot taken while the program draws deadlocks both, and
//     Terminal.Close after them: the first run of test/pty on vttest hung
//     for ten minutes exactly so.
//   - Snapshot, like any cell-by-cell read, takes the emulator's lock once
//     per cell. A program writing without pause (the starfield, or a
//     tight echo loop) retakes the lock between every two cells, so one
//     120x40 read starves for seconds.
//
// Render takes the emulator's lock once for the whole screen, which also
// means a screen is never two frames mixed. Its styling is stripped here.
func Screen(term *vttest.Terminal) string {
	lines := strings.Split(stripEscapes(term.Emulator.Render()), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// stripEscapes drops the escape sequences Render writes: CSI (styles),
// OSC (hyperlinks), and any other ESC sequence. Every byte of
// an escape sequence is ASCII and every byte of a multi-byte UTF-8
// character is not, so a byte scan cannot cut a character in half.
func stripEscapes(s string) string {
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
		case '[': // CSI: parameters, then one final byte in 0x40-0x7e
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

// Send types text into term as raw input, escape sequences and all, the
// way a person's keystrokes arrive. Like Screen it goes to the emulator
// directly: Terminal.SendText takes the Terminal's mutex before the
// emulator's lock, the order Snapshot deadlocks on.
func Send(term *vttest.Terminal, text string) {
	term.Emulator.SendText(text)
}

// Match decides whether a screen is the one being waited for.
type Match func(screen string) bool

// ContainsAny matches a screen showing at least one of texts.
func ContainsAny(texts ...string) Match {
	return func(screen string) bool {
		for _, text := range texts {
			if strings.Contains(screen, text) {
				return true
			}
		}
		return false
	}
}

// ContainsAll matches a screen showing every one of texts at once.
func ContainsAll(texts ...string) Match {
	return func(screen string) bool {
		for _, text := range texts {
			if !strings.Contains(screen, text) {
				return false
			}
		}
		return true
	}
}

// TimeoutError is a wait that ran out of time. Screen is the last screen
// the wait saw, which is usually the most useful thing a failure can say.
type TimeoutError struct {
	Within time.Duration
	Screen string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("no match within %s of wall clock; last screen:\n%s", e.Within, e.Screen)
}

// Wait looks at term's screen every PollInterval until match accepts it,
// and returns that screen. After within it gives up with a *TimeoutError.
//
// The deadline is kept outside the looking: a snapshot takes the
// emulator's lock, and an emulator stuck writing a reply into a pty
// nobody reads would hold it for ever. The wait still ends on time; only
// the goroutine taking the snapshot is left behind, until the terminal is
// closed.
func Wait(term *vttest.Terminal, within time.Duration, match Match) (string, error) {
	type look struct {
		screen string
		ok     bool
	}
	looks := make(chan look, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		ticker := time.NewTicker(PollInterval)
		defer ticker.Stop()
		for {
			screen := Screen(term)
			ok := match(screen)
			select {
			case <-looks: // keep only the latest look
			default:
			}
			looks <- look{screen: screen, ok: ok}
			if ok {
				return
			}
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
		}
	}()

	timer := time.NewTimer(within)
	defer timer.Stop()
	var last string
	for {
		select {
		case l := <-looks:
			last = l.screen
			if l.ok {
				return l.screen, nil
			}
		case <-timer.C:
			select {
			case l := <-looks:
				last = l.screen
				if l.ok {
					return l.screen, nil
				}
			default:
			}
			return "", &TimeoutError{Within: within, Screen: last}
		}
	}
}
