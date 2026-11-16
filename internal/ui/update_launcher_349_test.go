package ui

// #349: Update's confirm screen offers the launcher's own installer
// command, in Remove's copyable box, only when a newer launcher is known
// (the splash's notice version). Driven through AppModel from the splash,
// so no constructor API is assumed.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const (
	launcherRow349 = "to update the launcher as well, copy and run"
	launcherCmd349 = "curl -fsSL https://raw.githubusercontent.com/tomlawesome/orbit/main/scripts/get-orbit.sh | bash"
)

// updateConfirm349 opens Update's confirm screen at 80x26 from the splash,
// with or without an update notice.
func updateConfirm349(t *testing.T, withNotice bool) string {
	t.Helper()
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))
	if withNotice {
		m, _ = updateAs(t, m, updateAvailableMsg{version: "v9.9.9"})
	}
	for i := 0; i < 6 && m.splash.menu.sel != menuUpdate; i++ {
		m, _ = updateAs(t, m, key(tea.KeyDown))
	}
	if m.splash.menu.sel != menuUpdate {
		t.Fatalf("setup: cannot select Update:\n%s", screen(m))
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = driveUntil(t, m, cmd, func(m tea.Model) bool { return strings.Contains(screen(m), "update this deployment") })
	s := screen(m)
	if !strings.Contains(s, "Update Orbit") {
		t.Fatalf("setup: not on Update's confirm screen:\n%s", s)
	}
	return s
}

func TestUpdateLauncher349_KnownVersionShowsRowAndExactCommand(t *testing.T) {
	s := updateConfirm349(t, true)
	if !strings.Contains(s, launcherRow349) {
		t.Fatalf("no %q row:\n%s", launcherRow349, s)
	}
	for _, r := range rowsOf(s) {
		if w := lipgloss.Width(r); w > 80 {
			t.Errorf("a row is %d cells, want at most 80: %q", w, r)
		}
	}
	if got := narrowReadBack(narrowBoxInside(t, s)); got != launcherCmd349 {
		t.Errorf("the box reads back as %q, want %q:\n%s", got, launcherCmd349, s)
	}
}

func TestUpdateLauncher349_NoKnownVersionShowsNeitherRowNorBox(t *testing.T) {
	s := updateConfirm349(t, false)
	for _, bad := range []string{launcherRow349, "get-orbit", "curl", "╭"} {
		if strings.Contains(s, bad) {
			t.Errorf("without a known version the screen shows %q:\n%s", bad, s)
		}
	}
}
