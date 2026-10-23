package ui

// Pins #277: when every expected service is running (none faulted or
// missing) and the terminal is too short for the full list, the running
// services fold into the one "✓ N running" row, as they do when some service
// is faulted.

import (
	"strings"
	"testing"
)

func TestRepairServicesRoom_AHealthyDeploymentFoldsOnAShortTerminal(t *testing.T) {
	have := up("orbit-db", "orbit-app", "orbit-clamav", "orbit-ollama", "orbit-tika")
	const wantRunning = 5

	tall := servicesModel(t, "full", 80, 60, have, nil)
	if n := len(svcRows(t, tall)); n != wantRunning || foldedRow.MatchString(plain(tall)) {
		t.Fatalf("setup: the tall screen shows %d service rows (want the full %d, unfolded):\n%s", n, wantRunning, screen(tall))
	}
	h := contentRows(screen(tall))
	if h == 0 {
		t.Fatalf("setup: no content on the tall screen:\n%s", screen(tall))
	}

	// One row short of H + 1: the last row is left to the sky.
	m := servicesModel(t, "full", 80, h, have, nil)
	s := screen(m)
	if !strings.Contains(plain(m), "✓ 5 running") {
		t.Errorf("at height %d (the full screen is %d rows) no \"✓ 5 running\" row shows:\n%s", h, h, s)
	}
	if n := len(svcRows(t, m)); n != 0 {
		t.Errorf("at height %d (the full screen is %d rows) %d per-service rows show, want none:\n%s", h, h, n, s)
	}
}
