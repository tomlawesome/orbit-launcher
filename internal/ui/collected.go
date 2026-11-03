package ui

import (
	"errors"
	"os"
	"sync"
)

// collectedConfig is the guided configuration a launcher session has
// collected, kept for that session only: a private (0700) temp tree
// holding the session's .env-orbit and, when it has one,
// .orbit-secrets/. It is never the deployment folder. Every engine run
// that follows has its fresh configuration tree seeded from it
// (deploy.BuildEngineCommandWithConfig), so the installer's own
// transaction is what writes the answers into the folder, and its
// rollbacks are what take them out again (orbit docs/engine-events.md,
// Non-interactive contract, #1519).
//
// The app shares one across its Install flows, so Install again and
// Install from the menu re-seed without asking; an InstallModel built
// on its own starts with none and asks again. It is removed (release)
// when a Remove stand-down completes and, whatever screen ends the
// launcher, on exit (FinishDeferredCleanup): nothing is kept
// between launcher sessions, and a secret never outlives the session
// outside the deployment.
type collectedConfig struct {
	mu  sync.Mutex
	dir string
}

// collectedTrees are the collected trees not yet removed, so the
// launcher's exit can remove every one: held are the kept trees, and
// adopting the new trees keep is still copying answers into. A new tree
// is registered here the moment it exists, before anything is written
// into it (the register-at-creation pattern of a cleanup registry), so
// an exit while adopt runs removes it too, without waiting for adopt.
var collectedTrees = struct {
	mu       sync.Mutex
	held     map[*collectedConfig]struct{}
	adopting map[string]struct{}
}{held: map[*collectedConfig]struct{}{}, adopting: map[string]struct{}{}}

// errCollectedReleased is keep's failure when the launcher's exit
// removed the new tree while adopt was copying into it.
var errCollectedReleased = errors.New("the launcher is closing: the collected configuration was removed")

// seed is the collected tree to seed the next run from, or "" when
// nothing has been collected. Safe on a nil receiver.
func (c *collectedConfig) seed() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dir
}

// keep copies a configuration session's answers out of sessionTree
// (adopt is deploy.AdoptConfig or a test's) into a new private tree,
// and makes that the one later runs are seeded from. The tree it
// replaces is removed only once the new one is complete, so a failed
// keep leaves the earlier answers in place.
func (c *collectedConfig) keep(sessionTree string, adopt adoptConfigFunc) error {
	// MkdirTemp makes it 0700, owned by this user, and empty.
	dir, err := os.MkdirTemp("", "orbit-launcher-collected-*")
	if err != nil {
		return err
	}
	collectedTrees.mu.Lock()
	collectedTrees.adopting[dir] = struct{}{}
	collectedTrees.mu.Unlock()

	adoptErr := adopt(sessionTree, dir)

	collectedTrees.mu.Lock()
	_, stillOurs := collectedTrees.adopting[dir]
	delete(collectedTrees.adopting, dir)
	if adoptErr != nil || !stillOurs {
		collectedTrees.mu.Unlock()
		// Removed again even when the exit already did: adopt may have
		// written into it after that.
		os.RemoveAll(dir)
		if adoptErr != nil {
			return adoptErr
		}
		return errCollectedReleased
	}
	c.mu.Lock()
	old := c.dir
	c.dir = dir
	c.mu.Unlock()
	collectedTrees.held[c] = struct{}{}
	collectedTrees.mu.Unlock()
	if old != "" {
		os.RemoveAll(old)
	}
	return nil
}

// release removes the collected tree. Safe on a nil receiver and when
// nothing was collected.
func (c *collectedConfig) release() {
	if c == nil {
		return
	}
	// The registry's lock first, as keep takes them, so a keep finishing
	// now either lands before this (and its tree goes) or after.
	collectedTrees.mu.Lock()
	c.mu.Lock()
	dir := c.dir
	c.dir = ""
	c.mu.Unlock()
	delete(collectedTrees.held, c)
	collectedTrees.mu.Unlock()
	if dir != "" {
		os.RemoveAll(dir)
	}
}

// releaseCollectedTrees removes every collected tree still held or
// being adopted: the launcher is exiting. It does not wait for an adopt
// in progress; that keep finds its tree gone and fails.
func releaseCollectedTrees() {
	collectedTrees.mu.Lock()
	var held []*collectedConfig
	for c := range collectedTrees.held {
		held = append(held, c)
	}
	var adopting []string
	for dir := range collectedTrees.adopting {
		adopting = append(adopting, dir)
		delete(collectedTrees.adopting, dir)
	}
	collectedTrees.mu.Unlock()
	for _, dir := range adopting {
		os.RemoveAll(dir)
	}
	for _, c := range held {
		c.release()
	}
}
