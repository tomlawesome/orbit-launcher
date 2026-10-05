package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Edge and exit paths on the two end screens: the success screen and
// the Remove flow.

func successUpdate(t *testing.T, m SuccessModel, msgs ...tea.Msg) (SuccessModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var updated tea.Model
		updated, cmd = m.Update(msg)
		m = updated.(SuccessModel)
	}
	return m, cmd
}

func TestSuccessModel_EveryWayOutLeavesForTheTerminal(t *testing.T) {
	for name, k := range map[string]tea.KeyPressMsg{"Esc": key(tea.KeyEsc), "Ctrl+C": ctrlC(), "q": runeKey('q')} {
		m, cmd := successUpdate(t, newTestSuccessModel(), k)
		if m.Chosen != "terminal" || !isQuit(cmd) {
			t.Errorf("%s: Chosen = %q, quit = %v; want terminal and quit", name, m.Chosen, isQuit(cmd))
		}
	}
	m, cmd := successUpdate(t, newTestSuccessModel(), runeKey('x'))
	if m.Chosen != "" || cmd != nil {
		t.Fatal("an unbound key left the success screen")
	}
}

func TestSuccessModel_UpFromTheTopWrapsToMenu(t *testing.T) {
	m, _ := successUpdate(t, newTestSuccessModel(), key(tea.KeyUp))
	if !strings.Contains(stripANSI(m.View().Content), "▸ Menu") {
		t.Fatalf("Up from Get into Orbit should select Menu:\n%s", stripANSI(m.View().Content))
	}
}

func TestSuccessModel_NoURLMeansNoBrowserLaunch(t *testing.T) {
	opened := false
	m := NewSuccessModel("", 0, "v")
	m.openURL = func(string) error { opened = true; return nil }
	m, _ = successUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 26}, key(tea.KeyEnter))
	if opened {
		t.Fatal("a launch was attempted with no URL to open")
	}
	if !strings.Contains(stripANSI(m.View().Content), "no browser here — copy the URL above") {
		t.Fatalf("screen:\n%s", stripANSI(m.View().Content))
	}
}

func TestSuccessModel_TickDrivesTheOneShotDriftThenHolds(t *testing.T) {
	m := newTestSuccessModel()
	if m.Init() == nil {
		t.Fatal("an animated success screen must start its tick chain")
	}
	var cmd tea.Cmd
	for i := 0; i < 8; i++ {
		m, cmd = successUpdate(t, m, tickMsg{})
	}
	if m.star.Drift != 0 || cmd == nil {
		t.Fatalf("drift = %v during the beat of stillness, want 0 (and the chain re-armed)", m.star.Drift)
	}
	m, _ = successUpdate(t, m, tickMsg{})
	if d := m.star.Drift; d <= 0 || d >= 1 {
		t.Fatalf("drift = %v once easing starts, want between 0 and 1", d)
	}
	for i := 0; i < 20; i++ {
		m, _ = successUpdate(t, m, tickMsg{})
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
	m, cmd := successUpdate(t, m, tickMsg{})
	if cmd != nil || m.driftTick != 0 {
		t.Fatal("a tick re-armed the chain or advanced an unbuilt sky")
	}
	if m.View().Content != "" {
		t.Fatal("before the first resize the view should be blank")
	}
	if _, cmd := successUpdate(t, m, struct{}{}); cmd != nil {
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

func removeUpdate(t *testing.T, m RemoveModel, msgs ...tea.Msg) (RemoveModel, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var updated tea.Model
		updated, cmd = m.Update(msg)
		m = updated.(RemoveModel)
	}
	return m, cmd
}

func TestRemoveModel_FailedScreenNamesTheErrorAndOnlyExits(t *testing.T) {
	m := newTestRemoveModel(func(context.Context, string) error { return errors.New("docker daemon not running") })
	m, cmd := removeUpdate(t, m, key(tea.KeyEnter))
	if !strings.Contains(stripANSI(m.View().Content), "standing down Orbit…") {
		t.Fatalf("working screen:\n%s", stripANSI(m.View().Content))
	}
	m, _ = removeUpdate(t, m, cmd())
	s := stripANSI(m.View().Content)
	for _, want := range []string{"Could not stand down Orbit", "docker daemon not running", "▸ Exit"} {
		if !strings.Contains(s, want) {
			t.Errorf("failed screen lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, "Copy command") {
		t.Errorf("a failed stand-down must not offer the removal command:\n%s", s)
	}
	// Arrows have nowhere to go; Enter and Esc both leave.
	m, cmd = removeUpdate(t, m, key(tea.KeyDown))
	if cmd != nil || m.doneSel != 0 {
		t.Fatal("arrows moved a one-row menu")
	}
	if _, cmd := removeUpdate(t, m, key(tea.KeyEnter)); !isQuit(cmd) {
		t.Fatal("Exit on the failed screen did not quit")
	}
	if _, cmd := removeUpdate(t, m, key(tea.KeyEsc)); !isQuit(cmd) {
		t.Fatal("Esc on the failed screen did not quit")
	}
	if _, cmd := removeUpdate(t, m, runeKey('z')); cmd != nil {
		t.Fatal("an unbound key acted on the failed screen")
	}
}

func TestRemoveModel_CtrlCCancels(t *testing.T) {
	m, cmd := removeUpdate(t, newTestRemoveModel(nil), ctrlC())
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
	m, _ = removeUpdate(t, m, key(tea.KeyEnter))
	m, cmd := removeUpdate(t, m, key(tea.KeyEnter), key(tea.KeyEsc), key(tea.KeyUp))
	if cmd != nil || m.state != removeStateStandingDown || called != 0 {
		t.Fatalf("keys during the stand-down acted: state = %v, calls = %d", m.state, called)
	}
	if _, cmd := removeUpdate(t, m, tickMsg{}); cmd == nil {
		t.Fatal("the sky's tick chain must keep running while standing down")
	}
	if _, cmd := removeUpdate(t, m, struct{}{}); cmd != nil {
		t.Fatal("an unrelated message produced a command")
	}
}

func TestRemoveModel_ConfirmToggleAndUnboundKey(t *testing.T) {
	m, _ := removeUpdate(t, newTestRemoveModel(nil), key(tea.KeyDown))
	if !strings.Contains(stripANSI(m.View().Content), "▸ Cancel") {
		t.Fatalf("Down should select Cancel:\n%s", stripANSI(m.View().Content))
	}
	m, cmd := removeUpdate(t, m, runeKey('y'))
	if cmd != nil || m.state != removeStateConfirm {
		t.Fatal("an unbound key acted on the confirm screen")
	}
}

func TestRemoveModel_WithoutDeploymentDetailsUsesHonestPlaceholders(t *testing.T) {
	var gotDir string
	m := NewRemoveModel(nil)
	m.standDown = func(_ context.Context, dir string) error { gotDir = dir; return nil }
	m, _ = removeUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	if !strings.Contains(stripANSI(m.View().Content), "no deployment details found") {
		t.Fatalf("confirm screen:\n%s", stripANSI(m.View().Content))
	}
	m, cmd := removeUpdate(t, m, key(tea.KeyEnter))
	m, _ = removeUpdate(t, m, cmd())
	if gotDir != "" {
		t.Fatalf("stand-down was handed %q, want no directory", gotDir)
	}
	// The command is wrapped across lines at 80 columns, so look for the
	// placeholder path in both of its halves rather than the one-line form.
	s := stripANSI(m.View().Content)
	if !strings.Contains(s, "the deployment directory") || !strings.Contains(s, "--project-directory /opt/orbit") || !strings.Contains(s, "sudo rm -rf /opt/orbit") {
		t.Fatalf("done screen should fall back to placeholders:\n%s", s)
	}
}
