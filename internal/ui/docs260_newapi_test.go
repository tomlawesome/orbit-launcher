package ui

// Blind tests for #260 that need the new memory seam,
// deploy.ReadHostMemory, a func() (uint64, bool) the install flow reads
// the machine's memory through. They live apart so the rest of the
// package's tests compile and run before it exists.
//
// Written from the specification alone.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// docs260Memory makes the machine report bytes (or fail to) for the
// rest of the test.
func docs260Memory(t *testing.T, bytes uint64, ok bool) {
	t.Helper()
	old := deploy.ReadHostMemory
	deploy.ReadHostMemory = func() (uint64, bool) { return bytes, ok }
	t.Cleanup(func() { deploy.ReadHostMemory = old })
}

const (
	docs260Short = "Documents needs 7 GB of memory in all and this machine has 4 GB."
	docs260GB    = uint64(1) << 30
)

// docs260Status is the status column of the wide table at width w.
func docs260Status(t *testing.T, w int) map[string]string {
	t.Helper()
	rows := profileScreenRows(t, w)
	nameCol := (w-67)/2 + 2
	got := map[string]string{}
	for _, name := range []string{"Standard", "Documents", "Agent", "Full"} {
		r := rows[rowOfName(t, rows, name)]
		if nameCol+55 > len(r) {
			t.Fatalf("w=%d: row too short for a status column: %q", w, string(r))
		}
		got[name] = strings.TrimSpace(string(r[nameCol+55:]))
	}
	return got
}

// docs260LineCount is how many screen rows hold text.
func docs260LineCount(rows [][]rune, text string) int {
	n := 0
	for _, r := range rows {
		if strings.Contains(string(r), text) {
			n++
		}
	}
	return n
}

// docs260Roomy is what the table must look like when the machine has
// enough memory, or its memory is unknown.
func docs260Roomy(t *testing.T) {
	t.Helper()
	for _, w := range []int{80, 120} {
		want := map[string]string{"Standard": "available", "Documents": "+1 GB", "Agent": "not yet", "Full": "not yet"}
		got := docs260Status(t, w)
		for name, status := range want {
			if got[name] != status {
				t.Errorf("w=%d: %s reads %q, want %q", w, name, got[name], status)
			}
		}
		rows := profileScreenRows(t, w)
		for _, bad := range []string{"needs 7 GB", "this machine has", "not enough"} {
			if n := docs260LineCount(rows, bad); n != 0 {
				t.Errorf("w=%d: %q on the screen with enough memory", w, bad)
			}
		}
	}
}

// Spec 1 (#260): Documents can be chosen and starts a run whose
// environment holds ORBIT_INSTALL_PROFILE=processing, never in argv.
func TestDocs260_DocumentsRunCarriesTheProcessingProfileInItsEnvironment(t *testing.T) {
	docs260Memory(t, 16*docs260GB, true)
	target, out := docs260Recorder(t)
	m, s := docs260Model(t, target)
	docs260Run(t, m, s, 1, "Documents")
	if got := docs260Lines(t, out, "profile"); len(got) != 1 || got[0] != "processing" {
		t.Errorf("the installer saw %s = %q, want exactly one run with %q", docs260Env, got, "processing")
	}
	docs260NoProfileInArgv(t, out)
}

// Spec 3 (#260): with 16 GB, Standard reads available, Documents +1 GB,
// Agent and Full not yet, and nothing explains anything under the table.
func TestDocs260_StatusColumnWithPlentyOfMemory(t *testing.T) {
	docs260Memory(t, 16*docs260GB, true)
	docs260Roomy(t)
}

// Spec 3 (#260): with 4 GB, Documents reads not enough, one line under
// the table gives the reason, Standard stays available.
func TestDocs260_StatusColumnAndReasonWithTooLittleMemory(t *testing.T) {
	docs260Memory(t, 4*docs260GB, true)
	for _, w := range []int{80, 120} {
		want := map[string]string{"Standard": "available", "Documents": "not enough", "Agent": "not yet", "Full": "not yet"}
		got := docs260Status(t, w)
		for name, status := range want {
			if got[name] != status {
				t.Errorf("w=%d: %s reads %q, want %q", w, name, got[name], status)
			}
		}
		rows := profileScreenRows(t, w)
		if n := docs260LineCount(rows, docs260Short); n != 1 {
			t.Fatalf("w=%d: the reason is on %d rows, want exactly one", w, n)
		}
		if n := docs260LineCount(rows, "needs"); n != 1 {
			t.Errorf("w=%d: %d rows say \"needs\", want only the one reason line", w, n)
		}
		full := rowOfName(t, rows, "Full")
		for i, r := range rows {
			if strings.Contains(string(r), docs260Short) && i <= full {
				t.Errorf("w=%d: the reason is not under the table (row %d, Full is row %d)", w, i, full)
			}
		}
	}
}

// Spec 3 (#260): Documents needs 7 GB in all: exactly 7 is enough, a
// byte less is not.
func TestDocs260_SevenGBIsTheDocumentsThreshold(t *testing.T) {
	docs260Memory(t, 7*docs260GB, true)
	if got := docs260Status(t, 80)["Documents"]; got != "+1 GB" {
		t.Errorf("with exactly 7 GB Documents reads %q, want +1 GB", got)
	}
	docs260Memory(t, 7*docs260GB-1, true)
	if got := docs260Status(t, 80)["Documents"]; got != "not enough" {
		t.Errorf("with a byte under 7 GB Documents reads %q, want not enough", got)
	}
}

// Spec 3 (#260): the same holds in the two-row form below 68 columns.
func TestDocs260_NarrowTableShowsTheSameStatusAndReason(t *testing.T) {
	docs260Memory(t, 4*docs260GB, true)
	rows := profileScreenRows(t, 50)
	for _, r := range rows {
		if len(r) > 50 {
			t.Errorf("row wider than 50: %q", string(r))
		}
	}
	if docs260LineCount(rows, "Documents · not enough") != 1 {
		t.Errorf("no \"Documents · not enough\" head:\n%s", joinRows(rows))
	}
	if docs260LineCount(rows, "Standard · available") != 1 {
		t.Errorf("no \"Standard · available\" head:\n%s", joinRows(rows))
	}
	if !strings.Contains(flat(joinRows(rows)), docs260Short) {
		t.Errorf("the reason is missing, wrapped or not:\n%s", joinRows(rows))
	}
	docs260Memory(t, 16*docs260GB, true)
	rows = profileScreenRows(t, 50)
	if docs260LineCount(rows, "Documents · +1 GB") != 1 {
		t.Errorf("no \"Documents · +1 GB\" head:\n%s", joinRows(rows))
	}
	if strings.Contains(flat(joinRows(rows)), "this machine has") {
		t.Errorf("a reason shows with enough memory:\n%s", joinRows(rows))
	}
}

func joinRows(rows [][]rune) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(string(r))
		b.WriteByte('\n')
	}
	return b.String()
}

// Spec 3 (#260): Enter on Documents with 4 GB stays on the table and
// starts nothing.
func TestDocs260_EnterOnDocumentsWithTooLittleMemoryStaysOnTheTable(t *testing.T) {
	docs260Memory(t, 4*docs260GB, true)
	target, out := docs260Recorder(t)
	m, s := docs260Model(t, target)
	m = docs260Down(t, m, 1)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if m.state != installStateProfile {
		t.Errorf("state = %v, want the table (installStateProfile):\n%s", m.state, screen(m))
	}
	// The test asserts nothing starts, so there is no event to wait for:
	// a short quiet window is the check.
	m = driveQuiet(t, m, cmd, settleQuiet, s)
	if m.state != installStateProfile {
		t.Errorf("after settling, state = %v, want the table:\n%s", m.state, screen(m))
	}
	if !strings.Contains(screen(m), "Choose a deployment profile") {
		t.Errorf("the table is not on screen:\n%s", screen(m))
	}
	if got := docs260Lines(t, out, "profile"); len(got) != 0 {
		t.Errorf("an installer ran: %q", got)
	}
}

// Spec 4 (#260): with 4 GB Standard can still be chosen and starts a run.
func TestDocs260_StandardStillRunsWithTooLittleMemory(t *testing.T) {
	docs260Memory(t, 4*docs260GB, true)
	target, out := docs260Recorder(t)
	m, s := docs260Model(t, target)
	docs260Run(t, m, s, 0, "Standard")
	if got := docs260Lines(t, out, "profile"); len(got) != 1 || got[0] != "standard" {
		t.Errorf("the installer saw %s = %q, want one run with %q", docs260Env, got, "standard")
	}
}

// Spec 5 (#260): memory that cannot be read changes nothing: rows as
// with 16 GB, no line under the table. The bytes returned beside a
// false are not to be believed.
func TestDocs260_UnreadableMemoryShowsTheRoomyTable(t *testing.T) {
	docs260Memory(t, 4*docs260GB, false)
	docs260Roomy(t)
}

// Spec 5 (#260): and Documents can then be chosen.
func TestDocs260_UnreadableMemoryLetsDocumentsRun(t *testing.T) {
	docs260Memory(t, 0, false)
	target, out := docs260Recorder(t)
	m, s := docs260Model(t, target)
	docs260Run(t, m, s, 1, "Documents")
	if got := docs260Lines(t, out, "profile"); len(got) != 1 || got[0] != "processing" {
		t.Errorf("the installer saw %s = %q, want one run with %q", docs260Env, got, "processing")
	}
}

// Spec 6 (#260): Install again on the stopped screen sends the same
// profile again, Documents and Standard alike.
func TestDocs260_InstallAgainSendsTheSameProfile(t *testing.T) {
	for _, c := range []struct {
		row, want string
		downs     int
	}{{"Documents", "processing", 1}, {"Standard", "standard", 0}} {
		t.Run(c.row, func(t *testing.T) {
			docs260Memory(t, 16*docs260GB, true)
			target, out := docs260Recorder(t)
			m, s := docs260Model(t, target)
			m = docs260Run(t, m, s, c.downs, c.row)
			if m.run.state != runFailed {
				t.Fatalf("the first run did not stop (state %v):\n%s", m.run.state, screen(m))
			}
			if got := caretLabel(screen(m)); got != "Install again" {
				t.Fatalf("the caret is on %q, want Install again:\n%s", got, screen(m))
			}
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			m = drive(t, m, cmd, s)
			got := docs260Lines(t, out, "profile")
			if len(got) != 2 || got[0] != c.want || got[1] != c.want {
				t.Errorf("the two runs saw %s = %q, want %q both times", docs260Env, got, c.want)
			}
			docs260NoProfileInArgv(t, out)
		})
	}
}

// Spec 7 (#260): with the status text and the reason line in, the
// table still fits at 120, 80, 68, 67 and 50 columns and adds no hint
// line.
func TestDocs260_TableStillFitsWithTheReasonLine(t *testing.T) {
	docs260Memory(t, 4*docs260GB, true)
	for _, w := range []int{120, 80, 68, 67, 50} {
		for _, r := range profileScreenRows(t, w) {
			if len(r) > w {
				t.Errorf("w=%d: row wider than the screen: %q", w, string(r))
			}
			for _, hint := range []string{"Enter ", "Esc ", "↑", "↓", "navigate"} {
				if strings.Contains(string(r), hint) {
					t.Errorf("w=%d: a keybind hint line (%q): %q", w, hint, string(r))
				}
			}
		}
	}
}
