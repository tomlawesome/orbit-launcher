package ui

// The package's shared test helpers, one home each (#215; #199 UI-8,
// UI-19): keys, one Update helper for every model, one command driver,
// one screen helper per kind, one engine event builder and the sink an
// engine run pushes into.

import (
	"bytes"
	"io"
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
//
// first makes the driver take a pushed message before the next queued
// command runs, as the program's own loop sees a message arriving while
// a command works; drive otherwise runs the whole command chain first.
type sink struct {
	msgs  chan tea.Msg
	first bool
}

func newSink() *sink { return &sink{msgs: make(chan tea.Msg, 64)} }

func (s *sink) Send(msg tea.Msg) { s.msgs <- msg }

// drive runs cmd and every command its messages lead to, the way the
// program would: a tea.BatchMsg is expanded and each of its commands
// run, and every other message goes back into Update. A test driving an
// engine run passes that run's sink, which drive drains the way
// bubbletea drains its own message queue — a command result and a
// pushed message are the same thing once they reach Update. The sky's
// ticks are dropped: they only move stars, and a frozen test has no use
// for an endless chain.
//
// With a sink, drive ends when the engine stream ends and nothing more
// follows it. A drive that stops anywhere else says what ends it with
// driveUntil, which returns the moment that holds; a drive that never
// reaches its end fails after driveDeadline with the screen it was
// stuck on, rather than returning early with a screen nobody checked.
func drive[M tea.Model](t *testing.T, m M, cmd tea.Cmd, pushed ...*sink) M {
	t.Helper()
	return driveLoop(t, m, cmd, nil, nil, 0, pushed...)
}

// driveUntil is drive that also ends as soon as done(m) holds once every
// command has run and every message already pushed has been taken. done
// must be false for the model the drive starts from, or the drive ends
// before the message it was started for has arrived.
func driveUntil[M tea.Model](t *testing.T, m M, cmd tea.Cmd, done func(tea.Model) bool, pushed ...*sink) M {
	t.Helper()
	return driveLoop(t, m, cmd, nil, done, 0, pushed...)
}

// driveWatching is drive that also looks at the screen after every update
// on the way and fails the test if it ever shows one of never. A check of
// the screen the drive ends on cannot see a screen that was shown on the
// way and replaced; this one can.
func driveWatching[M tea.Model](t *testing.T, m M, cmd tea.Cmd, never []string, pushed ...*sink) M {
	t.Helper()
	return driveLoop(t, m, cmd, never, nil, 0, pushed...)
}

// driveWatchingUntil is driveUntil and driveWatching together.
func driveWatchingUntil[M tea.Model](t *testing.T, m M, cmd tea.Cmd, never []string, done func(tea.Model) bool, pushed ...*sink) M {
	t.Helper()
	return driveLoop(t, m, cmd, never, done, 0, pushed...)
}

// driveQuiet is the one drive that waits out a clock. It is for a test
// that asserts nothing more happens: it takes every message pushed and
// ends once window has passed with none, since "nothing arrived" is
// only ever shown by waiting.
func driveQuiet[M tea.Model](t *testing.T, m M, cmd tea.Cmd, window time.Duration, pushed ...*sink) M {
	t.Helper()
	return driveLoop(t, m, cmd, nil, nil, window, pushed...)
}

// driveDeadline is how long a drive with a sink may wait for what ends
// it. Generous: a drive that reaches its end returns at once, so the
// deadline is only ever spent by a drive that has already failed.
const driveDeadline = 30 * time.Second

func driveLoop[M tea.Model](t *testing.T, m M, cmd tea.Cmd, never []string, done func(tea.Model) bool, quiet time.Duration, pushed ...*sink) M {
	t.Helper()
	var queue *sink
	if len(pushed) > 0 {
		queue = pushed[0]
	}
	deadline := time.Now().Add(driveDeadline)
	cmds := []tea.Cmd{cmd}
	for i := 0; ; i++ {
		if i > 1000 {
			t.Fatal("command chain did not settle")
		}
		var msg tea.Msg
		taken := false
		if queue != nil && queue.first {
			select {
			case msg = <-queue.msgs:
				taken = true
			default:
			}
		}
		switch {
		case taken:
		case len(cmds) > 0:
			next := cmds[0]
			cmds = cmds[1:]
			if next == nil {
				continue
			}
			msg = next()
		case queue != nil:
			// Everything already pushed is taken before done is asked, so
			// a model is never judged with a message waiting for it.
			select {
			case msg = <-queue.msgs:
			default:
				if done != nil && done(m) {
					return m
				}
				wait, stop := time.Until(deadline), "did not settle"
				if quiet > 0 {
					wait, stop = quiet, ""
				}
				timer := time.NewTimer(wait)
				select {
				case msg = <-queue.msgs:
					timer.Stop()
				case <-timer.C:
					if stop == "" {
						return m
					}
					t.Fatalf("the drive %s within %v; the screen it stopped on:\n%s", stop, driveDeadline, screen(m))
				}
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
		if len(never) > 0 {
			shown := screen(m)
			for _, gone := range never {
				if strings.Contains(shown, gone) {
					t.Errorf("a screen showing %q came up after %T:\n%s", gone, msg, shown)
				}
			}
		}
		// Nothing follows the stream ending, so waiting for more would
		// only spend the deadline above.
		if _, ended := msg.(engineStreamEndedMsg); ended && more == nil && len(cmds) == 0 {
			return m
		}
		cmds = append(cmds, more)
	}
}

// runOf is the engine run of the flow m is showing: an Install or Update
// model's own, or the app's while it is on one of those flows.
func runOf(m tea.Model) (engineRun, bool) {
	switch v := m.(type) {
	case InstallModel:
		return v.run, v.state == installStateRunning
	case UpdateModel:
		return v.run, true
	case AppModel:
		switch v.state {
		case appStateInstall:
			return v.install.run, v.install.state == installStateRunning
		case appStateUpdate:
			return v.update.run, true
		}
	}
	return engineRun{}, false
}

// runIn is a drive's end: the model's engine run is in one of states.
// A failed run is not counted until the stopped screen's folder read has
// answered, because that answer changes what the screen shows.
func runIn(states ...engineRunState) func(tea.Model) bool {
	return func(m tea.Model) bool {
		r, ok := runOf(m)
		if !ok || (r.state == runFailed && r.folderPending) {
			return false
		}
		for _, s := range states {
			if r.state == s {
				return true
			}
		}
		return false
	}
}

// runEnded is a drive's end when the run has finished either way.
var runEnded = runIn(runFailed, runSucceeded)

// runAtRest is a drive's end when the run is waiting for the person (the
// sign-in choice), finished, or left: nothing is on its way into it.
var runAtRest = runIn(runConfigSignInMode, runFailed, runSucceeded, runLeft)

// appAtRest is a drive's end for the whole app: a run at rest, or the
// success screen the app moves to when a run succeeds.
var appAtRest = anyOf(onApp(appStateSuccess), runAtRest)

// shows is a drive's end when the screen holds every one of wants.
func shows(wants ...string) func(tea.Model) bool {
	return func(m tea.Model) bool {
		s := screen(m)
		for _, want := range wants {
			if !strings.Contains(s, want) {
				return false
			}
		}
		return true
	}
}

// settleQuiet is how long driveQuiet waits for a message that a test
// says must not come: the one clock left in the drives, kept short.
const settleQuiet = 500 * time.Millisecond

// anyOf is a drive's end when any of conds holds.
func anyOf(conds ...func(tea.Model) bool) func(tea.Model) bool {
	return func(m tea.Model) bool {
		for _, c := range conds {
			if c(m) {
				return true
			}
		}
		return false
	}
}

// onApp is a drive's end when the app is on state.
func onApp(state appState) func(tea.Model) bool {
	return func(m tea.Model) bool {
		a, ok := m.(AppModel)
		return ok && a.state == state
	}
}

// shortNoticeTick makes the notice's countdown redraw every millisecond
// instead of every second for the rest of the test. A test that shortens
// the countdown itself (noticeDuration, flowNoticeDuration) calls this
// too, or every redraw still waits a real second. Call it before the
// program starts, so the cleanup runs after the program has stopped.
func shortNoticeTick(t testing.TB) {
	t.Helper()
	old := noticeTickInterval
	noticeTickInterval = time.Millisecond
	t.Cleanup(func() { noticeTickInterval = old })
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
	"Update Orbit": true, "Stand down Orbit": true,
	"Continue anyway": true, "Open the guided installer": true,
	"Install again": true, "Update again": true, "Open Repair": true,
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

// recordedOutput wraps a teatest output stream and keeps every byte read
// through it. teatest.WaitFor consumes what it reads, so a "was X ever
// shown" check made after a later wait sees only what came after that
// wait; hand every wait this wrapper instead of tm.Output() and ask it
// afterwards. everything also reads whatever is still unread.
type recordedOutput struct {
	r    io.Reader
	seen bytes.Buffer
}

func recordOutput(r io.Reader) *recordedOutput { return &recordedOutput{r: r} }

func (o *recordedOutput) Read(p []byte) (int, error) {
	n, err := o.r.Read(p)
	o.seen.Write(p[:n])
	return n, err
}

// everything is all the output seen so far with the escape codes
// removed, so text the renderer wrote in separate pieces reads as one.
func (o *recordedOutput) everything() string {
	_, _ = io.Copy(io.Discard, o)
	return ansi.Strip(o.seen.String())
}

// bestOf is the shortest of runs timings of f. The shortest is the one a
// busy host disturbed least, so it stands for the work itself.
func bestOf(runs int, f func()) time.Duration {
	best := time.Duration(1<<63 - 1)
	for range runs {
		start := time.Now()
		f()
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best
}

// requireNearLinear fails if run(big) takes much more than big/small times
// as long as run(small): twice and a quarter that, so a four-fold input
// may take up to nine times as long where linear work takes four and
// quadratic work sixteen. It compares two sizes on the same host in the
// same breath, so a slow or loaded runner changes both sides and nothing
// here depends on how long a second is. big must be four times small.
func requireNearLinear(t *testing.T, what string, small, big int, run func(size int)) {
	t.Helper()
	if big != 4*small {
		t.Fatalf("%s: sizes %d and %d are not four-fold", what, small, big)
	}
	run(small) // once to warm up, unmeasured
	shortest := bestOf(7, func() { run(small) })
	longest := bestOf(7, func() { run(big) })
	ratio := float64(longest) / float64(shortest)
	if ratio > 9 {
		t.Errorf("%s: %d times the input took %.1f times as long (%v for %d, %v for %d); linear work takes about 4, quadratic about 16",
			what, big/small, ratio, shortest, small, longest, big)
	}
}
