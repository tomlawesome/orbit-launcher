package engine

import "testing"

func TestParseEvent_ContractLine(t *testing.T) {
	e, ok := ParseEvent("phase=compose component=compose state=running reason=compose-validation action=validate elapsed=41s")
	if !ok {
		t.Fatal("expected a valid event")
	}
	want := Event{Phase: "compose", Component: "compose", State: "running", Reason: "compose-validation", Action: "validate", ElapsedSeconds: 41}
	if e != want {
		t.Fatalf("event = %+v, want %+v", e, want)
	}
}

func TestParseEvent_SimulationAndUnknownTrailingFields(t *testing.T) {
	e, ok := ParseEvent("phase=host component=host state=completed reason=host-tools action=check elapsed=3s simulation=true future=field")
	if !ok {
		t.Fatal("expected a valid event — unknown trailing key=value fields must be tolerated")
	}
	if !e.Simulation {
		t.Error("simulation=true not parsed")
	}
}

func TestParseEvent_UnknownEnumValuesCarriedVerbatim(t *testing.T) {
	// The contract guarantees unrecognised values surface as literal
	// tokens; the parser must carry them through for the UI to render
	// unstyled, never reject or rewrite them.
	e, ok := ParseEvent("phase=quantum component=flux state=oscillating reason=unknown action=undulate elapsed=7s")
	if !ok {
		t.Fatal("expected a valid event — unknown enum values are renderable, not rejected")
	}
	if e.Phase != "quantum" || e.State != "oscillating" {
		t.Fatalf("enum tokens rewritten: %+v", e)
	}
}

func TestParseEvent_MalformedElapsedIsZero(t *testing.T) {
	for _, elapsed := range []string{"elapsed=abc", "elapsed=12", "elapsed=s", "elapsed=-4s"} {
		e, ok := ParseEvent("phase=host component=host state=waiting reason=initial action=begin " + elapsed)
		if !ok {
			t.Fatalf("%s: expected a valid event", elapsed)
		}
		if e.ElapsedSeconds != 0 {
			t.Errorf("%s: ElapsedSeconds = %d, want 0", elapsed, e.ElapsedSeconds)
		}
	}
}

func TestParseEvent_RejectsProseAndPartialLines(t *testing.T) {
	for _, line := range []string{
		"",
		"Orbit is ready.",
		"Public URL: https://mail.example.com",
		"Orbit installer: configuration fields requiring attention: APP_URL.",
		"phase=host component=host state=waiting",                      // missing reason/action/elapsed
		"phase=host prose that merely starts with an event-like token", // bare words
	} {
		if _, ok := ParseEvent(line); ok {
			t.Errorf("%q: parsed as an event, want rejection", line)
		}
	}
}

func TestEvent_OutcomeHelpers(t *testing.T) {
	success := Event{Phase: "complete", Component: "installer", State: "completed", Reason: "deployment-ready", Action: "complete"}
	if !success.IsSuccess() || success.IsTerminalFailure() {
		t.Error("success event misclassified")
	}

	refusal := Event{Phase: "configuration", Component: "configuration", State: "failed", Reason: "configuration-failure", Action: "retry"}
	if !refusal.NeedsConfiguration() || !refusal.IsTerminalFailure() {
		t.Error("configuration refusal misclassified")
	}

	blocked := Event{Phase: "rollback", Component: "installer", State: "blocked", Reason: "repair-unavailable", Action: "repair"}
	if !blocked.IsTerminalFailure() || blocked.NeedsConfiguration() {
		t.Error("blocked event misclassified")
	}

	// A completed state outside the complete phase is progress, not
	// success — the contract keys success to the phase, and so must we.
	midway := Event{Phase: "assets", Component: "assets", State: "completed", Reason: "assets-verified", Action: "fetch"}
	if midway.IsSuccess() {
		t.Error("mid-run completed event misread as overall success")
	}
}
