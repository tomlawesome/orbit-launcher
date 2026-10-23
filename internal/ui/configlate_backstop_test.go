package ui

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// The exit wait's backstop paths (#277). Leaving a session cancels its
// import, so the real flows end within one file and never reach these;
// they are pinned here directly so a slow machine still gets them right.

// A session left before its preparation starts has nothing to prepare.
func TestConfigLate_ABegunAfterStopPreparesNothing(t *testing.T) {
	l := &configLate{}
	l.stop()
	if l.begin() {
		t.Fatal("a preparation began after the session was left")
	}
	done := make(chan struct{})
	go func() { l.awaitPreparation(time.Second); close(done) }()
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("waiting on a preparation that never began did not return at once")
	}
}

// Past the limit, a release still waiting on a preparation that has not
// ended runs at the exit itself, once, and the set forgets the session.
func TestConfigLate_PastTheLimitTheWaitingReleaseRunsAtExit(t *testing.T) {
	l := &configLate{}
	if !l.begin() {
		t.Fatal("setup: the preparation did not begin")
	}
	var runs atomic.Int32
	if !l.releaseRunFilesAfter(func() { runs.Add(1) }) {
		t.Fatal("setup: the release was not kept for the running preparation")
	}
	l.awaitPreparation(10 * time.Millisecond)
	if got := runs.Load(); got != 1 {
		t.Fatalf("the waiting release ran %d times at the limit, want once", got)
	}
	for _, held := range deferredReleases.list() {
		if held == l {
			t.Fatal("the session is still listed as holding a release")
		}
	}
	l.end()
	if got := runs.Load(); got != 1 {
		t.Fatalf("the preparation's end ran the release again (%d runs)", got)
	}
}

// Past the limit, a release the preparation has already begun is waited
// for to its end: the exit never overtakes a removal under way.
func TestConfigLate_PastTheLimitARemovalUnderWayIsWaitedFor(t *testing.T) {
	l := &configLate{}
	if !l.begin() {
		t.Fatal("setup: the preparation did not begin")
	}
	started, unblock := make(chan struct{}), make(chan struct{})
	var finished atomic.Bool
	l.releaseRunFilesAfter(func() {
		close(started)
		<-unblock
		finished.Store(true)
	})
	go l.end()
	<-started
	go func() { time.Sleep(50 * time.Millisecond); close(unblock) }()
	l.awaitPreparation(10 * time.Millisecond)
	if !finished.Load() {
		t.Fatal("the exit wait returned while the removal was still running")
	}
}

// Set after WithDeploymentStatus, the services reader is still what the
// splash counts with.
func TestAppModel_WithServicesReaderAfterTheStatusProbeIsWhatTheSplashCounts(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
	testutil.WriteComposeFile(t, filepath.Join(dir, deploy.ComposeFile)) // installed: only then is there a probe
	reader := &readerRecord{have: readerAppHave}
	m := NewAppModelNoAnimation()
	m.targetDir = dir
	m = m.WithDeploymentStatus(func(context.Context, string) deploy.HealthReason { return deploy.HealthNotResponding })
	m = m.WithServicesReader(reader.read)
	cmd := m.splash.probeHealthCmd()
	if cmd == nil {
		t.Fatal("the splash has no probe")
	}
	msg, ok := cmd().(healthResultMsg)
	if !ok || !msg.counted || msg.running != 2 {
		t.Fatalf("probe = %+v, want the reader's count (2 running, counted)", msg)
	}
}
