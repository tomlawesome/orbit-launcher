package ui

// Pins #225 (Esc on a finished flow's screens goes back to the menu),
// from the issue and #210's rule, not from the code. Owner decision
// (2026-10-11, answer 3a): on the screens shown after a flow finishes,
// Esc goes back to the main menu, as it already does after Repair. That
// is Remove's "removed" and "failed" screens and Install's success
// screen. Rows that say Exit/Terminal and Ctrl-C still quit; there is no `q`
// shortcut on any of them (#226).
//
// "Back to the menu" is the flow's leave outcome: Outcome() reading Done
// and WantsMenu without Succeeded, and no tea.Quit (see leave_flow_test.go).
// The success screen is a model of its own that AppModel reads; it has
// no Outcome() today, and its existing Menu row says "menu" in Chosen.
// Either signal counts as leaving for the menu here, so the fix may use
// whichever fits; the AppModel tests below are what pin the behaviour.

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// finishedScreen is a flow sitting on one of the screens shown after it
// has finished, as a model on its own.
type finishedScreen struct {
	name string
	// model builds the flow on that screen.
	model func(t *testing.T) tea.Model
	// exitKeys are the keys from that screen's default row to its
	// Exit/Terminal row, ending with the Enter that chooses it.
	exitKeys []tea.Msg
}

func removeFinished(t *testing.T, standDownErr error) tea.Model {
	t.Helper()
	m := newTestRemoveModel(func(context.Context, string) error { return standDownErr })
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Stand down Orbit is preselected
	if cmd == nil {
		t.Fatal("confirming Remove started nothing")
	}
	m, _ = updateAs(t, m, cmd())
	return m
}

func removedScreen(t *testing.T) tea.Model {
	t.Helper()
	m := removeFinished(t, nil).(RemoveModel)
	if m.state != removeStateDone {
		t.Fatalf("Remove is not on the removed screen (state %v):\n%s", m.state, screen(m))
	}
	return m
}

func removeFailedScreen(t *testing.T) tea.Model {
	t.Helper()
	m := removeFinished(t, errors.New("docker daemon not running")).(RemoveModel)
	if m.state != removeStateFailed {
		t.Fatalf("Remove is not on the failed screen (state %v):\n%s", m.state, screen(m))
	}
	return m
}

func installSuccessScreen(t *testing.T) tea.Model {
	t.Helper()
	m := newTestSuccessModel()
	if s := screen(m); !strings.Contains(s, "Get into Orbit") {
		t.Fatalf("not on the success screen:\n%s", s)
	}
	return m
}

func finishedScreens() []finishedScreen {
	return []finishedScreen{
		// Copy command is the default row; Exit is two below it, under
		// Menu (#228).
		{"Remove removed", removedScreen, []tea.Msg{key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)}},
		// Menu is the default row; Exit is the one below it (#228).
		{"Remove failed", removeFailedScreen, []tea.Msg{key(tea.KeyDown), key(tea.KeyEnter)}},
		// Get into Orbit, then Terminal.
		{"Install success", installSuccessScreen, []tea.Msg{key(tea.KeyDown), key(tea.KeyEnter)}},
	}
}

// leftForMenuOrChosen reads the leave signal off a finished flow's model:
// its Outcome(), where it has one, or the success screen's Chosen.
func leftForMenuOrChosen(m tea.Model) bool {
	if f, ok := m.(interface{ Outcome() flowOutcome }); ok && leftForMenu(f.Outcome()) {
		return true
	}
	if s, ok := m.(SuccessModel); ok {
		return s.Chosen == "menu"
	}
	return false
}

// A finished flow's screen is a real screen before Esc, so what the
// tests below read afterwards cannot be a default value.
func TestEscAfterFlow_ScreensAreFinishedAndNotLeavingYet(t *testing.T) {
	for _, c := range finishedScreens() {
		t.Run(c.name, func(t *testing.T) {
			m := c.model(t)
			if leftForMenuOrChosen(m) {
				t.Fatal("the screen already reads as leaving for the menu before any key")
			}
			if s := screen(m); strings.TrimSpace(s) == "" {
				t.Fatal("the screen is blank")
			}
		})
	}
}

func TestEscAfterFlow_EscGoesBackToTheMenu(t *testing.T) {
	for _, c := range finishedScreens() {
		t.Run(c.name, func(t *testing.T) {
			m, cmd := updateAs(t, c.model(t), key(tea.KeyEsc))
			if isQuit(cmd) {
				t.Fatal("Esc quit the launcher; on a finished flow's screen it goes back to the menu")
			}
			if !leftForMenuOrChosen(m) {
				t.Fatalf("after Esc the flow does not read as leaving for the menu (outcome %+v)", outcomeIfAny(m))
			}
			if s, ok := m.(SuccessModel); ok && s.Chosen == "terminal" {
				t.Fatal("Esc chose Terminal")
			}
		})
	}
}

// outcomeIfAny is the model's Outcome() for a failure message, or the
// zero value for a model that has none.
func outcomeIfAny(m tea.Model) flowOutcome {
	if f, ok := m.(interface{ Outcome() flowOutcome }); ok {
		return f.Outcome()
	}
	return flowOutcome{}
}

func TestEscAfterFlow_ExitRowAndCtrlCStillQuitAndQDoesNothing(t *testing.T) {
	for _, c := range finishedScreens() {
		t.Run(c.name+"/exit row", func(t *testing.T) {
			m, cmd := updateAs(t, c.model(t), c.exitKeys...)
			if !isQuit(cmd) {
				t.Fatal("the Exit/Terminal row did not quit")
			}
			if leftForMenuOrChosen(m) {
				t.Fatal("the Exit/Terminal row was taken as back-to-menu")
			}
		})
		t.Run(c.name+"/q does nothing", func(t *testing.T) {
			for _, r := range []rune{'q', 'Q'} {
				m, cmd := updateAs(t, c.model(t), runeKey(r))
				if cmd != nil || isQuit(cmd) {
					t.Fatalf("%q did something (quit = %v); it has no meaning here", r, isQuit(cmd))
				}
				if leftForMenuOrChosen(m) {
					t.Fatalf("%q left the screen", r)
				}
			}
		})
		t.Run(c.name+"/ctrl-c", func(t *testing.T) {
			m, cmd := updateAs(t, c.model(t), ctrlC())
			if !isQuit(cmd) {
				t.Fatal("Ctrl+C did not quit")
			}
			if leftForMenuOrChosen(m) {
				t.Fatal("Ctrl+C was taken as back-to-menu")
			}
		})
	}
}

// --- Through AppModel -----------------------------------------------------

// appFinished is an AppModel sitting on one of the finished screens,
// reached the way a person gets there, with the window at 80x24.
type appFinished struct {
	name string
	// app builds the AppModel on the screen.
	app      func(t *testing.T) AppModel
	exitKeys []tea.Msg
}

func appOnRemoveScreen(t *testing.T, standDownErr error) AppModel {
	t.Helper()
	m := leaveApp(t)
	// The path is incidental: a short one keeps the removal command's
	// words fitting the box whole at 80x24 (#271 criterion r).
	short := shortTempDir(t)
	writeEnv(t, short, "APP_URL=https://mail.example.test\n")
	m.targetDir = short
	m, _ = updateAs(t, m, key(tea.KeyUp), key(tea.KeyUp)) // Remove
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateRemove {
		t.Fatalf("Remove did not open (state %v):\n%s", m.state, screen(m))
	}
	// The stand-down and the clipboard are the flow's own seams; nothing
	// here may reach Docker or the real terminal.
	m.remove.standDown = func(context.Context, string) error { return standDownErr }
	m.remove.clipboard = io.Discard
	m, cmd = updateAs(t, m, key(tea.KeyEnter)) // Stand down Orbit
	m = drive(t, m, cmd)
	return m
}

func appOnRemoved(t *testing.T) AppModel {
	t.Helper()
	m := appOnRemoveScreen(t, nil)
	if m.state != appStateRemove || m.remove.state != removeStateDone {
		t.Fatalf("not on the removed screen (app state %v, remove state %v):\n%s", m.state, m.remove.state, screen(m))
	}
	return m
}

func appOnRemoveFailed(t *testing.T) AppModel {
	t.Helper()
	m := appOnRemoveScreen(t, errors.New("docker daemon not running"))
	if m.state != appStateRemove || m.remove.state != removeStateFailed {
		t.Fatalf("not on the failed screen (app state %v, remove state %v):\n%s", m.state, m.remove.state, screen(m))
	}
	return m
}

// appOnInstallSuccess runs Install to its success screen through the
// app, with a fake engine that completes.
func appOnInstallSuccess(t *testing.T) AppModel {
	t.Helper()
	queue := newSink()
	m := NewAppModelNoAnimation().WithSender(queue.Send).WithoutVolumeCheck().WithVersion("v9.9.9")
	m.targetDir = t.TempDir()
	m.flowSeams = engineRunSeams{
		prepareEngine: fakeEngine(nil, successStream()...),
		detect:        fakeDetect("https://mail.example.com"),
	}
	m.flowNoticeDuration = time.Millisecond
	shortNoticeTick(t)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Install
	m = drive(t, m, cmd)
	m, cmd = updateAs(t, m, key(tea.KeyEnter)) // Standard profile -> the notice
	m = drive(t, m, cmd)
	if s := screen(m); !strings.Contains(s, "A note before you install") {
		t.Fatalf("not on Install's notice:\n%s", s)
	}
	m, cmd = updateAs(t, m, key(tea.KeyEnd), tea.KeyPressMsg{Text: noticePhrase}, key(tea.KeyEnter))
	m = drive(t, m, cmd, queue)
	if m.state != appStateSuccess {
		t.Fatalf("Install did not reach the success screen (state %v):\n%s", m.state, screen(m))
	}
	return m
}

func appFinishedScreens() []appFinished {
	return []appFinished{
		{name: "Remove removed", app: appOnRemoved, exitKeys: []tea.Msg{key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)}},
		{name: "Remove failed", app: appOnRemoveFailed, exitKeys: []tea.Msg{key(tea.KeyDown), key(tea.KeyEnter)}},
		{name: "Install success", app: appOnInstallSuccess, exitKeys: []tea.Msg{key(tea.KeyDown), key(tea.KeyEnter)}},
	}
}

// Esc brings the main menu back, as the program would run it: sized to
// the window as it is now (resized while on the finished screen), drawn,
// and answering keys.
func TestEscAfterFlow_AppShowsTheMenuSizedAndLive(t *testing.T) {
	for _, c := range appFinishedScreens() {
		t.Run(c.name, func(t *testing.T) {
			m := c.app(t)
			// The window changes while the finished screen is up, so a
			// menu sized from a stale or remembered size shows.
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 100, Height: 30})

			m, cmd := updateAs(t, m, key(tea.KeyEsc))
			if isQuit(cmd) {
				t.Fatal("Esc quit the launcher; on a finished flow's screen it goes back to the menu")
			}
			m = drive(t, m, cmd)
			if m.state != appStateSplash {
				t.Fatalf("after Esc the app is in state %v, want the menu:\n%s", m.state, screen(m))
			}
			s := screen(m)
			if !strings.Contains(s, "O R B I T") || !strings.Contains(s, "Install") {
				t.Fatalf("the menu is not showing after Esc:\n%s", s)
			}

			// Sized: the menu knows the current window, not zero and not
			// the size it had before the resize.
			if m.splash.width != 100 || m.splash.height != 30 {
				t.Fatalf("the menu is %dx%d, want the window's 100x30", m.splash.width, m.splash.height)
			}

			// Live: Down moves the selection, and Enter on the row
			// opens its flow (neither quits).
			before := m.splash.menu.sel
			m, cmd = updateAs(t, m, key(tea.KeyDown))
			if isQuit(cmd) {
				t.Fatal("Down on the returned menu quit the launcher")
			}
			if want := (before + 1) % len(m.splash.menu.items); m.splash.menu.sel != want {
				t.Fatalf("Down moved the selection to %d, want %d: the menu is not live", m.splash.menu.sel, want)
			}
			moved := screen(m)
			if moved == s {
				t.Fatal("the screen did not change when the selection moved")
			}
			m, cmd = updateAs(t, m, key(tea.KeyEnter))
			if isQuit(cmd) {
				t.Fatal("choosing a row on the returned menu quit the launcher")
			}
			m = drive(t, m, cmd)
			if m.state == appStateSplash {
				t.Fatalf("Enter on the returned menu opened nothing:\n%s", screen(m))
			}
		})
	}
}

func TestEscAfterFlow_AppExitRowAndCtrlCStillQuitAndQDoesNothing(t *testing.T) {
	for _, c := range appFinishedScreens() {
		t.Run(c.name+"/exit row", func(t *testing.T) {
			if _, cmd := updateAs(t, c.app(t), c.exitKeys...); !isQuit(cmd) {
				t.Fatal("the Exit/Terminal row did not quit the program")
			}
		})
		t.Run(c.name+"/q does nothing", func(t *testing.T) {
			for _, r := range []rune{'q', 'Q'} {
				before := c.app(t)
				m, cmd := updateAs(t, before, runeKey(r))
				if isQuit(cmd) {
					t.Fatalf("%q quit the program; there is no q shortcut", r)
				}
				m = drive(t, m, cmd)
				if m.state != before.state {
					t.Fatalf("%q moved the app from state %v to %v", r, before.state, m.state)
				}
				if screen(m) != screen(before) {
					t.Fatalf("%q changed the screen:\n%s", r, screen(m))
				}
			}
		})
		t.Run(c.name+"/ctrl-c", func(t *testing.T) {
			m, cmd := updateAs(t, c.app(t), ctrlC())
			if !isQuit(cmd) {
				t.Fatal("Ctrl+C did not quit the program")
			}
			if m.state == appStateSplash {
				t.Fatal("Ctrl+C went back to the menu")
			}
		})
	}
}

// --- Repair: the rule this completes ---------------------------------------

// Regression guard: Repair's post-run screens already send Esc to the
// menu. This must stay true while the other flows catch up.
func TestEscAfterFlow_RepairPostRunScreensStillGoBackToTheMenu(t *testing.T) {
	t.Run("error screen", func(t *testing.T) {
		m, _ := updateAs(t, sizedRepair(t), repairReadyMsg{err: errors.New("target directory: no such file")})
		if !strings.Contains(screen(m), "Diagnosis stopped") {
			t.Fatalf("not on the error screen:\n%s", screen(m))
		}
		if leftForMenu(m.Outcome()) {
			t.Fatal("already leaving before Esc")
		}
		m, cmd := updateAs(t, m, key(tea.KeyEsc))
		if isQuit(cmd) {
			t.Fatal("Esc on Repair's error screen quit the launcher")
		}
		if o := m.Outcome(); !leftForMenu(o) {
			t.Fatalf("outcome after Esc = %+v, want Done and WantsMenu without Succeeded", o)
		}
	})

	t.Run("through the app after a clear diagnosis", func(t *testing.T) {
		app, loop, cmd := repairViaApp(t, fakeRepairStream(`echo 'diagnosis result=healthy checked=13 skipped=0'; exit 0`))
		loop.settle(cmd, "the clear diagnosis", func() bool {
			return strings.Contains(screen(app), "Diagnosis clear")
		})
		loop.settle(loop.update(tea.WindowSizeMsg{Width: 100, Height: 30}), "the window size to be taken", func() bool {
			return app.width == 100 && app.height == 30
		})
		next := loop.update(key(tea.KeyEsc))
		if isQuit(next) {
			t.Fatal("Esc on Repair's finished diagnosis quit the launcher")
		}
		loop.settle(next, "the menu at the window's size", func() bool {
			return app.state == appStateSplash && app.splash.width == 100 && app.splash.height == 30
		})
		if app.state != appStateSplash {
			t.Fatalf("Esc left the app in state %v, want the menu:\n%s", app.state, screen(app))
		}
		if app.splash.width != 100 || app.splash.height != 30 {
			t.Fatalf("the menu is %dx%d, want the window's 100x30", app.splash.width, app.splash.height)
		}
		// Live, by the same reading the Esc tests above use.
		before := app.splash.menu.sel
		loop.settle(loop.update(key(tea.KeyDown)), "Down to move the selection", func() bool {
			return app.splash.menu.sel != before
		})
		if want := (before + 1) % len(app.splash.menu.items); app.splash.menu.sel != want {
			t.Fatalf("Down moved the selection to %d, want %d: the menu is not live", app.splash.menu.sel, want)
		}
	})
}
