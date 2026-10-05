package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// Key handling on the Install flow's screens before the engine runs:
// every screen's way back, its way out, and what an unbound key does.

func installUpdate(t *testing.T, m InstallModel, msgs ...tea.Msg) (InstallModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var updated tea.Model
		updated, cmd = m.Update(msg)
		m = updated.(InstallModel)
	}
	return m, cmd
}

func TestInstallModel_ProfileArrowsWrapAndShowTheSelection(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	m, _ = installUpdate(t, m, key(tea.KeyUp))
	if m.profileSel != 2 || !strings.Contains(screen(m), "▸ Full") {
		t.Fatalf("Up from Standard should wrap to Full (sel=%d):\n%s", m.profileSel, screen(m))
	}
	m, _ = installUpdate(t, m, key(tea.KeyDown))
	if m.profileSel != 0 || !strings.Contains(screen(m), "▸ Standard") {
		t.Fatalf("Down from Full should wrap to Standard (sel=%d)", m.profileSel)
	}
	m, cmd := installUpdate(t, m, runeKey('j'))
	if cmd != nil || m.profileSel != 0 || m.state != installStateProfile {
		t.Fatal("an unbound key moved the profile screen")
	}
}

func TestInstallModel_EscOnTheProfileScreenQuits(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	if _, cmd := installUpdate(t, m, key(tea.KeyEsc)); !isQuit(cmd) {
		t.Fatal("Esc on the first screen of the flow should quit")
	}
}

func TestInstallModel_CtrlCQuitsFromAnyPreRunScreen(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	m, _ = installUpdate(t, m, key(tea.KeyEnter)) // confirm
	if _, cmd := installUpdate(t, m, ctrlC()); !isQuit(cmd) {
		t.Fatal("Ctrl+C on the confirm screen did not quit")
	}
}

func TestInstallModel_UnavailableProfileExplainsAndOnlyGoesBack(t *testing.T) {
	for _, back := range []tea.KeyPressMsg{key(tea.KeyEnter), key(tea.KeyEsc)} {
		m, _ := newTestInstallModel(engineRunSeams{})
		m, _ = installUpdate(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // AI
		s := screen(m)
		for _, want := range []string{"This profile isn't available yet", "local-model configuration", "▸ Back"} {
			if !strings.Contains(s, want) {
				t.Errorf("unavailable screen lacks %q:\n%s", want, s)
			}
		}
		m, cmd := installUpdate(t, m, runeKey('x'))
		if cmd != nil || m.state != installStateUnavailableProfile {
			t.Fatal("an unbound key left the unavailable screen")
		}
		m, _ = installUpdate(t, m, back)
		if m.state != installStateProfile {
			t.Fatalf("%v should return to the profile choice, state = %v", back, m.state)
		}
	}
}

func TestInstallModel_ConfirmBackRowReturnsToProfile(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	m, _ = installUpdate(t, m, key(tea.KeyEnter))
	if s := screen(m); !strings.Contains(s, "Ready to install") || !strings.Contains(s, "/opt/orbit") {
		t.Fatalf("confirm screen:\n%s", s)
	}
	m, _ = installUpdate(t, m, key(tea.KeyDown))
	if !strings.Contains(screen(m), "▸ Back") {
		t.Fatalf("Down should select Back:\n%s", screen(m))
	}
	m, _ = installUpdate(t, m, key(tea.KeyUp), key(tea.KeyUp)) // toggles: Install now, Back
	if m.confirmSel != 1 {
		t.Fatalf("Up toggles between the two rows, sel = %d", m.confirmSel)
	}
	m, cmd := installUpdate(t, m, runeKey('y'))
	if cmd != nil || m.state != installStateConfirm {
		t.Fatal("an unbound key acted on the confirm screen")
	}
	m, cmd = installUpdate(t, m, key(tea.KeyEnter))
	if cmd != nil || m.state != installStateProfile {
		t.Fatalf("Back should return to the profile screen without starting anything, state = %v", m.state)
	}
}

func TestInstallModel_StaleVolumeBackAndEscQuitWithoutInstalling(t *testing.T) {
	stale := func(t *testing.T) InstallModel {
		m := NewInstallModel("/opt/orbit", "v0.1.0")
		m.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "orbit-db-data"})
		m, _ = installUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 24}, m.Init()())
		if m.state != installStateStaleVolume {
			t.Fatalf("state = %v, want the pre-flight screen", m.state)
		}
		return m
	}

	t.Run("Back", func(t *testing.T) {
		m := stale(t)
		m, _ = installUpdate(t, m, key(tea.KeyDown))
		if !strings.Contains(screen(m), "▸ Back") {
			t.Fatalf("Down should select Back:\n%s", screen(m))
		}
		m, _ = installUpdate(t, m, key(tea.KeyUp), key(tea.KeyUp))
		if m.volumeSel != 1 {
			t.Fatalf("Up toggles the two rows, sel = %d", m.volumeSel)
		}
		if _, cmd := installUpdate(t, m, key(tea.KeyEnter)); !isQuit(cmd) {
			t.Fatal("Back on the pre-flight screen should leave the launcher")
		}
	})
	t.Run("Esc", func(t *testing.T) {
		if _, cmd := installUpdate(t, stale(t), key(tea.KeyEsc)); !isQuit(cmd) {
			t.Fatal("Esc on the pre-flight screen should leave the launcher")
		}
	})
	t.Run("unbound key", func(t *testing.T) {
		m, cmd := installUpdate(t, stale(t), runeKey('d'))
		if cmd != nil || m.state != installStateStaleVolume || m.volumeSel != 0 {
			t.Fatal("an unbound key acted on the pre-flight screen")
		}
	})
}

func TestInstallModel_WheelOnlyScrollsTheNotice(t *testing.T) {
	m, _ := newTestInstallModel(engineRunSeams{})
	m, cmd := installUpdate(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
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
	m, cmd := installUpdate(t, m, tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	if cmd != nil {
		t.Fatal("scrolling must not start anything")
	}
	if m.notice.offset != noticeWheelRows {
		t.Fatalf("wheel down: offset = %d, want %d", m.notice.offset, noticeWheelRows)
	}
}
