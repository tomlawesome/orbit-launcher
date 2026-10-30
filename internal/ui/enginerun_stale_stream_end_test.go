package ui

// #296: when guided configuration ends the launcher retries the
// installer on a new engine stream. The previous run's "stream ended"
// message can arrive after the retry is already streaming; it names the
// stream that ended (engineStreamEndedMsg.stream), and the run must not
// take it for the retry's own stream being cut. An ending that names
// the run's current stream, or none, is treated as cut as before.
//
// Written from the issue's specification alone, without the code. The
// helpers startedRun and runUpdate are in enginerun_model_test.go;
// stoppableStream and optionalWaitScript are in enginerun_stop_test.go
// and enginerun_stop_success_test.go; stopDied is in
// stopped_screen_test.go.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const engineStoppedUnsaid = "the engine stopped without reporting how the run finished"

// retriedOnASecondStream is retriedInstall (enginerun_stop_retry_line_test.go)
// that also returns the first run's stream, which that helper hides.
func retriedOnASecondStream(t *testing.T) (r engineRun, first *engine.Stream, cleaned *int) {
	t.Helper()
	cleaned = new(int)
	first = stoppableStream(t, optionalWaitScript)
	r = startedRun(t, "install", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: first, cleanup: func() error { *cleaned++; return nil }})
	if r.state != runStreaming {
		t.Fatalf("setup: first run state = %v, want runStreaming", r.state)
	}
	r, _ = runUpdate(t, r, engineStreamMsg{msg: clamavWaiting()})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: diedExit137.msg})
	if r.state != runFailed {
		t.Fatalf("setup: state after the installer died = %v, want runFailed:\n%s", r.state, runScreen(r))
	}
	if got := caretLabel(runScreen(r)); got != "Install again" {
		t.Fatalf("setup: the caret is on %q, want %q:\n%s", got, "Install again", runScreen(r))
	}
	r, _ = runUpdate(t, r, key(tea.KeyEnter))
	if r.state != runPreparing {
		t.Fatalf("setup: state after Enter on Install again = %v, want runPreparing", r.state)
	}
	second := stoppableStream(t, optionalWaitScript)
	r, _ = runUpdate(t, r, engineReadyMsg{stream: second, cleanup: func() error { *cleaned++; return nil }})
	if r.state != runStreaming {
		t.Fatalf("setup: the retried run's state = %v, want runStreaming:\n%s", r.state, runScreen(r))
	}
	if r.stream != second || r.stream == first {
		t.Fatalf("setup: the retried run is not on its own, second stream")
	}
	return r, first, cleaned
}

func TestEngineRun_AStaleStreamEndAfterTheRetryStartedIsIgnored(t *testing.T) {
	r, first, cleaned := retriedOnASecondStream(t)
	current := r.stream
	before := *cleaned
	screenBefore := runScreen(r)

	r, _ = runUpdate(t, r, engineStreamEndedMsg{stream: first})

	if r.state != runStreaming {
		t.Fatalf("state after the first stream's ending = %v, want runStreaming:\n%s", r.state, runScreen(r))
	}
	if r.stream != current {
		t.Errorf("the run's current stream changed after a stale ending")
	}
	if *cleaned != before {
		t.Errorf("the retried run's files were released by a stale ending (cleanup calls %d -> %d)", before, *cleaned)
	}
	screen := flat(runScreen(r))
	if strings.Contains(screen, stopDied) || strings.Contains(screen, engineStoppedUnsaid) {
		t.Errorf("a stale ending showed the stopped-without-saying outcome:\n%s", screen)
	}
	if got := flat(screenBefore); got != screen {
		t.Errorf("the screen changed on a stale ending:\nbefore:\n%s\nafter:\n%s", got, screen)
	}

	// Later events from the current stream are still handled.
	r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 0}})
	if !r.Done || !r.Succeeded {
		t.Fatalf("the current stream's success after a stale ending: Done=%v Succeeded=%v state=%v:\n%s", r.Done, r.Succeeded, r.state, runScreen(r))
	}
}

func TestEngineRun_TheCurrentStreamEndingStillCutsTheRun(t *testing.T) {
	cleaned := 0
	r := startedRun(t, "install", engineRunSeams{})
	s := &engine.Stream{C: make(chan any)}
	r, _ = runUpdate(t, r, engineReadyMsg{stream: s, cleanup: func() error { cleaned++; return nil }})
	if r.state != runStreaming || r.stream != s {
		t.Fatalf("setup: state = %v, want runStreaming on the ready stream", r.state)
	}

	r, _ = runUpdate(t, r, engineStreamEndedMsg{stream: r.stream})

	if cleaned != 1 {
		t.Errorf("staged script cleaned %d times, want once", cleaned)
	}
	screen := flat(runScreen(r))
	if r.state != runFailed {
		t.Fatalf("state after the current stream ended = %v, want runFailed:\n%s", r.state, screen)
	}
	if !strings.Contains(screen, stopDied) && !strings.Contains(screen, engineStoppedUnsaid) {
		t.Errorf("the cut run lacks its stopped-without-saying outcome:\n%s", screen)
	}
}
