// Package vtscreen is the one way test/pty and test/live read a program's
// screen: the program runs on a real pty, Charm's x/vt terminal emulator
// draws everything it writes there, and this package turns the emulator's
// rendered cells into plain text and waits, against a wall-clock deadline,
// for text to appear on it (#181).
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
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"
	"github.com/creack/pty"
)

// PollInterval is how often a wait looks at the screen.
const PollInterval = 50 * time.Millisecond

// Terminal is a pty with an emulator on its far end: what a program
// writes to the pty is drawn on the emulator's screen, and what the
// emulator answers (cursor position and colour queries) goes back in as
// the program's input, as a real terminal's would.
//
// It is built on x/vt directly rather than on Charm's vttest, which wraps
// the same emulator (#181): vttest pulls in a font renderer for a PNG
// feature these suites do not use, and its Snapshot and SendText lock the
// emulator in an order that deadlocks against a program that keeps
// redrawing — the first run of test/pty on it hung for ten minutes so.
type Terminal struct {
	emu  *vt.SafeEmulator
	pty  *os.File // the terminal's side
	tty  *os.File // the program's side
	once sync.Once
}

// New opens a cols x rows terminal. The pty is sized before any program
// starts on it, so a program's first size query sees the real size.
//
// If raw is not nil, every byte the program writes is copied to it too,
// before the emulator draws it: the exact stream, for a log. A failed
// write to raw is ignored, so a broken log cannot stop the screen.
func New(cols, rows int, raw io.Writer) (*Terminal, error) {
	ptmx, tty, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}
	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}); err != nil {
		_ = ptmx.Close()
		_ = tty.Close()
		return nil, fmt.Errorf("size pty: %w", err)
	}
	t := &Terminal{emu: vt.NewSafeEmulator(cols, rows), pty: ptmx, tty: tty}
	// The program's output, drawn. The emulator never stops reading,
	// whether or not anything is looking at the screen, so a program is
	// never held up by a full pty.
	var drawn io.Writer = t.emu
	if raw != nil {
		drawn = io.MultiWriter(ignoreErrors{raw}, t.emu)
	}
	go func() { _, _ = io.Copy(drawn, t.pty) }()
	// The emulator's answers, back to the program.
	go func() { _, _ = io.Copy(t.pty, t.emu) }()
	return t, nil
}

// ignoreErrors reports every write as whole, so io.MultiWriter carries on
// to the emulator whatever happens to the log.
type ignoreErrors struct{ w io.Writer }

func (e ignoreErrors) Write(p []byte) (int, error) {
	_, _ = e.w.Write(p)
	return len(p), nil
}

// Start runs cmd on the terminal. Stdin, stdout and stderr default to the
// pty; a stream the caller already set is left alone.
func (t *Terminal) Start(cmd *exec.Cmd) error {
	if cmd.Stdin == nil {
		cmd.Stdin = t.tty
	}
	if cmd.Stdout == nil {
		cmd.Stdout = t.tty
	}
	if cmd.Stderr == nil {
		cmd.Stderr = t.tty
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", cmd.Path, err)
	}
	return nil
}

// Output is the program's side of the pty, for a test that draws on the
// screen itself.
func (t *Terminal) Output() io.Writer {
	return t.tty
}

// Close shuts the terminal down. It does not wait for the copying to
// stop, so it cannot hang on an emulator stuck mid-write.
func (t *Terminal) Close() error {
	var err error
	t.once.Do(func() {
		// Closing the emulator's answer pipe first frees a write
		// blocked on it, and ends the copy reading it. That pipe is
		// closed directly, not through the emulator's Close, which sets
		// a flag the copy's reads check without a lock.
		if pw, ok := t.emu.InputPipe().(io.Closer); ok {
			_ = pw.Close()
		}
		err = t.pty.Close()
		if tErr := t.tty.Close(); err == nil {
			err = tErr
		}
	})
	return err
}

// Screen is term's screen as it is now, as plain text: one line per row,
// trailing spaces trimmed, styling dropped.
//
// It uses the emulator's Render, which takes the emulator's lock once for
// the whole screen. A cell-by-cell read takes it once per cell, and a
// program writing without pause (the starfield, or a tight echo loop)
// retakes it between every two cells, so one 120x40 read starves for
// seconds (#181). One lock also means a screen is never two frames mixed.
// Render's styling is stripped here.
func Screen(term *Terminal) string {
	lines := strings.Split(stripEscapes(term.emu.Render()), "\n")
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
// way a person's keystrokes arrive. It writes to the pty itself, not
// through the emulator, so typing never waits on the emulator's lock.
func Send(term *Terminal, text string) {
	_, _ = io.WriteString(term.pty, text)
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
func Wait(term *Terminal, within time.Duration, match Match) (string, error) {
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
