package ui

// Stands in for the part of TestConfigure286_ConfirmTellsThemItAlsoUpdates-
// OrbitOnItsOwnLine that cannot pass: that test finds a row with screenRow,
// which trims "." from both ends, so a row ending in the full stop of
// "This also updates Orbit to its newest release." never equals the label.
// This one trims the sky glyphs and the caret but keeps the full stop.

import (
	"strings"
	"testing"
)

func TestConfigure_ConfirmAlsoUpdatesLineIsItsOwnRowAboveTheChangeRow(t *testing.T) {
	for _, c := range []struct{ profiles, to string }{{"-", "Documents"}, {"processing", "Standard"}} {
		t.Run("to "+c.to, func(t *testing.T) {
			m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), c.profiles, true))
			m = cfg286ToConfirm(t, m, c.to)
			at, change := -1, -1
			for i, line := range strings.Split(screen(m), "\n") {
				switch strings.TrimSpace(strings.Trim(line, " ▸·✦●⟡")) {
				case cfg286AlsoLine:
					at = i
				case "Change to " + c.to:
					change = i
				}
			}
			if at < 0 || change < 0 || at >= change {
				t.Errorf("the line is at row %d and the Change row at %d, want the line on its own row above it:\n%s", at, change, screen(m))
			}
		})
	}
}
