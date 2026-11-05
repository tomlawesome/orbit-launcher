package ui

// #334: when the folder an Orbit was started from no longer exists,
// Update, Repair and Remove say so and offer "Carry on from this folder"
// (same build), or say why they cannot (another build). Install says the
// record is here or that nothing is, and never offers the row. Written
// from the design note (issue #334) before the code. Owner decisions,
// 2026-11-03: the gone test is strict (a folder that exists is elsewhere,
// whatever it holds); Install's no-record advice prints
// `docker compose --project-name <name> down`.
//
// The answers reach the flows as deploy.Mismatch with FolderGone and
// BuildDiffers set, as CompareRunning sets them.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// goneFolder is a path that does not exist on any machine running the
// tests.
const goneFolder = "/srv/orbit-gone-334/production"

const (
	goneCarryOnRow = "Carry on from this folder"
	goneLine       = "That folder no longer exists."
	goneCarryOn    = "Move it back there, or carry on from this folder."
	goneOtherBuild = "That folder no longer exists, and this folder's .env-orbit names a different build from the one running, so it can't carry that Orbit on."
	goneCorrect    = "Correct .env-orbit here, or move the folder back, then try again."

	installGoneRecordHere = "That folder no longer exists, and its record is in this folder."
	installGoneUseOthers  = "Use Update, Repair or Remove here instead."
	installGoneNoRecord   = "That folder no longer exists, and its settings and secrets went with it, so there is nothing here to carry on from."
	installGoneStop       = "Stop that Orbit first: docker compose --project-name orbit down"

	splashGoneRow   = "started from a folder that no longer exists"
	splashGoneStep  = "update, repair or remove can carry on from this folder"
	splashGoneBuild = "this folder's .env-orbit names a different build; correct it first"
)

func goneMismatch(differs bool) deploy.Mismatch {
	m := deploy.Mismatch{Kind: deploy.MismatchFolder, Folder: goneFolder, FolderGone: true}
	if differs {
		m.BuildDiffers, m.Started, m.Recorded = true, "v0.3.0", "v1.2.0"
	}
	return m
}

func goneMsg(dir string) runningResultMsg {
	return runningResultMsg{dir: dir, mismatch: goneMismatch(false)}
}

// goneReader answers an app container started from goneFolder: the same
// build as the record, or another.
func goneReader(differs bool) runningReadFunc {
	return func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
		app := deploy.RunningApp{Found: true, Image: recordedImage, Folder: goneFolder, State: "running"}
		if differs {
			app.Image, app.Version = "ghcr.io/tomlawesome/orbit@sha256:other", "v0.3.0"
		}
		return app, nil
	}
}

type goneCounters struct {
	prepared, asked, dates atomic.Int32
}

// goneApp is the app after the running check answered that the container
// was started from goneFolder (a folder that does not exist). asked
// counts reader calls, dates the install-date lookups, prepared the
// diagnoses Repair would start.
func goneApp(t *testing.T, differs bool) (AppModel, string, *goneCounters) {
	t.Helper()
	if _, err := os.Stat(goneFolder); err == nil {
		t.Fatalf("setup: %s exists", goneFolder)
	}
	dir := installedFolder(t)
	c := &goneCounters{}
	seams := func(m AppModel) AppModel {
		m.flowSeams.prepareRepair = func(context.Context, string, deploy.RepairMode) (*engine.Stream, error) {
			c.prepared.Add(1)
			return sleepingStream(t), nil
		}
		m.flowInstalledAt = func(context.Context, *deploy.Deployment) (_ time.Time) {
			c.dates.Add(1)
			return
		}
		return m
	}
	read := goneReader(differs)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		reading(func(ctx context.Context, d *deploy.Deployment) (deploy.RunningApp, error) {
			c.asked.Add(1)
			return read(ctx, d)
		}), seams))
	if s := screen(m); !strings.Contains(s, splashGoneRow) {
		t.Fatalf("setup: the splash does not show the gone folder:\n%s", s)
	}
	return m, dir, c
}

// assertCarryOn checks the whole carry-on screen.
func assertCarryOn(t *testing.T, m tea.Model) {
	t.Helper()
	s := screen(m)
	flat := oneLine(s)
	for _, want := range []string{"This Orbit was started from " + goneFolder, goneLine, goneCarryOn, refuseFaint} {
		if !strings.Contains(flat, want) {
			t.Errorf("the screen lacks %q:\n%s", want, s)
		}
	}
	if strings.Contains(flat, refuseShared) {
		t.Errorf("the screen still gives advice that cannot be followed:\n%s", s)
	}
	if got := screenLabels(s); !sameLabels(got, []string{goneCarryOnRow, "Cancel"}) || caretLabel(s) != goneCarryOnRow {
		t.Errorf("rows = %q with the caret on %q, want Carry on from this folder and Cancel, caret on the first:\n%s", got, caretLabel(s), s)
	}
	raw := m.View().Content
	if !strings.Contains(raw, style.DegradedText.Render(style.SymbolMark)) {
		t.Errorf("the mark is not amber")
	}
	if row := rawRowOf(t, m, goneLine); !strings.Contains(row, style.MutedText.Render(goneLine)) {
		t.Errorf("the first line is not muted: %q", row)
	}
	if row := rawRowOf(t, m, "nothing has been"); !strings.Contains(row, openingOf(style.Tagline)+"nothing has been") {
		t.Errorf("the promise is not faint: %q", row)
	}
}

// assertGoneDeadEnd checks the another-build dead end.
func assertGoneDeadEnd(t *testing.T, m tea.Model) {
	t.Helper()
	s := screen(m)
	flat := oneLine(s)
	for _, want := range []string{"This Orbit was started from " + goneFolder, goneOtherBuild, goneCorrect, refuseFaint} {
		if !strings.Contains(flat, want) {
			t.Errorf("the screen lacks %q:\n%s", want, s)
		}
	}
	if got := screenLabels(s); !sameLabels(got, []string{"Menu"}) || caretLabel(s) != "Menu" {
		t.Errorf("rows = %q with the caret on %q, want Menu alone:\n%s", got, caretLabel(s), s)
	}
	if strings.Contains(s, goneCarryOnRow) {
		t.Errorf("the carry-on row is offered for another build:\n%s", s)
	}
}

// --- the screens ---------------------------------------------------------

func TestGoneFolder_FlowsOpenOnTheCarryOnScreen(t *testing.T) {
	for _, flow := range otherFolderFlows {
		t.Run(flow.label, func(t *testing.T) {
			m, _, c := goneApp(t, false)

			m = openFlow(t, m, flow.label)

			assertCarryOn(t, m)
			if strings.Contains(screen(m), flow.wrong) {
				t.Errorf("the flow's own screen (%q) is showing", flow.wrong)
			}
			if n := c.prepared.Load(); n != 0 {
				t.Errorf("Repair's diagnosis was started %d times before the row", n)
			}
			if n := c.dates.Load(); n != 0 {
				t.Errorf("the install-date lookup ran %d times before the row", n)
			}
		})
	}
}

func TestGoneFolder_AnotherBuildIsADeadEndWithMenu(t *testing.T) {
	for _, flow := range otherFolderFlows {
		t.Run(flow.label, func(t *testing.T) {
			m, _, c := goneApp(t, true)

			m = openFlow(t, m, flow.label)

			assertGoneDeadEnd(t, m)
			if n := c.prepared.Load(); n != 0 {
				t.Errorf("Repair's diagnosis was started %d times", n)
			}
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			m = drive(t, m, cmd)
			if m.state != appStateSplash {
				t.Errorf("Menu left the app in state %v, want the menu", m.state)
			}
		})
	}
}

// A folder that exists keeps today's refusal, with its advice.
func TestGoneFolder_ExistingFolderKeepsTodaysRefusal(t *testing.T) {
	back := shortTempDir(t) // exists, holds no .env-orbit
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		runningAs(deploy.RunningApp{Found: true, Image: recordedImage, Folder: back, State: "running"}, nil)))

	m = openFlow(t, m, "Update")

	assertRefusal(t, m, back, refuseUpdate, "Pull the latest Orbit")
	if strings.Contains(screen(m), goneCarryOnRow) || strings.Contains(screen(m), "no longer exists") {
		t.Errorf("an existing folder was treated as gone:\n%s", screen(m))
	}
}

func TestGoneFolder_CancelAndEscReturnToTheMenu(t *testing.T) {
	for _, flow := range otherFolderFlows {
		for _, leave := range []struct {
			name string
			k    tea.KeyPressMsg
		}{{"esc", key(tea.KeyEsc)}, {"Cancel", key(tea.KeyEnter)}} {
			t.Run(flow.label+" "+leave.name, func(t *testing.T) {
				m, _, c := goneApp(t, false)
				m = openFlow(t, m, flow.label)
				before := c.asked.Load()
				if leave.name == "Cancel" {
					m = moveCaretTo(t, m, "Cancel")
				}

				m, cmd := updateAs(t, m, leave.k)
				m = drive(t, m, cmd)

				if m.state != appStateSplash {
					t.Fatalf("state = %v, want the menu:\n%s", m.state, screen(m))
				}
				if c.asked.Load() <= before {
					t.Errorf("the check was not run again on the way back")
				}
				if n := c.prepared.Load() + c.dates.Load(); n != 0 {
					t.Errorf("leaving started %d lookups or diagnoses", n)
				}
			})
		}
	}
}

// --- carrying on -----------------------------------------------------------

// snapshot lists dir's entries with sizes and mtimes.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	var out []string
	err := filepath.Walk(dir, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s %d %s", p, fi.Size(), fi.ModTime().UTC().Format("15:04:05.000000000")))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

func TestGoneFolder_CarryOnInUpdateLandsOnConfirmAndLooksUpTheDate(t *testing.T) {
	m, dir, c := goneApp(t, false)
	m = openFlow(t, m, "Update")
	before, asked := snapshot(t, dir), c.asked.Load()

	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Carry on from this folder
	m = drive(t, m, cmd)

	if m.state != appStateUpdate || !strings.Contains(screen(m), "Update Orbit") || strings.Contains(screen(m), "This Orbit was started from") {
		t.Fatalf("not on Update's confirmation:\n%s", screen(m))
	}
	if n := c.dates.Load(); n != 1 {
		t.Errorf("the install-date lookup ran %d times, want 1", n)
	}
	if got := snapshot(t, dir); got != before {
		t.Errorf("the row changed files:\nbefore:\n%s\nafter:\n%s", before, got)
	}
	if n := c.asked.Load(); n != asked {
		t.Errorf("the row made %d Docker reads, want none", n-asked)
	}
}

func TestGoneFolder_CarryOnInRepairStartsTheDiagnosis(t *testing.T) {
	m, dir, c := goneApp(t, false)
	m = openFlow(t, m, "Repair")
	if n := c.prepared.Load(); n != 0 {
		t.Fatalf("setup: %d diagnoses before the row", n)
	}
	before, asked := snapshot(t, dir), c.asked.Load()

	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)

	if n := c.prepared.Load(); n != 1 {
		t.Errorf("the diagnosis was started %d times, want 1", n)
	}
	if m.state != appStateRepair || strings.Contains(screen(m), "This Orbit was started from") {
		t.Errorf("Repair did not carry on:\n%s", screen(m))
	}
	if got := snapshot(t, dir); got != before {
		t.Errorf("the row changed files")
	}
	if n := c.asked.Load(); n != asked {
		t.Errorf("the row made %d Docker reads, want none", n-asked)
	}
}

// openCarriedRemove opens Remove, presses the row and withholds Remove's
// own running answer, delivering everything else.
func openCarriedRemove(t *testing.T, m AppModel, sd *standDownCounter) (AppModel, removeRunningMsg) {
	t.Helper()
	m = openFlow(t, m, "Remove")
	assertCarryOn(t, m)
	m.remove.standDown = sd.fn
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	var held []removeRunningMsg
	for _, msg := range answersOf(cmd) {
		if r, ok := msg.(removeRunningMsg); ok {
			held = append(held, r)
			continue
		}
		m, _ = updateAs(t, m, msg)
	}
	if len(held) != 1 {
		t.Fatalf("the row started %d running checks of Remove's own, want 1", len(held))
	}
	if m.state != appStateRemove || !strings.Contains(screen(m), "Stand down Orbit") {
		t.Fatalf("carry on did not land on Remove's confirmation:\n%s", screen(m))
	}
	return m, held[0]
}

func TestGoneFolder_RemoveEnterProceedsOnAGoneAnswer(t *testing.T) {
	m, _, _ := goneApp(t, false)
	sd := &standDownCounter{}
	m, held := openCarriedRemove(t, m, sd)

	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if n := sd.calls.Load(); n != 0 {
		t.Fatalf("the stand-down ran before Remove's own check answered")
	}
	m, cmd := updateAs(t, m, held)
	m = drive(t, m, cmd)

	if n := sd.calls.Load(); n != 1 {
		t.Errorf("the stand-down ran %d times on a gone answer, want 1:\n%s", n, screen(m))
	}
	if strings.Contains(screen(m), "This Orbit was started from") {
		t.Errorf("a gone answer refused after carry on:\n%s", screen(m))
	}
}

func TestGoneFolder_RemoveEnterRefusesWhenTheFolderIsBack(t *testing.T) {
	back := shortTempDir(t)
	dir := installedFolder(t)
	var calls atomic.Int32
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		reading(func(ctx context.Context, d *deploy.Deployment) (deploy.RunningApp, error) {
			folder := goneFolder
			if calls.Add(1) > 1 {
				folder = back // moved back meanwhile
			}
			return deploy.RunningApp{Found: true, Image: recordedImage, Folder: folder, State: "running"}, nil
		})))
	sd := &standDownCounter{}
	m, held := openCarriedRemove(t, m, sd)

	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m, _ = updateAs(t, m, held)
	m = drive(t, m, cmd)

	assertRefusal(t, m, back, refuseRemove, "Stand down Orbit")
	if n := sd.calls.Load(); n != 0 {
		t.Errorf("the stand-down ran %d times with the folder back", n)
	}
}

// --- late answers ------------------------------------------------------------

func TestGoneFolder_LateAnswerAfterCarryOn(t *testing.T) {
	t.Run("update: gone is ignored", func(t *testing.T) {
		m, dir, _ := silentApp(t)
		m = openFlow(t, m, "Update")
		m, _ = updateAs(t, m, goneMsg(dir))
		assertCarryOn(t, m)
		m, cmd := updateAs(t, m, key(tea.KeyEnter))
		m = drive(t, m, cmd)
		before := screen(m)

		m, _ = updateAs(t, m, goneMsg(dir))

		if screen(m) != before || strings.Contains(screen(m), "This Orbit was started from") {
			t.Errorf("a gone answer after carry on changed the screen:\n%s", screen(m))
		}
	})
	t.Run("update: folder back refuses", func(t *testing.T) {
		m, dir, _ := silentApp(t)
		m = openFlow(t, m, "Update")
		m, _ = updateAs(t, m, goneMsg(dir))
		m, cmd := updateAs(t, m, key(tea.KeyEnter))
		m = drive(t, m, cmd)
		back := shortTempDir(t)

		m, _ = updateAs(t, m, runningResultMsg{dir: dir, mismatch: folderMismatch(back)})

		assertRefusal(t, m, back, refuseUpdate, "Pull the latest Orbit")
	})
	t.Run("repair: gone is ignored", func(t *testing.T) {
		m, dir, c := silentApp(t)
		m = openFlow(t, m, "Repair")
		m, _ = updateAs(t, m, goneMsg(dir))
		assertCarryOn(t, m)
		m, cmd := updateAs(t, m, key(tea.KeyEnter))
		m = drive(t, m, cmd)
		before := screen(m)

		m, _ = updateAs(t, m, goneMsg(dir))

		if screen(m) != before || strings.Contains(screen(m), "This Orbit was started from") {
			t.Errorf("a gone answer after carry on changed the screen:\n%s", screen(m))
		}
		_ = c
	})
	t.Run("remove: gone is ignored, folder back refuses", func(t *testing.T) {
		m, dir, _ := silentApp(t)
		m = openFlow(t, m, "Remove")
		m, _ = updateAs(t, m, goneMsg(dir))
		assertCarryOn(t, m)
		m, cmd := updateAs(t, m, key(tea.KeyEnter))
		m = drive(t, m, cmd)
		before := screen(m)

		m, _ = updateAs(t, m, goneMsg(dir))
		if screen(m) != before {
			t.Errorf("a gone answer after carry on changed the screen:\n%s", screen(m))
		}
		back := shortTempDir(t)
		m, _ = updateAs(t, m, runningResultMsg{dir: dir, mismatch: folderMismatch(back)})
		assertRefusal(t, m, back, refuseRemove, "Stand down Orbit")
	})
}

// A late gone answer, before any row is pressed, turns an open flow into
// the carry-on screen as it turns it into the refusal today.
func TestGoneFolder_LateGoneAnswerOpensTheCarryOnScreen(t *testing.T) {
	m, dir, _ := silentApp(t)
	m = openFlow(t, m, "Update")

	m, _ = updateAs(t, m, goneMsg(dir))

	assertCarryOn(t, m)
}

// Carrying on is forgotten when the flow is left: the next time the
// flow opens it asks again.
func TestGoneFolder_CarryOnIsForgottenWhenTheFlowIsLeft(t *testing.T) {
	m, _, _ := goneApp(t, false)
	m = openFlow(t, m, "Update")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	m, cmd = updateAs(t, m, key(tea.KeyEsc)) // Cancel on the confirm
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Fatalf("setup: state = %v, want the menu", m.state)
	}

	m = openFlow(t, m, "Update")

	assertCarryOn(t, m)
}

// --- Install -------------------------------------------------------------------

func assertInstallGone(t *testing.T, m tea.Model, lines ...string) {
	t.Helper()
	s := screen(m)
	flat := oneLine(s)
	for _, want := range append([]string{clashTitle + goneFolder, refuseFaint}, lines...) {
		if !strings.Contains(flat, want) {
			t.Errorf("the screen lacks %q:\n%s", want, s)
		}
	}
	for _, not := range []string{goneCarryOnRow, "Cancel", "Continue anyway", profileTitle, refuseShared} {
		if strings.Contains(flat, not) {
			t.Errorf("the screen shows %q:\n%s", not, s)
		}
	}
	if got := screenLabels(s); !sameLabels(got, []string{"Menu"}) || caretLabel(s) != "Menu" {
		t.Errorf("rows = %q with the caret on %q, want Menu alone:\n%s", got, caretLabel(s), s)
	}
}

func TestGoneFolder_InstallWithTheRecordHereIsADeadEnd(t *testing.T) {
	m, vol, clash := clashInstall(t, installedFolder(t), startedFrom(goneFolder))

	m, _ = updateAs(t, m, vol, clash)

	if m.state != installStateOtherFolder {
		t.Fatalf("state = %v, want the dead end:\n%s", m.state, screen(m))
	}
	assertInstallGone(t, m, installGoneRecordHere, installGoneUseOthers)
	if strings.Contains(screen(m), "docker compose") {
		t.Errorf("the record-here screen advises a command:\n%s", screen(m))
	}
}

// #338 A-F8: with this folder's record naming another build, Install says
// what Update, Repair and Remove say, in the same words, and advises no
// stop command (the way on is to correct the record).
func TestGoneFolder_InstallWithTheRecordHereOfAnotherBuildSaysWhatUpdateSays(t *testing.T) {
	m, vol, clash := clashInstall(t, installedFolder(t), goneReader(true))

	m, _ = updateAs(t, m, vol, clash)

	if m.state != installStateOtherFolder {
		t.Fatalf("state = %v, want the dead end:\n%s", m.state, screen(m))
	}
	assertInstallGone(t, m, goneOtherBuild, goneCorrect)
	flat := oneLine(screen(m))
	for _, not := range []string{"docker compose", installGoneRecordHere, installGoneUseOthers, installGoneNoRecord} {
		if strings.Contains(flat, not) {
			t.Errorf("the other-build screen shows %q:\n%s", not, screen(m))
		}
	}
}

// #338 A-R4: a .env-orbit here that exists but cannot be read stops the
// install on the unreadable-record screen, before any Docker call. (Today
// the clash check takes the error for "no clash" and the profile screen
// shows.) A directory stands in for the unreadable file, so the test also
// runs as root.
func TestGoneFolder_InstallWithAnUnreadableRecordHereStopsOnTheUnreadableScreen(t *testing.T) {
	dir := shortTempDir(t)
	if err := os.Mkdir(filepath.Join(dir, deploy.EnvFile), 0o700); err != nil {
		t.Fatal(err)
	}
	var asked atomic.Int32
	m, vol, clash := clashInstall(t, dir, func(ctx context.Context, d *deploy.Deployment) (deploy.RunningApp, error) {
		asked.Add(1)
		return goneReader(false)(ctx, d)
	})

	m, _ = updateAs(t, m, vol, clash)

	s := screen(m)
	assertCouldntReadScreen(t, s, dir)
	if strings.Contains(oneLine(s), profileTitle) {
		t.Errorf("the profile screen was reached past an unreadable record:\n%s", s)
	}
	for _, not := range []string{installGoneRecordHere, installGoneNoRecord, "no longer exists", "docker compose"} {
		if strings.Contains(oneLine(s), not) {
			t.Errorf("the screen shows %q:\n%s", not, s)
		}
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("Docker was asked %d times before the unreadable record stopped the install, want 0", n)
	}
}

// Esc and the Menu row leave Install's unreadable screen for the menu
// and start nothing, as on the other-folder dead end (#338).
func TestGoneFolder_InstallUnreadableEscAndMenuReturnToTheMenuWithoutInstalling(t *testing.T) {
	for _, c := range []struct {
		name string
		k    tea.KeyPressMsg
	}{{"Esc", key(tea.KeyEsc)}, {"the Menu row", key(tea.KeyEnter)}} {
		t.Run(c.name, func(t *testing.T) {
			dir := shortTempDir(t)
			if err := os.Mkdir(filepath.Join(dir, deploy.EnvFile), 0o700); err != nil {
				t.Fatal(err)
			}
			m, vol, clash := clashInstall(t, dir, startedFrom(goneFolder))
			m, _ = updateAs(t, m, vol, clash)
			if m.state != installStateUnreadable {
				t.Fatalf("setup: state = %v, want the unreadable screen:\n%s", m.state, screen(m))
			}
			var prepared atomic.Int32
			m.seams.prepareEngine = func(context.Context, string, string) (*engine.Stream, func() error, error) {
				prepared.Add(1)
				return nil, nil, errors.New("must not be called")
			}

			m, cmd := updateAs(t, m, c.k)

			if cmd != nil {
				t.Errorf("leaving started a command")
			}
			if !leftForMenu(m.Outcome()) {
				t.Errorf("Outcome = %+v, want back to the menu", m.Outcome())
			}
			if m.state == installStateRunning {
				t.Error("leaving started the install")
			}
			if n := prepared.Load(); n != 0 {
				t.Errorf("the engine was prepared %d times, want none", n)
			}
		})
	}
}

func TestGoneFolder_InstallWithNoRecordHereIsADeadEndWithTheStopCommand(t *testing.T) {
	m, vol, clash := clashInstall(t, clashDir, startedFrom(goneFolder))

	m, _ = updateAs(t, m, vol, clash)

	if m.state != installStateOtherFolder {
		t.Fatalf("state = %v, want the dead end:\n%s", m.state, screen(m))
	}
	assertInstallGone(t, m, installGoneNoRecord, installGoneStop)
}

// A recorded folder that exists, empty of .env-orbit, is not gone: Install
// keeps today's refusal.
func TestGoneFolder_InstallExistingFolderIsNotGone(t *testing.T) {
	back := shortTempDir(t)
	m, vol, clash := clashInstall(t, clashDir, startedFrom(back))

	m, _ = updateAs(t, m, vol, clash)

	assertClashRefusal(t, m, back)
	if strings.Contains(screen(m), "no longer exists") {
		t.Errorf("an existing folder was treated as gone:\n%s", screen(m))
	}
}

// --- the splash ----------------------------------------------------------------

func TestGoneFolder_SplashRows(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))

	m = answer(t, m, dir, goneMismatch(false))

	s := screen(m)
	inARow(t, s, dir, splashGoneRow, goneFolder, splashGoneStep)
	if strings.Contains(s, otherFolderRow) || strings.Contains(s, otherFolderStep) {
		t.Errorf("today's other-folder rows are showing:\n%s", s)
	}
	if got := screenLabels(s); !sameLabels(got, menuLabels) {
		t.Errorf("menu rows = %q, want %q", got, menuLabels)
	}
	if row := rawRowOf(t, m, splashGoneRow); !strings.Contains(row, style.DegradedText.Render(splashGoneRow)) {
		t.Errorf("the sentence is not amber: %q", row)
	}
	if row := rawRowOf(t, m, goneFolder); !strings.Contains(row, style.MutedText.Render(goneFolder)) {
		t.Errorf("the folder is not muted: %q", row)
	}
	if row := rawRowOf(t, m, splashGoneStep); !strings.Contains(row, style.Tagline.Render(splashGoneStep)) {
		t.Errorf("the step is not faint: %q", row)
	}
}

func TestGoneFolder_SplashAnotherBuildSaysCorrectItFirst(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))

	m = answer(t, m, dir, goneMismatch(true))

	s := screen(m)
	inARow(t, s, dir, splashGoneRow, goneFolder, splashGoneBuild)
	if strings.Contains(s, splashGoneStep) {
		t.Errorf("the carry-on promise is shown for another build:\n%s", s)
	}
}

// The caret follows the normal rules: alive preselects Update, degraded
// Repair; the Exit rest was for dead ends and there are none now.
func TestGoneFolder_SplashCaretRules(t *testing.T) {
	for _, differs := range []bool{false, true} {
		t.Run(fmt.Sprintf("alive, build differs %v", differs), func(t *testing.T) {
			dir := installedFolder(t)
			m := answer(t, started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding)), dir, goneMismatch(differs))
			if got := caretLabel(screen(m)); got != "Update" {
				t.Errorf("the caret is on %q, want Update:\n%s", got, screen(m))
			}
		})
		t.Run(fmt.Sprintf("degraded, folder first, build differs %v", differs), func(t *testing.T) {
			dir := installedFolder(t)
			m := folderApp(t, dir, 80, 26, deploy.HealthResponding)
			m = answer(t, m, dir, goneMismatch(differs))
			m, _ = updateAs(t, m, healthResultMsg{reason: deploy.HealthNotResponding})
			if got := caretLabel(screen(m)); got != "Repair" {
				t.Errorf("the caret is on %q, want Repair:\n%s", got, screen(m))
			}
		})
		t.Run(fmt.Sprintf("degraded, health first, build differs %v", differs), func(t *testing.T) {
			dir := installedFolder(t)
			m := folderApp(t, dir, 80, 26, deploy.HealthResponding)
			m, _ = updateAs(t, m, healthResultMsg{reason: deploy.HealthNotResponding})
			m = answer(t, m, dir, goneMismatch(differs))
			if got := caretLabel(screen(m)); got != "Repair" {
				t.Errorf("the caret is on %q, want Repair:\n%s", got, screen(m))
			}
		})
	}
}

// The caret is the person's once they have moved it.
func TestGoneFolder_SplashDoesNotMoveAPlacedCaret(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))
	m, _ = updateAs(t, m, key(tea.KeyDown)) // Repair

	m = answer(t, m, dir, goneMismatch(false))

	if got := caretLabel(screen(m)); got != "Repair" {
		t.Errorf("the caret is on %q, want it left on Repair", got)
	}
}

// --- the menu grammar ------------------------------------------------------------

func goneScreen(flow string, differs bool) func(t *testing.T) tea.Model {
	return func(t *testing.T) tea.Model {
		m, _, _ := goneApp(t, differs)
		return openFlow(t, m, flow)
	}
}

// The new rows are registered in the grammar tables: Carry on is a
// choosing row, Cancel goes where Esc goes (the menu), Menu leaves.
func TestGoneFolder_MenuGrammar(t *testing.T) {
	for _, flow := range []string{"Update", "Repair", "Remove"} {
		t.Run(flow+" carry on", func(t *testing.T) {
			m := goneScreen(flow, false)(t)
			if got := screenLabels(screen(m)); !sameLabels(got, []string{goneCarryOnRow, "Cancel"}) {
				t.Fatalf("rows = %q", got)
			}
			// Up and Down wrap over the two rows; no other key acts.
			down, cmd := updateAs(t, m, key(tea.KeyDown))
			if cmd != nil || caretLabel(screen(down)) != "Cancel" {
				t.Errorf("Down: caret %q, cmd %v, want Cancel and no command", caretLabel(screen(down)), cmd != nil)
			}
			for _, k := range []tea.KeyPressMsg{runeKey('x'), runeKey('q'), runeKey('1'), key(tea.KeyLeft), key(tea.KeyRight), key(tea.KeyTab)} {
				after, cmd := updateAs(t, m, k)
				if cmd != nil || screen(after) != screen(m) {
					t.Errorf("%q did something on the carry-on screen", k.String())
				}
			}
			if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
				t.Error("Ctrl+C did not quit")
			}
		})
		t.Run(flow+" dead end", func(t *testing.T) {
			m := goneScreen(flow, true)(t)
			if got := screenLabels(screen(m)); !sameLabels(got, []string{"Menu"}) {
				t.Fatalf("rows = %q", got)
			}
			for _, k := range []tea.KeyPressMsg{runeKey('x'), runeKey('q'), key(tea.KeyDown), key(tea.KeyUp)} {
				after, cmd := updateAs(t, m, k)
				if cmd != nil || screen(after) != screen(m) {
					t.Errorf("%q did something on the dead end", k.String())
				}
			}
		})
	}
}

// --- a late other-build answer after carrying on (#334) ---------------------

func TestGoneFolder_LateOtherBuildAnswerAfterCarryOnRefuses(t *testing.T) {
	for _, flow := range []string{"Update", "Repair", "Remove"} {
		t.Run(flow, func(t *testing.T) {
			m, dir, _ := silentApp(t)
			m = openFlow(t, m, flow)
			m, _ = updateAs(t, m, goneMsg(dir))
			assertCarryOn(t, m)
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			m = drive(t, m, cmd)

			m, _ = updateAs(t, m, runningResultMsg{dir: dir, mismatch: goneMismatch(true)})

			assertGoneDeadEnd(t, m)
		})
	}
}

func TestGoneFolder_RemoveEnterRefusesOnAGoneOtherBuildAnswer(t *testing.T) {
	m, _, _ := goneApp(t, false)
	sd := &standDownCounter{}
	m, held := openCarriedRemove(t, m, sd)
	held.gone, held.buildDiffers, held.otherFolder = true, true, goneFolder

	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m, cmd2 := updateAs(t, m, held)
	m = drive(t, m, cmd)
	m = drive(t, m, cmd2)

	if n := sd.calls.Load(); n != 0 {
		t.Errorf("the stand-down ran %d times on a gone, other-build answer", n)
	}
	assertGoneDeadEnd(t, m)
}
