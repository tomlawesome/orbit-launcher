package deploy

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// pipeWaitDelay is how long a stopped command's Wait still waits for
// its output pipes once the command has exited or its context is done.
// A child the command started can keep stdout open long after the
// command itself is gone; past this delay Wait closes the pipes and
// returns instead of waiting on that child (#207).
const pipeWaitDelay = 2 * time.Second

// detachedCommand builds every engine script run: name args... run in
// dir, detached from the controlling terminal (Setsid), so nothing it
// starts can open /dev/tty and prompt through the alt screen. The
// script leads its own process group, so cancelling ctx stops the whole
// group — the script's own children (docker, sleep, a stray background
// job) with it — and pipeWaitDelay bounds the wait for pipes a survivor
// still holds. Callers that never cancel pass context.Background(); a
// streamed run is stopped by engine.Stream.Kill, which kills the same
// group.
//
// Docker queries and the terminal handoff (BuildInstallCommand) do not
// come through here: they stay attached to the launcher's session.
func detachedCommand(ctx context.Context, dir, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return killGroup(cmd) }
	cmd.WaitDelay = pipeWaitDelay
	return cmd
}

// killGroup kills the process group cmd leads. While any member of the
// group is alive its id cannot be handed to a new process, so the
// signal reaches only cmd's own tree; an empty group reads as done.
func killGroup(cmd *exec.Cmd) error {
	err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
