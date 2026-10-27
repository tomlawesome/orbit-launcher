package ui

import (
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
// InstallModel holds one for its whole life, so Install again re-seeds
// without asking; a new InstallModel starts with none and asks again.
// It is removed when the flow ends (release) and, whatever screen ends
// the launcher, on exit (FinishDeferredCleanup): nothing is kept
// between launcher sessions, and a secret never outlives the session
// outside the deployment.
type collectedConfig struct {
	mu  sync.Mutex
	dir string
}

// collectedTrees are the collected trees not yet removed, so the
// launcher's exit can remove every one.
var collectedTrees = struct {
	mu   sync.Mutex
	held map[*collectedConfig]struct{}
}{held: map[*collectedConfig]struct{}{}}

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
	if err := adopt(sessionTree, dir); err != nil {
		os.RemoveAll(dir)
		return err
	}
	c.mu.Lock()
	old := c.dir
	c.dir = dir
	c.mu.Unlock()
	collectedTrees.mu.Lock()
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
	c.mu.Lock()
	dir := c.dir
	c.dir = ""
	c.mu.Unlock()
	collectedTrees.mu.Lock()
	delete(collectedTrees.held, c)
	collectedTrees.mu.Unlock()
	if dir != "" {
		os.RemoveAll(dir)
	}
}

// releaseCollectedTrees removes every collected tree still held: the
// launcher is exiting.
func releaseCollectedTrees() {
	collectedTrees.mu.Lock()
	var held []*collectedConfig
	for c := range collectedTrees.held {
		held = append(held, c)
	}
	collectedTrees.mu.Unlock()
	for _, c := range held {
		c.release()
	}
}
