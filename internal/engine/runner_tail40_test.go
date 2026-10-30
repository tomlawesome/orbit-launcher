package engine

import "testing"

// #261 item 4: the stop message can carry a whole instruction block, so
// the engine keeps the last 40 stderr lines, not 12.
func TestStream_StderrTailKeepsTheLast40Lines(t *testing.T) {
	s := startScript(t, `
for i in $(seq 1 50); do echo "stderr line $i" >&2; done
exit 2
`)
	_, _, done := collect(t, s)

	if len(done.StderrTail) != 40 {
		t.Fatalf("len(StderrTail) = %d, want 40", len(done.StderrTail))
	}
	if got := done.StderrTail[0]; got != "stderr line 11" {
		t.Errorf("first kept line = %q, want %q", got, "stderr line 11")
	}
	if got := done.StderrTail[39]; got != "stderr line 50" {
		t.Errorf("last kept line = %q, want %q", got, "stderr line 50")
	}
}
