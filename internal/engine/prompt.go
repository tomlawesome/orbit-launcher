package engine

import (
	"strconv"
	"strings"
)

// Machine prompt protocol v0 — orbit docs/engine-events.md "Machine
// prompts (v0)". A second, independent line grammar layered under the
// configuration phase: configure.sh, run with
// ORBIT_CONFIGURE_PROMPTS=machine, writes one protocol line per
// exchange and reads exactly one answer line from stdin per prompt.
// Lines carry only the fixed field/kind/reason vocabulary — never a
// configuration value, never the secret.

// Prompt is the engine's request for one configuration value. The
// engine is now blocked reading one answer line from stdin.
type Prompt struct {
	Field    string
	Kind     string // url | text | secret — unknown values carried verbatim
	Attempt  int    // 1-based; bounded at 3 by the engine
	Required bool
}

// PromptReject reports that the last answer failed the engine's own
// validation; a fresh Prompt for the same field follows.
type PromptReject struct {
	Field  string
	Reason string // reason class, e.g. not-https — unknown carried verbatim
}

// PromptAccept reports the last answer validated and was taken.
type PromptAccept struct{ Field string }

// PromptAbort reports the engine gave up on this field (third rejected
// answer, or stdin closed) and is failing through its refusal path.
type PromptAbort struct{ Field string }

// ParsePromptLine parses one stdout line as a machine-prompt protocol
// line, returning one of Prompt, PromptReject, PromptAccept or
// PromptAbort. ok is false for anything else — engine events, prose,
// blank lines — which callers treat exactly as they always have.
func ParsePromptLine(line string) (msg any, ok bool) {
	tokens := strings.Fields(line)
	if len(tokens) < 2 {
		return nil, false
	}
	kind := tokens[0]
	switch kind {
	case "prompt", "prompt-reject", "prompt-accept", "prompt-abort":
	default:
		return nil, false
	}

	fields := map[string]string{}
	for _, token := range tokens[1:] {
		key, value, found := strings.Cut(token, "=")
		if !found || key == "" {
			// A bare word means this is prose that merely starts with
			// a protocol word — not a protocol line.
			return nil, false
		}
		// Unknown trailing key=value fields are tolerated, same as the
		// event stream contract; last occurrence wins.
		fields[key] = value
	}
	if fields["field"] == "" {
		return nil, false
	}

	switch kind {
	case "prompt":
		attempt, err := strconv.Atoi(fields["attempt"])
		if err != nil || attempt < 1 {
			attempt = 1
		}
		return Prompt{
			Field:    fields["field"],
			Kind:     fields["kind"],
			Attempt:  attempt,
			Required: fields["required"] == "true",
		}, true
	case "prompt-reject":
		if fields["reason"] == "" {
			return nil, false
		}
		return PromptReject{Field: fields["field"], Reason: fields["reason"]}, true
	case "prompt-accept":
		return PromptAccept{Field: fields["field"]}, true
	default:
		return PromptAbort{Field: fields["field"]}, true
	}
}
