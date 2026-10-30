package engine

// v0.4.3-c audit Low C1 (issue #277). This file does not compile until
// RawLineMsg has the new field the specification names:
//
//	type RawLineMsg struct {
//		Text      string
//		Truncated bool // the reader cut the line to 16 KiB
//	}
//
// It is kept apart so that the failure is recorded in one place.

import (
	"strings"
	"testing"
	"time"
)

// lowsCRawLines drains the stream and returns every RawLineMsg.
func lowsCRawLines(t *testing.T, s *Stream) []RawLineMsg {
	t.Helper()
	var raws []RawLineMsg
	timeout := time.After(10 * time.Second)
	for {
		select {
		case msg, ok := <-s.C:
			if !ok {
				return raws
			}
			if m, ok := msg.(RawLineMsg); ok {
				raws = append(raws, m)
			}
		case <-timeout:
			t.Fatal("stream did not finish in time")
		}
	}
}

// C1 (#277): a line longer than 16 KiB is delivered cut to 16 KiB with
// Truncated true; a normal line, and one exactly 16 KiB long (nothing
// cut), have it false.
func TestLowsC_C1_RawLineMsgSaysWhetherTheLineWasCut(t *testing.T) {
	s := startScript(t, `printf 'a normal line\n'
printf '%0.sx' $(seq 1 16384); printf '\n'
printf '%0.sy' $(seq 1 20000); printf '\n'
printf 'after\n'
exit 0`)
	raws := lowsCRawLines(t, s)
	if len(raws) != 4 {
		t.Fatalf("raw lines = %d, want 4", len(raws))
	}
	if raws[0].Text != "a normal line" || raws[0].Truncated {
		t.Errorf("normal line = %q truncated=%v, want not truncated", raws[0].Text, raws[0].Truncated)
	}
	if len(raws[1].Text) != 16*1024 || raws[1].Truncated {
		t.Errorf("exactly 16 KiB line: len %d truncated=%v, want 16384 and not truncated (nothing was cut)", len(raws[1].Text), raws[1].Truncated)
	}
	if len(raws[2].Text) != 16*1024 || strings.Trim(raws[2].Text, "y") != "" || !raws[2].Truncated {
		t.Errorf("over-long line: len %d truncated=%v, want 16384 and truncated", len(raws[2].Text), raws[2].Truncated)
	}
	if raws[3].Text != "after" || raws[3].Truncated {
		t.Errorf("line after the cut one = %q truncated=%v, want not truncated", raws[3].Text, raws[3].Truncated)
	}
}
