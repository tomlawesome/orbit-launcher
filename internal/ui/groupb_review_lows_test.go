package ui

// v0.4.3 group B review lows (issue #277).
//
//   - W3: Repair's diagnosis checks the folder's name before it reads
//     anything in the folder, the ownership check of .env-orbit and
//     .orbit-secrets included. A folder with a control character in its
//     name whose config files belong to another account gets the
//     folder-name refusal, never the foreign-owner stop or a chown command.
//   - L3 with W5: on the stopped screen after an Install refused before
//     writing, at 80 and 120 columns the folder listing line takes exactly
//     one row, naming fewer entries before "and N more" when needed, so the
//     menu's row numbers match the "reading the folder" frame. At 50
//     columns two rows are allowed (not asserted).
//
// Written from the specification alone.

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Foreign-owned config files in a folder whose name holds ESC: the
// refusal sentence shows, no chown command shows, and the ownership
// seam (and the engine) is never asked.
func TestGroupB_W3_ControlNamedFolderIsRefusedBeforeTheOwnershipCheck(t *testing.T) {
	dir := filepath.Join(shortTempDir(t), "ab\x1bcd")
	m := NewRepairModel(dir, "v9.9.9")
	p := &rfProbe{own: rfOwn(rfEnv, rfSecretA)}
	p.wire(&m)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	msg := m.Init()()
	m, _ = updateAs(t, m, msg)
	s := screen(m)

	if m.state != repairError {
		t.Errorf("state = %v, want the stopped screen:\n%s", m.state, s)
	}
	gbStSentenceOnScreen(t, s)
	for _, gone := range []string{"chown", "sudo", "can't check these files", "owned by"} {
		if strings.Contains(flat(s), gone) {
			t.Errorf("the screen shows %q:\n%s", gone, s)
		}
	}
	if len(p.calls) != 0 {
		t.Errorf("seams were asked %v, want none: the folder's name is checked before anything in the folder is read", p.calls)
	}
}

// The folder listing is one row at 80 and 120 columns, and the menu
// stays where "reading the folder" had put it.
func TestGroupB_L3_TheFolderListingTakesOneRowAt80And120Columns(t *testing.T) {
	entries := []gbStEntry{
		{"backups-2025", true}, {"notes.txt", false}, {"old-site", true}, {"photos", true}, {"readme.md", false},
	}
	for _, width := range []int{80, 120} {
		t.Run(fmt.Sprintf("%d columns", width), func(t *testing.T) {
			dir := folderWith(t, "notes.txt", "old-site/", "backups-2025/", "photos/", "readme.md")
			seam := gbStAnswers(entries[0], entries[1], entries[2], entries[3], entries[4])
			m := NewInstallModel(dir, "v9.9.9")
			m.seams = engineRunSeams{prepareEngine: gbStRefusedEngine(), detect: fakeDetect("https://mail.example.com"), listFolder: seam.read}
			q := newSink()
			m.send = q.Send
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 26})
			m, cmd := startInstallRun(t, m)
			m, held := gbStDrive(t, m, cmd, q, seam, true)
			if m.run.state != runFailed {
				t.Fatalf("the run did not stop (state %v):\n%s", m.run.state, screen(m))
			}
			if held == nil {
				t.Fatalf("the folder was not read by a command:\n%s", screen(m))
			}
			first := screen(m)
			firstRows := gbStMenuRows(first)

			m, cmd = updateAs(t, m, held)
			m = drive(t, m, cmd)
			after := screen(m)

			at := gbStRowStarting(after, "in the folder:")
			if at < 0 {
				t.Fatalf("no listing row:\n%s", after)
			}
			line := gbStLine(after, at)
			if !strings.HasSuffix(line, gbStListingTail) {
				t.Errorf("the listing row %q does not end %q (it runs onto a second row):\n%s", line, gbStListingTail, after)
			}
			if got := gbStLine(after, at+1); got != stepFixNamedAbove {
				t.Errorf("the row under the listing is %q, want the next step %q (the listing took more than one row):\n%s", got, stepFixNamedAbove, after)
			}
			if got := gbStMenuRows(after); !slices.Equal(got, firstRows) {
				t.Errorf("the menu rows moved: lines %v while reading, %v after the answer:\n%s", firstRows, got, after)
			}
			if names := strings.Count(line, ","); names < 4 && !strings.Contains(line, " more") {
				t.Errorf("entries were left out without an \"and N more\": %q", line)
			}
		})
	}
}
