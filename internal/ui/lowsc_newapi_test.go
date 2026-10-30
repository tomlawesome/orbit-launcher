package ui

// v0.4.3-c audit Low C2 (issue #277). This file does not compile until
// engine.RawLineMsg has the new field the specification names:
//
//	type RawLineMsg struct {
//		Text      string
//		Truncated bool // the engine reader cut the line to 16 KiB
//	}
//
// It is kept apart so that the failure is recorded in one place.

import (
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// C2 (#277): a recovery-command line the engine reader cut is never
// shown or copied: the failed event after it draws no box and offers no
// Copy command.
func TestLowsC_C2_ACutRecoveryCommandIsNeitherShownNorCopied(t *testing.T) {
	_, s := lowsCInstallRun(t, 120, 40,
		hostDone, rollbackRunning,
		engine.RawLineMsg{Text: "recovery-command: " + engineCommand, Truncated: true},
		parsedEvent(t, retryEvent), exit1())
	if lowsCHasBox(s) {
		t.Errorf("a box is drawn for a cut recovery-command line:\n%s", s)
	}
	if hasRow(s, "Copy command") {
		t.Errorf("Copy command is offered for a cut recovery-command line:\n%s", s)
	}
}
