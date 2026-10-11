package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Edge and exit paths on the two end screens: the success screen and
// the Remove flow.

func TestSuccessModel_EveryWayOutLeavesForTheTerminal(t *testing.T) {
	// Esc is one step back, to the menu (#225); Ctrl+C quits. There is no
	// q shortcut (#226).
	m, cmd := updateAs(t, newTestSuccessModel(), ctrlC())
	if m.Chosen != "terminal" || !isQuit(cmd) {
		t.Errorf("Ctrl+C: Chosen = %q, quit = %v; want terminal and quit", m.Chosen, isQuit(cmd))
	}
	for _, r := range []rune{'q', 'Q'} {
		m, cmd := updateAs(t, newTestSuccessModel(), runeKey(r))
		if m.Chosen != "" || isQuit(cmd) || cmd != nil {
			t.Errorf("%q: Chosen = %q, cmd = %v; want q to do nothing", r, m.Chosen, cmd != nil)
		}
	}
	if m, _ := updateAs(t, newTestSuccessModel(), key(tea.KeyEsc)); m.Outcome() != backToMenu || m.Chosen == "terminal" {
		t.Errorf("Esc: Chosen = %q; want back to the menu", m.Chosen)
	}
	m, cmd = updateAs(t, newTestSuccessModel(), runeKey('x'))
	if m.Chosen != "" || cmd != nil {
		t.Fatal("an unbound key left the success screen")
	}
}

func TestSuccessModel_UpFromTheTopWrapsToMenu(t *testing.T) {
	m, _ := updateAs(t, newTestSuccessModel(), key(tea.KeyUp))
	if !strings.Contains(screen(m), "▸ Menu") {
		t.Fatalf("Up from Get into Orbit should select Menu:\n%s", screen(m))
	}
}

func TestSuccessModel_NoURLMeansNoBrowserLaunch(t *testing.T) {
	opened := false
	m := NewSuccessModel("", 0, "v")
	m.openURL = func(string) error { opened = true; return nil }
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26}, key(tea.KeyEnter))
	if opened {
		t.Fatal("a launch was attempted with no URL to open")
	}
	if !strings.Contains(screen(m), "no browser here — copy the URL above") {
		t.Fatalf("screen:\n%s", screen(m))
	}
}

func TestSuccessModel_TickDrivesTheOneShotDriftThenHolds(t *testing.T) {
	m := newTestSuccessModel()
	if m.Init() == nil {
		t.Fatal("an animated success screen must start its tick chain")
	}
	var cmd tea.Cmd
	for i := 0; i < 8; i++ {
		m, cmd = updateAs(t, m, tickMsg{})
	}
	if m.star.Drift != 0 || cmd == nil {
		t.Fatalf("drift = %v during the beat of stillness, want 0 (and the chain re-armed)", m.star.Drift)
	}
	m, _ = updateAs(t, m, tickMsg{})
	if d := m.star.Drift; d <= 0 || d >= 1 {
		t.Fatalf("drift = %v once easing starts, want between 0 and 1", d)
	}
	for i := 0; i < 20; i++ {
		m, _ = updateAs(t, m, tickMsg{})
	}
	if m.star.Drift != 1 {
		t.Fatalf("drift = %v after the beat, want it to settle at 1", m.star.Drift)
	}
}

func TestSuccessModel_NoAnimationNeverTicks(t *testing.T) {
	m := NewSuccessModel("https://mail.example.com", 0, "v")
	m.noAnimation = true
	if m.Init() != nil {
		t.Fatal("without animation there must be no tick chain")
	}
	// A tick before the first resize has no sky to move.
	m, cmd := updateAs(t, m, tickMsg{})
	if cmd != nil || m.driftTick != 0 {
		t.Fatal("a tick re-armed the chain or advanced an unbuilt sky")
	}
	if m.View().Content != "" {
		t.Fatal("before the first resize the view should be blank")
	}
	if _, cmd := updateAs(t, m, struct{}{}); cmd != nil {
		t.Fatal("an unrelated message produced a command")
	}
}

// fakeOpener puts an executable named name on an otherwise empty PATH;
// it records the URL it was asked to open.
func fakeOpener(t *testing.T, name string) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "opened")
	script := "#!/bin/sh\nprintf '%s' \"$1\" > " + record + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return record
}

func TestDefaultOpenURL_UsesThePlatformOpener(t *testing.T) {
	for _, opener := range []string{"xdg-open", "open"} {
		t.Run(opener, func(t *testing.T) {
			record := fakeOpener(t, opener)
			if err := defaultOpenURL("https://mail.example.com"); err != nil {
				t.Fatalf("defaultOpenURL: %v", err)
			}
			deadline := time.Now().Add(5 * time.Second)
			for {
				got, err := os.ReadFile(record)
				if err == nil && string(got) == "https://mail.example.com" {
					return
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s was never asked to open the URL (got %q)", opener, got)
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	}
}

func TestDefaultOpenURL_NoOpenerIsAnError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := defaultOpenURL("https://mail.example.com"); err == nil || err.Error() != "no opener available" {
		t.Fatalf("err = %v, want no opener available", err)
	}
}

func TestRemoveModel_FailedScreenNamesTheErrorAndOffersMenuThenExit(t *testing.T) {
	m := newTestRemoveModel(func(context.Context, string) error { return errors.New("docker daemon not running") })
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if !strings.Contains(screen(m), "standing down Orbit") {
		t.Fatalf("working screen:\n%s", screen(m))
	}
	m, _ = updateAs(t, m, cmd())
	s := screen(m)
	for _, want := range []string{"Could not stand down Orbit", "docker daemon not running", "▸ Menu"} {
		if !strings.Contains(s, want) {
			t.Errorf("failed screen lacks %q:\n%s", want, s)
		}
	}
	if got := screenLabels(s); !slices.Equal(got, []string{"Menu", "Exit"}) {
		t.Errorf("failed screen rows = %q, want Menu then Exit (#228)", got)
	}
	if strings.Contains(s, "Copy command") {
		t.Errorf("a failed stand-down must not offer the removal command:\n%s", s)
	}
	// Menu is the default row: Enter goes back to the menu, nothing quits.
	if m, cmd := updateAs(t, m, key(tea.KeyEnter)); m.Outcome() != backToMenu || isQuit(cmd) {
		t.Fatal("Menu on the failed screen did not go back to the menu")
	}
	// Exit is the row below it.
	down, cmd := updateAs(t, m, key(tea.KeyDown))
	if cmd != nil || !strings.Contains(screen(down), "▸ Exit") {
		t.Fatal("Down did not move to the Exit row")
	}
	if _, cmd := updateAs(t, down, key(tea.KeyEnter)); !isQuit(cmd) {
		t.Fatal("Exit on the failed screen did not quit")
	}
	if m, cmd := updateAs(t, m, key(tea.KeyEsc)); m.Outcome() != backToMenu || isQuit(cmd) {
		t.Fatal("Esc on the failed screen did not go back to the menu")
	}
	if _, cmd := updateAs(t, m, runeKey('z')); cmd != nil {
		t.Fatal("an unbound key acted on the failed screen")
	}
}

func TestRemoveModel_CtrlCCancels(t *testing.T) {
	m, cmd := updateAs(t, newTestRemoveModel(nil), ctrlC())
	if m.state != removeStateCancelled || !isQuit(cmd) {
		t.Fatalf("state = %v, quit = %v", m.state, isQuit(cmd))
	}
	if m.View().Content != "" {
		t.Fatal("a cancelled flow draws nothing")
	}
}

func TestRemoveModel_KeysWhileStandingDownAreIgnored(t *testing.T) {
	called := 0
	m := newTestRemoveModel(func(context.Context, string) error { called++; return nil })
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	m, cmd := updateAs(t, m, key(tea.KeyEnter), key(tea.KeyEsc), key(tea.KeyUp))
	if cmd != nil || m.state != removeStateStandingDown || called != 0 {
		t.Fatalf("keys during the stand-down acted: state = %v, calls = %d", m.state, called)
	}
	if _, cmd := updateAs(t, m, tickMsg{}); cmd == nil {
		t.Fatal("the sky's tick chain must keep running while standing down")
	}
	if _, cmd := updateAs(t, m, struct{}{}); cmd != nil {
		t.Fatal("an unrelated message produced a command")
	}
}

func TestRemoveModel_ConfirmToggleAndUnboundKey(t *testing.T) {
	m, _ := updateAs(t, newTestRemoveModel(nil), key(tea.KeyDown))
	if !strings.Contains(screen(m), "▸ Cancel") {
		t.Fatalf("Down should select Cancel:\n%s", screen(m))
	}
	m, cmd := updateAs(t, m, runeKey('y'))
	if cmd != nil || m.state != removeStateConfirm {
		t.Fatal("an unbound key acted on the confirm screen")
	}
}
