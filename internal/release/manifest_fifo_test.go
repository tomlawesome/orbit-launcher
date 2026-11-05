package release

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// ORBIT_RELEASE_MANIFEST naming a FIFO (any non-regular file) must not
// block the lookup: it answers promptly with an error, so the target is
// unknown (#336; deploy.Detect opens non-blocking and refuses the same).
func TestResolveTarget_ManifestFifoReturnsPromptlyWithAnError(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "manifest.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("no FIFO here: %v", err)
	}
	t.Setenv(releaseManifestEnv, fifo)

	type result struct {
		t   Target
		err error
	}
	got := make(chan result, 1)
	go func() {
		tg, err := ResolveTarget(context.Background())
		got <- result{tg, err}
	}()
	select {
	case r := <-got:
		if r.err == nil {
			t.Errorf("err = nil, want an error for a non-regular manifest; target %+v", r.t)
		}
		if r.t.Version != "" {
			t.Errorf("version %q, want none", r.t.Version)
		}
	case <-time.After(2 * time.Second):
		// Unblock the stuck open so the goroutine ends with the test.
		if f, err := openNonblockWrite(fifo); err == nil {
			f.Close()
		}
		t.Fatal("ResolveTarget blocked on a FIFO manifest: no answer within 2s")
	}
}

func openNonblockWrite(path string) (*os.File, error) {
	return os.OpenFile(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0)
}
