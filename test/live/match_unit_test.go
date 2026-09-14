package live

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	expect "github.com/Netflix/go-expect"
)

// TestTailMatcher_FindsMarkerAcrossLooks is the matcher's contract: a
// marker is found whether it arrives whole, one byte at a time, or
// straddling the point the previous look stopped at.
func TestTailMatcher_FindsMarkerAcrossLooks(t *testing.T) {
	m := &tailMatcher{markers: []string{"Get into Orbit", "Installation stopped"}}
	var buf bytes.Buffer
	if m.Match(&buf) {
		t.Fatal("matched an empty buffer")
	}
	filler := strings.Repeat("│ ⠋ application starting        │\r\n", 40)
	for _, r := range filler + "Get into" {
		buf.WriteRune(r)
		if m.Match(&buf) {
			t.Fatalf("matched before the marker was complete, at %q", buf.String())
		}
	}
	buf.WriteString(" Orbit")
	if !m.Match(&buf) {
		t.Fatal("marker completed across two looks was not found")
	}
	if !m.Match(&buf) {
		t.Fatal("a marker inside the overlap window must still match")
	}
	m = &tailMatcher{markers: []string{"Installation stopped"}}
	buf.Reset()
	buf.WriteString(filler)
	buf.WriteString("Installation stopped")
	buf.WriteString(filler)
	if !m.Match(&buf) {
		t.Fatal("a marker already in the buffer on the first look was not found")
	}
}

// TestExpectAny_DrainsBigOutputInLinearTime reproduces #159 / #165 in
// the small: a program repaints frames faster than a rescanning matcher
// can read them, so the marker that has long since been printed is never
// reached. It drives go-expect's real pty exactly as the live harness
// does — one rune at a time, one Match per rune — through some 400 KB
// of launcher-shaped frames and then the success marker, and requires
// the marker within a budget an O(n) drain clears in well under a
// second. Measured with expect.Regexp in place of expectAny: the budget
// expires with the process CPU-bound in the matcher; the real harness on
// a 170 KB run took 616 s to reach the success screen the launcher had
// shown at about 120 s.
func TestExpectAny_DrainsBigOutputInLinearTime(t *testing.T) {
	console, err := expect.NewConsole()
	if err != nil {
		t.Fatalf("pty: %v", err)
	}
	defer console.Close()

	// One frame is the shape the mission console paints on every engine
	// event: a boxed, padded, spinner-carrying screen a little over 2 KB.
	var frame strings.Builder
	frame.WriteString("\x1b[H\x1b[2J")
	for range 30 {
		fmt.Fprintf(&frame, "\x1b[38;5;69m│\x1b[0m ⠋ application starting %-60s │\r\n", "")
	}
	const frames = 120
	go func() {
		tty := console.Tty()
		for range frames {
			if _, err := tty.WriteString(frame.String()); err != nil {
				return
			}
		}
		_, _ = tty.WriteString("Get into Orbit\r\n")
	}()

	type outcome struct {
		out string
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		out, err := console.Expect(expectAny("Get into Orbit", "Installation stopped"))
		done <- outcome{out, err}
	}()
	const budget = 60 * time.Second
	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("expect: %v", got.err)
		}
		if !strings.Contains(got.out, "Get into Orbit") {
			t.Fatalf("returned output does not carry the marker; ends %q", got.out[max(len(got.out)-80, 0):])
		}
	case <-time.After(budget):
		t.Fatalf("the marker after %d frames (%d bytes) was not reached within %s: the harness cannot keep up with the program", frames, frames*frame.Len(), budget)
	}
}
