package deploy

// Lows from #236 for the Remove lane (blind tests, written from the issue
// text and the security ledger only).

import (
	"context"
	"strings"
	"testing"
	"time"
)

// 236-18: when Docker hangs and the launcher's own limit ends the
// stand-down, the error must say that running Stand down again finishes
// the job. The limit is still named (call_limits_test.go pins that).
func TestStandDown_TimeoutErrorSaysRunningStandDownAgainFinishesTheJob(t *testing.T) {
	hangingDockerOnPath(t)
	setStandDownTimeout(t, 200*time.Millisecond)

	done := make(chan error, 1)
	go func() { done <- StandDown(context.Background(), trustedDeployment(t)) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a stand-down whose docker never finished returned no error")
		}
		msg := strings.ToLower(err.Error())
		if !strings.Contains(msg, "stand down") || !strings.Contains(msg, "again") {
			t.Errorf("timeout error = %q, want it to say running Stand down again finishes the job", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("StandDown did not give up at its limit")
	}
}

// 236-c9 (SP1-3): the copied removal command is "sudo rm -rf <dir>", so
// it is never built for the filesystem root or the operator's home
// directory, however the path is spelled. The refusal is an error and
// no command, the same shape as the empty-directory refusal.
func TestRemovalCommand_RefusesTheRootAndTheHomeDirectory(t *testing.T) {
	const home = "/home/someone"
	t.Setenv("HOME", home)
	for _, dir := range []string{"/", "//", "/.", home, home + "/", home + "/.", "/home/someone/../someone"} {
		cmd, err := RemovalCommand(dir)
		if err == nil {
			t.Errorf("RemovalCommand(%q) = %q with no error, want a refusal", dir, cmd)
		}
		if cmd != "" {
			t.Errorf("RemovalCommand(%q) returned %q alongside its error, want no command", dir, cmd)
		}
		words, werr := RemovalCommandWords(dir)
		if werr == nil || len(words) != 0 {
			t.Errorf("RemovalCommandWords(%q) = %q, %v; want a refusal and no words", dir, words, werr)
		}
	}
}

// The refusal must not catch a dedicated folder: one inside home, or a
// folder whose name merely starts like home.
func TestRemovalCommand_StillNamesADedicatedFolder(t *testing.T) {
	t.Setenv("HOME", "/home/someone")
	for _, dir := range []string{"/home/someone/orbit", "/home/someone2", "/opt/orbit"} {
		if _, err := RemovalCommand(dir); err != nil {
			t.Errorf("RemovalCommand(%q): %v, want a command for a dedicated folder", dir, err)
		}
	}
}
