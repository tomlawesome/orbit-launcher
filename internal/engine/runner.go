package engine

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// stderrTailLines bounds how much stderr is kept for the failure
// screen: the useful part is always the end, and a stop message can be
// a whole instruction block (#261), which the screen fits to its rows.
const stderrTailLines = 40

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

	cmd *exec.Cmd
	// Terminate and Kill are each idempotent, and separately so: a Kill
	// after a Terminate (the grace ran out) must still send SIGKILL.
	termOnce sync.Once
	killOnce sync.Once

	// mu guards reaped: once cmd.Wait has returned, the PID no longer
	// names the engine and must not be signalled.
	mu     sync.Mutex
	reaped bool
	// done is closed when reaped is set.
	done chan struct{}
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
	// Plain pipes rather than StdoutPipe and StderrPipe: Wait closes
	// those as soon as the process exits, so it can only be called once
	// they are read to the end, and a leftover child holding stdout then
	// keeps Wait, and the bound on the pipes, from ever starting. With
	// pipes of our own, Wait runs alongside the reading (#236).
	stdout, outW, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	stderr, errW, err := os.Pipe()
	if err != nil {
		stdout.Close()
		outW.Close()
		return nil, nil, err
	}
	cmd.Stdout, cmd.Stderr = outW, errW
	err = cmd.Start()
	// The child holds its own copies; the launcher's write ends must go,
	// or the readers never see end-of-file.
	outW.Close()
	errW.Close()
	if err != nil {
		stdout.Close()
		stderr.Close()
		return nil, nil, err
	}

	s := &Stream{C: make(chan any, 64), cmd: cmd, done: make(chan struct{})}

	// Wait as soon as the process exits, then give the readers
	// cmd.WaitDelay to drain what is left, as exec does for its own
	// pipes: past it a child still holding the pipes is no longer
	// waited for.
	waited := make(chan error, 1)
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		s.reaped = true
		s.mu.Unlock()
		close(s.done)
		if cmd.WaitDelay > 0 {
			limit := time.Now().Add(cmd.WaitDelay)
			_ = stdout.SetReadDeadline(limit)
			_ = stderr.SetReadDeadline(limit)
		}
		waited <- err
	}()

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
		stderr.Close()
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
		stdout.Close()

		// Every line, and the stderr tail, is in before DoneMsg goes out.
		tail := <-tailCh
		err := <-waited
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

// Done is closed once the engine process has exited and been reaped.
func (s *Stream) Done() <-chan struct{} { return s.done }

// Terminate asks the engine's whole process group to stop (SIGTERM) and
// leaves it to exit by itself, so install.sh's EXIT trap can roll the
// run back; pair it with a bounded wait on Done and then Kill (#240).
// The engine runs as a session leader (Setsid), so its own children
// (docker, curl) get the signal too. Idempotent. Once the process has
// been reaped Terminate sends nothing at all: by then its PID, and the
// process group it named, may belong to an unrelated process.
func (s *Stream) Terminate() {
	s.termOnce.Do(func() { s.signal(syscall.SIGTERM, false) })
}

// killReapWait bounds Kill's wait for the killed process to be reaped.
// SIGKILL takes effect in milliseconds; the bound is for a process
// stuck in the kernel, which no signal can hurry.
const killReapWait = 2 * time.Second

// Kill ends the engine at once (SIGKILL) — the whole process group,
// and the process itself in case it is not a group leader — and
// returns once the process has been reaped (bounded by killReapWait),
// so a caller that removes the engine's files next does not race a
// process still ending. Idempotent, and sends nothing once the process
// has been reaped (see Terminate). Reports whether the signal reached a
// process that had not yet been reaped, so a caller can tell a kill
// that ended the engine from one that found it already gone.
func (s *Stream) Kill() bool {
	var sent bool
	s.killOnce.Do(func() { sent = s.signal(syscall.SIGKILL, true) })
	if sent {
		select {
		case <-s.done:
		case <-time.After(killReapWait):
		}
	}
	return sent
}

func (s *Stream) signal(sig syscall.Signal, alsoProcess bool) bool {
	if s.cmd == nil || s.cmd.Process == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.reaped {
		return false
	}
	// Negative pid addresses the process group the engine leads.
	// Best effort: the process may already be gone.
	if err := syscall.Kill(-s.cmd.Process.Pid, sig); errors.Is(err, syscall.ESRCH) && !alsoProcess {
		// No such group: the process does not lead one, so it gets the
		// signal itself rather than none at all.
		_ = s.cmd.Process.Signal(sig)
	}
	if alsoProcess {
		_ = s.cmd.Process.Kill()
	}
	return true
}
