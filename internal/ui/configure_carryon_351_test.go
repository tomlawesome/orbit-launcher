package ui

// #351 item 1: after the other-folder refusal, "Run from this folder" in
// Configure lands on the screen Configure picks for that deployment when
// opened from its own folder. Only the profile table goes on to a run.
// Nothing-here and prepared-only cannot reach the refusal through the
// real flow (checked: a prepared-only folder opens straight on its own
// dead end, the splash shows no other-folder state for it, and the late
// running check only turns the profile, confirm and unavailable screens
// into the refusal), so those states are not tested here.

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// cfg351Gone is the app over dir after the running check answered that
// the container was started from the gone folder.
func cfg351Gone(t *testing.T, dir string) AppModel {
	t.Helper()
	docs260Memory(t, 16*docs260GB, true)
	fakeDockerOnPath(t)
	s := newSink()
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		runningAs(deploy.RunningApp{Found: true, Image: recordedImage, Folder: goneFolder, State: "running"}, nil)))
	m.flowSend = s.Send
	return m
}

// cfg351Carry opens Configure through the refusal and chooses the row.
func cfg351Carry(t *testing.T, m AppModel) AppModel {
	t.Helper()
	m = openFlow(t, m, "Configure")
	assertCarryOn(t, m, "Update")
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Run from this folder
	return drive(t, m, cmd)
}

func TestConfigure351_CarryOnLandsOnTheDeadEndConfigureShowsFromItsOwnFolder(t *testing.T) {
	cases := []struct {
		name, profiles string
		installed      bool
		want           string
	}{
		{"unsupported profile", "ai", true, "This deployment's profile can't be changed from the launcher yet"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out := folderMovedRecorder(t)
			dir := cfg286Folder(t, shortTempDir(t), c.profiles, c.installed)

			// The reference: the same deployment opened from its own folder.
			ref, _ := cfg286App(t, dir)
			ref = cfg286Open(t, ref)
			cfg286DeadEnd(t, screen(ref), c.want)

			m := cfg351Carry(t, cfg351Gone(t, dir))

			cfg286DeadEnd(t, screen(m), c.want)
			if strings.Contains(screen(m), cfg286Table) {
				t.Errorf("the profile table is showing:\n%s", screen(m))
			}
			if got, want := screenLabels(screen(m)), screenLabels(screen(ref)); !sameLabels(got, want) {
				t.Errorf("rows = %q, want the reference's %q", got, want)
			}
			// Its one row leaves; no run starts anywhere on the way.
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			m = drive(t, m, cmd)
			if m.state != appStateSplash {
				t.Errorf("Menu left the app in state %v, want the menu", m.state)
			}
			if got := cfg286Recorded(t, out); len(got) != 0 {
				t.Errorf("an engine run started: %q", got)
			}
		})
	}
}

// The ordinary installed deployment still lands on the profile table, and
// its run asks the engine to recreate.
func TestConfigure351_CarryOnForAnInstalledDeploymentStillLandsOnTheTable(t *testing.T) {
	out := folderMovedRecorder(t)
	dir := cfg286Folder(t, shortTempDir(t), "-", true)
	m := cfg351Carry(t, cfg351Gone(t, dir))
	cfg286OnTable(t, m)
	if got := cfg286Recorded(t, out); len(got) != 0 {
		t.Errorf("a run started before the profile was chosen: %q", got)
	}
}
