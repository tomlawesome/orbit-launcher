package ui

import (
	"fmt"
	"io"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// promptSession is one machine-prompt session (#211): the engine asks
// for a field, the person types an answer, the engine rejects, accepts
// or gives up. In-console configuration and Repair's credential
// rotation both speak this grammar and draw it the same way; what Esc
// does and what surrounds the prompt stay with each flow.
type promptSession struct {
	prompt *engine.Prompt // non-nil while one answer is being typed
	reason string         // honest words for the last rejection
	// last and aborted name the field last asked for and the one the
	// engine gave up on (its prompt-abort line); "" until they happen.
	last, aborted string
	// sawPrompt records that the engine spoke the protocol at all:
	// configuration tells a legacy configure.sh apart by its absence.
	sawPrompt bool
	input     inputLine
}

// applyLine takes one line of the engine's output. ok is false when the
// line is not a prompt line, so the caller can read it as its own.
func (s promptSession) applyLine(text string) (_ promptSession, ok bool) {
	parsed, ok := engine.ParsePromptLine(text)
	if !ok {
		return s, false
	}
	switch p := parsed.(type) {
	case engine.Prompt:
		prompt := p
		s.prompt = &prompt
		s.last = p.Field
		s.sawPrompt = true
		s.input = inputLine{}
	case engine.PromptReject:
		s.reason = rejectionWords(p.Reason)
		s.prompt = nil
	case engine.PromptAccept:
		s.reason = ""
		s.prompt = nil
	case engine.PromptAbort:
		// The engine gives up (third rejection or closed input) and
		// exits through its refusal path.
		s.aborted = p.Field
		s.prompt = nil
	}
	return s, true
}

// key applies one key press while a prompt is open; with none open it
// does nothing. Enter writes the answer and its newline to stdin (when
// there is one) and closes the prompt, returning the field answered and
// the answer so a flow can keep what it needs (configuration's origin);
// field is "" for every other key. Esc is the caller's, and never
// reaches here.
func (s promptSession) key(msg tea.KeyPressMsg, stdin io.Writer) (_ promptSession, field, answer string) {
	if s.prompt == nil {
		return s, "", ""
	}
	if msg.Code == tea.KeyEnter {
		field, answer = s.prompt.Field, s.input.String()
		if stdin != nil {
			fmt.Fprintln(stdin, answer)
		}
		s.prompt, s.input = nil, inputLine{}
		return s, field, answer
	}
	s.input, _ = s.input.edit(msg)
	return s, "", ""
}

// paste adds a paste to the answer while a prompt is open (inputLine.
// paste); it never submits, whatever the text holds. With no prompt
// open it does nothing.
func (s promptSession) paste(text string) promptSession {
	if s.prompt != nil {
		s.input = s.input.paste(text)
	}
	return s
}

// view writes the session's part of the screen: the waiting words, or
// the field's label, guidance, input row, rejection and attempt count.
// origin is the accepted APP_URL, for guidance that names this
// deployment's callback URL ("" when the flow has none). width is the
// screen's: an answer too long for the input row shows by its end, so
// the cursor stays in view (#223); 0 (not yet sized) shows it whole.
// frame is the flow's spinner frame, for the working line shown while
// no prompt is open.
func (s promptSession) view(b *strings.Builder, origin string, width, frame int) {
	p := s.prompt
	if p == nil {
		fmt.Fprintln(b, workingLine(frame, "talking to the engine", ""))
		return
	}

	label, hint, notes := promptWords(p.Field, origin)
	fmt.Fprintln(b, style.Ink.Render(label))
	if hint != "" {
		fmt.Fprintln(b, style.Tagline.Render(hint))
	}
	for _, note := range notes {
		fmt.Fprintln(b, style.MutedText.Render(note))
	}
	fmt.Fprintln(b)

	// The input row. A secret is never echoed — not even its length.
	caret := style.MenuCaret.Render(style.SymbolSelected) + " "
	cursor := style.AccentText.Render(style.SymbolCursor)
	// Nor, for a secret, where the cursor sits (#258): it stays drawn
	// at the start, wherever the editing keys have moved it.
	var before, after string
	switch {
	case p.Kind == "secret": // both stay empty
	case width > 0:
		before, after = s.input.around(max(width-lipgloss.Width(caret)-lipgloss.Width(cursor), 0))
	default:
		before, after = s.input.around(lipgloss.Width(s.input.String()))
	}
	row := caret + style.Ink.Render(before) + cursor
	if after != "" { // a cursor at the end draws the row byte for byte as before
		row += style.Ink.Render(after)
	}
	fmt.Fprintln(b, row)

	if s.reason != "" {
		fmt.Fprintln(b)
		fmt.Fprintln(b, style.DegradedText.Render(s.reason))
	}
	if p.Attempt > 1 {
		fmt.Fprintln(b, style.Tagline.Render(fmt.Sprintf("attempt %d of %d", p.Attempt, engine.PromptMaxAttempts)))
	}
}

// giveUpWords is the failure screen's reason when the engine gave up: the
// field it stopped asking for (the abort line's, else the last one asked)
// and, if it had rejected an answer, the rejection's words on a row
// of their own. origin is as
// for view.
func (s promptSession) giveUpWords(origin string) string {
	field := s.aborted
	if field == "" {
		field = s.last
	}
	if field == "" {
		return "Orbit's setup stopped before the configuration was finished."
	}
	label, _, _ := promptWords(field, origin)
	words := "Orbit's setup stopped asking for " + label + "."
	if s.reason != "" {
		// Its own row, so wrapping never splits the reason.
		words += "\nLast answer: " + s.reason
	}
	return words
}
