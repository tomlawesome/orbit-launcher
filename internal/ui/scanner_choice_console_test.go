package ui

// Blind tests for #340, the mission console (spec items 13 to 15):
// a skipped step reads as done by choice, the three optional parts show
// their plain names on every console line, and a skipped event is never
// pending, never a failure.

import (
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

func scanner340Line(t *testing.T, e engine.Event) (raw, plain string) {
	t.Helper()
	c := newConsole("Install — Standard", "v1", func() time.Time { return time.Time{} }).setSize(80, 26)
	raw = c.entryLine(consoleEntry{isEvent: true, event: e}, false)
	return raw, ansi.Strip(raw)
}

func scanner340Parse(t *testing.T, line string) engine.Event {
	t.Helper()
	if !strings.Contains(line, "elapsed=") {
		line += " elapsed=0s" // the engine ends every event with its clock
	}
	e, ok := engine.ParseEvent(line)
	if !ok {
		t.Fatalf("%q is not an event", line)
	}
	return e
}

// Spec 13: skipped by choice.
func TestScanner340_SkippedEntriesReadDoneByChoice(t *testing.T) {
	for _, c := range []struct{ component, plain string }{
		{"clamav", "scanner"}, {"tika", "text extraction"}, {"ollama", "local model"},
	} {
		for _, shape := range []string{
			"phase=preparation component=" + c.component + " state=skipped reason=service-preparation action=skip",
			"phase=optional component=" + c.component + " state=skipped reason=optional-status action=skip",
		} {
			raw, plain := scanner340Line(t, scanner340Parse(t, shape))
			if !strings.HasPrefix(plain, "✓") {
				t.Errorf("%q: the line starts %q, want the done glyph ✓", shape, plain)
			}
			if !strings.Contains(plain, c.plain+" off by choice") {
				t.Errorf("%q: the line %q lacks %q", shape, plain, c.plain+" off by choice")
			}
			if strings.Contains(plain, c.component) {
				t.Errorf("%q: the line %q still shows the token %q", shape, plain, c.component)
			}
			if strings.HasPrefix(plain, "·") || strings.Contains(plain, "✗") {
				t.Errorf("%q: the line %q reads as pending or failed", shape, plain)
			}
			// Faint, not the success colour.
			if !strings.HasPrefix(raw, style.Tagline.Render(style.SymbolSuccess)) {
				t.Errorf("%q: the glyph is not in the faint style: %q", shape, raw)
			}
			if strings.Contains(raw, style.SuccessText.Render(style.SymbolSuccess)) {
				t.Errorf("%q: the glyph is in the success colour: %q", shape, raw)
			}
		}
	}
}

// Spec 13: plain names on every line of the three; the rest verbatim.
func TestScanner340_PlainNamesOnEveryConsoleLine(t *testing.T) {
	for _, c := range []struct{ line, shown, token string }{
		{"phase=optional component=tika state=running reason=optional-status action=start", "text extraction", "tika"},
		{"phase=optional component=clamav state=healthy reason=optional-status action=health", "scanner", "clamav"},
		{"phase=optional component=ollama state=waiting reason=optional-status action=continue", "local model", "ollama"},
	} {
		_, plain := scanner340Line(t, scanner340Parse(t, c.line))
		if !strings.Contains(plain, c.shown) || strings.Contains(plain, c.token) {
			t.Errorf("%q: the line %q should show %q and not %q", c.line, plain, c.shown, c.token)
		}
	}
	for _, c := range []struct{ line, token string }{
		{"phase=application component=orbit-app state=healthy reason=application-health action=health", "orbit-app"},
		{"phase=host component=host state=completed reason=host-tools action=check", "host"},
	} {
		_, plain := scanner340Line(t, scanner340Parse(t, c.line))
		if !strings.Contains(plain, c.token) {
			t.Errorf("%q: the line %q lost the component %q", c.line, plain, c.token)
		}
	}
}

// Spec 13: a skipped state with another action, or none, stays literal.
func TestScanner340_SkippedWithAnotherActionStaysLiteral(t *testing.T) {
	for _, line := range []string{
		"phase=optional component=clamav state=skipped reason=optional-status action=continue",
		"phase=optional component=clamav state=skipped reason=optional-status",
	} {
		_, plain := scanner340Line(t, scanner340Parse(t, line))
		if !strings.HasPrefix(plain, "✓") || !strings.Contains(plain, "skipped") || strings.Contains(plain, "by choice") {
			t.Errorf("%q: the line %q should have ✓ and the word skipped, not by choice", line, plain)
		}
	}
}

// scanner340SkippedRun runs an install through the two skipped scanner
// events and the completion event, with raw prose lines after success,
// and ends it with exit code 0.
func scanner340SkippedRun(t *testing.T, raw ...string) engineRun {
	t.Helper()
	r := startedRun(t, "install", engineRunSeams{})
	r, _ = runUpdate(t, r, engineReadyMsg{stream: stoppableStream(t, "echo ready; sleep 30"), cleanup: func() error { return nil }})
	if r.state != runStreaming {
		t.Fatalf("setup: state = %v, want runStreaming", r.state)
	}
	for _, line := range []string{
		"phase=host component=host state=completed reason=host-tools action=check elapsed=0s",
		"phase=preparation component=clamav state=skipped reason=service-preparation action=skip elapsed=1s",
		"phase=application component=application state=healthy reason=application-health action=health elapsed=2s",
		"phase=optional component=clamav state=skipped reason=optional-status action=skip elapsed=3s",
		"phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=4s",
	} {
		r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.EventMsg{Event: scanner340Parse(t, line)}})
	}
	for _, line := range raw {
		r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.RawLineMsg{Text: line}})
	}
	r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 0}})
	return r
}

// Spec 14: skipped is not pending.
func TestScanner340_SkippedIsNotPending(t *testing.T) {
	for _, line := range []string{
		"phase=preparation component=clamav state=skipped reason=service-preparation action=skip",
		"phase=optional component=clamav state=skipped reason=optional-status action=skip",
	} {
		if optionalStillWaiting(scanner340Parse(t, line)) {
			t.Errorf("%q counts as an optional still waiting", line)
		}
	}
	r := scanner340SkippedRun(t, "Private scanner: off by choice (uploaded and mailed documents are not virus-scanned).")
	if r.waiting["clamav"] {
		t.Errorf("the scanner is marked as still waiting after it was skipped")
	}
	if notes := r.serviceNotes(); len(notes) != 0 {
		t.Errorf("serviceNotes = %q, want none for a skipped scanner", notes)
	}
	if r.state != runSucceeded {
		t.Errorf("state = %v, want runSucceeded:\n%s", r.state, runScreen(r))
	}
}

// Spec 15: skipped is not failure.
func TestScanner340_SkippedIsNotFailure(t *testing.T) {
	for _, line := range []string{
		"phase=preparation component=clamav state=skipped reason=service-preparation action=skip",
		"phase=optional component=clamav state=skipped reason=optional-status action=skip",
	} {
		if scanner340Parse(t, line).IsTerminalFailure() {
			t.Errorf("%q is a terminal failure", line)
		}
	}
	r := scanner340SkippedRun(t)
	if r.state != runSucceeded {
		t.Errorf("state = %v, want runSucceeded:\n%s", r.state, runScreen(r))
	}
	if r.lastFailed != nil || r.runErr != nil {
		t.Errorf("the run recorded a failure: %v %v", r.lastFailed, r.runErr)
	}
}
