package engine

import (
	"bufio"
	"errors"
	"io"
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
	stream, _, err := start(cmd, false)
	return stream, err
}

// StartInteractive is Start with the child's stdin piped as well — the
// shape the machine prompt protocol needs (configure.sh reads exactly
// one answer line from stdin per prompt line it writes). The caller
// writes answer lines to the returned writer and closes it when done;
// a close with a prompt outstanding is the engine's documented
// end-of-input abort.
func StartInteractive(cmd *exec.Cmd) (*Stream, io.WriteCloser, error) {
	return start(cmd, true)
}

func start(cmd *exec.Cmd, withStdin bool) (*Stream, io.WriteCloser, error) {
	var stdin io.WriteCloser
	if withStdin {
		var err error
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, nil, err
		}
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, err
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

		// Join the stderr drain before Wait: with StderrPipe, Wait
		// closes the pipes as soon as the process exits, and on a slow
		// machine that can cut the drain off mid-read and lose the
		// tail (seen as a real release-gate failure).
		tail := <-tailCh
		err := cmd.Wait()
		done := DoneMsg{Err: err, StderrTail: tail}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			done.ExitCode = exitErr.ExitCode()
		} else if err != nil {
			done.ExitCode = -1
		}
		s.C <- done
		close(s.C)
	}()

	return s, stdin, nil
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
