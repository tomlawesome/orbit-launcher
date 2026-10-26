package ui

// Blind tests for #260 (the Documents profile in the install flow),
// written from the specification alone. This file needs no new API: it
// runs the real fetch-stage-run path against a local install.sh that
// records what it was given, so the profile is read from the process
// the launcher starts, not from any internal field.
//
// The memory-dependent tests are in docs260_newapi_test.go.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

const docs260Env = "ORBIT_INSTALL_PROFILE"

// docs260Recorder points the launcher's local install.sh override at a
// script that appends the profile variable and the arguments it was
// started with to files, then stops with a failure (so the stopped
// screen, and its Install again row, appear). It returns the folder the
// run is installed into and the folder holding the records.
func docs260Recorder(t *testing.T) (target, out string) {
	t.Helper()
	out = t.TempDir()
	target = t.TempDir()
	t.Setenv("ORBIT_T260_OUT", out)
	// The launcher's own environment must not decide the answer.
	t.Setenv(docs260Env, "")
	if err := os.Unsetenv(docs260Env); err != nil {
		t.Fatal(err)
	}
	installScriptAt(t, "printf '%s\\n' \"${"+docs260Env+"-<unset>}\" >> \"$ORBIT_T260_OUT/profile\"\n"+
		"printf '%s\\n' \"$*\" >> \"$ORBIT_T260_OUT/argv\"\n"+
		"echo recorded >&2\n"+
		// A terminal failure the installer could retry: without one the
		// stopped screen offers the guided installer, not Install again.
		"echo 'phase=host component=host state=failed reason=docker-host action=retry elapsed=0s'\n"+
		"exit 1\n")
	return target, out
}

func docs260Lines(t *testing.T, out, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(out, name))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

// docs260Model is an install flow at 80 columns in a real folder.
func docs260Model(t *testing.T, target string) (InstallModel, *sink) {
	t.Helper()
	m := NewInstallModel(target, "v9.9.9")
	s := newSink()
	m.send = s.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	return m, s
}

// docs260Down moves the caret n rows down the profile table.
func docs260Down(t *testing.T, m InstallModel, n int) InstallModel {
	t.Helper()
	for i := 0; i < n; i++ {
		m, _ = updateAs(t, m, key(tea.KeyDown))
	}
	return m
}

// docs260Accept presses Enter on the row the caret is on and takes the
// development notice through, the way a person does (as acceptNotice,
// but with a message that names the row).
func docs260Accept(t *testing.T, m InstallModel, row string) (InstallModel, tea.Cmd) {
	t.Helper()
	noticeTime := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	flowClock := m.seams.now
	m.seams.now = func() time.Time { return noticeTime }
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if m.state != installStateNotice {
		t.Fatalf("Enter on %s did not open the development notice (state %v):\n%s", row, m.state, screen(m))
	}
	noticeTime = noticeTime.Add(m.noticeDur())
	m, _ = updateAs(t, m, key(tea.KeyEnd), tea.KeyPressMsg{Text: noticePhrase})
	m.seams.now = flowClock
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if m.state != installStateRunning || cmd == nil {
		t.Fatalf("the accepted notice did not start a run (state %v):\n%s", m.state, screen(m))
	}
	return m, cmd
}

// docs260Run chooses the row n places down and drives the run it starts
// until it stops.
func docs260Run(t *testing.T, m InstallModel, s *sink, n int, row string) InstallModel {
	t.Helper()
	m = docs260Down(t, m, n)
	m, cmd := docs260Accept(t, m, row)
	return drive(t, m, cmd, s)
}

// docs260NoProfileInArgv fails when any argument the installer was
// started with names the profile.
func docs260NoProfileInArgv(t *testing.T, out string) {
	t.Helper()
	argv := docs260Lines(t, out, "argv")
	if len(argv) == 0 {
		t.Fatal("the installer was never started")
	}
	for _, a := range argv {
		for _, bad := range []string{"standard", "processing", docs260Env} {
			if strings.Contains(strings.ToLower(a), strings.ToLower(bad)) {
				t.Errorf("the installer's arguments %q name the profile (%q); it travels in the environment only", a, bad)
			}
		}
	}
}

// Spec 1 (#260): Standard starts a run whose environment holds
// ORBIT_INSTALL_PROFILE=standard, never in argv.
func TestDocs260_StandardRunCarriesTheStandardProfileInItsEnvironment(t *testing.T) {
	target, out := docs260Recorder(t)
	m, s := docs260Model(t, target)
	m = docs260Run(t, m, s, 0, "Standard")
	if got := docs260Lines(t, out, "profile"); len(got) != 1 || got[0] != "standard" {
		t.Errorf("the installer saw %s = %q, want exactly one run with %q", docs260Env, got, "standard")
	}
	docs260NoProfileInArgv(t, out)
}

// Spec 2 (#260): Agent and Full still end in the dead end, with this
// body text and nothing about Documents.
func TestDocs260_AgentAndFullStillLeadToTheNotYetDeadEnd(t *testing.T) {
	const body = "Agent and Full need a local model, which Orbit doesn't use yet."
	for _, c := range []struct {
		row   string
		downs int
	}{{"Agent", 2}, {"Full", 3}} {
		t.Run(c.row, func(t *testing.T) {
			m, _ := docs260Model(t, t.TempDir())
			m = docs260Down(t, m, c.downs)
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			if m.state != installStateUnavailableProfile {
				t.Fatalf("Enter on %s: state %v, want the dead end:\n%s", c.row, m.state, screen(m))
			}
			if cmd != nil {
				if msg := cmd(); msg != nil {
					if _, tick := msg.(tickMsg); !tick {
						t.Errorf("Enter on %s started something: %T", c.row, msg)
					}
				}
			}
			s := screen(m)
			if !strings.Contains(flat(s), body) {
				t.Errorf("the dead end does not say %q:\n%s", body, s)
			}
			if strings.Contains(flat(s), "Documents") {
				t.Errorf("the dead end still names Documents:\n%s", s)
			}
		})
	}
}

// Spec 7 (#260): at 68 columns and over the table is one row per
// profile, below 68 it is two rows, no row is wider than the screen and
// no hint line about keys is added.
func TestDocs260_TableLayoutAtEveryWidth(t *testing.T) {
	names := []string{"Standard", "Documents", "Agent", "Full"}
	for _, w := range []int{120, 80, 68, 67, 50} {
		rows := profileScreenRows(t, w)
		for _, r := range rows {
			if len(r) > w {
				t.Errorf("w=%d: row wider than the screen: %q", w, string(r))
			}
			for _, hint := range []string{"Enter ", "Esc ", "↑", "↓", "navigate", "to select", "to choose"} {
				if strings.Contains(string(r), hint) {
					t.Errorf("w=%d: a keybind hint line (%q): %q", w, hint, string(r))
				}
			}
		}
		prev := -1
		for i, name := range names {
			var ri int
			if w >= 68 {
				ri = rowOfName(t, rows, name)
				nameCol := (w-67)/2 + 2
				if !hasAt(rows[ri], nameCol, name) {
					t.Errorf("w=%d: %s not at column %d: %q", w, name, nameCol, string(rows[ri]))
				}
				if i < len(names)-1 {
					if next := rowOfName(t, rows, names[i+1]); next != ri+1 {
						t.Errorf("w=%d: %s is not on the row after %s", w, names[i+1], name)
					}
				}
			} else {
				ri = -1
				for j, r := range rows {
					if strings.Contains(string(r), name+" · ") {
						ri = j
					}
				}
				if ri < 0 {
					t.Errorf("w=%d: no two-row head for %s", w, name)
					continue
				}
				if strings.TrimSpace(string(rows[ri+1])) == "" {
					t.Errorf("w=%d: %s has no second row", w, name)
				}
			}
			if ri <= prev {
				t.Errorf("w=%d: %s out of order", w, name)
			}
			prev = ri
		}
	}
}
