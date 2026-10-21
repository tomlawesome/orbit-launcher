//go:build unix

package deploy

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// namedPipeEnv makes dir/.env-orbit a named pipe with no writer, the
// file that makes an open(2) block forever.
func namedPipeEnv(t *testing.T) (dir, pipe string) {
	t.Helper()
	dir = t.TempDir()
	pipe = filepath.Join(dir, EnvFile)
	if err := syscall.Mkfifo(pipe, 0o600); err != nil {
		t.Skipf("cannot make a named pipe here: %v", err)
	}
	return dir, pipe
}

// within runs f and reports whether it returned in a couple of seconds.
// If it did not, the pipe is opened from the other side so the blocked
// reader is released and cannot leak into other tests or touch t late.
func within(pipe string, f func()) bool {
	done := make(chan struct{})
	go func() { f(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(2 * time.Second):
		// Release the blocked reader: open and close the pipe from the
		// other side until it returns (it then reads end-of-file).
		for {
			select {
			case <-done:
				return false
			case <-time.After(20 * time.Millisecond):
				if w, err := os.OpenFile(pipe, os.O_RDWR|syscall.O_NONBLOCK, 0); err == nil {
					w.Close()
				}
			}
		}
	}
}

// 236-c10 (SP2-1): a .env-orbit that is not a regular file is not a
// deployment, and reading it must never block.
func TestDetect_NamedPipeIsNotReadAndNeverBlocks(t *testing.T) {
	dir, pipe := namedPipeEnv(t)
	var d *Deployment
	if !within(pipe, func() { d, _ = Detect(dir) }) {
		t.Fatalf("Detect is still blocked after 2s on a named pipe called %s", EnvFile)
	}
	if d != nil {
		t.Errorf("Detect on a named pipe = %+v, want no deployment", d)
	}
}

// 236-c10 (SP2-1): Repair reads the deployment before its script trust
// check; a planted pipe must not hang it, and it must not build a command.
func TestRepairCommand_NamedPipeNeverBlocks(t *testing.T) {
	dir, pipe := namedPipeEnv(t)
	var cmd *exec.Cmd
	var err error
	if !within(pipe, func() { cmd, err = RepairCommand(dir, RepairCheck) }) {
		t.Fatalf("RepairCommand is still blocked after 2s on a named pipe called %s", EnvFile)
	}
	if cmd != nil {
		t.Errorf("RepairCommand built %v for a named pipe", cmd.Args)
	}
	if err == nil {
		t.Error("RepairCommand on a named pipe returned neither a command nor an error")
	}
}

// #195 round 5: when scripts/ is a plain file the lookup hits ENOTDIR
// beneath it. That is "scripts unavailable" like every other absent case,
// not a generic read error, for repair and for configuration alike.
func TestScriptLookup_ScriptsBeingAFileIsUnavailable(t *testing.T) {
	t.Run("repair", func(t *testing.T) {
		dir := newLookupDir(t)
		testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
		writeExact(t, filepath.Join(dir, "scripts"), 0o644)
		cmd, err := RepairCommand(dir, RepairCheck)
		if cmd != nil || !errors.Is(err, ErrRepairUnavailable) {
			t.Errorf("RepairCommand with scripts as a file = %v, %v; want ErrRepairUnavailable", cmd, err)
		}
	})
	t.Run("configure", func(t *testing.T) {
		dir := newLookupDir(t)
		writeExact(t, filepath.Join(dir, "scripts"), 0o644)
		endSession, err := OpenConfigTree(dir)
		if err == nil && endSession != nil {
			endSession()
		}
		if !errors.Is(err, ErrNoConfigTree) {
			t.Errorf("OpenConfigTree with scripts as a file: err = %v, want ErrNoConfigTree", err)
		}
	})
}
