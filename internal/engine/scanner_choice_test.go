package engine

// Blind tests for #340 (spec items 13 to 15, the engine's side): the
// skip action and what a skipped event is not.

import "testing"

func TestScanner340_ActionSkipIsTheEnginesWord(t *testing.T) {
	if ActionSkip != "skip" {
		t.Errorf("ActionSkip = %q, want skip", ActionSkip)
	}
}

func TestScanner340_SkippedScannerEventsParseAsSkipAndAreNotFailures(t *testing.T) {
	for _, line := range []string{
		"phase=preparation component=clamav state=skipped reason=service-preparation action=skip elapsed=1s",
		"phase=optional component=clamav state=skipped reason=optional-status action=skip elapsed=1s",
	} {
		e, ok := ParseEvent(line)
		if !ok {
			t.Fatalf("%q did not parse as an event", line)
		}
		if e.State != StateSkipped || e.Action != ActionSkip || e.Component != "clamav" {
			t.Errorf("%q parsed as %+v", line, e)
		}
		if e.IsTerminalFailure() {
			t.Errorf("%q is a terminal failure", line)
		}
		if e.IsSuccess() {
			t.Errorf("%q counts as the success event", line)
		}
	}
}
