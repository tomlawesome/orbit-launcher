package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The Maintenance row (digit 5) opens the flow from the app, a probe
// answer reaches it, and Esc comes back to the menu (#242).
func TestAppModel_MaintenanceDigitOpensTheFlowAndEscComesBack(t *testing.T) {
	dir, _ := maintDeployment(t, true)
	m := NewAppModel().WithoutVolumeCheck()
	m.targetDir = dir
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30}, runeKey('s'), runeKey('5'))
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if m.state != appStateMaintenance {
		t.Fatalf("state = %v, want the Maintenance flow:\n%s", m.state, screen(m))
	}
	if cmd == nil {
		t.Fatal("opening the flow returned no command")
	}
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30}) // the app's own size command, run by hand
	has(t, m.maintenance, "Close Orbit for maintenance", "Reopen Orbit")
	m, _ = updateAs(t, m, key(tea.KeyEsc))
	if m.state != appStateSplash {
		t.Errorf("Esc left the app in state %v, want the menu", m.state)
	}
}
