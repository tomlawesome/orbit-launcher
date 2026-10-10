package ui

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"
	"github.com/charmbracelet/x/vt"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// screenOf draws everything a test model has written so far on a terminal
// emulator of the given size and returns the screen as plain text, one
// line per row, styling dropped. The emulator cuts each row at the right
// edge exactly as a terminal does, so this sees what an operator sees —
// a raw-stream match would pass on text a terminal never shows.
func screenOf(out []byte, cols, rows int) string {
	emu := vt.NewEmulator(cols, rows)
	// The program's start-up queries get answers, which the emulator
	// writes into a pipe; left unread, the first answer blocks Write.
	done := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, emu); close(done) }()
	// A pty turns every newline into carriage return + newline (onlcr)
	// before a terminal sees it; the emulator is fed directly, so do the
	// same here, or a bare newline leaves the cursor mid-row.
	_, _ = emu.Write(bytes.ReplaceAll(out, []byte("\n"), []byte("\r\n")))
	rendered := emu.Render()
	if pw, ok := emu.InputPipe().(io.Closer); ok {
		_ = pw.Close()
	}
	<-done
	lines := strings.Split(stripANSI(rendered), "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n")
}

// altScreenModel shows a flow screen the way cmd/orbit-launcher does —
// in the alternate screen, owning the whole window — so the emulator
// draws the frame a real terminal would, not an inline rendering.
type altScreenModel struct{ tea.Model }

func (a altScreenModel) Init() tea.Cmd { return a.Model.Init() }

func (a altScreenModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := a.Model.Update(msg)
	return altScreenModel{m}, cmd
}

func (a altScreenModel) View() tea.View {
	v := a.Model.View()
	v.AltScreen = true
	return v
}

// removeDoneScreen drives Remove through a successful stand-down on a
// cols x rows terminal and returns the stood-down screen as a terminal
// shows it. The whole done view arrives in one frame, so the screen that
// first shows "stood down" is the one an operator reads.
func removeDoneScreen(t *testing.T, cols, rows int, targetDir string) string {
	t.Helper()
	d := &deploy.Deployment{TargetDir: targetDir, AppURL: "https://mail.example.com"}
	m := NewRemoveModel(d)
	m.standDown = func(context.Context, string) error { return nil }
	m.lookupInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }

	tm := teatest.NewTestModel(t, altScreenModel{m}, teatest.WithInitialTermSize(cols, rows))
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return strings.Contains(screenOf(out, cols, rows), "Stand down Orbit")
	}, teatest.WithDuration(2*time.Second))
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	var screen string
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		screen = screenOf(out, cols, rows)
		return strings.Contains(screen, "stood down")
	}, teatest.WithDuration(2*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})  // Exit
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter}) // quit
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
	return screen
}

// The removal command is the one line on this screen an operator must be
// able to read whole: its tail is the part that deletes everything, and
// not every terminal honours the clipboard copy. At 80 columns the whole
// command must be on screen, wrapped at shell token boundaries with a
// continuation backslash, so it can be read, typed or pasted as shown.
func TestRemoveModel_DoneScreenShowsTheWholeRemovalCommandAt80Columns(t *testing.T) {
	assertWholeRemovalCommandOnScreen(t, 80, 24, "/opt/orbit")
}

func TestRemoveModel_DoneScreenShowsTheWholeRemovalCommandAt60Columns(t *testing.T) {
	assertWholeRemovalCommandOnScreen(t, 60, 24, "/opt/orbit")
}

func TestRemoveModel_DoneScreenShowsTheWholeRemovalCommandForALongPath(t *testing.T) {
	assertWholeRemovalCommandOnScreen(t, 80, 24, "/srv/containers/mail/orbit-production")
}

func assertWholeRemovalCommandOnScreen(t *testing.T, cols, rows int, targetDir string) {
	t.Helper()
	screen := removeDoneScreen(t, cols, rows, targetDir)

	// Every shell token of the command must be on screen, and the lines
	// it was wrapped into must join back into exactly that command when
	// the backslash continuations are honoured.
	want := removalLine(t, targetDir)
	got := joinedCommand(screen)
	if got != want {
		t.Fatalf("at %d columns the removal command on screen reads\n  %q\nwant\n  %q\nscreen:\n%s", cols, got, want, screen)
	}
	for _, row := range strings.Split(screen, "\n") {
		if w := len([]rune(row)); w > cols {
			t.Errorf("a row is %d cells on a %d-column screen: %q", w, cols, row)
		}
	}
}

// joinedCommand finds the boxed command on a rendered stood-down screen
// and reassembles it the way a shell would read the typed lines: a
// trailing backslash joins the next line, and the box border and padding
// are not part of the command.
func joinedCommand(screen string) string {
	var parts []string
	for _, row := range strings.Split(screen, "\n") {
		row = strings.TrimSpace(row)
		if !strings.HasPrefix(row, "│") {
			continue
		}
		inner := strings.TrimSpace(strings.Trim(row, "│"))
		if inner == "" {
			continue
		}
		parts = append(parts, inner)
	}
	var b strings.Builder
	for i, part := range parts {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimSuffix(part, " \\"))
	}
	return strings.TrimSuffix(b.String(), " \\")
}
