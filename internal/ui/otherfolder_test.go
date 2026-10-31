package ui

// #316 (owner answer 6b): when the deployment's app container was started
// from another folder, Update, Repair and Remove open as a refusal that
// names that folder, in the grammar of the no-deployment dead end: the
// amber mark, a title, muted body lines, one Menu row. Written from the
// design note (issue note 46624, section 3) before the code.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// The refusal's wording, per flow, and the lines every refusal shares.
const (
	refuseUpdate = "Update here would act on that folder's Orbit, and Orbit's installer refuses it."
	refuseRepair = "Repair here would read and change that folder's Orbit using this folder's files."
	refuseRemove = "Remove here would stop that folder's Orbit."
	refuseShared = "Start the launcher from that folder instead."
	refuseFaint  = "nothing has been changed, and nothing will be by this screen"
)

// otherFolderFlows are the three flows the refusal stands in for.
var otherFolderFlows = []struct {
	label string // the menu row
	body  string // the refusal's first body line
	wrong string // what a flow that was not refused shows
}{
	{"Update", refuseUpdate, "Pull the latest Orbit"},
	{"Repair", refuseRepair, "reading the deployment"},
	{"Remove", refuseRemove, "Stand down Orbit"},
}

// otherFolderApp is the app after the running check has answered that
// the container was started from other. prepared counts the diagnoses
// Repair would have started.
func otherFolderApp(t *testing.T, other string) (m AppModel, dir string, prepared, asked *atomic.Int32) {
	t.Helper()
	dir = installedFolder(t)
	prepared, asked = new(atomic.Int32), new(atomic.Int32)
	counting := func(m AppModel) AppModel {
		m.flowSeams.prepareRepair = func(context.Context, string, deploy.RepairMode) (*engine.Stream, error) {
			prepared.Add(1)
			return sleepingStream(t), nil
		}
		return m
	}
	m = started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		reading(func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
			asked.Add(1)
			return deploy.RunningApp{Found: true, Image: recordedImage, Folder: other, State: "running"}, nil
		}), counting))
	if s := screen(m); !strings.Contains(s, otherFolderRow) || caretLabel(s) != "Exit" {
		t.Fatalf("setup: the splash does not show the other-folder state with the caret on Exit:\n%s", s)
	}
	return m, dir, prepared, asked
}

// openFlow chooses the menu row label and lets the flow start.
func openFlow(t *testing.T, m AppModel, label string) AppModel {
	t.Helper()
	m = moveCaretTo(t, m, label)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	return drive(t, m, cmd)
}

// assertRefusal checks the whole refusal screen: the amber mark, a title
// naming the folder, the flow's body line, the shared line, the faint
// line and the one Menu row, and none of the flow's own screen.
func assertRefusal(t *testing.T, m tea.Model, other, body, wrong string) {
	t.Helper()
	s := screen(m)
	flat := oneLine(s)
	for _, want := range []string{"This Orbit was started from " + other, body, refuseShared, refuseFaint} {
		if !strings.Contains(flat, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(s, wrong) {
		t.Errorf("the flow's own screen (%q) is showing:\n%s", wrong, s)
	}
	if got := screenLabels(s); !sameLabels(got, []string{"Menu"}) || caretLabel(s) != "Menu" || strings.Count(s, "▸") != 1 {
		t.Errorf("the refusal's rows are %q with the caret on %q, want Menu alone:\n%s", got, caretLabel(s), s)
	}
	raw := m.View().Content
	if amber := style.DegradedText.Render(style.SymbolMark); !strings.Contains(raw, amber) {
		t.Errorf("the mark is not amber (style.DegradedText)")
	}
	// The shared line is its own muted row; the faint line begins in the
	// faint style (it may wrap, so only its start is looked for).
	if row := rawRowOf(t, m, refuseShared); !strings.Contains(row, style.MutedText.Render(refuseShared)) {
		t.Errorf("the shared line is not muted (style.MutedText): %q", row)
	}
	tag, muted := openingOf(style.Tagline), openingOf(style.MutedText)
	if tag == muted {
		t.Fatalf("setup: Tagline and MutedText open alike (%q)", tag)
	}
	if row := rawRowOf(t, m, "nothing has been"); !strings.Contains(row, tag+"nothing has been") {
		t.Errorf("the faint line is not in the faint style (style.Tagline): %q", row)
	}
}

// openingOf is the escape sequence a style writes before its text.
func openingOf(sty interface{ Render(...string) string }) string {
	before, _, _ := strings.Cut(sty.Render("X"), "X")
	return before
}

// Update opened with the answer in hand is the refusal as its first
// screen, not the confirmation.
func TestOtherFolder_UpdateOpensOnTheRefusal(t *testing.T) {
	m, _, _, _ := otherFolderApp(t, otherProduction)

	m = openFlow(t, m, "Update")

	if m.state != appStateUpdate {
		t.Fatalf("state = %v, want the update flow", m.state)
	}
	assertRefusal(t, m, otherProduction, refuseUpdate, "Pull the latest Orbit")
}

// Repair starts no diagnosis: it would read the other folder's stack as
// if it were this one's.
func TestOtherFolder_RepairOpensOnTheRefusalAndStartsNoDiagnosis(t *testing.T) {
	m, _, prepared, _ := otherFolderApp(t, otherProduction)

	m = openFlow(t, m, "Repair")

	if m.state != appStateRepair {
		t.Fatalf("state = %v, want the repair flow", m.state)
	}
	assertRefusal(t, m, otherProduction, refuseRepair, "reading the deployment")
	if n := prepared.Load(); n != 0 {
		t.Errorf("the diagnosis was started %d times, want none", n)
	}
}

func TestOtherFolder_RemoveOpensOnTheRefusal(t *testing.T) {
	m, _, _, _ := otherFolderApp(t, otherProduction)

	m = openFlow(t, m, "Remove")

	if m.state != appStateRemove {
		t.Fatalf("state = %v, want the remove flow", m.state)
	}
	assertRefusal(t, m, otherProduction, refuseRemove, "Stand down Orbit")
}

// The refusal names the folder as the check read it, escaped like every
// untrusted path.
func TestOtherFolder_FolderControlRunesShowAsEscapes(t *testing.T) {
	m, _, _, _ := otherFolderApp(t, "/srv/a\x1b[999mZZ")

	m = openFlow(t, m, "Update")

	if raw := m.View().Content; strings.Contains(raw, "\x1b[999m") {
		t.Errorf("the other folder's escape sequence reached the terminal: %q", raw)
	}
	if flat := oneLine(screen(m)); !strings.Contains(flat, `This Orbit was started from /srv/a\x1b[999mZZ`) {
		t.Errorf("the title does not show the escape as \\x1b:\n%s", screen(m))
	}
}

// Esc and the Menu row both go back to the menu, where the check runs
// again.
func TestOtherFolder_EscAndMenuReturnToTheMenu(t *testing.T) {
	for _, flow := range otherFolderFlows {
		for _, leave := range []struct {
			name string
			k    tea.KeyPressMsg
		}{{"esc", key(tea.KeyEsc)}, {"the Menu row", key(tea.KeyEnter)}} {
			t.Run(flow.label+" "+leave.name, func(t *testing.T) {
				m, _, _, asked := otherFolderApp(t, otherProduction)
				m = openFlow(t, m, flow.label)
				before := asked.Load()

				m, cmd := updateAs(t, m, leave.k)
				m = drive(t, m, cmd)

				if m.state != appStateSplash {
					t.Fatalf("state = %v, want the menu:\n%s", m.state, screen(m))
				}
				s := screen(m)
				if strings.Contains(s, "This Orbit was started from") || !strings.Contains(s, "O R B I T") {
					t.Errorf("the menu is not showing:\n%s", s)
				}
				if got := screenLabels(s); !sameLabels(got, menuLabels) {
					t.Errorf("the menu rows are %q, want %q", got, menuLabels)
				}
				if n := asked.Load(); n <= before {
					t.Errorf("the check was not run again on the way back to the menu (%d calls, %d before)", n, before)
				}
				if !strings.Contains(s, otherFolderRow) || caretLabel(s) != "Exit" {
					t.Errorf("the menu does not show the other-folder state again:\n%s", s)
				}
			})
		}
	}
}

// No other key does anything on the refusal: not a letter, not a digit,
// not an arrow.
func TestOtherFolder_OtherKeysDoNothing(t *testing.T) {
	others := map[string]tea.KeyPressMsg{
		"q": runeKey('q'), "x": runeKey('x'), "y": runeKey('y'), "1": runeKey('1'), "space": runeKey(' '),
		"tab": key(tea.KeyTab), "left": key(tea.KeyLeft), "right": key(tea.KeyRight),
		"up": key(tea.KeyUp), "down": key(tea.KeyDown), "backspace": key(tea.KeyBackspace),
	}
	for _, flow := range otherFolderFlows {
		t.Run(flow.label, func(t *testing.T) {
			m, _, prepared, _ := otherFolderApp(t, otherProduction)
			m = openFlow(t, m, flow.label)
			want, state := screen(m), m.state

			for name, k := range others {
				var cmd tea.Cmd
				m, cmd = updateAs(t, m, k)
				if isQuit(cmd) {
					t.Fatalf("%s quit the launcher", name)
				}
				if m.state != state || screen(m) != want {
					t.Fatalf("%s changed the refusal (state %v):\n%s", name, m.state, screen(m))
				}
			}
			if n := prepared.Load(); n != 0 {
				t.Errorf("a key started %d diagnoses", n)
			}
		})
	}
}

// A folder nobody chose the length of wraps to at most four rows, the
// last ending in an ellipsis, and the screen's rest still reads.
func TestOtherFolder_LongFolderWrapsToFourRows(t *testing.T) {
	long := "/srv" + strings.Repeat("/orbit-deployments-x", 16)
	m, _, _, _ := otherFolderApp(t, long)

	m = openFlow(t, m, "Update")

	s := screen(m)
	rows := rowsOf(s)
	first := lineIndex(s, "This Orbit was started from")
	if first < 0 {
		t.Fatalf("no title:\n%s", s)
	}
	end := first
	for end < len(rows) && screenRow(rows[end]) != "" {
		end++
	}
	if title := rows[first:end]; len(title) != 4 {
		t.Errorf("the title is %d rows, want the four the long folder is cut to:\n%s", len(title), s)
	} else if !strings.HasSuffix(textOf(title[3]), "…") {
		t.Errorf("the last title row %q does not end in …", textOf(title[3]))
	}
	for _, want := range []string{refuseUpdate, refuseShared} {
		if !strings.Contains(oneLine(s), want) {
			t.Errorf("the refusal lacks %q:\n%s", want, s)
		}
	}
	for i, r := range rows {
		if n := len([]rune(strings.TrimRight(r, " "))); n > 80 {
			t.Errorf("row %d is %d cells on an 80-cell screen: %q", i, n, r)
		}
	}
}

// The refusal is for the other folder's container only: one this folder
// started leaves the flows as they always were.
func TestOtherFolder_NoRefusalWhenTheContainerIsThisFolders(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		runningAs(deploy.RunningApp{Found: true, Image: recordedImage, Folder: dir, State: "running"}, nil)))

	m = openFlow(t, m, "Update")

	s := screen(m)
	if strings.Contains(s, "This Orbit was started from") || !strings.Contains(s, "Update Orbit") {
		t.Errorf("Update did not open on its confirmation:\n%s", s)
	}
}
