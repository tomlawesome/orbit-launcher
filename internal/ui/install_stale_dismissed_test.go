package ui

// Issue #338 (A-R3): the stale-volume warning is shown until the person
// continues past it, and then not again in the same Install flow, however
// late the other-project answer arrives.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const staleDismissedTitle = "This machine already has an Orbit database"

func staleDismissedVolume() deploy.DatabaseVolume {
	return deploy.DatabaseVolume{Name: "orbit-db-data"}
}

// The volume answer is read and dismissed first, the other-project answer
// arrives later: its "Continue anyway" must not bring the warning back.
func TestInstallStaleVolume_DismissedWarningNotShownAgainAfterAdvisory(t *testing.T) {
	m, vol, _, other := opInstall(t, clashDir, opFinding(opProject, otherProduction), staleDismissedVolume())
	m, _ = updateAs(t, m, vol)
	if m.state != installStateStaleVolume {
		t.Fatalf("setup: state = %v, want the stale-volume screen:\n%s", m.state, screen(m))
	}
	m, _ = updateAs(t, m, key(tea.KeyEnter)) // Continue anyway
	if m.state != installStateProfile {
		t.Fatalf("setup: state = %v, want the profile screen:\n%s", m.state, screen(m))
	}
	m, _ = updateAs(t, m, other)
	if m.state != installStateOtherProject {
		t.Fatalf("setup: state = %v, want the advisory:\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, key(tea.KeyEnter)) // Continue anyway

	if m.state == installStateStaleVolume || strings.Contains(screen(m), staleDismissedTitle) {
		t.Errorf("a dismissed stale-volume warning came back (state %v):\n%s", m.state, screen(m))
	}
	if m.state != installStateProfile {
		t.Errorf("state = %v, want the profile screen:\n%s", m.state, screen(m))
	}
}

// Guard: the volume answer first, not yet dismissed, then the advisory over
// the warning: Continue anyway still shows the warning.
func TestInstallStaleVolume_UnacknowledgedWarningShowsAfterAdvisory_VolumeFirst(t *testing.T) {
	m, vol, _, other := opInstall(t, clashDir, opFinding(opProject, otherProduction), staleDismissedVolume())
	m, _ = updateAs(t, m, vol, other)
	if m.state != installStateOtherProject {
		t.Fatalf("setup: state = %v, want the advisory:\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, key(tea.KeyEnter))

	if m.state != installStateStaleVolume || !strings.Contains(screen(m), staleDismissedTitle) {
		t.Errorf("state = %v, want the unacknowledged warning:\n%s", m.state, screen(m))
	}
}

// Guard: the advisory first, the volume answer arriving while it is up:
// Continue anyway shows the warning.
func TestInstallStaleVolume_UnacknowledgedWarningShowsAfterAdvisory_AdvisoryFirst(t *testing.T) {
	m, vol, _, other := opInstall(t, clashDir, opFinding(opProject, otherProduction), staleDismissedVolume())
	m, _ = updateAs(t, m, other, vol)
	if m.state != installStateOtherProject {
		t.Fatalf("setup: state = %v, want the advisory:\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, key(tea.KeyEnter))

	if m.state != installStateStaleVolume || !strings.Contains(screen(m), staleDismissedTitle) {
		t.Errorf("state = %v, want the unacknowledged warning:\n%s", m.state, screen(m))
	}
}
