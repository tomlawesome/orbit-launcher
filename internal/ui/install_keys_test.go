package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// Key handling on the Install flow's screens before the engine runs:
// every screen's way back, its way out, and what an unbound key does.

func TestInstallModel_ProfileArrowsWrapAndShowTheSelection(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	m, _ = updateAs(t, m, key(tea.KeyUp))
	if m.profile.sel != 3 || !strings.Contains(screen(m), "▸ Full") {
		t.Fatalf("Up from Standard should wrap to Full (sel=%d):\n%s", m.profile.sel, screen(m))
	}
	m, _ = updateAs(t, m, key(tea.KeyDown))
	if m.profile.sel != 0 || !strings.Contains(screen(m), "▸ Standard") {
		t.Fatalf("Down from Full should wrap to Standard (sel=%d)", m.profile.sel)
	}
	m, cmd := updateAs(t, m, runeKey('j'))
	if cmd != nil || m.profile.sel != 0 || m.state != installStateProfile {
		t.Fatal("an unbound key moved the profile screen")
	}
}

// Esc on the flow's first screen is back to the main menu (#210, owner
// decision 4a), and starts nothing.
func TestInstallModel_EscOnTheProfileScreenGoesBackToTheMenu(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	m, cmd := updateAs(t, m, key(tea.KeyEsc))
	if cmd != nil {
		t.Fatal("Esc on the first screen of the flow should run nothing")
	}
	if o := m.Outcome(); !leftForMenu(o) {
		t.Fatalf("outcome after Esc = %+v, want back to the menu", o)
	}
	if m.state == installStateRunning {
		t.Fatal("Esc started the install")
	}
}

func TestInstallModel_CtrlCQuitsFromAnyPreRunScreen(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
		t.Fatal("Ctrl+C on the profile screen did not quit")
	}
	m, _ = updateAs(t, m, key(tea.KeyEnter)) // notice
	if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
		t.Fatal("Ctrl+C on the notice did not quit")
	}
	// The refusal for a container started from another folder (#318).
	if _, cmd := updateAs(t, clashed(t, otherProduction), ctrlC()); !isQuit(cmd) {
		t.Fatal("Ctrl+C on the other-folder refusal did not quit")
	}
	// The advisory for an Orbit under another Compose project (#321).
	if _, cmd := updateAs(t, advisory(t, otherProduction, opProject), ctrlC()); !isQuit(cmd) {
		t.Fatal("Ctrl+C on the other-project advisory did not quit")
	}
}

func TestInstallModel_UnavailableProfileExplainsAndOnlyGoesBack(t *testing.T) {
	for _, back := range []tea.KeyPressMsg{key(tea.KeyEnter), key(tea.KeyEsc)} {
		m, _ := newTestInstallModel(engineRunSeams{})
		m, _ = updateAs(t, m, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)) // Agent
		s := screen(m)
		for _, want := range []string{"This profile isn't available yet", "need a local model", "▸ Back"} {
			if !strings.Contains(s, want) {
				t.Errorf("unavailable screen lacks %q:\n%s", want, s)
			}
		}
		m, cmd := updateAs(t, m, runeKey('x'))
		if cmd != nil || m.state != installStateUnavailableProfile {
			t.Fatal("an unbound key left the unavailable screen")
		}
		m, _ = updateAs(t, m, back)
		if m.state != installStateProfile {
			t.Fatalf("%v should return to the profile choice, state = %v", back, m.state)
		}
	}
}

func TestInstallModel_ProfileScreenNamesTheFolderAndIgnoresUnboundKeys(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	if s := screen(m); !strings.Contains(s, "Orbit installs into /opt/orbit") {
		t.Fatalf("profile screen:\n%s", s)
	}
	m, _ = updateAs(t, m, key(tea.KeyDown))
	if !strings.Contains(screen(m), "▸ Documents") {
		t.Fatalf("Down should select Documents:\n%s", screen(m))
	}
	m, _ = updateAs(t, m, key(tea.KeyUp))
	if m.profile.sel != 0 {
		t.Fatalf("Up should return to Standard, sel = %d", m.profile.sel)
	}
	m, cmd := updateAs(t, m, runeKey('y'))
	if cmd != nil || m.state != installStateProfile {
		t.Fatal("an unbound key acted on the profile screen")
	}
}

// The pre-flight takes the first screen's place, so Cancel and Esc there
// are back to the main menu (#210), and neither installs.
func TestInstallModel_StaleVolumeCancelAndEscGoBackToTheMenuWithoutInstalling(t *testing.T) {
	stale := func(t *testing.T) InstallModel {
		m := NewInstallModel("/opt/orbit", "v0.1.0")
		m.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "orbit-db-data"})
		m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 24}, m.Init()())
		if m.state != installStateStaleVolume {
			t.Fatalf("state = %v, want the pre-flight screen", m.state)
		}
		return m
	}

	t.Run("Cancel", func(t *testing.T) {
		m := stale(t)
		m, _ = updateAs(t, m, key(tea.KeyDown))
		if !strings.Contains(screen(m), "▸ Cancel") {
			t.Fatalf("Down should select Cancel:\n%s", screen(m))
		}
		m, _ = updateAs(t, m, key(tea.KeyUp), key(tea.KeyUp))
		if m.volume.sel != 1 {
			t.Fatalf("Up toggles the two rows, sel = %d", m.volume.sel)
		}
		m, cmd := updateAs(t, m, key(tea.KeyEnter))
		if cmd != nil || m.state == installStateRunning || !leftForMenu(m.Outcome()) {
			t.Fatalf("Cancel on the pre-flight screen should go back to the menu, starting nothing (cmd=%v state=%v outcome=%+v)", cmd != nil, m.state, m.Outcome())
		}
	})
	t.Run("Esc", func(t *testing.T) {
		m, cmd := updateAs(t, stale(t), key(tea.KeyEsc))
		if cmd != nil || m.state == installStateRunning || !leftForMenu(m.Outcome()) {
			t.Fatalf("Esc on the pre-flight screen should go back to the menu, starting nothing (cmd=%v state=%v outcome=%+v)", cmd != nil, m.state, m.Outcome())
		}
	})
	t.Run("unbound key", func(t *testing.T) {
		m, cmd := updateAs(t, stale(t), runeKey('d'))
		if cmd != nil || m.state != installStateStaleVolume || m.volume.sel != 0 {
			t.Fatal("an unbound key acted on the pre-flight screen")
		}
	})
}

func TestInstallModel_WheelOnlyScrollsTheNotice(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	m, cmd := updateAs(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if cmd != nil || m.state != installStateProfile {
		t.Fatal("the wheel acted outside the notice")
	}
	if m.View().MouseMode != tea.MouseModeNone {
		t.Fatal("mouse reporting must be off outside the notice")
	}
}

func TestInstallModel_UnsizedViewIsBlank(t *testing.T) {
	if got := NewInstallModel("/opt/orbit", "v").View().Content; got != "" {
		t.Fatalf("before the first resize the view should be blank, got %q", got)
	}
}

func TestInstallModel_WheelDownScrollsTheNoticeOnward(t *testing.T) {
	m, _, _ := openNotice(t, 80, 26, engineRunSeams{})
	m, cmd := updateAs(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if cmd != nil {
		t.Fatal("scrolling must not start anything")
	}
	if m.notice.offset != noticeWheelRows {
		t.Fatalf("wheel down: offset = %d, want %d", m.notice.offset, noticeWheelRows)
	}
}
