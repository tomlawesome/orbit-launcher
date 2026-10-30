package engine

// #294: Event.AfterCommit says whether an event comes from after the
// installer's commit, the point past which an interrupted Update rolls
// forward instead of undoing anything. Orbit's contract
// (docs/engine-events.md in Orbit, "install.sh after the commit"): the
// first event after the commit is phase=compose state=completed
// reason=compose-validation; every preparation, database, application,
// optional and complete event follows it. A compose failure, and every
// earlier event, is before the commit.
//
// Written from the issue alone, without the code.

import "testing"

func TestEvent_AfterCommit(t *testing.T) {
	for _, c := range []struct {
		name string
		e    Event
		want bool
	}{
		{"host waiting", Event{Phase: "host", Component: "host", State: "waiting", Reason: "initial", Action: "begin"}, false},
		{"host completed", Event{Phase: "host", Component: "host", State: "completed", Reason: "host-tools", Action: "check"}, false},
		{"host failed", Event{Phase: "host", Component: "host", State: "failed", Reason: "docker-host", Action: "retry"}, false},
		{"configuration running", Event{Phase: "configuration", Component: "configuration", State: "running", Reason: "configuration-validation", Action: "validate"}, false},
		{"configuration completed", Event{Phase: "configuration", Component: "configuration", State: "completed", Reason: "configuration-validation", Action: "validate"}, false},
		{"compose running", Event{Phase: "compose", Component: "compose", State: "running", Reason: "compose-validation", Action: "validate"}, false},
		{"compose failed", Event{Phase: "compose", Component: "compose", State: "failed", Reason: "docker-host", Action: "retry"}, false},
		{"compose completed (the commit)", Event{Phase: "compose", Component: "compose", State: "completed", Reason: "compose-validation", Action: "validate"}, true},
		{"preparation image pull", Event{Phase: "preparation", Component: "image", State: "running", Reason: "image-pull", Action: "pull"}, true},
		{"preparation compose up starting", Event{Phase: "preparation", Component: "compose", State: "starting", Reason: "service-start", Action: "start"}, true},
		{"preparation failed", Event{Phase: "preparation", Component: "compose", State: "failed", Reason: "failure", Action: "repair"}, true},
		{"database running", Event{Phase: "database", Component: "database", State: "running", Reason: "database-health", Action: "health"}, true},
		{"application starting", Event{Phase: "application", Component: "application", State: "starting", Reason: "application-health", Action: "start"}, true},
		{"application failed", Event{Phase: "application", Component: "application", State: "failed", Reason: "failure", Action: "repair"}, true},
		{"optional waiting", Event{Phase: "optional", Component: "clamav", State: "waiting", Reason: "optional-status", Action: "continue"}, true},
		{"complete", Event{Phase: "complete", Component: "installer", State: "completed", Reason: "deployment-ready", Action: "complete"}, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := c.e.AfterCommit(); got != c.want {
				t.Errorf("AfterCommit() of %+v = %v, want %v", c.e, got, c.want)
			}
		})
	}
}

// The same, through the parser, for the contract's own commit line.
func TestEvent_AfterCommitOfTheParsedCommitLine(t *testing.T) {
	for line, want := range map[string]bool{
		"phase=compose component=compose state=completed reason=compose-validation action=validate elapsed=41s":                  true,
		"phase=compose component=compose state=failed reason=docker-host action=retry elapsed=41s":                               false,
		"phase=preparation component=compose state=starting reason=service-start action=start elapsed=42s":                       true,
		"phase=configuration component=configuration state=completed reason=configuration-validation action=validate elapsed=9s": false,
	} {
		e, ok := ParseEvent(line)
		if !ok {
			t.Fatalf("not an event line: %q", line)
		}
		if got := e.AfterCommit(); got != want {
			t.Errorf("AfterCommit() of %q = %v, want %v", line, got, want)
		}
	}
}
