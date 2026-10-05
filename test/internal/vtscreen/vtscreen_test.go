package vtscreen

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// draw writes s to term's pty from the program's side, as a program
// running on it would.
func draw(t *testing.T, term *Terminal, s string) {
	t.Helper()
	if _, err := io.WriteString(term.Output(), s); err != nil {
		t.Fatalf("write to pty: %v", err)
	}
}

func newTerminal(t *testing.T) *Terminal {
	t.Helper()
	term, err := New(120, 40, nil)
	if err != nil {
		t.Fatalf("create virtual terminal: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })
	return term
}

// TestScreenWait_FindsTextSplitAcrossRedraws is the case the byte stream
// could not match (#181): a host name drawn in pieces, with styling and
// a cursor move elsewhere between them, the way a diff renderer paints
// a styled, centred line. test/live's Remove subtest could not assert
// the splash's FQDN because of it. The screen holds the text whole once
// the last piece lands, and not before.
func TestScreenWait_FindsTextSplitAcrossRedraws(t *testing.T) {
	term := newTerminal(t)
	const host = "orbit-live-test-42.internal"

	draw(t, term, "\x1b[2J\x1b[5;40H\x1b[38;5;69morbit-live\x1b[0m")
	_, err := Wait(term, 300*time.Millisecond, ContainsAny(host))
	var timeout *TimeoutError
	if !errors.As(err, &timeout) {
		t.Fatalf("half-drawn host: err = %v, want a *TimeoutError", err)
	}
	if !strings.Contains(timeout.Screen, "orbit-live") {
		t.Errorf("timeout's last screen = %q, want the half-drawn host", timeout.Screen)
	}

	draw(t, term, "\x1b[1;1H\x1b[1m·\x1b[0m\x1b[5;50H-test-42\x1b[38;5;69m.internal\x1b[0m")
	screen, err := Wait(term, 5*time.Second, ContainsAll(host, "·"))
	if err != nil {
		t.Fatalf("host completed across two writes was not found: %v", err)
	}
	if got := strings.Split(screen, "\n")[4]; strings.TrimSpace(got) != host {
		t.Errorf("row 5 = %q, want just %q", got, host)
	}
}

// TestScreenWait_KeepsUpWithFastRepaints is #159 / #165 in the small:
// a program repaints launcher-shaped frames far faster than anyone
// reads them, then shows the success marker. go-expect's rescanning
// matcher once fell so far behind that output that a finished install
// looked frozen. A screen wait looks at one screen whatever came
// before it, so the marker must be found well inside the budget.
func TestScreenWait_KeepsUpWithFastRepaints(t *testing.T) {
	term := newTerminal(t)

	// One frame is the shape the mission console paints on every engine
	// event: a boxed, padded, spinner-carrying screen a little over 2 KB.
	var frame strings.Builder
	frame.WriteString("\x1b[H\x1b[2J")
	for range 30 {
		fmt.Fprintf(&frame, "\x1b[38;5;69m│\x1b[0m ⠋ application starting %-60s │\r\n", "")
	}
	const frames = 120
	go func() {
		for range frames {
			if _, err := io.WriteString(term.Output(), frame.String()); err != nil {
				return
			}
		}
		_, _ = io.WriteString(term.Output(), "Get into Orbit\r\n")
	}()

	const budget = 60 * time.Second
	start := time.Now()
	screen, err := Wait(term, budget, ContainsAny("Get into Orbit", "Installation stopped"))
	if err != nil {
		t.Fatalf("the marker after %d frames (%d bytes) was not reached: %v", frames, frames*frame.Len(), err)
	}
	if !strings.Contains(screen, "Get into Orbit") {
		t.Fatalf("returned screen does not carry the marker:\n%s", screen)
	}
	t.Logf("marker found after %s", time.Since(start).Round(time.Millisecond))
}

// TestStripEscapes covers each kind of sequence the emulator's Render
// writes, next to multi-byte text that must come through whole.
func TestStripEscapes(t *testing.T) {
	cases := map[string]string{
		"\x1b[1;38;5;69m▸ Install\x1b[m":                          "▸ Install",
		"\x1b]8;;https://orbit.example\x1b\\link\x1b]8;;\x1b\\ ✓": "link ✓",
		"\x1b]0;title\x07after":                                   "after",
		"a\x1b(Bb":                                                "ab",
		"trailing\x1b":                                            "trailing",
	}
	for in, want := range cases {
		if got := stripEscapes(in); got != want {
			t.Errorf("stripEscapes(%q) = %q, want %q", in, got, want)
		}
	}
}

// syncBuffer is a log the emulator's copy and the test can share.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// The raw log gets the exact stream, escape sequences and all, that the
// screen shows drawn.
func TestNew_CopiesTheRawStreamToTheLog(t *testing.T) {
	var log syncBuffer
	term, err := New(80, 24, &log)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })

	const stream = "\x1b[2J\x1b[3;5H\x1b[1mlogged\x1b[0m"
	draw(t, term, stream)
	if _, err := Wait(term, 5*time.Second, ContainsAny("logged")); err != nil {
		t.Fatalf("screen: %v", err)
	}
	if got := log.String(); got != stream {
		t.Errorf("log = %q, want the raw stream %q", got, stream)
	}
}

type brokenLog struct{}

func (brokenLog) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// A log that fails every write cannot stop the screen being drawn.
func TestNew_BrokenLogDoesNotStopTheScreen(t *testing.T) {
	term, err := New(80, 24, brokenLog{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })

	draw(t, term, "first ")
	draw(t, term, "second")
	if _, err := Wait(term, 5*time.Second, ContainsAll("first second")); err != nil {
		t.Fatalf("screen after a failing log: %v", err)
	}
}

// A program started on the terminal sees a terminal of the size asked
// for -- sized before it starts -- draws on the screen, and reads what
// Send types, as a person's keystrokes.
func TestStart_RunsAProgramOnTheTerminal(t *testing.T) {
	term, err := New(100, 30, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = term.Close() })

	cmd := exec.Command("sh", "-c", `echo "size $(stty size)"; read line; echo "got:$line"`)
	if err := term.Start(cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if _, err := Wait(term, 10*time.Second, ContainsAny("size 30 100")); err != nil {
		t.Fatalf("program did not see a 100x30 terminal: %v", err)
	}
	Send(term, "typed\r")
	if _, err := Wait(term, 10*time.Second, ContainsAny("got:typed")); err != nil {
		t.Fatalf("program did not read what was sent: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("program exited with %v", err)
	}
}

// A stream the caller already set is left alone: output sent elsewhere
// never reaches the screen.
func TestStart_LeavesPresetStreamsAlone(t *testing.T) {
	term := newTerminal(t)
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("sh", "-c", "read line; echo out:$line; echo err >&2")
	cmd.Stdin = strings.NewReader("given\n")
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := term.Start(cmd); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("program: %v", err)
	}
	if stdout.String() != "out:given\n" || stderr.String() != "err\n" {
		t.Errorf("stdout %q, stderr %q; want the preset streams used", stdout.String(), stderr.String())
	}
	draw(t, term, "marker")
	screen, err := Wait(term, 5*time.Second, ContainsAny("marker"))
	if err != nil {
		t.Fatalf("screen: %v", err)
	}
	if strings.Contains(screen, "out:") || strings.Contains(screen, "err") {
		t.Errorf("preset streams' output reached the screen:\n%s", screen)
	}
}

func TestStart_ReportsAProgramThatCannotStart(t *testing.T) {
	term := newTerminal(t)
	missing := filepath.Join(t.TempDir(), "no-such-program")
	err := term.Start(exec.Command(missing))
	if err == nil || !strings.Contains(err.Error(), "start "+missing) {
		t.Fatalf("err = %v, want one naming the program", err)
	}
}

// Close can be called again -- a test's own Close and its cleanup's --
// and the second call is a no-op, not an error from closing twice.
func TestClose_IsIdempotent(t *testing.T) {
	term, err := New(80, 24, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := term.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := term.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := io.WriteString(term.Output(), "x"); err == nil {
		t.Error("the program's side still takes writes after Close")
	}
}

// A timeout says how long it waited and what the screen showed last.
func TestTimeoutError_NamesTheBudgetAndTheScreen(t *testing.T) {
	err := &TimeoutError{Within: 1500 * time.Millisecond, Screen: "Installing…"}
	want := "no match within 1.5s of wall clock; last screen:\nInstalling…"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

// A wait ends on time even while a look at the screen is stuck: the
// deadline is kept outside the looking.
func TestWait_EndsOnTimeWhileALookIsStuck(t *testing.T) {
	term := newTerminal(t)
	release := make(chan struct{})
	defer close(release)
	stuck := func(string) bool { <-release; return true }

	start := time.Now()
	_, err := Wait(term, 200*time.Millisecond, stuck)
	var timeout *TimeoutError
	if !errors.As(err, &timeout) || timeout.Within != 200*time.Millisecond {
		t.Fatalf("err = %v, want a *TimeoutError for 200ms", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("wait took %s, want it to end near its 200ms deadline", took)
	}
}
