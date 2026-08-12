package engine

import (
	"bufio"
	"errors"
	"os/exec"
	"sync"
	"syscall"
)

// stderrTailLines bounds how much stderr is kept for the failure
// screen: the engine's guidance messages are short and the useful part
// is always the end.
const stderrTailLines = 12

// maxLineBytes bounds a single scanned line — a legacy engine's
// progress output (docker pull bars) can be long, but nothing
// legitimate approaches this.
const maxLineBytes = 16 * 1024

// EventMsg is one parsed engine event, in emission order.
type EventMsg struct{ Event Event }

// RawLineMsg is a non-event stdout line — legacy-engine prose or the
// engine's human summary. Display-only by contract: never a machine
// signal.
type RawLineMsg struct{ Text string }

// DoneMsg reports the engine process's end. Err is nil for exit 0;
// otherwise the *exec.ExitError (or start/wait failure). StderrTail is
// the last few stderr lines — the engine's guidance prose — for the
// failure screen's detail block.
type DoneMsg struct {
	Err        error
	ExitCode   int
	StderrTail []string
}

// Stream is a running engine process whose stdout is being consumed as
// the event stream. Messages arrive on C in order; the final message is
// always exactly one DoneMsg, after which C is closed.
type Stream struct {
	C chan any

	cmd  *exec.Cmd
	once sync.Once
}

// Start launches cmd with stdout piped (which is what makes the engine
// select plain mode) and begins streaming. The caller must have built
// cmd so that it cannot prompt — see deploy.BuildEngineCommand, which
// detaches the process from the controlling terminal so the engine's
// documented non-interactive contract engages.
func Start(cmd *exec.Cmd) (*Stream, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	s := &Stream{C: make(chan any, 64), cmd: cmd}

	// stderr is drained concurrently into a tail ring so a chatty
	// stderr can never deadlock the process against a full pipe.
	tailCh := make(chan []string, 1)
	go func() {
		var tail []string
		scanner := bufio.NewScanner(stderr)
		scanner.Buffer(make([]byte, 0, 4096), maxLineBytes)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			tail = append(tail, line)
			if len(tail) > stderrTailLines {
				tail = tail[1:]
			}
		}
		tailCh <- tail
	}()

	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 4096), maxLineBytes)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			if event, ok := ParseEvent(line); ok {
				s.C <- EventMsg{Event: event}
			} else {
				s.C <- RawLineMsg{Text: line}
			}
		}

		err := cmd.Wait()
		done := DoneMsg{Err: err, StderrTail: <-tailCh}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			done.ExitCode = exitErr.ExitCode()
		} else if err != nil {
			done.ExitCode = -1
		}
		s.C <- done
		close(s.C)
	}()

	return s, nil
}

// Kill terminates the engine's whole process group — the engine runs
// as a session leader (Setsid), so its own children (docker, curl) go
// with it. Idempotent; safe after natural exit.
func (s *Stream) Kill() {
	s.once.Do(func() {
		if s.cmd != nil && s.cmd.Process != nil {
			// Negative pid addresses the process group the engine
			// leads. Best effort: the process may already be gone.
			_ = syscall.Kill(-s.cmd.Process.Pid, syscall.SIGTERM)
			_ = s.cmd.Process.Kill()
		}
	})
}
