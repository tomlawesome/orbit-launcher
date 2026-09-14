package ui

import (
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// Issue #159: the engine stream used to be drained by a chain of
// one-shot commands, each message fetched by a command that had to hand
// back a fresh one. That chain held a single token, and any path that
// failed to pass it on stopped the run for good — the engine finished,
// posted DoneMsg, exited, and nothing ever read it. With no tick chain
// under ORBIT_LAUNCHER_NO_ANIMATION, nothing was left to wake the
// program and the screen froze with the install complete underneath.
//
// A long-lived reader holds no token to lose. These tests pin that:
// delivery does not depend on what the model makes of any message, and
// a stream that stops early is reported rather than swallowed.

func streamingRun(t *testing.T) engineRun {
	t.Helper()
	return engineRun{
		state:   runStreaming,
		stream:  &engine.Stream{C: make(chan any, 1)},
		console: newConsole("Install", "test", time.Now),
	}
}

// TestEngineStreamReader_DeliversEveryMessageThenSaysTheStreamEnded is
// the defect, stated as the invariant that replaces it. Two of these
// payloads — an unrecognised type and a nil — are ones handleStream has
// no case for. Under the old chain either ended the run silently.
// Delivery no longer asks the model's opinion, so both go through and
// DoneMsg behind them still arrives.
func TestEngineStreamReader_DeliversEveryMessageThenSaysTheStreamEnded(t *testing.T) {
	type somethingElse struct{ Text string }

	stream := &engine.Stream{C: make(chan any, 4)}
	delivered := make(chan tea.Msg, 8)

	cmd := readEngineStream(func(msg tea.Msg) { delivered <- msg }, stream)
	if cmd == nil {
		t.Fatal("no reader started: the run would never receive an engine message")
	}
	cmd()

	stream.C <- engine.EventMsg{Event: engine.Event{Phase: "database", State: "starting"}}
	stream.C <- somethingElse{Text: "not a contract message"}
	stream.C <- nil
	stream.C <- engine.DoneMsg{}
	close(stream.C)

	for i := 1; i <= 4; i++ {
		select {
		case msg := <-delivered:
			if _, ok := msg.(engineStreamMsg); !ok {
				t.Fatalf("message %d was %T, want engineStreamMsg", i, msg)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("message %d never arrived: delivery stopped part-way through the stream", i)
		}
	}

	select {
	case msg := <-delivered:
		if _, ok := msg.(engineStreamEndedMsg); !ok {
			t.Fatalf("after the channel closed the reader sent %T, want engineStreamEndedMsg", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the reader never reported the stream ending, so a stalled run would look identical to a finished one")
	}
}

// TestEngineRun_AStreamThatStopsMidRunFailsVisibly covers the freeze
// itself: the engine stopping without ever reporting an outcome. Sitting
// on the running screen forever is the one response that tells the
// operator nothing.
func TestEngineRun_AStreamThatStopsMidRunFailsVisibly(t *testing.T) {
	after, _ := streamingRun(t).update(engineStreamEndedMsg{})

	if after.state != runFailed {
		t.Errorf("state = %v, want runFailed: a run nobody can report on has not succeeded", after.state)
	}
	if after.runErr == nil {
		t.Error("the run failed with no error, so the failure screen has nothing to show")
	}
}

// TestEngineRun_AStreamEndingAfterTheRunResolvedChangesNothing is the
// ordinary case, and the one that must not be mistaken for the above:
// DoneMsg resolved the run and the channel closed behind it.
func TestEngineRun_AStreamEndingAfterTheRunResolvedChangesNothing(t *testing.T) {
	run := streamingRun(t)
	run.state = runFailed
	run.runErr = errors.New("the engine said why")

	after, cmd := run.update(engineStreamEndedMsg{})

	if after.runErr.Error() != "the engine said why" {
		t.Errorf("runErr = %v, want the engine's own error kept", after.runErr)
	}
	if cmd != nil {
		t.Error("the closed stream armed something; there is nothing left to read")
	}
}

// TestEngineRun_ARunWithNoSenderSaysSoRatherThanWaiting guards the
// plumbing. A flow built without a sender cannot receive engine output
// at all, and the failure this whole change removes is precisely a run
// that waits in silence for output that cannot arrive.
func TestEngineRun_ARunWithNoSenderSaysSoRatherThanWaiting(t *testing.T) {
	cmd := readEngineStream(nil, &engine.Stream{C: make(chan any)})
	if cmd == nil {
		t.Fatal("a run with no sender started nothing and would wait forever")
	}

	ended, ok := cmd().(engineStreamEndedMsg)
	if !ok {
		t.Fatalf("a run with no sender produced %T, want engineStreamEndedMsg", cmd())
	}
	if ended.reason == nil {
		t.Error("the run ended with no reason, so the operator is told nothing")
	}
}

// TestEngineRun_DoneStopsTheRun is the other half: when the run really
// is over, consuming DoneMsg resolves it and arms nothing.
func TestEngineRun_DoneStopsTheRun(t *testing.T) {
	run := streamingRun(t)
	run.detect = fakeDetect("https://done.example.test")

	after, cmd := run.update(engineStreamMsg{msg: engine.DoneMsg{}})
	if after.state != runSucceeded {
		t.Errorf("state = %v, want runSucceeded", after.state)
	}
	if cmd != nil {
		t.Error("the finished run armed a command; there is nothing left to read")
	}
}
