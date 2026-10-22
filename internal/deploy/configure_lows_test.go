package deploy

// #195 P2-R1: configure.sh --check gets a limit of its own, as
// StandDown and the fetch do. This file does not compile until the seam
// exists; it is kept apart so the failure is recorded in one place.
//
// The seam it names, beside standDownTimeout and scriptFetchTimeout in
// package deploy:
//
//	var configCheckTimeout = <the default limit>   // time.Duration
//
// RunConfigCheck applies it when the caller's context has no shorter
// deadline, and a check that runs out of time fails with an error that
// names the limit that applied (as the StandDown and fetch errors do).
// The UI's half (Esc and Ctrl-C cancel the check) is in
// internal/ui/enginerun_lows_test.go.

import (
	"context"
	"strings"
	"testing"
	"time"
)

func setConfigCheckTimeout(t *testing.T, d time.Duration) {
	t.Helper()
	prev := configCheckTimeout
	configCheckTimeout = d
	t.Cleanup(func() { configCheckTimeout = prev })
}

func TestRunConfigCheck_GivesUpAtItsOwnLimitWithNoCallerDeadline(t *testing.T) {
	setConfigCheckTimeout(t, 300*time.Millisecond)
	dir, pidFile := lingeringCheckTree(t) // a check that never finishes, with a child on its stdout

	done := make(chan error, 1)
	go func() {
		_, err := RunConfigCheck(context.Background(), dir)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a check that never finished returned no error")
		}
		if !strings.Contains(err.Error(), "300ms") {
			t.Errorf("error should name the 300ms limit that applied, got: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("RunConfigCheck is still waiting 6s into a 300ms limit with no caller deadline: it has no bound of its own")
	}

	deadline := time.Now().Add(5 * time.Second)
	for _, pid := range waitForPIDs(t, pidFile, 2) {
		for running(pid) {
			if time.Now().After(deadline) {
				t.Fatalf("the check's process %d is still running after the limit ran out", pid)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
}
