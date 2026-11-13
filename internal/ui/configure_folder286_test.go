package ui

// #286 review finding A (#316, #334): Configure refuses another folder
// exactly as Update does. Mirrors otherfolder_test.go, otherfolder_late_test.go,
// gonefolder_test.go and foldermoved_test.go for the Configure flow. The
// refusal's flow-specific body line is not asserted (the spec gives none);
// everything else is the shared dead end.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

const cfg286Table = "Choose a deployment profile"

// cfg286Recorded is how many engine runs the recorder saw.
func cfg286Recorded(t *testing.T, out string) []string { return recordedLines(t, out, "var") }

// Known before Configure opens, the folder exists: the dead end naming it.
func TestConfigure286Folder_OpensOnTheRefusalWhenTheFolderIsKnown(t *testing.T) {
	out := folderMovedRecorder(t)
	m, _, _, _ := otherFolderApp(t, otherProduction)

	m = openFlow(t, m, "Configure")

	if m.state != appStateConfigure {
		t.Fatalf("state = %v, want the configure flow", m.state)
	}
	assertRefusal(t, m, otherProduction, "", cfg286Table)
	// Menu is the only row: Enter leaves, and no run ever starts here.
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Errorf("Menu on the refusal left the app in state %v, want the menu", m.state)
	}
	if got := cfg286Recorded(t, out); len(got) != 0 {
		t.Errorf("Configure started an engine run from this folder: %q", got)
	}
}

// A recorded folder that is gone: the carry-on screen, as Update's.
func TestConfigure286Folder_GoneFolderOffersTheRowsUpdateOffers(t *testing.T) {
	m, _, c := goneApp(t, false)

	m = openFlow(t, m, "Configure")

	assertCarryOn(t, m, "Update")
	if strings.Contains(screen(m), cfg286Table) {
		t.Errorf("the profile screen is showing instead of the dead end:\n%s", screen(m))
	}
	if n := c.dates.Load(); n != 0 {
		t.Errorf("the install-date lookup ran %d times before the row", n)
	}
}

// Gone and another build: Menu alone.
func TestConfigure286Folder_GoneFolderAndAnotherBuildIsADeadEnd(t *testing.T) {
	m, _, _ := goneApp(t, true)

	m = openFlow(t, m, "Configure")

	assertGoneDeadEnd(t, m)
}

// The carry-on row lands on the profile screen and sends the signal to
// the run, in the environment only.
func TestConfigure286Folder_RunFromThisFolderSendsFolderMoved(t *testing.T) {
	out := folderMovedRecorder(t)
	m, _, s := goneAppAt(t, goneFolder)
	m = openFlow(t, m, "Configure")
	if got := caretLabel(screen(m)); got != goneRunRow {
		t.Fatalf("setup: caret on %q, want %q:\n%s", got, goneRunRow, screen(m))
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Run from this folder
	m = drive(t, m, cmd)
	cfg286OnTable(t, m)

	m = cfg286TableMoveTo(t, m, "Documents")
	m, cmd = updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	cfg286OnConfirm(t, m)
	cfg286Start(t, m, s, "Documents")

	if got := cfg286Recorded(t, out); len(got) != 1 || got[0] != "recreate" {
		t.Errorf("the engine saw %s = %q, want exactly one run with %q", folderMovedEnv, got, "recreate")
	}
	for _, a := range recordedLines(t, out, "argv") {
		if strings.Contains(strings.ToLower(a), "recreate") || strings.Contains(a, folderMovedEnv) {
			t.Errorf("the engine's arguments %q name the variable or its value", a)
		}
	}
}

// Move it back leaves with the note, starting nothing.
func TestConfigure286Folder_MoveItBackLeavesTheNoteAndStartsNoRun(t *testing.T) {
	out := folderMovedRecorder(t)
	m, _, _ := goneAppAt(t, goneFolder)
	m = openFlow(t, m, "Configure")
	m = moveCaretTo(t, m, goneMoveRow)

	m, cmd := updateAs(t, m, key(tea.KeyEnter))

	quit := false
	for _, msg := range answersOf(cmd) {
		if _, ok := msg.(tea.QuitMsg); ok {
			quit = true
		}
	}
	if !quit {
		t.Errorf("Move it back did not quit the program")
	}
	if note := leaveNote(t, m); !strings.Contains(note, goneFolder) {
		t.Errorf("LeaveNote = %q, want the old path %q", note, goneFolder)
	}
	if got := cfg286Recorded(t, out); len(got) != 0 {
		t.Errorf("Move it back started an engine run: %q", got)
	}
}

// Late result, profile screen showing, folder exists: becomes the refusal.
func TestConfigure286Folder_LateOtherFolderResultOnTheProfileScreenBecomesTheRefusal(t *testing.T) {
	m, dir, _ := silentApp(t)
	m = openFlow(t, m, "Configure")
	cfg286OnTable(t, m)

	m, _ = updateAs(t, m, lateFolderMsg(dir))

	if m.state != appStateConfigure {
		t.Fatalf("state = %v, want it to stay on the configure flow's refusal", m.state)
	}
	assertRefusal(t, m, otherProduction, "", cfg286Table)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Errorf("Menu on the refusal left the app in state %v, want the menu", m.state)
	}
}

// Late result, profile screen showing, folder gone: the carry-on screen.
func TestConfigure286Folder_LateGoneResultOnTheProfileScreenBecomesTheCarryOnScreen(t *testing.T) {
	m, dir, _ := silentApp(t)
	m = openFlow(t, m, "Configure")
	cfg286OnTable(t, m)

	m, _ = updateAs(t, m, goneMsg(dir))

	assertCarryOn(t, m, "Update")
	if strings.Contains(screen(m), cfg286Table) {
		t.Errorf("the profile screen is still showing:\n%s", screen(m))
	}
}

// After carrying on, a folder that is back refuses (as Update's does).
func TestConfigure286Folder_FolderBackAfterCarryOnRefuses(t *testing.T) {
	m, dir, _ := silentApp(t)
	m = openFlow(t, m, "Configure")
	m, _ = updateAs(t, m, goneMsg(dir))
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Run from this folder
	m = drive(t, m, cmd)
	cfg286OnTable(t, m)
	back := shortTempDir(t)

	m, _ = updateAs(t, m, runningResultMsg{dir: dir, mismatch: folderMismatch(back)})

	assertRefusal(t, m, back, "", cfg286Table)
}
