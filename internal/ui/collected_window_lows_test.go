package ui

// Pins #304 SEC-S2-2: keep() makes the new collected tree with
// MkdirTemp and registers it for the exit's cleanup only after adopt has
// copied the answers (the OIDC secret among them) into it. A quit in
// between (FinishDeferredCleanup runs while adopt is still copying)
// leaves a 0700 tree holding the secret in $TMPDIR that nothing removes.
// The seam is the adopt function keep is given: the test quits from
// inside it. Paired with TestCollected_FailedKeepLeavesTheEarlierTreeAndNoTempBehind
// (adopt fails: the new tree goes, the earlier one stays) and
// TestCollected_KeepCopiesTheSessionTreeIntoAPrivateTemp (no quit: the
// tree is kept).

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCollected_QuitWhileAdoptingLeavesNoTreeBehind(t *testing.T) {
	tmp := collectedTemp(t)
	session := collectedSession(t)
	c := &collectedConfig{}
	t.Cleanup(c.release)

	cleanupDone := make(chan struct{})
	removedDuringAdopt := false
	adopt := func(src, dest string) error {
		// The answers are partly copied: the secret is already there.
		if err := os.MkdirAll(filepath.Join(dest, ".orbit-secrets"), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dest, ".orbit-secrets", "oidc-client-secret"), []byte("s\n"), 0o600); err != nil {
			return err
		}
		// The person quits: the exit's cleanup runs on its own goroutine.
		go func() {
			FinishDeferredCleanup()
			close(cleanupDone)
		}()
		select {
		case <-cleanupDone:
			_, err := os.Lstat(dest)
			removedDuringAdopt = os.IsNotExist(err)
		case <-time.After(time.Second):
			// The cleanup waits for the keep (a lock held): it must
			// still find the tree once the keep returns.
		}
		return nil
	}
	_ = c.keep(session, adopt)
	select {
	case <-cleanupDone:
	case <-time.After(10 * time.Second):
		t.Fatal("the exit's cleanup never finished")
	}
	if left := listCollected(t, tmp); len(left) != 0 {
		t.Fatalf("a quit while adopting left %q in TMPDIR (removed during adopt: %v)", left, removedDuringAdopt)
	}
}

// The tree is created where the cleanup can see it before adopt writes
// anything: a quit even before the first file is copied leaves nothing.
func TestCollected_TreeIsRegisteredBeforeAdoptRuns(t *testing.T) {
	tmp := collectedTemp(t)
	session := collectedSession(t)
	c := &collectedConfig{}
	t.Cleanup(c.release)

	var dest string
	done := make(chan struct{})
	adopt := func(_, d string) error {
		dest = d
		// The quit, before anything is written.
		go func() {
			releaseCollectedTrees()
			close(done)
		}()
		select {
		case <-done:
			if _, err := os.Lstat(d); !os.IsNotExist(err) {
				t.Errorf("the new tree %s survived the exit's cleanup during adopt (err=%v)", d, err)
			}
		case <-time.After(2 * time.Second):
			t.Error("the exit's cleanup waited on the adopt instead of removing the registered tree")
		}
		return os.ErrClosed // the adopt then fails on the removed tree
	}
	if err := c.keep(session, adopt); err == nil {
		t.Fatal("keep reported success after its adopt failed")
	}
	<-done
	if dest == "" {
		t.Fatal("adopt never ran")
	}
	if left := listCollected(t, tmp); len(left) != 0 {
		t.Fatalf("left in TMPDIR: %q", left)
	}
}
