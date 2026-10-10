package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// rejoin reads wrapped lines back the way a shell does: a trailing
// backslash joins the next line, and leading indentation is whitespace.
func rejoin(lines []string) string {
	var b strings.Builder
	for i, l := range lines {
		if i > 0 {
			b.WriteString(" ")
		}
		b.WriteString(strings.TrimSpace(strings.TrimSuffix(l, " \\")))
	}
	return b.String()
}

func TestWrapShellCommand_FitsOnOneLineUnchanged(t *testing.T) {
	cmd := removalLine(t, "/opt/orbit")
	got := wrapShellCommand(cmd, len(cmd))
	if len(got) != 1 || got[0] != cmd {
		t.Fatalf("wrapShellCommand(fits) = %q, want the command untouched", got)
	}
}

// Whatever the width, the lines must read back as exactly the command,
// no line may be wider than the limit (bar a single word that is), and
// "&&" must lead its own line so the destructive half is a line apart.
func TestWrapShellCommand_ReadsBackAsTheSameCommand(t *testing.T) {
	for _, dir := range []string{"/opt/orbit", "/srv/containers/mail/orbit-production", "/home/tom/my orbit", "/opt/it's"} {
		cmd := removalLine(t, dir)
		for _, limit := range []int{40, 56, 76, 100} {
			lines := wrapShellCommand(cmd, limit)
			if got := rejoin(lines); got != cmd {
				t.Errorf("limit %d, dir %q: lines %q read back as\n  %q\nwant\n  %q", limit, dir, lines, got, cmd)
			}
			for i, l := range lines {
				// An option and its value are one unit by design; a line
				// may only be over the limit when it holds a single unit.
				content := strings.TrimSpace(strings.TrimSuffix(l, " \\"))
				if lipgloss.Width(l) > limit && len(shellUnits(content)) > 1 {
					t.Errorf("limit %d, dir %q: line %d is %d cells with a break available: %q", limit, dir, i, lipgloss.Width(l), l)
				}
				if i < len(lines)-1 && !strings.HasSuffix(l, " \\") {
					t.Errorf("limit %d, dir %q: line %d does not continue with a backslash: %q", limit, dir, i, l)
				}
				if strings.Contains(l, "&&") && !strings.HasPrefix(l, "  && ") {
					t.Errorf("limit %d, dir %q: && does not lead its own line: %q", limit, dir, l)
				}
			}
		}
	}
}

// A quoted path with a space in it is one shell word and must never be
// broken, however narrow the screen.
func TestWrapShellCommand_NeverBreaksInsideQuotes(t *testing.T) {
	cmd := removalLine(t, "/home/tom/my orbit")
	for _, l := range wrapShellCommand(cmd, 30) {
		if strings.Count(l, "'")%2 != 0 {
			t.Errorf("line breaks a quoted word: %q", l)
		}
	}
}

// An option stays with its value: no line ends on "--env-file \".
func TestWrapShellCommand_KeepsAnOptionWithItsValue(t *testing.T) {
	cmd := removalLine(t, "/opt/orbit")
	for _, l := range wrapShellCommand(cmd, 60) {
		trimmed := strings.TrimSuffix(l, " \\")
		if strings.HasSuffix(trimmed, "--env-file") || strings.HasSuffix(trimmed, "--project-directory") {
			t.Errorf("line ends on an option without its value: %q", l)
		}
	}
}

func TestWrapWords(t *testing.T) {
	got := wrapWords("volumes are still on disk at /srv/containers/mail/orbit-production —", 56)
	want := []string{"volumes are still on disk at", "/srv/containers/mail/orbit-production —"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("wrapWords = %q, want %q", got, want)
	}
	if got := wrapWords("short", 56); len(got) != 1 || got[0] != "short" {
		t.Errorf("wrapWords(short) = %q", got)
	}
	if got := wrapWords("", 56); len(got) != 0 {
		t.Errorf("wrapWords(empty) = %q, want nothing", got)
	}
}
