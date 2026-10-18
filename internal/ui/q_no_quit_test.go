package ui

// Pins #226 through AppModel, the way a person meets it: there is no q
// shortcut to quit. On the lit main menu q does nothing and the menu
// stays live; during the arrival animation q is just another key that
// skips to the lit menu. Ctrl-C still quits.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestQ_AppMenuQDoesNotQuitAndTheMenuStaysLive(t *testing.T) {
	for _, r := range []rune{'q', 'Q'} {
		m := leaveApp(t)
		before := m.splash.menu.sel

		m, cmd := updateAs(t, m, runeKey(r))
		if isQuit(cmd) {
			t.Fatalf("%q on the menu quit the launcher", r)
		}
		m = drive(t, m, cmd)
		if m.state != appStateSplash {
			t.Fatalf("%q left the menu (state %v):\n%s", r, m.state, screen(m))
		}
		if m.splash.Chosen != "" || m.splash.menu.sel != before {
			t.Fatalf("%q chose or moved: Chosen = %q, sel = %d (was %d)", r, m.splash.Chosen, m.splash.menu.sel, before)
		}
		if s := screen(m); !strings.Contains(s, "O R B I T") || !strings.Contains(s, "Install") {
			t.Fatalf("the menu is not showing after %q:\n%s", r, s)
		}

		// Still live: Down moves the selection, and Ctrl-C still quits.
		m, cmd = updateAs(t, m, key(tea.KeyDown))
		if isQuit(cmd) {
			t.Fatal("Down quit the launcher")
		}
		if want := (before + 1) % len(m.splash.menu.items); m.splash.menu.sel != want {
			t.Fatalf("Down moved the selection to %d, want %d: the menu is not live", m.splash.menu.sel, want)
		}
		if _, cmd = updateAs(t, m, ctrlC()); !isQuit(cmd) {
			t.Fatal("Ctrl+C no longer quits from the menu")
		}
	}
}

func TestQ_AppDuringTheArrivalSkipsToTheLitMenu(t *testing.T) {
	m := NewAppModel().WithoutVolumeCheck()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	if m.splash.introDone {
		t.Fatal("the arrival should be playing on a fresh animated app")
	}
	if strings.Contains(screen(m), "Install") {
		t.Fatal("the menu must not be visible at the start of the arrival")
	}

	m, cmd := updateAs(t, m, runeKey('q'))
	if isQuit(cmd) {
		t.Fatal("q during the arrival quit the launcher")
	}
	if m.state != appStateSplash || m.splash.Chosen != "" {
		t.Fatalf("q during the arrival left the splash or chose: state %v, Chosen %q", m.state, m.splash.Chosen)
	}
	if !m.splash.introDone {
		t.Fatal("q during the arrival must skip it, like any other key")
	}
	if m.splash.menu.sel != 0 {
		t.Fatal("the skipping q must be swallowed")
	}
	if s := screen(m); !strings.Contains(s, "Install") {
		t.Fatalf("after the skip the lit menu must be showing:\n%s", s)
	}

	// Now on the lit menu, a second q does nothing either.
	m, cmd = updateAs(t, m, runeKey('q'))
	if isQuit(cmd) || m.state != appStateSplash {
		t.Fatal("a second q on the lit menu quit or left it")
	}
}

func TestEsc_AppMenuEscDoesNotQuitAndTheMenuStaysLive(t *testing.T) {
	m := leaveApp(t)
	before := m.splash.menu.sel

	// Several in a row: someone backing out of a flow with repeated Esc
	// must not close the launcher.
	for i := 0; i < 3; i++ {
		var cmd tea.Cmd
		m, cmd = updateAs(t, m, key(tea.KeyEsc))
		if isQuit(cmd) {
			t.Fatalf("Esc #%d on the menu quit the launcher", i+1)
		}
		m = drive(t, m, cmd)
		if m.state != appStateSplash || m.splash.Chosen != "" || m.splash.menu.sel != before {
			t.Fatalf("Esc #%d changed the menu: state %v, Chosen %q, sel %d", i+1, m.state, m.splash.Chosen, m.splash.menu.sel)
		}
	}
	if s := screen(m); !strings.Contains(s, "O R B I T") || !strings.Contains(s, "Install") {
		t.Fatalf("the menu is not showing after Esc:\n%s", s)
	}
	m, cmd := updateAs(t, m, key(tea.KeyDown))
	if isQuit(cmd) {
		t.Fatal("Down quit the launcher")
	}
	if want := (before + 1) % len(m.splash.menu.items); m.splash.menu.sel != want {
		t.Fatalf("Down moved the selection to %d, want %d: the menu is not live", m.splash.menu.sel, want)
	}
	if _, cmd = updateAs(t, m, ctrlC()); !isQuit(cmd) {
		t.Fatal("Ctrl+C no longer quits from the menu")
	}
}

func TestEsc_AppMenuExitRowStillQuits(t *testing.T) {
	m := leaveApp(t)
	m, _ = updateAs(t, m, key(tea.KeyUp)) // wraps to the last row, Exit
	if _, cmd := updateAs(t, m, key(tea.KeyEnter)); !isQuit(cmd) {
		t.Fatal("the menu's Exit row did not quit")
	}
}

func TestEsc_AppDuringTheArrivalSkipsToTheLitMenu(t *testing.T) {
	m := NewAppModel().WithoutVolumeCheck()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	if m.splash.introDone {
		t.Fatal("the arrival should be playing on a fresh animated app")
	}
	m, cmd := updateAs(t, m, key(tea.KeyEsc))
	if isQuit(cmd) {
		t.Fatal("Esc during the arrival quit the launcher")
	}
	if m.state != appStateSplash || !m.splash.introDone || m.splash.menu.sel != 0 {
		t.Fatalf("Esc during the arrival: state %v, introDone %v, sel %d; want the lit menu, swallowed", m.state, m.splash.introDone, m.splash.menu.sel)
	}
	if s := screen(m); !strings.Contains(s, "Install") {
		t.Fatalf("after the skip the lit menu must be showing:\n%s", s)
	}
}
