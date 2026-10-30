package ui

// Pins the second round of the fix for #248 (note 40776): on Remove's
// stand-down screen the command box shows the folder escaped, like the
// sentence above it, while Copy command keeps the raw command. The
// specification says:
//
//   - a. with a folder holding an LF, no control byte reaches the screen
//     and no extra row appears inside the box, at 80 and 120 columns;
//   - b. with a CR or an ESC in the folder, every box row starts and ends
//     with the box side and the escapes show as text;
//   - c. the box text, joined, equals visibleText of the one-line command;
//   - d. Copy command writes the command byte for byte as
//     deploy.RemovalCommand returns it;
//   - e. for a plain folder the box is unchanged.

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// removeDoneModel drives Remove to its stand-down screen for a folder at
// a width, with the clipboard going to buf.
func removeDoneModel(t *testing.T, folder string, width int, buf *bytes.Buffer) RemoveModel {
	t.Helper()
	m := NewRemoveModel(&deploy.Deployment{TargetDir: folder, AppURL: "https://mail.example.com"})
	m.standDown = func(context.Context, string) error { return nil }
	m.clipboard = buf
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("confirming ran nothing")
	}
	m, _ = updateAs(t, m, cmd())
	if m.state != removeStateDone {
		t.Fatalf("state = %v, want removeStateDone", m.state)
	}
	return m
}

// boxRowsOf is every screen row from the box's top edge to its bottom
// edge, the edges included, with the sky trimmed off the ends.
func boxRowsOf(t *testing.T, s string) []string {
	t.Helper()
	rows := folderRowsOf(s)
	top := -1
	for i, r := range rows {
		if top < 0 && strings.Contains(r, "╭") {
			top = i
			continue
		}
		if top >= 0 && strings.Contains(r, "╰") {
			return rows[top : i+1]
		}
	}
	t.Fatalf("no command box on the screen:\n%s", s)
	return nil
}

// boxInside is the box's text rows with the sides and the padding taken
// off, one string per row. A row that is not a whole box row fails.
func boxInside(t *testing.T, s string) []string {
	t.Helper()
	rows := boxRowsOf(t, s)
	var out []string
	for _, r := range rows[1 : len(rows)-1] {
		if !strings.HasPrefix(r, "│") || !strings.HasSuffix(r, "│") {
			t.Errorf("box row %q does not start and end with │:\n%s", r, s)
			continue
		}
		out = append(out, strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(r, "│"), "│")))
	}
	return out
}

// joinBox is the box text as one line: continuation backslashes and the
// indent are taken off, so only the command's own text remains.
func joinBox(inside []string) string {
	var parts []string
	for i, r := range inside {
		if i < len(inside)-1 {
			r = strings.TrimSuffix(r, " \\")
		}
		parts = append(parts, strings.TrimSpace(r))
	}
	return strings.Join(parts, " ")
}

// checkBoxScreenNoControl fails on any control byte on the screen other than the
// newline between rows.
func checkBoxScreenNoControl(t *testing.T, s, what string) {
	t.Helper()
	for _, r := range s {
		if r != '\n' && (unicode.IsControl(r) || r == 0x7f) {
			t.Errorf("%s: control rune %q reached the screen:\n%s", what, r, s)
			return
		}
	}
}

func TestRemoveBox_A_LFInTheFolderAddsNoControlByteAndNoRow(t *testing.T) {
	const folder = "/opt/x\nsudo rm -rf /"
	for _, width := range []int{80, 120} {
		s := screen(removeDoneModel(t, folder, width, &bytes.Buffer{}))
		checkBoxScreenNoControl(t, s, "LF folder")
		for _, r := range boxRowsOf(t, s) {
			if !strings.HasPrefix(r, "│") && !strings.HasPrefix(r, "╭") && !strings.HasPrefix(r, "╰") {
				t.Errorf("width %d: row %q inside the box is not a box row:\n%s", width, r, s)
			}
		}
		inside := boxInside(t, s)
		if !strings.Contains(noSpaces(joinBox(inside)), noSpaces(`/opt/x\nsudo`)) {
			t.Errorf("width %d: the box should show the LF as \\n, got %q", width, inside)
		}
	}
}

func TestRemoveBox_B_CRAndESCShowAsTextInsideWholeBoxRows(t *testing.T) {
	cases := []struct{ folder, shows string }{
		{"/opt/a\rb c", `/opt/a\rbc`},
		{"/opt/\x1b[2Jx", `/opt/\x1b[2Jx`},
	}
	for _, c := range cases {
		for _, width := range []int{80, 120} {
			s := screen(removeDoneModel(t, c.folder, width, &bytes.Buffer{}))
			checkBoxScreenNoControl(t, s, c.folder)
			inside := boxInside(t, s) // fails any row not bounded by │
			if got := noSpaces(joinBox(inside)); !strings.Contains(got, noSpaces(c.shows)) {
				t.Errorf("folder %q at %d: box text %q should contain %q", c.folder, width, got, c.shows)
			}
		}
	}
}

// The box shows the command itself, byte for byte (#277 W2): an odd
// folder's word is already in printable $'...' form, so there is no
// second escaping pass.
func TestRemoveBox_C_BoxTextJoinedIsTheCommand(t *testing.T) {
	for _, folder := range []string{"/opt/x\nsudo rm -rf /", "/opt/a\rb c", "/opt/\x1b[2Jx", "/opt/orbit"} {
		want := removalLine(t, folder)

		// Wide enough that the command is one line: exact.
		s := screen(removeDoneModel(t, folder, 300, &bytes.Buffer{}))
		inside := boxInside(t, s)
		if len(inside) != 1 || inside[0] != want {
			t.Errorf("folder %q at 300: box = %q, want one row %q", folder, inside, want)
		}

		// Wrapped: the same text, whatever the breaks.
		for _, width := range []int{80, 120} {
			s := screen(removeDoneModel(t, folder, width, &bytes.Buffer{}))
			if got := joinBox(boxInside(t, s)); noSpaces(got) != noSpaces(want) {
				t.Errorf("folder %q at %d: box text %q, want %q", folder, width, got, want)
			}
		}
	}
}

func TestRemoveBox_D_CopyCommandWritesTheRawCommand(t *testing.T) {
	for _, folder := range []string{"/opt/x\nsudo rm -rf /", "/opt/a\rb c", "/opt/\x1b[2Jx", "/opt/orbit"} {
		var buf bytes.Buffer
		m := removeDoneModel(t, folder, 80, &buf)
		_, copyCmd := updateAs(t, m, key(tea.KeyEnter)) // Copy command is selected by default
		if copyCmd == nil {
			t.Fatalf("folder %q: Copy command ran nothing", folder)
		}
		copyCmd()

		const prefix, suffix = "\x1b]52;c;", "\x07"
		got := buf.String()
		if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
			t.Fatalf("folder %q: clipboard write = %q, want an OSC 52 sequence", folder, got)
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix))
		if err != nil {
			t.Fatalf("folder %q: payload is not standard base64: %v", folder, err)
		}
		want, err := deploy.RemovalCommand(folder)
		if err != nil {
			t.Fatalf("RemovalCommand(%q): %v", folder, err)
		}
		if string(decoded) != want {
			t.Errorf("folder %q: copied %q, want the raw command %q", folder, decoded, want)
		}
	}
}

func TestRemoveBox_E_PlainFolderBoxIsUnchanged(t *testing.T) {
	const folder = "/opt/orbit"
	words := removalWords(t, folder)
	for _, width := range []int{80, 120} {
		want := wrapShellCommand(words, width-8)
		got := boxInside(t, screen(removeDoneModel(t, folder, width, &bytes.Buffer{})))
		if len(got) != len(want) {
			t.Fatalf("width %d: box has %d rows %q, want %d rows %q", width, len(got), got, len(want), want)
		}
		for i := range want {
			if got[i] != strings.TrimSpace(want[i]) {
				t.Errorf("width %d row %d: %q, want %q", width, i, got[i], strings.TrimSpace(want[i]))
			}
		}
	}
}
