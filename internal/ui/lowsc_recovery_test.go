package ui

// v0.4.3-c audit Lows (issue #277), recovery command items, written from
// the specification alone:
//
//   - A. The launcher takes a recovery command only from a stdout line
//     with the exact "recovery-command: " prefix that came immediately
//     before the terminal failed event carrying recovery=command.
//   - F. Copy command hands over exactly the command as the box shows it
//     (escaped), never a raw bidi or invisible character.
//
// (C2, a cut line is never shown or copied, needs a new field and is in
// lowsc_newapi_test.go.)

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// lowsCInstallRun is an Install run fed msgs as the engine stream would
// deliver them, drawn at w x h on the screen it stops on.
func lowsCInstallRun(t *testing.T, w, h int, msgs ...any) (engineRun, string) {
	t.Helper()
	run := newEngineRun(deploy.ActionInstall, rollbackFolder, "Install — Standard", "v1").
		withSeams(engineRunSeams{now: func() time.Time { return time.Time{} }})
	run, _ = run.start(w, h)
	run.state = runStreaming
	for _, msg := range msgs {
		run, _ = run.update(engineStreamMsg{msg: msg})
	}
	if run.state != runFailed {
		t.Fatalf("the run did not stop (state %v)", run.state)
	}
	return run, ansi.Strip(run.view(w, h))
}

// lowsCHasBox reports whether the screen draws a framed box.
func lowsCHasBox(s string) bool {
	for _, line := range strings.Split(s, "\n") {
		if gbStBoxLine(line) {
			return true
		}
	}
	return false
}

// lowsCRunCmds runs cmd, expanding batches, and returns what ran.
func lowsCRunCmds(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			lowsCRunCmds(c)
		}
	}
}

// lowsCPressEnterOnCopy puts the caret on the Copy command row at w x h,
// presses Enter, and returns what the launcher wrote to the terminal for
// the clipboard (os.Stdout is swapped for the duration).
func lowsCPressEnterOnCopy(t *testing.T, r engineRun, w, h int) string {
	t.Helper()
	onCopy := func() bool {
		return caretRowOf(ansi.Strip(r.view(w, h))) == "Copy command"
	}
	for i := 0; i < 8 && !onCopy(); i++ {
		r, _ = r.update(key(tea.KeyDown))
	}
	if !onCopy() {
		t.Fatalf("the caret never reached Copy command:\n%s", ansi.Strip(r.view(w, h)))
	}
	old := os.Stdout
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = pw
	var cmd tea.Cmd
	func() {
		defer func() { os.Stdout = old }()
		_, cmd = r.update(key(tea.KeyEnter))
		lowsCRunCmds(cmd)
	}()
	pw.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, pr); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// A, control: the line directly before the event still gives the box
// and the Copy row.
func TestLowsC_A_PrefixedLineJustBeforeTheEventStillGivesTheBoxAndCopy(t *testing.T) {
	_, s := lowsCInstallRun(t, 120, 40,
		hostDone, rollbackRunning,
		engine.RawLineMsg{Text: "recovery-command: " + engineCommand},
		parsedEvent(t, retryEvent), exit1())
	if !lowsCHasBox(s) {
		t.Errorf("no recovery-command box:\n%s", s)
	}
	if !hasRow(s, "Copy command") {
		t.Errorf("no Copy command row:\n%s", s)
	}
}

// A.1 (#277): a raw stdout line without the prefix just before the
// failed event is no command: no box, no Copy.
func TestLowsC_A1_AnUnprefixedLineBeforeTheEventIsNotACommand(t *testing.T) {
	const raw = "cd /srv/elsewhere && rm -rf /srv/elsewhere/data"
	_, s := lowsCInstallRun(t, 120, 40,
		hostDone, rollbackRunning,
		engine.RawLineMsg{Text: raw},
		parsedEvent(t, retryEvent), exit1())
	if lowsCHasBox(s) {
		t.Errorf("a box is drawn for a line that was not a recovery-command line:\n%s", s)
	}
	if hasRow(s, "Copy command") {
		t.Errorf("Copy command is offered for a line that was not a recovery-command line:\n%s", s)
	}
	for _, line := range strings.Split(s, "\n") {
		if gbStBoxLine(line) && strings.Contains(line, "elsewhere") {
			t.Errorf("the raw line is shown as a command: %q", line)
		}
	}
}

// A.2 (#277): a recovery-command line, then another raw stdout line,
// then the event: the command was not the line immediately before, so
// no box and no Copy.
func TestLowsC_A2_ARecoveryLineFollowedByAnotherLineIsNotACommand(t *testing.T) {
	_, s := lowsCInstallRun(t, 120, 40,
		hostDone, rollbackRunning,
		engine.RawLineMsg{Text: "recovery-command: " + engineCommand},
		engine.RawLineMsg{Text: "some other prose from the installer"},
		parsedEvent(t, retryEvent), exit1())
	if lowsCHasBox(s) {
		t.Errorf("a box is drawn though another line came between the command and the event:\n%s", s)
	}
	if hasRow(s, "Copy command") {
		t.Errorf("Copy command is offered though another line came between the command and the event:\n%s", s)
	}
}

// F (#277): the folder name holds U+202E. The box shows it escaped, and
// Copy hands over exactly those words: no raw bidi or invisible
// character, and the same text as the box.
func TestLowsC_F_CopyIsTheCommandAsTheBoxShowsItEscaped(t *testing.T) {
	const w, h = 120, 40
	command := "cd " + rollbackFolder + "/rlo\u202eexe/orbit-install-backups/" + rollbackStamp +
		"/engine && docker compose --project-name orbit --env-file .env-orbit down --remove-orphans"
	r, s := lowsCInstallRun(t, w, h,
		hostDone, rollbackRunning,
		engine.RawLineMsg{Text: "recovery-command: " + command},
		parsedEvent(t, retryEvent), exit1())

	boxed := joinedCommand(s)
	if boxed == "" {
		t.Fatalf("no recovery-command box:\n%s", s)
	}
	for _, r := range boxed {
		if r == 0x202e || (!unicode.IsPrint(r) && r != ' ') {
			t.Fatalf("the box itself shows a raw non-printing character %U: %q", r, boxed)
		}
	}

	copied := groupBDecodeClip(t, lowsCPressEnterOnCopy(t, r, w, h))
	for _, c := range copied {
		if c == 0x202e || (!unicode.IsPrint(c) && c != ' ') {
			t.Errorf("the copied text holds a raw non-printing character %U: %q", c, copied)
		}
	}
	if copied != boxed {
		t.Errorf("copied\n  %q\nbut the box shows\n  %q", copied, boxed)
	}
}
