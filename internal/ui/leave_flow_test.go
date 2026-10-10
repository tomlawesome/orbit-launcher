package ui

// Pins #210 (leaving a flow, #199 WI-6; UI-1) from the issue and the
// #199 decisions note, not from the code. Owner decision (2026-10-10):
// Esc is one step back; at a flow's first screen, back is the main menu,
// for Install, Update, Remove and Repair alike; Ctrl-C quits everywhere.
// "Back to the menu" is the flow's Outcome() reading Done and WantsMenu
// without Succeeded, and no tea.Quit; AppModel then shows the splash.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// leaveCase builds a flow sitting on one screen. outcome reads the
// flow's Outcome() through the interface, so a flow that lacks the
// method fails the test instead of the build.
type leaveCase struct {
	name  string
	model func(t *testing.T) tea.Model
}

func leaveOutcomeOf(t *testing.T, m tea.Model) flowOutcome {
	t.Helper()
	f, ok := m.(interface{ Outcome() flowOutcome })
	if !ok {
		t.Fatalf("%T has no Outcome() flowOutcome", m)
	}
	return f.Outcome()
}

// leftForMenu is what "back to the main menu" means for a flow model.
func leftForMenu(o flowOutcome) bool { return o.Done && o.WantsMenu && !o.Succeeded }

func leavePress(t *testing.T, m tea.Model, msgs ...tea.Msg) (tea.Model, tea.Cmd) {
	t.Helper()
	var cmd tea.Cmd
	for _, msg := range msgs {
		m, cmd = m.Update(msg)
	}
	return m, cmd
}

func staleInstall(t *testing.T) tea.Model {
	t.Helper()
	m := NewInstallModel("/opt/orbit", "v0.1.0")
	m.checkVolumes = staleVolumeCheck(deploy.DatabaseVolume{Name: "orbit-db-data"})
	updated, _ := installUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 24}, m.Init()())
	if !strings.Contains(screen(updated), "already has an Orbit database") {
		t.Fatalf("not on the pre-flight screen:\n%s", screen(updated))
	}
	return updated
}

func profileInstall(t *testing.T) tea.Model {
	t.Helper()
	m, _ := newTestInstallModel(engineRunSeams{})
	return m
}

func confirmUpdate(t *testing.T) tea.Model {
	t.Helper()
	m, _ := newTestUpdateModel(testDeployment(), engineRunSeams{})
	return m
}

func confirmRemove(t *testing.T) tea.Model {
	t.Helper()
	return newTestRemoveModel(func(context.Context, string) error { return nil })
}

func preparingRepair(t *testing.T) tea.Model { return sizedRepair(t) }

// firstScreens are the flows' first screens, one per flow plus
// Install's pre-flight (which takes the first place when it fires).
func firstScreens() []leaveCase {
	return []leaveCase{
		{"Install profile", profileInstall},
		{"Install stale-volume pre-flight", staleInstall},
		{"Update confirm", confirmUpdate},
		{"Remove confirm", confirmRemove},
		{"Repair reading", preparingRepair},
	}
}

// Passed already: the four flows expose Outcome() today; the change
// must keep that true while it merges the cancel paths.
func TestLeaveFlow_EveryFlowExposesOutcome(t *testing.T) {
	for _, c := range firstScreens() {
		if _, ok := c.model(t).(interface{ Outcome() flowOutcome }); !ok {
			t.Errorf("%s: model has no Outcome() flowOutcome", c.name)
		}
	}
}

func TestLeaveFlow_EscOnTheFirstScreenGoesBackToTheMenu(t *testing.T) {
	for _, c := range firstScreens() {
		t.Run(c.name, func(t *testing.T) {
			m := c.model(t)
			// The screen is a live flow before Esc, so the outcome
			// below cannot be a default value.
			if o := leaveOutcomeOf(t, m); o.Done || o.WantsMenu {
				t.Fatalf("outcome before Esc = %+v, want a flow still in progress", o)
			}
			m, cmd := leavePress(t, m, key(tea.KeyEsc))
			if isQuit(cmd) {
				t.Fatal("Esc on the first screen quit the launcher; it should go back to the menu")
			}
			if o := leaveOutcomeOf(t, m); !leftForMenu(o) {
				t.Fatalf("outcome after Esc = %+v, want Done and WantsMenu without Succeeded", o)
			}
		})
	}
}

// The "Cancel" row on Update/Remove and the "Back" row on Install's
// pre-flight are the same act as Esc (UI-1: one leave-flow outcome).
// Assumption flagged in the report: the issue lists Esc / Cancel / Back
// as one act, and the owner's answer names Esc only.
func TestLeaveFlow_CancelAndBackRowsAreTheSameActAsEsc(t *testing.T) {
	rows := []leaveCase{
		{"Install pre-flight Back row", staleInstall},
		{"Update Cancel row", confirmUpdate},
		{"Remove Cancel row", confirmRemove},
	}
	for _, c := range rows {
		t.Run(c.name, func(t *testing.T) {
			m, _ := leavePress(t, c.model(t), key(tea.KeyDown))
			m, cmd := leavePress(t, m, key(tea.KeyEnter))
			if isQuit(cmd) {
				t.Fatal("the leave row quit the launcher; it should go back to the menu")
			}
			if o := leaveOutcomeOf(t, m); !leftForMenu(o) {
				t.Fatalf("outcome after the leave row = %+v, want Done and WantsMenu without Succeeded", o)
			}
		})
	}
}

// Leaving must not run the thing the flow was about to do.
func TestLeaveFlow_LeavingStartsNothing(t *testing.T) {
	t.Run("Update", func(t *testing.T) {
		started := false
		seams := engineRunSeams{
			prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
				started = true
				return nil, nil, errors.New("must not be called")
			},
		}
		m, _ := newTestUpdateModel(testDeployment(), seams)
		m2, cmd := leavePress(t, m, key(tea.KeyEsc))
		if cmd != nil {
			cmd()
		}
		if started {
			t.Fatal("Esc started the engine")
		}
		if !leftForMenu(leaveOutcomeOf(t, m2)) {
			t.Fatal("Esc did not leave the flow (a start-nothing check that never left proves nothing)")
		}
	})
	t.Run("Remove", func(t *testing.T) {
		called := false
		m := newTestRemoveModel(func(context.Context, string) error { called = true; return nil })
		m2, cmd := leavePress(t, m, key(tea.KeyEsc))
		if cmd != nil {
			cmd()
		}
		if called {
			t.Fatal("Esc stood the deployment down")
		}
		if !leftForMenu(leaveOutcomeOf(t, m2)) {
			t.Fatal("Esc did not leave the flow (a start-nothing check that never left proves nothing)")
		}
	})
}

// Esc is ONE step back: inside Install it goes up one screen first and
// reaches the menu only from the first screen.
func TestLeaveFlow_InstallEscIsOneStepBack(t *testing.T) {
	m := profileInstall(t)
	m, _ = leavePress(t, m, key(tea.KeyEnter)) // profile -> confirm
	if !strings.Contains(screen(m.(InstallModel)), "Ready to install") {
		t.Fatalf("not on the confirm screen:\n%s", screen(m.(InstallModel)))
	}

	m, cmd := leavePress(t, m, key(tea.KeyEsc))
	if isQuit(cmd) || leaveOutcomeOf(t, m).Done {
		t.Fatal("Esc on the confirm screen left the flow; it is one step back, to the profile screen")
	}
	if s := screen(m.(InstallModel)); !strings.Contains(s, "Choose a deployment profile") {
		t.Fatalf("Esc on confirm should show the profile screen:\n%s", s)
	}

	m, cmd = leavePress(t, m, key(tea.KeyEsc))
	if isQuit(cmd) || !leftForMenu(leaveOutcomeOf(t, m)) {
		t.Fatalf("second Esc, on the first screen: quit=%v outcome=%+v, want the menu", isQuit(cmd), leaveOutcomeOf(t, m))
	}
}

func TestLeaveFlow_InstallUnavailableProfileEscGoesToTheProfileScreenFirst(t *testing.T) {
	m := profileInstall(t)
	m, _ = leavePress(t, m, key(tea.KeyDown), key(tea.KeyEnter)) // AI profile
	if !strings.Contains(screen(m.(InstallModel)), "This profile isn't available yet") {
		t.Fatalf("not on the unavailable screen:\n%s", screen(m.(InstallModel)))
	}
	m, cmd := leavePress(t, m, key(tea.KeyEsc))
	if isQuit(cmd) || leaveOutcomeOf(t, m).Done {
		t.Fatal("Esc on the unavailable screen left the flow; it is one step back")
	}
	if s := screen(m.(InstallModel)); !strings.Contains(s, "Choose a deployment profile") {
		t.Fatalf("Esc should show the profile screen:\n%s", s)
	}
	m, _ = leavePress(t, m, key(tea.KeyEsc))
	if !leftForMenu(leaveOutcomeOf(t, m)) {
		t.Fatal("Esc on the profile screen did not go back to the menu")
	}
}

// Ctrl-C quits, never "back": it must not read as a leave-to-menu.
func TestLeaveFlow_CtrlCQuitsFromEveryScreen(t *testing.T) {
	removeDone := func(t *testing.T) tea.Model {
		m := newTestRemoveModel(func(context.Context, string) error { return nil })
		m2, cmd := leavePress(t, m, key(tea.KeyEnter))
		m3, _ := leavePress(t, m2, cmd())
		if !strings.Contains(stripANSI(m3.View().Content), "irreversible") &&
			!strings.Contains(stripANSI(m3.View().Content), "rm ") {
			t.Fatalf("not on the removal-command screen:\n%s", stripANSI(m3.View().Content))
		}
		return m3
	}
	repairErr := func(t *testing.T) tea.Model {
		m, _ := leavePress(t, sizedRepair(t), repairReadyMsg{err: errors.New("target directory: no such file")})
		return m
	}
	cases := append(firstScreens(),
		leaveCase{"Install confirm", func(t *testing.T) tea.Model {
			m, _ := leavePress(t, profileInstall(t), key(tea.KeyEnter))
			return m
		}},
		leaveCase{"Install unavailable profile", func(t *testing.T) tea.Model {
			m, _ := leavePress(t, profileInstall(t), key(tea.KeyDown), key(tea.KeyEnter))
			return m
		}},
		leaveCase{"Update not found", func(t *testing.T) tea.Model {
			m, _ := newTestUpdateModel(nil, engineRunSeams{})
			return m
		}},
		leaveCase{"Remove removal-command screen", removeDone},
		leaveCase{"Repair error screen", repairErr},
	)
	// A flow whose run is still being started cancels the start on
	// Ctrl-C and quits when the start reports back, so it can kill a run
	// that started anyway (TestRepairModel_CtrlCWhilePreparingCancelsAndQuitsOnceTheRunReportsBack).
	// The report is fed in; it must then quit.
	reportBack := map[string]tea.Msg{
		"Repair reading": repairReadyMsg{err: context.Canceled},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, cmd := leavePress(t, c.model(t), ctrlC())
			if report, ok := reportBack[c.name]; ok && cmd == nil {
				m, cmd = leavePress(t, m, report)
			}
			if !isQuit(cmd) {
				t.Fatal("Ctrl+C did not quit")
			}
			if leaveOutcomeOf(t, m).WantsMenu {
				t.Fatal("Ctrl+C was taken as back-to-menu; it must quit")
			}
		})
	}
}

// leaveApp opens the launcher on its menu, with no Docker lookups, in
// a directory holding a deployment so Update and Remove have a screen.
func leaveApp(t *testing.T) AppModel {
	t.Helper()
	dir := t.TempDir()
	writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
	m := NewAppModelNoAnimation().WithoutVolumeCheck()
	m.flowInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }
	m.targetDir = dir
	m, _ = appUpdate(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	if s := stripANSI(m.View().Content); !strings.Contains(s, "O R B I T") {
		t.Fatalf("not on the menu:\n%s", s)
	}
	return m
}

// Through AppModel, the way a person meets it: the flow's Esc brings
// the splash back instead of ending the program.
func TestLeaveFlow_AppReturnsToTheMenuFromEachFlow(t *testing.T) {
	flows := []struct {
		name string
		keys []tea.Msg // from the preselected Install row to the flow
		want string    // the flow's first screen
	}{
		{"Install", nil, "Choose a deployment profile"},
		{"Update", []tea.Msg{key(tea.KeyDown)}, "Pull the latest Orbit"},
		{"Remove", []tea.Msg{key(tea.KeyUp), key(tea.KeyUp)}, "This stops Orbit"},
	}
	for _, f := range flows {
		t.Run(f.name, func(t *testing.T) {
			m := leaveApp(t)
			m, _ = appUpdate(t, m, f.keys...)
			m, cmd := appUpdate(t, m, key(tea.KeyEnter))
			m = appDrive(t, m, cmd)
			if s := stripANSI(m.View().Content); m.state == appStateSplash || !strings.Contains(s, f.want) {
				t.Fatalf("not on the %s first screen (state %v):\n%s", f.name, m.state, s)
			}

			m, cmd = appUpdate(t, m, key(tea.KeyEsc))
			if isQuit(cmd) {
				t.Fatalf("Esc on the %s first screen quit the launcher", f.name)
			}
			// The returning splash is sized by the command that comes
			// back with it, as the program would run it.
			m = appDrive(t, m, cmd)
			if m.state != appStateSplash {
				t.Fatalf("Esc on the %s first screen left state = %v, want the menu", f.name, m.state)
			}
			if s := stripANSI(m.View().Content); !strings.Contains(s, "O R B I T") {
				t.Fatalf("the menu is not showing after Esc:\n%s", s)
			}
		})
	}
}

func TestLeaveFlow_AppCtrlCQuitsFromEachFlow(t *testing.T) {
	flows := []struct {
		name string
		keys []tea.Msg
	}{
		{"Install", nil},
		{"Update", []tea.Msg{key(tea.KeyDown)}},
		{"Remove", []tea.Msg{key(tea.KeyUp), key(tea.KeyUp)}},
	}
	for _, f := range flows {
		t.Run(f.name, func(t *testing.T) {
			m := leaveApp(t)
			m, _ = appUpdate(t, m, f.keys...)
			m, cmd := appUpdate(t, m, key(tea.KeyEnter))
			m = appDrive(t, m, cmd)
			if m.state == appStateSplash {
				t.Fatalf("%s did not open", f.name)
			}
			if _, cmd := appUpdate(t, m, ctrlC()); !isQuit(cmd) {
				t.Fatalf("Ctrl+C in the %s flow did not quit", f.name)
			}
		})
	}
}
