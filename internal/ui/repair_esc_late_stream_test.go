package ui

// #319: Esc while Repair is still starting its diagnosis cancels the
// start and returns to the menu, but the start may report back with a
// run anyway. That run is read-only and nobody is watching it, so it is
// killed, as the one that arrives after Ctrl-C is, whether the Repair
// model or the app (already back on the splash) receives the message.
// Written from the issue's wording before the fix.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

func TestRepairModel_EscWhilePreparingKillsTheDiagnosisThatStartedAnyway(t *testing.T) {
	m := sizedRepair(t)
	if !strings.Contains(screen(m), "reading the deployment") {
		t.Fatalf("setup: Repair is not preparing:\n%s", screen(m))
	}
	s := sleepingStream(t)

	m, _ = updateAs(t, m, key(tea.KeyEsc))
	if !m.Done || !m.WantsMenu {
		t.Fatalf("Esc while preparing: Done=%v WantsMenu=%v, want both", m.Done, m.WantsMenu)
	}
	updateAs(t, m, repairReadyMsg{stream: s})

	if d := awaitEnd(t, s); d.ExitCode == 0 {
		t.Errorf("a diagnosis that started after Esc was left going (%+v)", d)
	}
}

func TestAppModel_EscWhilePreparingRepairKillsTheDiagnosisArrivingOnTheMenu(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{}, nil)))
	m = moveCaretTo(t, m, "Repair")
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if m.state != appStateRepair {
		t.Fatalf("setup: state = %v, want the repair flow", m.state)
	}
	m = sized(t, m, 80, 26)
	if s := screen(m); !strings.Contains(s, "reading the deployment") {
		t.Fatalf("setup: Repair is not preparing:\n%s", s)
	}
	s := sleepingStream(t)

	// The flow's start command is never run; the start reports back late.
	m, _ = updateAs(t, m, key(tea.KeyEsc))
	if m.state != appStateSplash {
		t.Fatalf("Esc while preparing: state = %v, want the menu", m.state)
	}
	menu := screen(m)
	if strings.Contains(menu, "reading the deployment") {
		t.Fatalf("setup: Esc did not leave Repair:\n%s", menu)
	}
	m, _ = updateAs(t, m, repairReadyMsg{stream: s})

	if d := awaitEnd(t, s); d.ExitCode == 0 {
		t.Errorf("a diagnosis that arrived on the menu after Esc was left going (%+v)", d)
	}
	if m.state != appStateSplash {
		t.Errorf("state = %v after the late stream, want the menu", m.state)
	}
	if got := screen(m); got != menu {
		t.Errorf("the late stream changed the menu:\nbefore:\n%s\nafter:\n%s", menu, got)
	}
}
