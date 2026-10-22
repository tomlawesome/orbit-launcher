package engine

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// #236 item 9 (re-checked in .agents/lows-v043/reverify-236.md): an
// install, repair or configure script that exits but leaves a background
// child holding its stdout must not leave the stream waiting for the
// child. The caller's bound on pipes (cmd.WaitDelay, which
// deploy.detachedCommand sets to pipeWaitDelay) has to start when the
// process exits, not after stdout has been read to end-of-file, which a
// leftover child holding the pipe never lets happen.
func TestStream_ProcessExitEndsTheStreamEvenWhileAChildHoldsStdout(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command("bash", "-c", fmt.Sprintf(`
echo "%s"
sleep 30 &
echo $! > "%s"
exit 0
`, stopEvent, pidFile))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.WaitDelay = 500 * time.Millisecond
	s, err := Start(cmd)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		s.Kill()
		if raw, err := os.ReadFile(pidFile); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	})

	sawEvent := false
	deadline := time.After(6 * time.Second)
	for {
		select {
		case msg, ok := <-s.C:
			if !ok {
				t.Fatal("the stream closed without a DoneMsg")
			}
			switch msg.(type) {
			case EventMsg:
				sawEvent = true
			case DoneMsg:
				if !sawEvent {
					t.Error("the engine's event line was lost")
				}
				return
			}
		case <-deadline:
			t.Fatal("no DoneMsg 6s after the engine exited with WaitDelay 500ms: " +
				"a background child holding stdout keeps the reader (and so the wait bound) from starting")
		}
	}
}
