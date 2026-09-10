package ui

import (
	"sync/atomic"

	tea "github.com/charmbracelet/bubbletea"
)

// ProgramSender hands a running program's Send method to code that has
// to push messages into the event loop without being asked for them
// first — currently the engine stream reader (#159).
//
// It exists because of an ordering problem, not a design preference:
// tea.NewProgram takes the model, so the model cannot be built holding
// the program. A sender is built first, given to the model, and
// attached to the program afterwards. Nothing sends before Run starts,
// because the only sender is a reader that a run has to have begun to
// exist at all.
//
// The pointer is atomic so the reader goroutine and the main goroutine
// that attaches the program are not racing over it.
type ProgramSender struct {
	program atomic.Pointer[tea.Program]
}

// NewProgramSender returns a sender with nothing attached yet.
func NewProgramSender() *ProgramSender { return &ProgramSender{} }

// Attach binds the sender to the running program.
func (s *ProgramSender) Attach(p *tea.Program) { s.program.Store(p) }

// Send delivers one message into the event loop, and does nothing
// before Attach. tea.Program.Send is itself safe once the program has
// finished: it selects on the program's context.
func (s *ProgramSender) Send(msg tea.Msg) {
	if p := s.program.Load(); p != nil {
		p.Send(msg)
	}
}
