package ui

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// pendingStartRepair returns a Repair model on its preparing screen whose
// diagnosis start has run (so it holds a context) but has not reported
// back, plus the context the start was given. The ready message is
// withheld on purpose: that is the window #221 is about. Ctrl-C in the
// same window is TestRepairModel_CtrlCWhilePreparingCancelsAndQuitsOnceTheRunReportsBack.
func pendingStartRepair(t *testing.T) (RepairModel, func() context.Context) {
	t.Helper()
	m := sizedRepair(t)
	var captured context.Context
	m.prepareRepair = func(ctx context.Context, _ string, _ deploy.RepairMode) (*engine.Stream, error) {
		captured = ctx
		return nil, errors.New("not now")
	}
	m.Init()()
	if captured == nil {
		t.Fatal("Init did not call the start function")
	}
	return m, func() context.Context { return captured }
}

// Esc while the start has not reported back must cancel it, as Ctrl-C
// does; otherwise the process the start launches outlives the screen.
// Paired with the check that the context is live before Esc, so the
// test cannot pass because the context was already dead.
func TestRepairModel_EscWhileStartIsPendingCancelsTheStart(t *testing.T) {
	m, startCtx := pendingStartRepair(t)
	if err := startCtx().Err(); err != nil {
		t.Fatalf("the start's context was cancelled before Esc: %v", err)
	}

	updateAs(t, m, key(tea.KeyEsc))

	if !errors.Is(startCtx().Err(), context.Canceled) {
		t.Fatalf("Esc during the start did not cancel it: ctx err = %v", startCtx().Err())
	}
}
