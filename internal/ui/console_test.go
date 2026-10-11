package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

// visibleWidth counts terminal cells, ignoring ANSI styling, the way
// the product measures (a wide character is two cells).
func visibleWidth(row string) int {
	return lipgloss.Width(row)
}

func testConsole() ConsoleModel {
	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	c := newConsole("Install — Standard", "v9.9.9", func() time.Time { return base })
	return c.setSize(80, 26)
}

func TestConsole_StageWordTracksFurthestPhase(t *testing.T) {
	c := testConsole()
	if got := c.stageWord(); got != "Contacting the engine" {
		t.Errorf("initial stage word = %q", got)
	}

	c = c.observeEvent(ev("host", "host", "completed", "host-tools", "check").Event)
	if got := c.stageWord(); got != "Checking host" {
		t.Errorf("stage word = %q, want Checking host", got)
	}

	c = c.observeEvent(ev("application", "application", "starting", "application-health", "start").Event)
	if got := c.stageWord(); got != "Starting Orbit" {
		t.Errorf("stage word = %q, want Starting Orbit", got)
	}

	// A later event for an earlier phase must never regress the bar —
	// the engine may emit per-component detail out of phase order.
	c = c.observeEvent(ev("host", "host", "completed", "host-tools", "check").Event)
	if got := c.stageWord(); got != "Starting Orbit" {
		t.Errorf("stage word regressed to %q", got)
	}
}

func TestConsole_RollbackTakesOverTheStageWord(t *testing.T) {
	c := testConsole()
	c = c.observeEvent(ev("database", "database", "failed", "database-auth-migration", "repair").Event)
	c = c.observeEvent(ev("rollback", "installer", "running", "rollback", "rollback").Event)
	if got := c.stageWord(); got != "Rolling back" {
		t.Errorf("stage word = %q, want Rolling back", got)
	}
}

func TestConsole_LegacyProseGetsWorkingStageWord(t *testing.T) {
	c := testConsole()
	c = c.observeRaw("Pulling ghcr.io/tomlawesome/orbit:latest")
	if got := c.stageWord(); got != "Working" {
		t.Errorf("stage word = %q, want Working for a legacy engine", got)
	}
}

func TestConsole_UnknownEnumValuesRenderLiterally(t *testing.T) {
	// The contract's renderable-but-unstyled rule: tokens this build
	// doesn't know show literally — never a guess, never a crash.
	c := testConsole()
	c = c.observeEvent(ev("hyperspace", "installer", "running", "unknown", "continue").Event)
	if got := c.stageWord(); got != "hyperspace" {
		t.Errorf("stage word = %q, want the literal unknown phase token", got)
	}

	c = c.observeEvent(ev("assets", "assets", "oscillating", "unknown", "continue").Event)
	if !strings.Contains(c.view(80, 26), "oscillating") {
		t.Error("an unknown state token must render literally")
	}

	// A known phase then moves the stage word forward again.
	c = c.observeEvent(ev("compose", "compose", "running", "compose-validation", "validate").Event)
	if got := c.stageWord(); got != "Validating services" {
		t.Errorf("stage word = %q, want Validating services after a known phase", got)
	}
}

func TestConsole_ViewRendersFrameEventsBarAndClock(t *testing.T) {
	c := testConsole()
	c = c.observeEvent(ev("host", "host", "completed", "host-tools", "check").Event)
	c = c.observeEvent(ev("identity", "image", "running", "image-identity", "inspect").Event)
	view := c.view(80, 26)

	for _, want := range []string{"╭", "╰", "ORBIT", "Install — Standard", "0:00", "image", "running", "host", "done", "Resolving image identity", "v9.9.9"} {
		if !strings.Contains(view, want) {
			t.Errorf("console view missing %q", want)
		}
	}
	if strings.Contains(view, "%") {
		t.Error("the stage bar must never show a percentage")
	}

	for _, row := range strings.Split(view, "\n") {
		if w := visibleWidth(row); w > 80 {
			t.Fatalf("console row wider than the terminal: %d cells", w)
		}
	}
}

func TestConsole_SimulationEventsAreLabelled(t *testing.T) {
	c := testConsole()
	e := ev("host", "host", "completed", "host-tools", "check").Event
	e.Simulation = true
	c = c.observeEvent(e)
	if !strings.Contains(c.view(80, 26), "simulation") {
		t.Error("a simulation event must be visibly labelled — a rehearsal must never look like a real run")
	}
}

func TestConsole_EntriesAreBounded(t *testing.T) {
	c := testConsole()
	for i := 0; i < maxConsoleEntries*2; i++ {
		c = c.observeRaw("chatty legacy line")
	}
	if len(c.entries) != maxConsoleEntries {
		t.Errorf("entries = %d, want bounded at %d", len(c.entries), maxConsoleEntries)
	}
}

func TestFormatClock(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{0, "0:00"},
		{42 * time.Second, "0:42"},
		{3*time.Minute + 7*time.Second, "3:07"},
		{61 * time.Minute, "1:01:00"},
	} {
		if got := formatClock(tc.d); got != tc.want {
			t.Errorf("formatClock(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestFormatAchieved(t *testing.T) {
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{
		{42 * time.Second, "42s"},
		{3*time.Minute + 42*time.Second, "3m 42s"},
		{10*time.Minute + 2*time.Second, "10m 02s"},
	} {
		if got := formatAchieved(tc.d); got != tc.want {
			t.Errorf("formatAchieved(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// A terminal can report zero rows mid-resize; the live console must
// still render rather than panic.
func TestConsole_ZeroHeightViewDoesNotPanic(t *testing.T) {
	_ = testConsole().setSize(80, 0).view(80, 0)
}
