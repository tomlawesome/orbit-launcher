// Package engine consumes orbit's engine event stream v0 — the
// plain-mode, fixed-vocabulary status lines install.sh emits on stdout
// (orbit docs/engine-events.md, orbit#305/#306; consumed per
// orbit-launcher#73). The parser is deliberately tolerant: unknown keys
// are ignored, unknown enum values are carried through verbatim for the
// UI to render unstyled, and any line that isn't an event is surfaced
// as raw text rather than dropped — a legacy install.sh (orbit main
// today) emits no events at all, and the mission console still shows
// its prose honestly.
package engine

import "strings"

// Event is one engine event stream v0 line. All five enum fields are
// carried as the literal tokens the engine emitted — never mapped or
// corrected here — so the UI layer can style known values and still
// render unknown ones (the contract's renderable-but-unstyled rule).
type Event struct {
	Phase     string
	Component string
	State     string
	Reason    string
	Action    string

	// ElapsedSeconds is the engine's own elapsed clock. Malformed
	// values parse as 0, matching the emitter's own rendering rule.
	ElapsedSeconds int

	// Simulation is true when the engine appended simulation=true —
	// a rehearsal run that must never be presented as a real one.
	Simulation bool
}

// Terminal states per the contract: failed and blocked are refusals or
// failures; completed on the complete phase is success.
const (
	StateFailed    = "failed"
	StateBlocked   = "blocked"
	StateCompleted = "completed"
	PhaseComplete  = "complete"

	// ReasonConfigurationFailure is the non-interactive refusal the
	// contract documents for incomplete configuration: the engine
	// refuses before starting Compose, and the consumer should re-run
	// configuration interactively (the terminal handoff stretch).
	ReasonConfigurationFailure = "configuration-failure"
)

// IsSuccess reports whether e is the contract's success event.
func (e Event) IsSuccess() bool {
	return e.Phase == PhaseComplete && e.State == StateCompleted
}

// IsTerminalFailure reports whether e is a refusal or failure outcome.
func (e Event) IsTerminalFailure() bool {
	return e.State == StateFailed || e.State == StateBlocked
}

// NeedsConfiguration reports whether e is the engine's
// configuration-required refusal — the signal to hand the real
// terminal over for guided configuration.
func (e Event) NeedsConfiguration() bool {
	return e.State == StateFailed && e.Reason == ReasonConfigurationFailure
}

// ParseEvent parses one stdout line as an engine event. ok is false for
// anything that isn't an event line — human prose, blank lines, the
// engine's completion summary — which callers should treat as raw
// display text, never as machine signal.
func ParseEvent(line string) (e Event, ok bool) {
	// An event line is exactly key=value tokens separated by single
	// spaces, and always leads with phase= — cheap rejection first so
	// arbitrary prose (which may contain '=') is never misparsed.
	if !strings.HasPrefix(line, "phase=") {
		return Event{}, false
	}

	seen := map[string]bool{}
	for _, token := range strings.Fields(line) {
		key, value, found := strings.Cut(token, "=")
		if !found || key == "" {
			// A bare word inside an otherwise event-shaped line means
			// this is prose that merely starts with "phase=".
			return Event{}, false
		}
		switch key {
		case "phase":
			e.Phase = value
		case "component":
			e.Component = value
		case "state":
			e.State = value
		case "reason":
			e.Reason = value
		case "action":
			e.Action = value
		case "elapsed":
			e.ElapsedSeconds = parseElapsed(value)
		case "simulation":
			e.Simulation = value == "true"
		default:
			// Unknown trailing key=value fields are explicitly allowed
			// by the contract; ignore them.
		}
		seen[key] = true
	}

	// All five enum fields plus elapsed are required for a v0 event.
	for _, key := range []string{"phase", "component", "state", "reason", "action", "elapsed"} {
		if !seen[key] {
			return Event{}, false
		}
	}
	return e, true
}

// parseElapsed parses "<seconds>s"; anything malformed is 0, matching
// the emitter's own malformed-input rule.
func parseElapsed(value string) int {
	digits, found := strings.CutSuffix(value, "s")
	if !found || digits == "" {
		return 0
	}
	seconds := 0
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0
		}
		seconds = seconds*10 + int(r-'0')
	}
	return seconds
}
