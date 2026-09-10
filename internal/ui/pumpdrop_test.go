package ui

import (
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// Issue #159: the engine pump is a single token. Each stream message is
// fetched by a command that must hand back a fresh one, so any path
// through handleStream that returns nil while the run is still going
// ends the run for good — the engine finishes, posts DoneMsg, exits, and
// nothing ever reads it. Under ORBIT_LAUNCHER_NO_ANIMATION there is no
// tick chain either, so nothing is left to wake the program and the
// screen freezes with the install complete underneath it.
//
// These tests pin the invariant rather than any one drop path: while the
// run is streaming, consuming a stream message must always leave a pump
// armed.

func streamingRun(t *testing.T) engineRun {
	t.Helper()
	return engineRun{
		state:   runStreaming,
		stream:  &engine.Stream{C: make(chan any, 1)},
		console: newConsole("Install", "test", time.Now),
	}
}

// TestEngineRun_AKnownStreamMessageKeepsThePumpArmed is the ordinary
// case, stated so the invariant is not left implicit.
func TestEngineRun_AKnownStreamMessageKeepsThePumpArmed(t *testing.T) {
	for _, tc := range []struct {
		name string
		msg  any
	}{
		{"event", engine.EventMsg{Event: engine.Event{Phase: "database", State: "starting"}}},
		{"raw line", engine.RawLineMsg{Text: "Container orbit-postgres Started"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, cmd := streamingRun(t).update(engineStreamMsg{msg: tc.msg})
			if cmd == nil {
				t.Fatal("no pump armed: the run would never receive another engine message")
			}
		})
	}
}

// TestEngineRun_AnUnknownStreamMessageKeepsThePumpArmed is the defect.
// Before the fix, handleStream's type switch fell through to
// `return r, nil` for anything it did not recognise, and the run stopped
// there — silently, with no failure screen and no error.
func TestEngineRun_AnUnknownStreamMessageKeepsThePumpArmed(t *testing.T) {
	type somethingElse struct{ Text string }

	_, cmd := streamingRun(t).update(engineStreamMsg{msg: somethingElse{Text: "not a contract message"}})
	if cmd == nil {
		t.Fatal("an unrecognised stream message ended the run: no pump armed, so DoneMsg can never arrive (#159)")
	}
}

// TestEngineRun_ANilStreamMessageKeepsThePumpArmed covers the same hole
// via a nil payload, which no type case matches either.
func TestEngineRun_ANilStreamMessageKeepsThePumpArmed(t *testing.T) {
	_, cmd := streamingRun(t).update(engineStreamMsg{msg: nil})
	if cmd == nil {
		t.Fatal("a nil stream message ended the run: no pump armed (#159)")
	}
}

// TestEngineRun_DoneStopsThePump is the other half: when the run really
// is over the pump must stop, or the model would read a closed channel
// forever. Every terminal path moves the state off runStreaming first,
// which is exactly what the re-arm keys off.
func TestEngineRun_DoneStopsThePump(t *testing.T) {
	run := streamingRun(t)
	run.detect = fakeDetect("https://done.example.test")

	after, cmd := run.update(engineStreamMsg{msg: engine.DoneMsg{}})
	if after.state != runSucceeded {
		t.Errorf("state = %v, want runSucceeded", after.state)
	}
	if cmd != nil {
		t.Error("the pump was re-armed after the run finished; it would read a closed channel forever")
	}
}
