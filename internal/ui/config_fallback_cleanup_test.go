package ui

// Issue #266: when guided configuration finds fields the console cannot
// ask for and falls back to the terminal installer, the session's staged
// configuration plan is cleaned up, exactly once.

import (
	"bytes"
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
)

// settleCount waits until n reaches at least one, then a little longer,
// so a second cleanup arriving late is seen, and returns the count.
func settleCount(t *testing.T, n *atomic.Int32, what string) int32 {
	t.Helper()
	waitCount(t, n, what)
	time.Sleep(300 * time.Millisecond)
	return n.Load()
}

// The ordinary path (every field can be asked): leaving at the first
// question runs the plan's cleanup once. This proves the counter sees a
// cleanup at all, so the fallback test's count is meaningful.
func TestConfigFallbackCleanup_AskablePlanCleanupIsCounted(t *testing.T) {
	var planCleaned atomic.Int32
	tm := startConfigJourney(t, engineRunSeams{
		prepareEngine: engineTwice(),
		prepareConfig: countingPlan(true, false, &planCleaned),
		startConfig:   scriptedConfig(machineInitScript, machineSecretScript),
		detect:        fakeDetect("https://orbit.example.test"),
	})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("How will people sign in?"))
	}, teatest.WithDuration(10*time.Second))
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEsc})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Repair")) && bytes.Contains(out, []byte("Remove"))
	}, teatest.WithDuration(10*time.Second))

	if got := settleCount(t, &planCleaned, "the plan's cleanup on leaving"); got != 1 {
		t.Errorf("plan cleanup ran %d times on leaving, want once", got)
	}

	tm.Send(ctrlC())
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

// The fallback path: a plan with fields the console cannot ask for hands
// over to the terminal installer, and the staged plan is cleaned up once,
// not left behind and not run twice.
func TestConfigFallbackCleanup_UnfixablePlanIsCleanedUpOnce(t *testing.T) {
	var planCleaned atomic.Int32
	handoffRan := make(chan struct{}, 1)
	tm := startConfigJourney(t, engineRunSeams{
		prepareEngine: engineTwice(),
		prepareConfig: func(context.Context, string, string) configPlanMsg {
			return configPlanMsg{plan: configPlan{
				configTree: "/nonexistent-tree-for-tests",
				cleanup:    func() { planCleaned.Add(1) },
				needInit:   true,
				unfixable:  []string{"SOME_FUTURE_FIELD"},
			}}
		},
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			return exec.Command("true"), func() error { return nil }, nil
		},
		runHandoff: func(*exec.Cmd) tea.Cmd {
			return func() tea.Msg {
				handoffRan <- struct{}{}
				return installFinishedMsg{}
			}
		},
		detect: fakeDetect("https://orbit.example.test"),
	})

	select {
	case <-handoffRan:
	case <-time.After(10 * time.Second):
		t.Fatal("unfixable fields never fell back to the terminal installer")
	}

	if got := settleCount(t, &planCleaned, "the staged plan's cleanup on the fallback"); got != 1 {
		t.Errorf("plan cleanup ran %d times on the fallback, want once", got)
	}

	tm.Send(ctrlC())
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
	if got := planCleaned.Load(); got != 1 {
		t.Errorf("plan cleanup ran %d times by the end of the run, want once", got)
	}
}
