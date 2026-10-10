package ui

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"errors"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// pendingStartRepair returns a Repair model on its preparing screen whose
// diagnosis start has run (so it holds a context) but has not reported
// back, plus the context the start was given and the ready message the
// start will eventually deliver. The ready message is withheld on
// purpose: that is the window #221 is about.
func pendingStartRepair(t *testing.T) (RepairModel, func() context.Context, func() tea.Msg) {
	t.Helper()
	m := sizedRepair(t)
	var captured context.Context
	m.prepare = func(ctx context.Context, _ string, _ deploy.RepairMode) (*engine.Stream, error) {
		captured = ctx
		return nil, errors.New("not now")
	}
	ready := m.Init()()
	if captured == nil {
		t.Fatal("Init did not call the start function")
	}
	return m, func() context.Context { return captured }, func() tea.Msg { return ready }
}

// Esc while the start has not reported back must cancel it, as Ctrl-C
// does; otherwise the process the start launches outlives the screen.
// Paired with the check that the context is live before Esc, so the
// test cannot pass because the context was already dead.
func TestRepairModel_EscWhileStartIsPendingCancelsTheStart(t *testing.T) {
	m, startCtx, _ := pendingStartRepair(t)
	if err := startCtx().Err(); err != nil {
		t.Fatalf("the start's context was cancelled before Esc: %v", err)
	}

	repairUpdate(t, m, key(tea.KeyEsc))

	if !errors.Is(startCtx().Err(), context.Canceled) {
		t.Fatalf("Esc during the start did not cancel it: ctx err = %v", startCtx().Err())
	}
}

// Passed already: Ctrl-C on the same screen cancels the start and quits
// only once the start reports back. The fix for Esc must keep this.
func TestRepairModel_CtrlCWhileStartIsPendingStillCancelsAndQuitsOnReport(t *testing.T) {
	m, startCtx, ready := pendingStartRepair(t)
	if err := startCtx().Err(); err != nil {
		t.Fatalf("the start's context was cancelled before Ctrl+C: %v", err)
	}

	m, cmd := repairUpdate(t, m, ctrlC())
	if cmd != nil {
		t.Fatal("Ctrl+C quit before the start reported back")
	}
	if !errors.Is(startCtx().Err(), context.Canceled) {
		t.Fatalf("Ctrl+C did not cancel the start: ctx err = %v", startCtx().Err())
	}
	if _, cmd = repairUpdate(t, m, ready()); !isQuit(cmd) {
		t.Fatal("the flow did not quit once the cancelled start reported back")
	}
}
