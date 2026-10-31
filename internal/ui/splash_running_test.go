package ui

// #316: the running check. The splash asks Docker which folder started
// the deployment's app container and which build it runs, and says so in
// up to three rows beneath the folder row when that contradicts the
// record. Written from the design note (issue note 46624, sections 2 and
// 4) before the code.
//
// The rows are tested by handing the app the check's answer
// (runningResultMsg) and, for the wiring, by a fake reader whose answer
// the app compares with the record.

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

const (
	otherFolderRow  = "started from another folder"
	otherFolderStep = "start the launcher from that folder instead"
	otherBuildRow   = "started from a different build"
	otherBuildStep  = "correct .env-orbit here, or restart Orbit from this folder, then update"
	otherProduction = "/srv/orbit-deployments/production"
)

// answer hands the app the check's answer for dir, as the splash's
// command would.
func answer(t *testing.T, m AppModel, dir string, mismatch deploy.Mismatch) AppModel {
	t.Helper()
	m, _ = updateAs(t, m, runningResultMsg{dir: dir, mismatch: mismatch})
	return m
}

func folderMismatch(other string) deploy.Mismatch {
	return deploy.Mismatch{Kind: deploy.MismatchFolder, Folder: other}
}

func buildMismatch(started, recorded string) deploy.Mismatch {
	return deploy.Mismatch{Kind: deploy.MismatchBuild, Started: started, Recorded: recorded}
}

// noRows is the screen text that the check's rows would add.
func noRows(t *testing.T, s string) {
	t.Helper()
	for _, text := range []string{otherFolderRow, otherFolderStep, otherBuildRow, otherBuildStep, "started v", "recorded"} {
		if strings.Contains(s, text) {
			t.Errorf("the screen shows %q although the check has nothing to say:\n%s", text, s)
		}
	}
}

// inARow checks that the texts are on consecutive rows, in order, and
// returns the first one's index.
func inARow(t *testing.T, s string, texts ...string) int {
	t.Helper()
	first := lineIndex(s, texts[0])
	for i, text := range texts {
		if first < 0 || lineIndex(s, text) != first+i {
			t.Fatalf("want %q on consecutive rows, in order; they are on rows %d..:\n%s", texts, first, s)
		}
	}
	return first
}

// menuLabels is the main menu, whatever the check says: rows never hide
// (#286).
var menuLabels = []string{"Install", "Update", "Repair", "Remove", "Exit"}

func sameLabels(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// A. The container was started from another folder: three rows, amber,
// muted, faint, and the caret moves to Exit.
func TestSplashRunning_OtherFolderShowsThreeRowsAndPreselectsExit(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))
	if got := caretLabel(screen(m)); got != "Update" {
		t.Fatalf("setup: the caret is on %q before the answer, want Update", got)
	}

	m = answer(t, m, dir, folderMismatch(otherProduction))

	s := screen(m)
	inARow(t, s, dir, otherFolderRow, otherProduction, otherFolderStep)
	if got := caretLabel(s); got != "Exit" {
		t.Errorf("the caret is on %q, want Exit:\n%s", got, s)
	}
	if got := screenLabels(s); !sameLabels(got, menuLabels) {
		t.Errorf("the menu rows are %q, want every row to stay: %q", got, menuLabels)
	}
	if row := rawRowOf(t, m, otherFolderRow); !strings.Contains(row, style.DegradedText.Render(otherFolderRow)) {
		t.Errorf("the sentence is not amber (style.DegradedText): %q", row)
	}
	if row := rawRowOf(t, m, otherProduction); !strings.Contains(row, style.MutedText.Render(otherProduction)) {
		t.Errorf("the other folder is not muted (style.MutedText): %q", row)
	}
	if row := rawRowOf(t, m, otherFolderStep); !strings.Contains(row, style.Tagline.Render(otherFolderStep)) {
		t.Errorf("the next step is not faint (style.Tagline): %q", row)
	}
}

// The caret is the person's once they have moved it.
func TestSplashRunning_OtherFolderDoesNotMoveACaretTheUserPlaced(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))
	m, _ = updateAs(t, m, key(tea.KeyDown)) // Repair
	if got := caretLabel(screen(m)); got != "Repair" {
		t.Fatalf("setup: the caret is on %q, want Repair", got)
	}

	m = answer(t, m, dir, folderMismatch(otherProduction))

	s := screen(m)
	if got := caretLabel(s); got != "Repair" {
		t.Errorf("the caret is on %q after the answer, want it left on Repair:\n%s", got, s)
	}
	if !strings.Contains(s, otherFolderRow) {
		t.Errorf("the rows are missing although the caret stayed:\n%s", s)
	}
}

// A degraded answer never takes the caret from Exit to Repair, in either
// order the two answers arrive in.
func TestSplashRunning_DegradedAfterOtherFolderKeepsExit(t *testing.T) {
	degraded := healthResultMsg{reason: deploy.HealthNotResponding}

	t.Run("folder first", func(t *testing.T) {
		dir := installedFolder(t)
		m := folderApp(t, dir, 80, 26, deploy.HealthResponding)
		m = answer(t, m, dir, folderMismatch(otherProduction))
		m, _ = updateAs(t, m, degraded)

		s := screen(m)
		if !strings.Contains(s, "degraded") {
			t.Fatalf("the degraded word is not shown:\n%s", s)
		}
		if got := caretLabel(s); got != "Exit" {
			t.Errorf("the caret is on %q, want it kept on Exit:\n%s", got, s)
		}
	})
	t.Run("health first", func(t *testing.T) {
		dir := installedFolder(t)
		m := folderApp(t, dir, 80, 26, deploy.HealthResponding)
		m, _ = updateAs(t, m, degraded)
		m = answer(t, m, dir, folderMismatch(otherProduction))

		if got := caretLabel(screen(m)); got != "Exit" {
			t.Errorf("the caret is on %q, want Exit (the person has not moved it):\n%s", got, screen(m))
		}
	})
}

// The other folder is cut from the start like the folder row.
func TestSplashRunning_OtherFolderPathIsCutFromTheStart(t *testing.T) {
	dir := installedFolder(t)
	other := "/srv/" + strings.Repeat("x", 70) + "/deployments/production"
	m := answer(t, folderApp(t, dir, 80, 26, deploy.HealthResponding), dir, folderMismatch(other))

	s := screen(m)
	if strings.Contains(s, other) {
		t.Fatalf("the whole path is on the screen:\n%s", s)
	}
	at := lineIndex(s, otherFolderRow)
	if at < 0 {
		t.Fatalf("the other-folder rows are missing:\n%s", s)
	}
	row := textOf(rowsOf(s)[at+1])
	if !strings.HasPrefix(row, "…") || !strings.HasSuffix(row, "/deployments/production") || !strings.HasSuffix(other, strings.TrimPrefix(row, "…")) {
		t.Errorf("the row under the sentence is %q, want the end of %q starting with …", row, other)
	}
	if got := len([]rune(row)); got != 78 {
		t.Errorf("the row is %d cells, want width-2 = 78", got)
	}
}

// The other folder is untrusted text too.
func TestSplashRunning_OtherFolderControlRunesShowAsEscapes(t *testing.T) {
	dir := installedFolder(t)
	m := answer(t, folderApp(t, dir, 80, 26, deploy.HealthResponding), dir, folderMismatch("/srv/a\x1b[999mZZ"))

	if raw := m.View().Content; strings.Contains(raw, "\x1b[999m") {
		t.Errorf("the other folder's escape sequence reached the terminal: %q", raw)
	}
	if s := screen(m); !strings.Contains(s, `/srv/a\x1b[999mZZ`) {
		t.Errorf("the escape does not show as \\x1b:\n%s", s)
	}
}

// B. The container is this folder's but a different build: three rows,
// and the caret stays on Update (5a).
func TestSplashRunning_OtherBuildShowsThreeRowsAndKeepsUpdate(t *testing.T) {
	dir := installedFolder(t, "ORBIT_CONFIG_APPLIED_VERSION=v1.2.0")
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))

	m = answer(t, m, dir, buildMismatch("v0.3.0", "v1.2.0"))

	s := screen(m)
	inARow(t, s, dir, otherBuildRow, "started v0.3.0 · recorded v1.2.0", otherBuildStep)
	if got := caretLabel(s); got != "Update" {
		t.Errorf("the caret is on %q, want it left on Update:\n%s", got, s)
	}
	if got := screenLabels(s); !sameLabels(got, menuLabels) {
		t.Errorf("the menu rows are %q, want every row to stay: %q", got, menuLabels)
	}
	if row := rawRowOf(t, m, otherBuildRow); !strings.Contains(row, style.DegradedText.Render(otherBuildRow)) {
		t.Errorf("the sentence is not amber (style.DegradedText): %q", row)
	}
	detail := "started v0.3.0 · recorded v1.2.0"
	if row := rawRowOf(t, m, detail); !strings.Contains(row, style.MutedText.Render(detail)) {
		t.Errorf("the detail is not muted (style.MutedText): %q", row)
	}
	if row := rawRowOf(t, m, otherBuildStep); !strings.Contains(row, style.Tagline.Render(otherBuildStep)) {
		t.Errorf("the next step is not faint (style.Tagline): %q", row)
	}
}

// A record with no applied version has nothing to put beside "started".
func TestSplashRunning_OtherBuildWithoutRecordedVersionShowsStartedOnly(t *testing.T) {
	dir := installedFolder(t)
	m := answer(t, folderApp(t, dir, 80, 26, deploy.HealthResponding), dir, buildMismatch("v0.3.0", ""))

	s := screen(m)
	inARow(t, s, otherBuildRow, "started v0.3.0", otherBuildStep)
	if strings.Contains(s, "recorded") {
		t.Errorf("the detail names a recorded version although the record has none:\n%s", s)
	}
}

// The next step is 71 cells: below 73 columns it wraps to two centred
// rows at width-2 and is the only row that gives way; the others cut.
func TestSplashRunning_OtherBuildNextStepWrapsBelow73Columns(t *testing.T) {
	dir := installedFolder(t, "ORBIT_CONFIG_APPLIED_VERSION=v1.2.0")

	t.Run("73 columns keeps one row", func(t *testing.T) {
		s := screen(answer(t, folderApp(t, dir, 73, 26, deploy.HealthResponding), dir, buildMismatch("v0.3.0", "v1.2.0")))
		if !strings.Contains(s, otherBuildStep) {
			t.Errorf("the next step is not whole on one row at 73 columns:\n%s", s)
		}
	})
	t.Run("72 columns wraps to two rows", func(t *testing.T) {
		s := screen(answer(t, folderApp(t, dir, 72, 26, deploy.HealthResponding), dir, buildMismatch("v0.3.0", "v1.2.0")))
		if strings.Contains(s, otherBuildStep) {
			t.Fatalf("the next step is on one row at 72 columns, where it is 71 cells and must wrap to width-2 = 70:\n%s", s)
		}
		first := lineIndex(s, "correct .env-orbit here")
		if first < 0 {
			t.Fatalf("the next step is missing:\n%s", s)
		}
		rows := rowsOf(s)
		a, b := textOf(rows[first]), textOf(rows[first+1])
		if got := a + " " + b; got != otherBuildStep {
			t.Errorf("rows %q and %q read %q, want the sentence in two rows", a, b, got)
		}
		for _, r := range []string{a, b} {
			if len([]rune(r)) > 70 {
				t.Errorf("the row %q is %d cells, want at most width-2 = 70", r, len([]rune(r)))
			}
		}
		if ca, cb := centreCol(rows[first]), centreCol(rows[first+1]); ca-cb > 1 || cb-ca > 1 {
			t.Errorf("the two rows are not centred on one column: %.1f and %.1f", ca, cb)
		}
		// The detail row cuts, it never shrinks or wraps.
		if !strings.Contains(s, "started v0.3.0 · recorded v1.2.0") {
			t.Errorf("the detail row is not whole:\n%s", s)
		}
	})
}

// A wins over B is the comparison's: another folder's build is not
// compared with this record, so one answer carries one kind. The screen
// shows whichever kind it was given, and only that.
func TestSplashRunning_OtherFolderShowsNothingOfTheBuildRows(t *testing.T) {
	dir := installedFolder(t)
	s := screen(answer(t, folderApp(t, dir, 80, 26, deploy.HealthResponding), dir, folderMismatch(otherProduction)))
	for _, text := range []string{otherBuildRow, otherBuildStep, "recorded"} {
		if strings.Contains(s, text) {
			t.Errorf("the other-folder screen shows %q:\n%s", text, s)
		}
	}
}

// Nothing to say is no rows, and no change of caret.
func TestSplashRunning_NoneShowsNoRow(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding))
	before := screen(m)

	m = answer(t, m, dir, deploy.Mismatch{})

	noRows(t, screen(m))
	if after := screen(m); after != before {
		t.Errorf("an answer of nothing changed the screen.\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// A check that could not run says nothing is wrong.
func TestSplashRunning_ErrorShowsNoRow(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		runningAs(deploy.RunningApp{}, errors.New("Cannot connect to the Docker daemon"))))

	s := screen(m)
	noRows(t, s)
	if got := caretLabel(s); got != "Update" {
		t.Errorf("the caret is on %q, want it left on Update:\n%s", got, s)
	}
	if strings.Contains(s, "Cannot connect") {
		t.Errorf("Docker's words reached the screen:\n%s", s)
	}
}

// A container the check finds in no list is silent, because the health
// probe's "not responding" already says it.
func TestSplashRunning_NoContainerShowsNoRow(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{}, nil)))

	s := screen(m)
	noRows(t, s)
	if got := caretLabel(s); got != "Update" {
		t.Errorf("the caret is on %q, want it left on Update:\n%s", got, s)
	}
}

// The reader's answer is compared with the record by deploy.CompareRunning,
// and what it finds is what the screen says.
func TestSplashRunning_TheReadersAnswerIsComparedWithTheRecord(t *testing.T) {
	t.Run("another folder", func(t *testing.T) {
		dir := installedFolder(t)
		var gotImage atomic.Value
		m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
			reading(func(_ context.Context, d *deploy.Deployment) (deploy.RunningApp, error) {
				gotImage.Store(d.Image)
				return deploy.RunningApp{Found: true, Image: recordedImage, Folder: otherProduction, State: "running"}, nil
			})))

		inARow(t, screen(m), otherFolderRow, otherProduction, otherFolderStep)
		if got := gotImage.Load(); got != recordedImage {
			t.Errorf("the reader was given the deployment with image %v, want the record's %q", got, recordedImage)
		}
		if got := caretLabel(screen(m)); got != "Exit" {
			t.Errorf("the caret is on %q, want Exit", got)
		}
	})
	t.Run("another build", func(t *testing.T) {
		dir := installedFolder(t, "ORBIT_CONFIG_APPLIED_VERSION=v1.2.0")
		m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
			runningAs(deploy.RunningApp{Found: true, Image: "ghcr.io/tomlawesome/orbit@sha256:def", Version: "v0.3.0", State: "exited"}, nil)))

		inARow(t, screen(m), otherBuildRow, "started v0.3.0 · recorded v1.2.0", otherBuildStep)
	})
	t.Run("the same folder and build", func(t *testing.T) {
		dir := installedFolder(t, "ORBIT_CONFIG_APPLIED_VERSION=v1.2.0")
		m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
			runningAs(deploy.RunningApp{Found: true, Image: recordedImage, Version: "v1.2.0", Folder: dir, State: "running"}, nil)))

		noRows(t, screen(m))
	})
}

// The answer lands whichever order the reader and the deployment were
// given to the app in.
func TestSplashRunning_ReaderAndStatusMayBeGivenInEitherOrder(t *testing.T) {
	dir := installedFolder(t)
	read := func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
		return deploy.RunningApp{Found: true, Image: recordedImage, Folder: otherProduction}, nil
	}
	probe := func(context.Context, string) deploy.HealthReason { return deploy.HealthResponding }

	for name, build := range map[string]func(AppModel) AppModel{
		"reader then status": func(m AppModel) AppModel { return m.WithRunningReader(read).WithDeploymentStatus(probe) },
		"status then reader": func(m AppModel) AppModel { return m.WithDeploymentStatus(probe).WithRunningReader(read) },
	} {
		t.Run(name, func(t *testing.T) {
			m := NewAppModelNoAnimation().WithoutVolumeCheck().WithServicesReader(nil)
			m.targetDir = dir
			m = started(t, sized(t, build(m), 80, 26))

			if s := screen(m); !strings.Contains(s, otherFolderRow) {
				t.Errorf("the check did not run:\n%s", s)
			}
		})
	}
}

// The check starts with the splash and shows nothing until it answers.
func TestSplashRunning_NothingBeforeTheAnswer(t *testing.T) {
	dir := installedFolder(t)
	asked, release := make(chan struct{}, 4), make(chan struct{})
	m := folderApp(t, dir, 80, 26, deploy.HealthResponding,
		reading(func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
			asked <- struct{}{}
			<-release
			return deploy.RunningApp{Found: true, Image: recordedImage, Folder: otherProduction}, nil
		}))

	// Init's commands run as the program runs them, each on its own, so
	// the one that waits on the reader does not hold the others.
	results := make(chan tea.Msg, 8)
	begin := m.Init()
	go func() {
		msg := begin()
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			results <- msg
			return
		}
		for _, c := range batch {
			if c != nil {
				go func() { results <- c() }()
			}
		}
	}()

	select {
	case <-asked:
	case <-time.After(5 * time.Second):
		t.Fatal("the splash did not ask the reader when it started")
	}
	noRows(t, screen(m))
	close(release)

	deadline := time.After(5 * time.Second)
	for !strings.Contains(screen(m), otherFolderRow) {
		select {
		case msg := <-results:
			m, _ = updateAs(t, m, msg)
		case <-deadline:
			t.Fatalf("the answer never reached the screen:\n%s", screen(m))
		}
	}
}

// A folder with only the pre-provisioned configuration has nothing of
// its own running: it is not checked, and Install stays preselected.
func TestSplashRunning_PreProvisionedFolderIsNotChecked(t *testing.T) {
	dir := shortTempDir(t)
	writeEnv(t, dir, "APP_URL=https://mail.example.com\nORBIT_IMAGE="+recordedImage+"\n") // no compose file
	var asked atomic.Int32
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		reading(func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
			asked.Add(1)
			return deploy.RunningApp{Found: true, Image: recordedImage, Folder: otherProduction}, nil
		})))

	s := screen(m)
	if n := asked.Load(); n != 0 {
		t.Errorf("the reader was asked %d times for a folder with no Compose file", n)
	}
	noRows(t, s)
	if got := caretLabel(s); got != "Install" {
		t.Errorf("the caret is on %q, want Install:\n%s", got, s)
	}
}

// A dormant folder has no record and no project to look under.
func TestSplashRunning_DormantFolderIsNotChecked(t *testing.T) {
	var asked atomic.Int32
	m := started(t, folderApp(t, shortTempDir(t), 80, 26, deploy.HealthResponding,
		reading(func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
			asked.Add(1)
			return deploy.RunningApp{Found: true, Image: recordedImage, Folder: otherProduction}, nil
		})))

	if n := asked.Load(); n != 0 {
		t.Errorf("the reader was asked %d times for a dormant folder", n)
	}
	noRows(t, screen(m))
}

// An unreadable record is not checked either.
func TestSplashRunning_UnreadableRecordIsNotChecked(t *testing.T) {
	var asked atomic.Int32
	m := started(t, folderApp(t, unreadableEnvDir(t), 80, 26, deploy.HealthResponding,
		reading(func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
			asked.Add(1)
			return deploy.RunningApp{Found: true, Image: recordedImage, Folder: otherProduction}, nil
		})))

	if n := asked.Load(); n != 0 {
		t.Errorf("the reader was asked %d times for an unreadable record", n)
	}
	noRows(t, screen(m))
}

// With the check off there is no question, no rows and no change of
// caret.
func TestSplashRunning_WithoutRunningCheckIsNotChecked(t *testing.T) {
	dir := installedFolder(t)
	var asked atomic.Int32
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		reading(func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
			asked.Add(1)
			return deploy.RunningApp{Found: true, Image: recordedImage, Folder: otherProduction}, nil
		}),
		func(m AppModel) AppModel { return m.WithoutRunningCheck() }))

	s := screen(m)
	if n := asked.Load(); n != 0 {
		t.Errorf("the reader was asked %d times with the check off", n)
	}
	noRows(t, s)
	if got := caretLabel(s); got != "Update" {
		t.Errorf("the caret is on %q, want it left on Update:\n%s", got, s)
	}
	if !strings.Contains(s, dir) {
		t.Errorf("the folder row is gone with the check; it is not part of it:\n%s", s)
	}
}

// The foot keeps its one line: the launcher's version and the recorded
// applied version. The `started … · recorded …` row explains it.
func TestSplashRunning_FootIsUnchangedByAMismatch(t *testing.T) {
	dir := installedFolder(t, "ORBIT_CONFIG_APPLIED_VERSION=v1.2.0")
	foot := func(m AppModel) string {
		rows := rowsOf(screen(m))
		return rows[len(rows)-1]
	}
	base := folderApp(t, dir, 80, 26, deploy.HealthResponding, func(m AppModel) AppModel { return m.WithVersion("v0.4.4") })
	want := foot(base)
	if !strings.Contains(want, "orbit-launcher v0.4.4") || !strings.Contains(want, "orbit v1.2.0") {
		t.Fatalf("setup: the foot is %q", want)
	}

	for name, mismatch := range map[string]deploy.Mismatch{
		"another folder": folderMismatch(otherProduction),
		"another build":  buildMismatch("v0.3.0", "v1.2.0"),
	} {
		if got := foot(answer(t, base, dir, mismatch)); got != want {
			t.Errorf("%s: the foot is %q, want %q", name, got, want)
		}
	}
}

// The worst case at 80x26: degraded with the drawer open, the three
// rows and the update notice make a 20-row centre block, which fits the
// 25-row body.
func TestSplashRunning_WorstCaseFitsTwentySixRows(t *testing.T) {
	dir := installedFolder(t, "ORBIT_CONFIG_APPLIED_VERSION=v1.2.0")
	m := folderApp(t, dir, 80, 26, deploy.HealthNotResponding,
		func(m AppModel) AppModel {
			return m.WithServicesReader(func(string) ([]deploy.ServiceState, error) { return nil, nil })
		})
	m = started(t, m)
	m, _ = updateAs(t, m, updateAvailableMsg{version: "v9.9.9"})
	m = answer(t, m, dir, buildMismatch("v0.3.0", "v1.2.0"))
	m, _ = updateAs(t, m, key(tea.KeyUp), key(tea.KeyUp), key(tea.KeyUp)) // Install, then the status row
	m, _ = updateAs(t, m, key(tea.KeyRight))                              // open the drawer

	s := screen(m)
	for _, want := range []string{"mail.example.com", "degraded", "not responding", "services", dir,
		otherBuildRow, "started v0.3.0 · recorded v1.2.0", otherBuildStep, "update available: v9.9.9", "Exit"} {
		if !strings.Contains(s, want) {
			t.Fatalf("the worst case lacks %q:\n%s", want, s)
		}
	}
	rows := rowsOf(s)
	if len(rows) > 26 {
		t.Errorf("the screen is %d rows, want at most 26", len(rows))
	}
	top := lineIndex(s, "O R B I T") - 2 // the mark, a blank row, then the wordmark
	exit := lineIndex(s, "Exit")
	if block := exit - top + 1; block != 20 {
		t.Errorf("the centre block is %d rows (rows %d to %d), want 20:\n%s", block, top, exit, s)
	}
	if exit >= 25 {
		t.Errorf("Exit is on row %d, want it inside the 25-row body above the foot:\n%s", exit, s)
	}
}
