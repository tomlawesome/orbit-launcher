package ui

// The package's shared test helpers, one home each (#215; #199 UI-8,
// UI-19): keys, one Update helper for every model, one command driver,
// one screen helper per kind, one engine event builder and the sink an
// engine run pushes into.

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// A key press is a code plus the modifiers held with it, and Text is the
// printable character it produced — empty for special keys like Enter.
func key(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }
func runeKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// ctrlC is the one place the Ctrl-C key is spelled out.
func ctrlC() tea.KeyPressMsg { return tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl} }

// updateAs feeds msgs to m in order and returns the model as its own
// type, with the command the last message produced.
func updateAs[T tea.Model](t *testing.T, m T, msgs ...tea.Msg) (T, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		var updated tea.Model
		updated, cmd = m.Update(msg)
		next, ok := updated.(T)
		if !ok {
			t.Fatalf("Update returned a %T, want a %T", updated, m)
		}
		m = next
	}
	return m, cmd
}

// sink collects the messages a model pushes into the event loop from
// outside the command chain. Only the engine stream reader does that
// (#159); in production the same route is tea.Program.Send.
type sink struct{ msgs chan tea.Msg }

func newSink() *sink { return &sink{msgs: make(chan tea.Msg, 64)} }

func (s *sink) Send(msg tea.Msg) { s.msgs <- msg }

// drive runs cmd and every command its messages lead to, the way the
// program would: a tea.BatchMsg is expanded and each of its commands
// run, and every other message goes back into Update. A test driving an
// engine run passes that run's sink, which drive drains the way
// bubbletea drains its own message queue — a command result and a
// pushed message are the same thing once they reach Update — until it
// has been quiet for two seconds. The sky's ticks are dropped: they only
// move stars, and a frozen test has no use for an endless chain.
func drive[M tea.Model](t *testing.T, m M, cmd tea.Cmd, pushed ...*sink) M {
	t.Helper()
	var queue *sink
	if len(pushed) > 0 {
		queue = pushed[0]
	}
	cmds := []tea.Cmd{cmd}
	for i := 0; ; i++ {
		if i > 1000 {
			t.Fatal("command chain did not settle")
		}
		var msg tea.Msg
		switch {
		case len(cmds) > 0:
			next := cmds[0]
			cmds = cmds[1:]
			if next == nil {
				continue
			}
			msg = next()
		case queue != nil:
			select {
			case msg = <-queue.msgs:
			case <-time.After(2 * time.Second):
				return m
			}
		default:
			return m
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			cmds = append(cmds, batch...)
			continue
		}
		if _, ok := msg.(tickMsg); ok || msg == nil {
			continue
		}
		var more tea.Cmd
		m, more = updateAs(t, m, msg)
		// Nothing follows the stream ending, so waiting for more would
		// only spend the timeout above.
		if _, ended := msg.(engineStreamEndedMsg); ended && more == nil && len(cmds) == 0 {
			return m
		}
		cmds = append(cmds, more)
	}
}

// screen is what a person would see of a model, escapes stripped.
func screen(m tea.Model) string { return ansi.Strip(m.View().Content) }

// runScreen is an engine run's screen at 80×26, escapes stripped; a run
// is drawn by its owning flow, so it has view(w, h) rather than View().
func runScreen(r engineRun) string { return ansi.Strip(r.view(80, 26)) }

// ev builds the engine event a contract-era run emits, as the message
// the stream delivers; .Event is the bare event.
func ev(phase, component, state, reason, action string) engine.EventMsg {
	return engine.EventMsg{Event: engine.Event{Phase: phase, Component: component, State: state, Reason: reason, Action: action}}
}

// rowLabels are every label a menu row carries on the screens the menu
// grammar tests read (#228); screenLabels picks these out of a screen.
var rowLabels = map[string]bool{
	"Install": true, "Update": true, "Repair": true, "Remove": true,
	"Install now": true, "Update Orbit": true, "Stand down Orbit": true,
	"Continue anyway": true, "Open the guided installer": true,
	"Get into Orbit": true, "Copy command": true, "Copy command · copied": true,
	"Cancel": true, "Back": true, "Go back": true, "Menu": true, "Exit": true, "Terminal": true,
}

// screenRow is one screen line with the starfield, the caret and the
// padding taken off its ends — the row's label when the line is a row.
func screenRow(line string) string {
	return strings.Trim(line, " ▸.·✦●⟡")
}

// screenLabels are the menu rows on a stripped screen, top to bottom,
// by label alone (the caret and the sky are not part of a row).
func screenLabels(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if row := screenRow(line); rowLabels[row] {
			out = append(out, row)
		}
	}
	return out
}

// caretLabel is the label of the row the caret is on in a stripped
// screen, "" when no row carries one.
func caretLabel(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "▸") && rowLabels[screenRow(line)] {
			return screenRow(line)
		}
	}
	return ""
}

// moveCaretTo presses Down until the caret is on label, and fails if it
// never gets there within a lap of the rows.
func moveCaretTo[M tea.Model](t *testing.T, m M, label string) M {
	t.Helper()
	for i := 0; i <= len(screenLabels(screen(m))); i++ {
		if caretLabel(screen(m)) == label {
			return m
		}
		m, _ = updateAs(t, m, key(tea.KeyDown))
	}
	t.Fatalf("the caret never reached %q; rows %q:\n%s", label, screenLabels(screen(m)), screen(m))
	return m
}
