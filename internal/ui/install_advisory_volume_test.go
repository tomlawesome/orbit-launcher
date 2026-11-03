package ui

// #330: when Install finds both another project's Orbit and a stale
// database volume, the advisory replaces the volume screen; choosing
// "Continue anyway" on the advisory must then show the stale-volume
// warning before the install goes ahead.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

func TestInstallOtherProject_ContinueAnywayThenShowsTheStaleVolumeWarning(t *testing.T) {
	for _, volumeFirst := range []bool{true, false} {
		name := "other project answers first"
		if volumeFirst {
			name = "volume answers first"
		}
		t.Run(name, func(t *testing.T) {
			m, vol, clash, other := opInstall(t, clashDir, opFinding(opProject, otherProduction), deploy.DatabaseVolume{Name: "orbit-db-data"})
			if volumeFirst {
				m, _ = updateAs(t, m, vol, clash, other)
			} else {
				m, _ = updateAs(t, m, clash, other, vol)
			}
			if m.state != installStateOtherProject {
				t.Fatalf("setup: state = %v, want the advisory:\n%s", m.state, screen(m))
			}

			m, _ = updateAs(t, m, key(tea.KeyEnter))

			if m.state != installStateStaleVolume || !strings.Contains(screen(m), "This machine already has an Orbit database") {
				t.Fatalf("state = %v after Continue anyway, want the stale-volume warning:\n%s", m.state, screen(m))
			}
		})
	}
}
