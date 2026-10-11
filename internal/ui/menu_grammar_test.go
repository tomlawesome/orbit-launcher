package ui

// Pins #228 (one menu grammar on every screen; #200 C1-C3) from the issue
// and the owner's decisions of 2026-10-11 (7a, 8a), not from the code:
//
//   - C1. A digit on the main menu moves the caret to its row and nothing
//     else; Enter acts, as on every other menu. No q quit, Esc does
//     nothing on the main menu, Ctrl-C quits (all settled by #226).
//   - C2. Every screen with rows is a menu: Up and Down move the caret,
//     Enter picks the selected row, Esc is one step back, every other key
//     does nothing. A dead end offers one row, the back act. An
//     after-screen offers the flow's own actions, then Menu, then Exit
//     (the success screen keeps Get into Orbit / Terminal / Menu).
//   - C3. A leaving row is named by what it does. Cancel declines a
//     question and goes where Esc goes; Back retreats from an information
//     screen to the previous one; Menu leaves a dead end or after-screen
//     for the main menu. Exit and Terminal quit.
//
// Rows are read off the screen (screenLabels), so the tests hold however
// the screens are built. A row is one of the labels in helpers_test.go's
// rowLabels.

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// landing says where a key press left a flow: "quit" when it ended the
// program, "menu" when it asked for the main menu, otherwise "screen".
func landing(m tea.Model, cmd tea.Cmd) string {
	switch {
	case isQuit(cmd):
		return "quit"
	case leftForMenuOrChosen(m):
		return "menu"
	}
	return "screen"
}

// grammarScreen is a flow sitting on one screen that has rows.
type grammarScreen struct {
	name  string
	build func(t *testing.T) tea.Model
	// rows are the menu rows on the screen, top to bottom, exactly; nil
	// when the screen's rows are not in rowLabels (the profile choice).
	rows []string
	// tail, instead of rows, names the last rows only: for screens
	// whose earlier rows belong to the flow (Repair's).
	tail []string
	// escShows is a piece of the screen Esc leaves on; empty when Esc
	// goes back to the main menu.
	escShows string
}

func updateNotFound(t *testing.T) tea.Model {
	t.Helper()
	m, _ := newTestUpdateModel(nil, engineRunSeams{})
	if m.state != updateStateNotFound {
		t.Fatalf("Update is not on its not-found screen (state %v)", m.state)
	}
	return m
}

func removeNotFound(t *testing.T) tea.Model {
	t.Helper()
	m, _ := updateAs(t, NewRemoveModel(nil), tea.WindowSizeMsg{Width: 80, Height: 24})
	if m.state != removeStateNotFound {
		t.Fatalf("Remove is not on its not-found screen (state %v)", m.state)
	}
	return m
}

func installUnavailableProfile(t *testing.T) tea.Model {
	t.Helper()
	m, _ := updateAs(t, profileInstall(t), key(tea.KeyDown), key(tea.KeyEnter)) // AI
	if !strings.Contains(screen(m), "This profile isn't available yet") {
		t.Fatalf("not on the unavailable-profile screen:\n%s", screen(m))
	}
	return m
}

func installConfirm(t *testing.T) tea.Model {
	t.Helper()
	m, _ := updateAs(t, profileInstall(t), key(tea.KeyEnter))
	if !strings.Contains(screen(m), "Ready to install") {
		t.Fatalf("not on Install's confirm screen:\n%s", screen(m))
	}
	return m
}

func repairErrorScreen(t *testing.T) tea.Model {
	t.Helper()
	m, _ := updateAs(t, sizedRepair(t), repairReadyMsg{err: errors.New("target directory: no such file")})
	return m
}

func grammarScreens() []grammarScreen {
	return []grammarScreen{
		// Dead ends: one row, the back act.
		{name: "Update not found", build: updateNotFound, rows: []string{"Menu"}},
		{name: "Remove not found", build: removeNotFound, rows: []string{"Menu"}},
		{name: "Install profile unavailable", build: installUnavailableProfile,
			rows: []string{"Back"}, escShows: "Choose a deployment profile"},
		// Questions: Cancel declines and goes where Esc goes.
		{name: "Install stale volume", build: staleInstall, rows: []string{"Continue anyway", "Cancel"}},
		{name: "Install confirm", build: installConfirm,
			rows: []string{"Install now", "Cancel"}, escShows: "Choose a deployment profile"},
		{name: "Update confirm", build: confirmUpdate, rows: []string{"Update Orbit", "Cancel"}},
		{name: "Remove confirm", build: confirmRemove, rows: []string{"Stand down Orbit", "Cancel"}},
		{name: "Install profile", build: profileInstall},
		// After-screens: the flow's actions, then Menu, then Exit.
		{name: "Remove removed", build: removedScreen, rows: []string{"Copy command", "Menu", "Exit"}},
		{name: "Remove failed", build: removeFailedScreen, rows: []string{"Menu", "Exit"}},
		{name: "Install success", build: installSuccessScreen, rows: []string{"Get into Orbit", "Terminal", "Menu"}},
		{name: "Repair error", build: repairErrorScreen, tail: []string{"Menu", "Exit"}},
	}
}

func (c grammarScreen) hasRows() bool { return c.rows != nil || c.tail != nil }

// wantLanding checks where a leaving row or Esc went against the case.
func (c grammarScreen) wantLanding(t *testing.T, what string, m tea.Model, cmd tea.Cmd) {
	t.Helper()
	got := landing(m, cmd)
	if c.escShows == "" {
		if got != "menu" {
			t.Errorf("%s landed on %q, want the main menu:\n%s", what, got, screen(m))
		}
		return
	}
	if got != "screen" || !strings.Contains(screen(m), c.escShows) {
		t.Errorf("%s landed on %q, want the screen showing %q:\n%s", what, got, c.escShows, screen(m))
	}
}

// C3: the rows, named exactly as the issue lists them per screen.
func TestMenuGrammar_EveryScreenOffersExactlyTheseRows(t *testing.T) {
	for _, c := range grammarScreens() {
		if !c.hasRows() {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			got := screenLabels(screen(c.build(t)))
			if c.rows != nil && !slices.Equal(got, c.rows) {
				t.Errorf("rows = %q, want %q", got, c.rows)
			}
			if c.tail != nil && !(len(got) >= len(c.tail) && slices.Equal(got[len(got)-len(c.tail):], c.tail)) {
				t.Errorf("rows = %q, want them to end %q", got, c.tail)
			}
		})
	}
}

// C3: no screen still carries a row under an old name.
func TestMenuGrammar_NoScreenUsesAnOldRowName(t *testing.T) {
	back := map[string]bool{"Install profile unavailable": true}
	for _, c := range grammarScreens() {
		t.Run(c.name, func(t *testing.T) {
			got := screenLabels(screen(c.build(t)))
			if slices.Contains(got, "Go back") {
				t.Errorf("the row %q is now Back (rows %q)", "Go back", got)
			}
			if slices.Contains(got, "Back") && !back[c.name] {
				t.Errorf("Back retreats from an information screen; this one is a question or a dead end (rows %q)", got)
			}
		})
	}
}

// C2: Up and Down move the caret over the rows (wrapping), and on a
// one-row screen have nowhere to go; neither quits nor leaves.
func TestMenuGrammar_UpAndDownMoveTheCaretOnly(t *testing.T) {
	for _, c := range grammarScreens() {
		if !c.hasRows() {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			m := c.build(t)
			labels := screenLabels(screen(m))
			start := caretLabel(screen(m))
			at := slices.Index(labels, start)
			if at < 0 {
				t.Fatalf("no caret on any row of %q:\n%s", labels, screen(m))
			}
			for _, step := range []struct {
				k    tea.KeyPressMsg
				move int
			}{{key(tea.KeyDown), 1}, {key(tea.KeyUp), -1}} {
				moved, cmd := updateAs(t, m, step.k)
				if cmd != nil || landing(moved, cmd) != "screen" {
					t.Fatalf("%v left the screen (landing %q, cmd %v)", step.k, landing(moved, cmd), cmd != nil)
				}
				want := labels[(at+step.move+len(labels))%len(labels)]
				if got := caretLabel(screen(moved)); got != want {
					t.Errorf("%v from %q put the caret on %q, want %q", step.k, start, got, want)
				}
			}
		})
	}
}

// C2: every other key does nothing: the screen, the caret and the
// program are as they were. Letters (q among them, #226), digits (C1:
// no screen treats a digit as a shortcut), Space, and the sideways keys.
func TestMenuGrammar_EveryOtherKeyDoesNothing(t *testing.T) {
	others := []tea.KeyPressMsg{
		runeKey('x'), runeKey('q'), runeKey('Q'), runeKey('1'), runeKey('5'),
		{Code: tea.KeySpace, Text: " "}, key(tea.KeyLeft), key(tea.KeyRight),
	}
	for _, c := range grammarScreens() {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range others {
				m := c.build(t)
				before := screen(m)
				after, cmd := updateAs(t, m, k)
				if cmd != nil {
					t.Errorf("%q issued a command (quit = %v)", k.String(), isQuit(cmd))
				}
				if landing(after, nil) != "screen" {
					t.Errorf("%q left the screen", k.String())
				}
				if got := screen(after); got != before {
					t.Errorf("%q changed the screen:\n%s", k.String(), got)
				}
			}
		})
	}
}

// C2: Esc is one step back, from every screen.
func TestMenuGrammar_EscIsOneStepBack(t *testing.T) {
	for _, c := range grammarScreens() {
		t.Run(c.name, func(t *testing.T) {
			m, cmd := updateAs(t, c.build(t), key(tea.KeyEsc))
			c.wantLanding(t, "Esc", m, cmd)
		})
	}
}

// C3: a Cancel, Back or Menu row's Enter lands where Esc lands on that
// screen; and a Menu row always lands on the main menu.
func TestMenuGrammar_LeavingRowsLandWhereEscLands(t *testing.T) {
	for _, c := range grammarScreens() {
		if !c.hasRows() {
			continue
		}
		for _, label := range []string{"Cancel", "Back", "Menu"} {
			if !slices.Contains(append(slices.Clone(c.rows), c.tail...), label) {
				continue
			}
			t.Run(c.name+"/"+label, func(t *testing.T) {
				m := moveCaretTo(t, c.build(t), label)
				m, cmd := updateAs(t, m, key(tea.KeyEnter))
				c.wantLanding(t, "the "+label+" row", m, cmd)
				if label == "Menu" && landing(m, cmd) != "menu" {
					t.Errorf("the Menu row landed on %q, want the main menu", landing(m, cmd))
				}
			})
		}
	}
}

// C2: Exit (and the success screen's Terminal) quit.
func TestMenuGrammar_ExitAndTerminalRowsQuit(t *testing.T) {
	for _, c := range grammarScreens() {
		for _, label := range []string{"Exit", "Terminal"} {
			if !slices.Contains(append(slices.Clone(c.rows), c.tail...), label) {
				continue
			}
			t.Run(c.name+"/"+label, func(t *testing.T) {
				m := moveCaretTo(t, c.build(t), label)
				if _, cmd := updateAs(t, m, key(tea.KeyEnter)); !isQuit(cmd) {
					t.Errorf("the %s row did not quit", label)
				}
			})
		}
	}
}

// Settled by #226 and kept: Ctrl-C quits from every one of these.
func TestMenuGrammar_CtrlCQuitsFromEveryScreen(t *testing.T) {
	for _, c := range grammarScreens() {
		t.Run(c.name, func(t *testing.T) {
			m, cmd := updateAs(t, c.build(t), ctrlC())
			if !isQuit(cmd) {
				t.Fatal("Ctrl+C did not quit")
			}
			if o, ok := m.(interface{ Outcome() flowOutcome }); ok && o.Outcome().WantsMenu {
				t.Error("Ctrl+C was taken as back-to-menu")
			}
		})
	}
}

// C2: the dead end offers one row, so Enter on it is the back act —
// not a quit, as Update's not-found screen had it for any key.
func TestMenuGrammar_DeadEndsBackOutAndNothingQuits(t *testing.T) {
	for _, c := range grammarScreens() {
		if !strings.HasSuffix(c.name, "not found") {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			m, cmd := updateAs(t, c.build(t), key(tea.KeyEnter))
			if isQuit(cmd) {
				t.Fatal("Enter quit; the dead end's one row is Menu")
			}
			if !leftForMenuOrChosen(m) {
				t.Fatal("Enter did not leave for the main menu")
			}
		})
	}
}

// C3: the notice's retreat row is Back, not Go back, and it lands where
// Esc lands: the confirm screen.
func TestMenuGrammar_NoticeRowIsBackAndLandsWhereEscLands(t *testing.T) {
	m, _, _ := openNotice(t, 80, 26, engineRunSeams{})
	if got := screenLabels(screen(m)); !slices.Equal(got, []string{"Back"}) {
		t.Fatalf("notice rows = %q, want just Back", got)
	}
	viaEsc, _ := updateAs(t, m, key(tea.KeyEsc))
	viaRow, cmd := updateAs(t, m, key(tea.KeyEnter))
	if isQuit(cmd) {
		t.Fatal("the Back row quit")
	}
	for name, got := range map[string]InstallModel{"Esc": viaEsc, "Back": viaRow} {
		if got.state != installStateConfirm || !strings.Contains(screen(got), "Ready to install") {
			t.Errorf("%s left the notice for state %v, want the confirm screen:\n%s", name, got.state, screen(got))
		}
	}
}

// C2/C3: the engine run's after-screens end Menu, Exit (they already
// did; this holds them to it while the key handling is shared).
func TestMenuGrammar_EngineRunAfterScreensEndMenuThenExit(t *testing.T) {
	t.Run("failed", func(t *testing.T) {
		r := startedRun(t, "install", engineRunSeams{})
		r, _ = runUpdate(t, r, engineReadyMsg{err: errors.New("boom")})
		if got := screenLabels(runScreen(r)); !slices.Equal(got, []string{"Open the guided installer", "Menu", "Exit"}) {
			t.Errorf("rows = %q, want the guided installer, Menu, Exit", got)
		}
		for _, k := range []tea.KeyPressMsg{runeKey('x'), runeKey('q'), runeKey('5'), key(tea.KeyLeft)} {
			r2, cmd := runUpdate(t, r, k)
			if cmd != nil || r2.Done || runScreen(r2) != runScreen(r) {
				t.Errorf("%q did something on the failed screen", k.String())
			}
		}
	})
}

// --- Through AppModel: Menu really lands on the live main menu -------------

// grammarApp is an AppModel sitting on a screen whose Menu row leaves
// for the main menu.
type grammarApp struct {
	name  string
	build func(t *testing.T) AppModel
}

func appOnUpdateNotFound(t *testing.T) AppModel {
	t.Helper()
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = shortTempDir(t) // no .env-orbit here
	m = openUpdate(t, m, nil)
	if m.state != appStateUpdate || !strings.Contains(screen(m), "No Orbit deployment found") {
		t.Fatalf("not on Update's not-found screen (state %v):\n%s", m.state, screen(m))
	}
	return m
}

func appOnRemoveNotFound(t *testing.T) AppModel {
	t.Helper()
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.targetDir = shortTempDir(t) // no .env-orbit here
	m = openRemove(t, m)
	if m.state != appStateRemove || !strings.Contains(screen(m), "No Orbit deployment found") {
		t.Fatalf("not on Remove's not-found screen (state %v):\n%s", m.state, screen(m))
	}
	return m
}

func grammarApps() []grammarApp {
	return []grammarApp{
		{"Update not found", appOnUpdateNotFound},
		{"Remove not found", appOnRemoveNotFound},
		{"Remove removed", appOnRemoved},
		{"Remove failed", appOnRemoveFailed},
		{"Install success", appOnInstallSuccess},
	}
}

// assertLiveMenu checks the app is showing the main menu, sized, drawn
// and answering the arrow keys.
func assertLiveMenu(t *testing.T, m AppModel) {
	t.Helper()
	if m.state != appStateSplash {
		t.Fatalf("the app is in state %v, want the main menu:\n%s", m.state, screen(m))
	}
	s := screen(m)
	if !strings.Contains(s, "O R B I T") || !strings.Contains(s, "Install") {
		t.Fatalf("the main menu is not showing:\n%s", s)
	}
	before := m.splash.menu.sel
	moved, cmd := updateAs(t, m, key(tea.KeyDown))
	if isQuit(cmd) || moved.splash.menu.sel != (before+1)%len(moved.splash.menu.items) {
		t.Fatalf("Down on the returned menu did not move the caret (sel %d -> %d)", before, moved.splash.menu.sel)
	}
}

func TestMenuGrammar_AppMenuRowLeavesForTheLiveMainMenu(t *testing.T) {
	for _, c := range grammarApps() {
		t.Run(c.name, func(t *testing.T) {
			m := moveCaretTo(t, c.build(t), "Menu")
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			if isQuit(cmd) {
				t.Fatal("the Menu row quit the launcher")
			}
			assertLiveMenu(t, drive(t, m, cmd))
		})
	}
}

// The dead ends on the real app: no key but Menu, Esc and Ctrl-C does
// anything, and the program stays up.
func TestMenuGrammar_AppDeadEndsIgnoreEveryOtherKey(t *testing.T) {
	for _, c := range grammarApps()[:2] {
		t.Run(c.name, func(t *testing.T) {
			for _, k := range []tea.KeyPressMsg{runeKey('x'), runeKey('q'), runeKey('5'), key(tea.KeySpace), key(tea.KeyLeft), key(tea.KeyUp), key(tea.KeyDown)} {
				before := c.build(t)
				m, cmd := updateAs(t, before, k)
				if isQuit(cmd) {
					t.Fatalf("%q quit the program", k.String())
				}
				m = drive(t, m, cmd)
				if m.state != before.state || screen(m) != screen(before) {
					t.Errorf("%q changed the dead end (state %v -> %v):\n%s", k.String(), before.state, m.state, screen(m))
				}
			}
		})
	}
}

// Esc and the Menu row land on the same live menu.
func TestMenuGrammar_AppEscLeavesForTheLiveMainMenu(t *testing.T) {
	for _, c := range grammarApps() {
		t.Run(c.name, func(t *testing.T) {
			m, cmd := updateAs(t, c.build(t), key(tea.KeyEsc))
			if isQuit(cmd) {
				t.Fatal("Esc quit the launcher")
			}
			assertLiveMenu(t, drive(t, m, cmd))
		})
	}
}

// --- C1 through AppModel: a digit moves the caret, Enter acts --------------

func TestMenuGrammar_AppDigitMovesTheCaretAndStartsNothing(t *testing.T) {
	for i, item := range MainMenu {
		t.Run(item.Label, func(t *testing.T) {
			m := leaveApp(t)
			m, cmd := updateAs(t, m, runeKey(rune('1'+i)))
			if isQuit(cmd) {
				t.Fatalf("the digit %d quit the launcher", i+1)
			}
			m = drive(t, m, cmd)
			if m.state != appStateSplash {
				t.Fatalf("the digit %d left the main menu for state %v", i+1, m.state)
			}
			if m.splash.menu.sel != i || !strings.Contains(screen(m), "▸ "+item.Label) {
				t.Errorf("the digit %d put the caret on row %d, want %d (%s):\n%s", i+1, m.splash.menu.sel, i, item.Label, screen(m))
			}
		})
	}
}

func TestMenuGrammar_AppEnterAfterADigitActs(t *testing.T) {
	flows := []struct {
		digit rune
		state appState
	}{
		{'1', appStateInstall},
		{'2', appStateUpdate},
		{'4', appStateRemove},
	}
	for _, f := range flows {
		t.Run(string(f.digit), func(t *testing.T) {
			m, _ := updateAs(t, leaveApp(t), runeKey(f.digit))
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			m = drive(t, m, cmd)
			if m.state != f.state {
				t.Errorf("Enter after %q left the app in state %v, want %v:\n%s", f.digit, m.state, f.state, screen(m))
			}
		})
	}
	t.Run("Exit", func(t *testing.T) {
		m, cmd := updateAs(t, leaveApp(t), runeKey('5'))
		if isQuit(cmd) {
			t.Fatal("5 quit the launcher; it only moves the caret to Exit")
		}
		if _, cmd := updateAs(t, m, key(tea.KeyEnter)); !isQuit(cmd) {
			t.Fatal("Enter on Exit did not quit")
		}
	})
}
