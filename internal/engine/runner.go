package engine

import (
	"bufio"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
)

// stderrTailLines bounds how much stderr is kept for the failure
// screen: the engine's guidance messages are short and the useful part
// is always the end.
const stderrTailLines = 12

// maxLineBytes bounds how much of a single line is kept — a legacy
// engine's progress output (docker pull bars) can be long, but nothing
// legitimate approaches this. Anything past it is discarded; the line
// itself is still delivered, and reading continues.
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
		readLines(stderr, func(line string) {
			tail = append(tail, line)
			if len(tail) > stderrTailLines {
				tail = tail[1:]
			}
		})
		tailCh <- tail
	}()

	go func() {
		readLines(stdout, func(line string) {
			if event, ok := ParseEvent(line); ok {
				s.C <- EventMsg{Event: event}
			} else {
				s.C <- RawLineMsg{Text: line}
			}
		})

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

// readLines calls emit once per newline-terminated line, skipping blank
// ones, and returns when the reader ends. A line longer than
// maxLineBytes is truncated to it and the remainder discarded — but the
// line is still delivered and reading carries on.
//
// bufio.Scanner cannot be used here. To Scanner an over-long line is a
// permanent error: Scan returns false and the loop ends. That loop is
// the only thing draining the engine's stdout pipe, so the engine then
// blocks on a full pipe, its stderr never reaches EOF, cmd.Wait is
// never called and DoneMsg is never sent — the launcher sits on a
// static screen while the install completes underneath it (#157). The
// real engine reaches an over-long line whenever a phase reports
// progress with carriage returns and no newline for long enough, which
// is what install.sh's database wait does.
func readLines(r io.Reader, emit func(string)) {
	br := bufio.NewReaderSize(r, 4096)
	var line []byte
	for {
		chunk, err := br.ReadSlice('\n')
		if room := maxLineBytes - len(line); room > 0 {
			if len(chunk) > room {
				chunk = chunk[:room]
			}
			line = append(line, chunk...)
		}
		if err == bufio.ErrBufferFull {
			// The line is longer than the read buffer — keep pulling it
			// in (and keep draining the pipe) until its newline arrives.
			continue
		}
		if err != nil {
			if text := trimLineEnd(line); text != "" {
				emit(text)
			}
			return
		}
		if text := trimLineEnd(line); text != "" {
			emit(text)
		}
		line = line[:0]
	}
}

// trimLineEnd drops the line terminator a reader kept, matching what
// bufio.Scanner used to hand back.
func trimLineEnd(line []byte) string {
	return strings.TrimRight(string(line), "\r\n")
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
