package ui

// #1576 step 5, written from the spec alone (scratchpad 1576/spec-step5.md).
// On the gone-folder screen of Update and Repair the row "Carry on from
// this folder" becomes "Run from this folder", and "Move it back" is new:
// it leaves the TUI, the launcher prints one stderr line naming the old
// path (made safe by visibleText) and exits 0, writing and moving nothing.
// In Update, "Run from this folder" gives the engine run
// ORBIT_INSTALL_FOLDER_MOVED=recreate in its environment, never argv, kept
// for a retry; no other Update run gets it. Screens: gonefolder_test.go.
//
// Assumed new name (the only one): AppModel.LeaveNote() string, the line
// main prints on stderr after the alternate screen closes, with exit 0.
// The variable's name is written out here as a literal on purpose, so the
// tests read it from the process the launcher starts, as docs260 does.

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const folderMovedEnv = "ORBIT_INSTALL_FOLDER_MOVED"

// folderMovedRecorder points the launcher's install.sh override at a
// script that records, per run, the variable (or <unset>) and its
// arguments, then fails in a way Update can retry.
func folderMovedRecorder(t *testing.T) (out string) {
	t.Helper()
	out = t.TempDir()
	t.Setenv("ORBIT_T1576_OUT", out)
	t.Setenv(folderMovedEnv, "")
	if err := os.Unsetenv(folderMovedEnv); err != nil {
		t.Fatal(err)
	}
	installScriptAt(t, "printf '%s\\n' \"${"+folderMovedEnv+"-<unset>}\" >> \"$ORBIT_T1576_OUT/var\"\n"+
		"printf '%s\\n' \"$*\" >> \"$ORBIT_T1576_OUT/argv\"\n"+
		"echo 'phase=host component=host state=failed reason=docker-host action=retry elapsed=0s'\n"+
		"exit 1\n")
	return out
}

// leaveNote reads AppModel.LeaveNote through an interface, so the
// tests build before the method exists and fail with a clear message.
func leaveNote(t *testing.T, m AppModel) string {
	t.Helper()
	n, ok := any(m).(interface{ LeaveNote() string })
	if !ok {
		t.Errorf("AppModel has no LeaveNote() string (the assumed name for the exit-0 stderr line)")
		return ""
	}
	return n.LeaveNote()
}

func recordedLines(t *testing.T, out, name string) []string { return docs260Lines(t, out, name) }

// goneAppAt is goneApp for a container started from folder.
func goneAppAt(t *testing.T, folder string) (AppModel, string, *sink) {
	t.Helper()
	if _, err := os.Lstat(folder); err == nil {
		t.Fatalf("setup: %q exists", folder)
	}
	dir := installedFolder(t)
	s := newSink()
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		runningAs(deploy.RunningApp{Found: true, Image: recordedImage, Folder: folder, State: "running"}, nil)))
	m.flowSend = s.Send
	if !strings.Contains(screen(m), splashGoneRow) {
		t.Fatalf("setup: the splash does not show the gone folder:\n%s", screen(m))
	}
	return m, dir, s
}

func untilScreenHas(sub string) func(tea.Model) bool {
	return func(m tea.Model) bool { return strings.Contains(screen(m), sub) }
}

// runUpdateFromTheGoneScreen presses the first row, then Update Orbit,
// and drives the failing engine to its stopped screen.
func runUpdateToStopped(t *testing.T, m AppModel, s *sink) AppModel {
	t.Helper()
	if !strings.Contains(screen(m), "Update Orbit") {
		t.Fatalf("setup: not on Update's confirmation:\n%s", screen(m))
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Update Orbit
	m = driveUntil(t, m, cmd, untilScreenHas(updateAgain), &sink{msgs: s.msgs, first: true})
	if !strings.Contains(screen(m), updateAgain) {
		t.Fatalf("setup: the run did not stop on a retryable failure:\n%s", screen(m))
	}
	return m
}

func TestFolderMoved_RunFromThisFolderInUpdateSendsTheVariableInTheEnvironmentOnly(t *testing.T) {
	out := folderMovedRecorder(t)
	m, _, s := goneAppAt(t, goneFolder)
	m = openFlow(t, m, "Update")
	if got := caretLabel(screen(m)); got != goneRunRow {
		t.Fatalf("setup: caret on %q, want %q:\n%s", got, goneRunRow, screen(m))
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Run from this folder
	m = drive(t, m, cmd)

	m = runUpdateToStopped(t, m, s)

	if got := recordedLines(t, out, "var"); len(got) != 1 || got[0] != "recreate" {
		t.Errorf("the engine saw %s = %q, want exactly one run with %q", folderMovedEnv, got, "recreate")
	}
	for _, a := range recordedLines(t, out, "argv") {
		if strings.Contains(strings.ToLower(a), "recreate") || strings.Contains(a, folderMovedEnv) || strings.Contains(strings.ToLower(a), "folder") {
			t.Errorf("the engine's arguments %q name the variable or its value; it travels in the environment only", a)
		}
	}
}

func TestFolderMoved_TheVariableIsKeptForARetry(t *testing.T) {
	out := folderMovedRecorder(t)
	m, _, s := goneAppAt(t, goneFolder)
	m = openFlow(t, m, "Update")
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Run from this folder
	m = drive(t, m, cmd)
	m = runUpdateToStopped(t, m, s)

	m = moveCaretTo(t, m, updateAgain)
	m, cmd = updateAs(t, m, key(tea.KeyEnter))
	m = driveUntil(t, m, cmd, func(tm tea.Model) bool {
		return len(recordedLines(t, out, "var")) >= 2 && strings.Contains(screen(tm), updateAgain)
	}, &sink{msgs: s.msgs, first: true})

	if got := recordedLines(t, out, "var"); len(got) != 2 || got[0] != "recreate" || got[1] != "recreate" {
		t.Errorf("the engine saw %s = %q over the run and its retry, want recreate both times", folderMovedEnv, got)
	}
}

func TestFolderMoved_AnUpdateNotReachedThroughTheRowGetsNoVariable(t *testing.T) {
	t.Run("a plain Update, the folder not gone", func(t *testing.T) {
		out := folderMovedRecorder(t)
		dir := installedFolder(t)
		s := newSink()
		m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{}, nil)))
		m.flowSend = s.Send
		m = openFlow(t, m, "Update")
		m = runUpdateToStopped(t, m, s)
		if got := recordedLines(t, out, "var"); len(got) != 1 || got[0] != "<unset>" {
			t.Errorf("the engine saw %s = %q, want one run with it unset", folderMovedEnv, got)
		}
	})
	t.Run("a plain Update with the variable already in the launcher's environment", func(t *testing.T) {
		out := folderMovedRecorder(t)
		t.Setenv(folderMovedEnv, "recreate")
		dir := installedFolder(t)
		s := newSink()
		m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{}, nil)))
		m.flowSend = s.Send
		m = openFlow(t, m, "Update")
		m = runUpdateToStopped(t, m, s)
		if got := recordedLines(t, out, "var"); len(got) != 1 || got[0] != "<unset>" {
			t.Errorf("the engine saw %s = %q, want one run with it unset", folderMovedEnv, got)
		}
	})
	t.Run("Cancel, then a plain Update later", func(t *testing.T) {
		out := folderMovedRecorder(t)
		m, _, s := goneAppAt(t, goneFolder)
		m = openFlow(t, m, "Update")
		m = moveCaretTo(t, m, "Cancel")
		m, cmd := updateAs(t, m, key(tea.KeyEnter))
		m = drive(t, m, cmd)
		if m.state != appStateSplash {
			t.Fatalf("Cancel did not go back to the menu:\n%s", screen(m))
		}
		if got := recordedLines(t, out, "var"); len(got) != 0 {
			t.Errorf("Cancel started an engine run: %q", got)
		}

		// The folder is back (a plain container now), so the next Update is a
		// plain one and must not carry the signal from the screen Cancel closed.
		m = started(t, runningAs(deploy.RunningApp{}, nil)(m))
		if strings.Contains(screen(m), splashGoneRow) {
			t.Fatalf("setup: the splash still shows the gone folder:\n%s", screen(m))
		}
		m = openFlow(t, m, "Update")
		m = runUpdateToStopped(t, m, s)
		if got := recordedLines(t, out, "var"); len(got) != 1 || got[0] != "<unset>" {
			t.Errorf("the engine saw %s = %q, want one run with it unset", folderMovedEnv, got)
		}
	})
}

func TestFolderMoved_MoveItBackStartsNoRunAndWritesNothing(t *testing.T) {
	out := folderMovedRecorder(t)
	for _, flow := range []string{"Update", "Repair"} {
		t.Run(flow, func(t *testing.T) {
			m, dir, _ := goneAppAt(t, goneFolder)
			m = openFlow(t, m, flow)
			m = moveCaretTo(t, m, goneMoveRow)
			before := snapshot(t, dir)

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
			if got := snapshot(t, dir); got != before {
				t.Errorf("Move it back changed files:\nbefore:\n%s\nafter:\n%s", before, got)
			}
			if got := recordedLines(t, out, "var"); len(got) != 0 {
				t.Errorf("Move it back started an engine run: %q", got)
			}
			if _, err := os.Lstat(goneFolder); err == nil {
				t.Errorf("Move it back made %s", goneFolder)
			}
		})
	}
}

// The line main prints after the screen closes: one line, the full old
// path, what to do, no command, and not the exit-1 note.
func TestFolderMoved_MoveItBackLeavesOneLineNamingTheOldPathAndNoFailure(t *testing.T) {
	for _, flow := range []string{"Update", "Repair"} {
		t.Run(flow, func(t *testing.T) {
			m, _, _ := goneAppAt(t, goneFolder)
			m = openFlow(t, m, flow)
			m = moveCaretTo(t, m, goneMoveRow)
			m, _ = updateAs(t, m, key(tea.KeyEnter))

			note := leaveNote(t, m)
			line := strings.TrimSpace(note)
			if line == "" || strings.Contains(line, "\n") {
				t.Fatalf("LeaveNote = %q, want exactly one non-empty line", note)
			}
			if !strings.Contains(line, goneFolder) {
				t.Errorf("LeaveNote = %q, want the full old path %q", note, goneFolder)
			}
			low := strings.ToLower(line)
			if !strings.Contains(low, "launcher") || !strings.Contains(low, "back") {
				t.Errorf("LeaveNote = %q, want it to say to put the folder back and run the launcher from it", note)
			}
			for _, cmd := range []string{"mv ", " mv", "docker ", "cd "} {
				if strings.Contains(low, cmd) {
					t.Errorf("LeaveNote = %q holds a command (%q); it must print none", note, cmd)
				}
			}
			if m.ExitNote() != "" {
				t.Errorf("ExitNote = %q: it forces exit 1, and the operator chose this", m.ExitNote())
			}
		})
	}
}

func TestFolderMoved_TheOldPathInTheLineIsMadeSafeAsOnScreen(t *testing.T) {
	// ESC and a bidi override: raw, either would restyle or reorder the
	// terminal line.
	hostile := "/srv/orbit-gone-1576/\x1b[2Jprod\u202eevil"
	m, _, _ := goneAppAt(t, hostile)
	m = openFlow(t, m, "Update")
	m = moveCaretTo(t, m, goneMoveRow)
	m, _ = updateAs(t, m, key(tea.KeyEnter))

	note := leaveNote(t, m)
	if strings.ContainsAny(note, "\x1b\u202e") {
		t.Errorf("LeaveNote = %q holds a raw control or bidi character", note)
	}
	for _, r := range strings.TrimSuffix(note, "\n") {
		if r < ' ' && r != '\t' {
			t.Errorf("LeaveNote = %q holds control character %q", note, r)
		}
	}
	if want := visibleText(hostile); !strings.Contains(note, want) {
		t.Errorf("LeaveNote = %q, want the path as visibleText shows it (%q)", note, want)
	}
	if strings.Count(strings.TrimSuffix(note, "\n"), "\n") != 0 {
		t.Errorf("LeaveNote = %q is more than one line", note)
	}
}

// Nothing else leaves a note: Cancel, Esc and Run from this folder keep
// the exit as it is today.
func TestFolderMoved_OtherRowsLeaveNoNote(t *testing.T) {
	for _, flow := range []string{"Update", "Repair"} {
		for _, row := range []string{goneRunRow, "Cancel"} {
			t.Run(flow+" "+row, func(t *testing.T) {
				m, _, _ := goneAppAt(t, goneFolder)
				m = openFlow(t, m, flow)
				m = moveCaretTo(t, m, row)
				m, cmd := updateAs(t, m, key(tea.KeyEnter))
				m = drive(t, m, cmd)
				if leaveNote(t, m) != "" {
					t.Errorf("LeaveNote = %q after %s", leaveNote(t, m), row)
				}
			})
		}
		t.Run(flow+" Esc", func(t *testing.T) {
			m, _, _ := goneAppAt(t, goneFolder)
			m = openFlow(t, m, flow)
			m, cmd := updateAs(t, m, key(tea.KeyEsc))
			m = drive(t, m, cmd)
			if leaveNote(t, m) != "" || m.state != appStateSplash {
				t.Errorf("Esc: note %q, state %v, want none and the menu", leaveNote(t, m), m.state)
			}
		})
	}
}

// Remove keeps its screen and rows, and has no Move it back.
func TestFolderMoved_RemoveIsUnchanged(t *testing.T) {
	m, _, _ := goneAppAt(t, goneFolder)
	m = openFlow(t, m, "Remove")
	assertCarryOn(t, m, "Remove")
	flat := oneLine(screen(m))
	for _, not := range []string{goneRunRow, goneBodyMade, goneBodyRun, "made again here"} {
		if strings.Contains(flat, not) {
			t.Errorf("Remove's screen says %q", not)
		}
	}
}
