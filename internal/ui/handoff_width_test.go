package ui

// Pins #288: while the installer has the terminal, no row of the engine
// run's handoff screen is wider than the screen, and the handoff line is
// still there in full once it wraps at word boundaries.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
)

const handoffLine = "in the installer — you'll return here when it finishes"

func TestInstallerHandoff_NoRowRunsPastTheScreen(t *testing.T) {
	for _, w := range c9Widths {
		t.Run(fmt.Sprintf("width_%d", w), func(t *testing.T) {
			r := newEngineRun("install", c9LongDir, "Install — Standard", "v1")
			r.console = newConsole("Install — Standard", "v1", func() time.Time { return time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC) }).setSize(w, 30)
			r.state = runHandoffRunning
			plain := ansi.Strip(r.view(w, 30))

			for i, row := range strings.Split(plain, "\n") {
				if got := visibleWidth(row); got > w {
					t.Errorf("row %d is %d cells wide on a %d-column screen: %q", i, got, w, strings.TrimSpace(row))
				}
			}
			if flat := proseOnly(plain); !strings.Contains(flat, handoffLine) {
				t.Errorf("the handoff line is not there in full, in order:\n%s", plain)
			}
		})
	}
}
