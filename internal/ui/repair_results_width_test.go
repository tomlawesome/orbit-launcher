package ui

// Pins #287: no line of any Repair results screen is wider than the
// screen, and the refused-rotation text is still there in full once it
// wraps.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const refusedRotationText = "credentials left as they were — nothing was rotated (no terminal was available to approve it)"

func TestRepairResults_NoLineRunsPastTheScreen(t *testing.T) {
	for _, w := range c9Widths {
		rp := func() RepairModel { return c9Size(t, NewRepairModel("/opt/orbit", "v1"), w) }

		screens := map[string]string{}
		c9ExecutedScreens(t, screens, w, rp)

		m := rp()
		m.state = repairExecuted
		m.execSummary = &engine.ExecutionSummary{Result: engine.ResultComplete}
		m.dangerous = &engine.Dangerous{Result: engine.ResultRefused, Reason: "non-interactive"}
		screens[fmt.Sprintf("repair_executed_dangerous_refused_%d", w)] = m.View().Content

		for name, view := range screens {
			t.Run(name, func(t *testing.T) {
				plain := ansi.Strip(view)
				for i, row := range strings.Split(plain, "\n") {
					if got := visibleWidth(row); got > w {
						t.Errorf("row %d is %d cells wide on a %d-column screen: %q", i, got, w, strings.TrimSpace(row))
					}
				}
				if strings.Contains(name, "dangerous_refused") {
					if flat := proseOnly(plain); !strings.Contains(flat, refusedRotationText) {
						t.Errorf("the refused-rotation text is not there in full:\n%s", plain)
					}
				}
			})
		}
	}
}
