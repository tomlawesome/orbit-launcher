package ui

// Pins #277 W2 (ratified, answer 9a), the screen side: the box shows a
// word holding a non-printing character in bash's $'...' form exactly as
// the command carries it, with no second escaping pass (no doubled
// backslash), and Copy command copies the box's words joined, byte for
// byte. Remove's command and Repair's chown command.

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

var groupBOddFolders = []string{
	"/srv/odd\x1bname",
	"/srv/new\nline",
	"/srv/rlo\u202eexe",
	"/srv/it's\x1b",
	`/srv/back\slash` + "\x1b",
}

func groupBDecodeClip(t *testing.T, got string) string {
	t.Helper()
	const prefix, suffix = "\x1b]52;c;", "\x07"
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("clipboard write = %q, want an OSC 52 sequence", got)
	}
	dec, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix))
	if err != nil {
		t.Fatalf("payload is not base64: %v", err)
	}
	return string(dec)
}

func groupBAllPrintable(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\n' && !unicode.IsPrint(r) || r == 0x202e {
			return false
		}
	}
	return true
}

func TestGroupB_W2_RemoveBoxAndCopyAreTheSameBytes(t *testing.T) {
	for _, dir := range groupBOddFolders {
		for _, w := range []int{80, 120} {
			var buf bytes.Buffer
			m := narrowDoneModel(t, dir, w, 40, &buf)
			s := screen(m)

			boxed := joinedCommand(s)
			if want := removalLine(t, dir); boxed != want {
				t.Errorf("dir %q at %d: the box reads\n  %q\nwant the command\n  %q\n%s", dir, w, boxed, want, s)
			}
			if !strings.Contains(boxed, "$'") {
				t.Errorf("dir %q at %d: the box has no $'...' word: %q", dir, w, boxed)
			}
			if strings.Contains(boxed, `\\x1b`) || strings.Contains(boxed, `\\n`) || strings.Contains(boxed, `\\u202e`) {
				t.Errorf("dir %q at %d: a backslash is doubled in the box (a second escaping pass): %q", dir, w, boxed)
			}
			if !groupBAllPrintable(boxed) {
				t.Errorf("dir %q at %d: the box holds a non-printing character: %q", dir, w, boxed)
			}

			_, cmd := updateAs(t, m, key(tea.KeyEnter)) // Copy command is the default row
			if cmd == nil {
				t.Fatalf("dir %q at %d: Copy command wrote nothing", dir, w)
			}
			cmd()
			if copied := groupBDecodeClip(t, buf.String()); copied != boxed {
				t.Errorf("dir %q at %d: copied\n  %q\nbut the box shows\n  %q", dir, w, copied, boxed)
			}
		}
	}
}

func TestGroupB_W2_RepairChownScreenAndCopyAreTheSameBytes(t *testing.T) {
	for _, dir := range groupBOddFolders {
		own := deploy.Ownership{Account: "alice", Files: []deploy.ForeignFile{{Path: ".env-orbit", Owner: "root"}}}
		m, buf := rfStop(t, dir, own)
		shown := rfCommandOn(t, screen(m))
		want := strings.Join(deploy.ChownCommandWords(dir, own), " ")
		if shown != want {
			t.Errorf("dir %q: the screen shows\n  %q\nwant the command\n  %q", dir, shown, want)
		}
		if !strings.Contains(shown, "$'") {
			t.Errorf("dir %q: the shown command has no $'...' word: %q", dir, shown)
		}
		if strings.Contains(shown, `\\x1b`) || strings.Contains(shown, `\\n`) || strings.Contains(shown, `\\u202e`) {
			t.Errorf("dir %q: a backslash is doubled on the screen: %q", dir, shown)
		}
		if !groupBAllPrintable(shown) {
			t.Errorf("dir %q: the shown command holds a non-printing character: %q", dir, shown)
		}
		_, cmd := updateAs(t, m, key(tea.KeyEnter))
		if cmd == nil {
			t.Fatalf("dir %q: Copy command wrote nothing", dir)
		}
		cmd()
		if copied := groupBDecodeClip(t, buf.String()); copied != shown {
			t.Errorf("dir %q: copied\n  %q\nbut the screen shows\n  %q", dir, copied, shown)
		}
	}
}
