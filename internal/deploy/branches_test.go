package deploy

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Branch tests for code added under #207 and #208 that the other tests
// leave unexercised. Each one asserts the branch's result, not just that
// it runs.

// limitApplied: a caller deadline far beyond the call's own limit does
// not shorten it.
func TestLimitApplied_ADistantDeadlineLeavesTheOwnLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	if got := limitApplied(ctx, 10*time.Second); got != 10*time.Second {
		t.Errorf("limitApplied = %v, want the call's own 10s", got)
	}
}

// limitApplied: a caller deadline of a few seconds, shorter than the
// own limit, is reported in whole seconds.
func TestLimitApplied_AShorterDeadlineOfSecondsIsRoundedToWholeSeconds(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := limitApplied(ctx, time.Hour); got != 5*time.Second {
		t.Errorf("limitApplied = %v, want the caller's 5s", got)
	}
}

// killGroup: when the group has no members left (the command already
// finished and was reaped) the kill reads as done, not as a failure, so
// cancelling a finished run is not reported as an error.
func TestKillGroup_AnEmptyGroupReadsAsProcessDone(t *testing.T) {
	cmd := detachedCommand(context.Background(), t.TempDir(), "true")
	if err := cmd.Run(); err != nil {
		t.Fatalf("run true: %v", err)
	}
	if err := killGroup(cmd); !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("killGroup on an empty group = %v, want os.ErrProcessDone", err)
	}
}

// shellQuote: an empty value must still arrive as one (empty) shell
// argument, so it is written as two single quotes rather than dropped.
func TestShellQuote_EmptyValueIsAnEmptyQuotedArgument(t *testing.T) {
	if got := shellQuote(""); got != "''" {
		t.Errorf("shellQuote(\"\") = %q, want ''", got)
	}
}

// Detect: an env file that exists but cannot be opened is an error, not
// "no deployment here". A unix socket stats fine but open(2) refuses it
// (ENXIO), which fails the same way for root and non-root users.
func TestDetect_AnEnvFileThatCannotBeOpenedIsAnError(t *testing.T) {
	// Socket paths are limited to about 100 bytes, so use a short dir.
	dir, err := os.MkdirTemp("/tmp", "dt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, EnvFile))
	if err != nil {
		t.Skipf("cannot create a unix socket here: %v", err)
	}
	defer ln.Close()

	d, err := Detect(dir)
	if err == nil {
		t.Fatalf("Detect = %+v, nil; want an error for an unopenable %s", d, EnvFile)
	}
	if d != nil {
		t.Errorf("Detect returned a deployment %+v alongside an error", d)
	}
}
