package ui

// #319: Esc while Repair is still starting its diagnosis cancels the
// start and returns to the menu, but the start may report back with a
// run anyway. That run is read-only and nobody is watching it, so it is
// killed, as the one that arrives after Ctrl-C is, whether the Repair
// model or the app (already back on the splash) receives the message.
// Written from the issue's wording before the fix.

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
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

// readyFrom runs the command a Repair open returned, expanding batches,
// and gives the repairReadyMsg its start reported. It is the start's own
// message, not one built by the test, so it carries whatever the flow
// stamps on it.
func readyFrom(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	var found tea.Msg
	queue := []tea.Cmd{cmd}
	for len(queue) > 0 {
		next := queue[0]
		queue = queue[1:]
		if next == nil {
			continue
		}
		msg := next()
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		if _, ok := msg.(repairReadyMsg); ok {
			if found != nil {
				t.Fatal("the open's command produced two repairReadyMsg")
			}
			found = msg
		}
	}
	if found == nil {
		t.Fatal("the open's command produced no repairReadyMsg")
	}
	return found
}

// reopenedRepair opens Repair from the splash, leaves it with Esc while
// it is still preparing, and opens it again. The first open's start
// reports stream A and the second's stream B, each through the command
// its open returned; neither message has been delivered.
func reopenedRepair(t *testing.T) (m AppModel, readyA, readyB tea.Msg, a, b *engine.Stream) {
	t.Helper()
	streams := []*engine.Stream{sleepingStream(t), sleepingStream(t)}
	a, b = streams[0], streams[1]
	calls := 0
	seam := func(m AppModel) AppModel {
		m.flowSeams.prepareRepair = func(context.Context, string, deploy.RepairMode) (*engine.Stream, error) {
			s := streams[calls]
			calls++
			return s, nil
		}
		return m
	}
	m = started(t, folderApp(t, installedFolder(t), 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{}, nil), seam))

	open := func() tea.Cmd {
		m = moveCaretTo(t, m, "Repair")
		var cmd tea.Cmd
		m, cmd = updateAs(t, m, key(tea.KeyEnter))
		if m.state != appStateRepair {
			t.Fatalf("setup: state = %v, want the repair flow", m.state)
		}
		m = sized(t, m, 80, 26)
		if s := screen(m); !strings.Contains(s, "reading the deployment") {
			t.Fatalf("setup: Repair is not preparing:\n%s", s)
		}
		return cmd
	}
	first := open()
	m, esc := updateAs(t, m, key(tea.KeyEsc))
	m = sized(t, drive(t, m, esc), 80, 26)
	if m.state != appStateSplash {
		t.Fatalf("setup: Esc while preparing: state = %v, want the menu", m.state)
	}
	second := open()

	// Both starts run, in the order of the opens, so A is the first
	// open's stream and B the second's.
	readyA, readyB = readyFrom(t, first), readyFrom(t, second)
	if calls != 2 {
		t.Fatalf("setup: %d diagnoses started, want 2", calls)
	}
	return m, readyA, readyB, a, b
}

// assertOnlyBKept checks the outcome both orders must reach: the stream
// from the flow that was left is killed, and the open flow reads the
// stream its own start produced and leaves it running.
func assertOnlyBKept(t *testing.T, m AppModel, a, b *engine.Stream) {
	t.Helper()
	select {
	case <-a.Done():
	case <-time.After(5 * time.Second):
		t.Error("the stream from the Repair flow that was left was not killed")
	}
	select {
	case <-b.Done():
		t.Error("the stream from the open Repair flow was killed")
	case <-time.After(300 * time.Millisecond):
	}
	if m.state != appStateRepair {
		t.Errorf("state = %v, want the open Repair flow", m.state)
	}
	if m.repair.stream != b {
		t.Errorf("the open Repair flow reads %p, want its own stream %p", m.repair.stream, b)
	}
}

func TestAppModel_ReopenedRepairKillsTheOldFlowsStreamWhenItArrivesFirst(t *testing.T) {
	m, readyA, readyB, a, b := reopenedRepair(t)

	m, _ = updateAs(t, m, readyA, readyB)

	assertOnlyBKept(t, m, a, b)
}

func TestAppModel_ReopenedRepairKillsTheOldFlowsStreamWhenItArrivesLast(t *testing.T) {
	m, readyA, readyB, a, b := reopenedRepair(t)

	m, _ = updateAs(t, m, readyB, readyA)

	assertOnlyBKept(t, m, a, b)
}
