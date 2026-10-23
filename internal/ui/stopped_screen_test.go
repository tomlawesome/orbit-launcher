package ui

// #257: the stopped screen says what happened to the folder and offers
// only what works. Written from the ratified design on the issue (the
// note of 2026-10-22 and the owner's answers 14a-18a), #261's rulings and
// the orchestrator's clarifications, without the code.
//
// What a person sees when an Install or an Update stops, keyed off the
// facts the launcher has: the terminal failed event's action, whether a
// rollback event was seen (and completed), a recovery command, whether
// any event arrived at all, its own failure to start the installer, and
// what is in the folder now. Every screen: one sentence about the folder
// and Docker, "stage: <the console's stage word>" when an event arrived,
// the installer's own lines, the folder listing (Install, refused before
// writing), the next step, then the rows, the forward one preselected.
// Never on screen: "next:", a reason, action or phase token.
//
// Names assumed from the design: flowOutcome.WantsRepair and
// appStateRepair; the row labels "Install again", "Update again" and
// "Open Repair".

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// The ratified sentences, verbatim. "<dir>" stands for the folder.
//
// Four are written with the contraction (can't, couldn't) the on-screen
// wording rule asks for (TestWording_NoSpelledOutNegativesOnScreen, owner
// rule 11a); the design spelled them out.
const (
	stopNothingChanged   = "Nothing was changed in <dir>."
	stopUndone           = "The install was undone: <dir> and Docker are as they were."
	stopUndoneButDocker  = "The install was undone in the folder, but something it started in Docker is still there."
	stopNotFullyUndone   = "The install couldn't be fully undone; the lines below say what is left."
	stopCannotGoAhead    = "The install can't go ahead here; nothing was changed."
	stopNotReady         = "Orbit is installed but not ready; its services are still running."
	stopDied             = "The installer stopped without saying how it ended."
	stopSaidNothing      = "The installer stopped without saying what went wrong."
	stopCouldNotStart    = "The launcher couldn't start the installer."
	stopUpdateNotChanged = "The update did not finish. Orbit's services were not changed."
	stopUpdateChanged    = "The update did not finish. Orbit's services were changed and may not all be running."
	stopUpdateCannot     = "The update can't go ahead; nothing was changed."
)

// The next-step lines, verbatim.
const (
	stepMoveThemOut     = "move them out of the folder, then install again"
	stepFixNamedAbove   = "fix what the installer names above, then install again"
	stepAnotherFolder   = "to use another folder, exit and run the launcher from there"
	stepDoWhatLinesSay  = "do what the lines above say in a terminal, then choose Install from the menu"
	stepDoWhatInstaller = "do what the installer says above, then choose Install from the menu"
	stepRepairCanLook   = "Repair can look at it"
	stepInstallingAgain = "installing again is safe: the installer checks the folder first"
	stepRunThenInstall  = "Run this in a terminal, then choose Install again"
	stepRunThenMenu     = "Run this in a terminal, then choose Install from the menu"
)

// The installer's own lines for the not-empty refusal (install.sh's
// wording, which the launcher shows but never reads).
var notEmptyStderr = []string{
	"Orbit installer: The installation directory is not empty and is not a recognizable Orbit deployment or safe pre-provisioned bootstrap.",
	"Orbit installer: Refusing to install here.",
}

// --- synthetic streams ---------------------------------------------------

var (
	hostDone        = ev("host", "host", "completed", "host-tools", "check")
	hostRefused     = ev("host", "host", "failed", "docker-host", "retry")
	hostAborted     = ev("host", "host", "failed", "docker-host", "abort")
	composeRunning  = ev("compose", "compose", "running", "compose-validation", "validate")
	composeFailed   = ev("compose", "compose", "failed", "docker-host", "retry")
	rollbackRunning = ev("rollback", "installer", "running", "rollback", "rollback")
	rollbackDone    = ev("rollback", "installer", "completed", "rollback", "rollback")
	appAborted      = ev("application", "application", "failed", "application-health", "abort")
	appRepair       = ev("application", "application", "failed", "application-health", "repair")
	appUnknown      = ev("application", "application", "failed", "application-health", "teleport")
	imageRetry      = ev("identity", "image", "failed", "image-registry", "retry")
)

// exit1 is the engine's exit after a failure, with its stderr tail.
func exit1(stderr ...string) engine.DoneMsg {
	return engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1"), StderrTail: stderr}
}

// stageAfter is the word the console's stage bar shows after events: the
// "stage:" line repeats it.
func stageAfter(events ...engine.EventMsg) string {
	c := newConsole("Install — Standard", "v9.9.9", func() time.Time { return time.Time{} })
	for _, e := range events {
		c = c.observeEvent(e.Event)
	}
	return "stage: " + c.stageWord()
}

// parsedEvent is an event line as the engine reads it, trailing fields
// (recovery=command) included.
func parsedEvent(t *testing.T, line string) engine.EventMsg {
	t.Helper()
	e, ok := engine.ParseEvent(line)
	if !ok {
		t.Fatalf("not an event line: %q", line)
	}
	return engine.EventMsg{Event: e}
}

// --- folders ---------------------------------------------------------------

// folderWith is a short real folder (so its path fits a screen row)
// holding the named entries: a name ending "/" is a directory.
func folderWith(t *testing.T, entries ...string) string {
	t.Helper()
	dir := shortTempDir(t)
	for _, e := range entries {
		p := filepath.Join(dir, strings.TrimSuffix(e, "/"))
		var err error
		if strings.HasSuffix(e, "/") {
			err = os.Mkdir(p, 0o700)
		} else {
			err = os.WriteFile(p, []byte("x\n"), 0o600)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// --- flows on the stopped screen ------------------------------------------

// stoppedInstallIn runs an Install in dir at w x h, the engine coming
// from prepare, through to the screen it stops on.
func stoppedInstallIn(t *testing.T, dir string, w, h int, prepare prepareEngineFunc) (InstallModel, *sink) {
	t.Helper()
	m := NewInstallModel(dir, "v9.9.9")
	m.seams = engineRunSeams{prepareEngine: prepare, detect: fakeDetect("https://mail.example.com")}
	s := newSink()
	m.send = s.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: w, Height: h})
	m, cmd := startInstallRun(t, m)
	m = drive(t, m, cmd, s)
	if m.run.state != runFailed {
		t.Fatalf("the run did not stop (state %v):\n%s", m.run.state, screen(m))
	}
	return m, s
}

// stoppedInstall is stoppedInstallIn at 80x26 for an engine replaying msgs.
func stoppedInstall(t *testing.T, dir string, msgs ...any) InstallModel {
	t.Helper()
	m, _ := stoppedInstallIn(t, dir, 80, 26, fakeEngine(nil, msgs...))
	return m
}

// stoppedUpdateIn runs an Update of the deployment in dir, the engine
// coming from prepare, through to the screen it stops on.
func stoppedUpdateIn(t *testing.T, dir string, prepare prepareEngineFunc) (UpdateModel, *sink) {
	t.Helper()
	d := &deploy.Deployment{TargetDir: dir, AppURL: "https://mail.example.com", Image: "ghcr.io/tomlawesome/orbit@sha256:abc"}
	m := NewUpdateModel(d, dir, "v9.9.9")
	m.seams = engineRunSeams{prepareEngine: prepare, detect: fakeDetect("https://mail.example.com")}
	s := newSink()
	m.send = s.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Update Orbit is preselected
	m = drive(t, m, cmd, s)
	if m.run.state != runFailed {
		t.Fatalf("the update did not stop (state %v):\n%s", m.run.state, screen(m))
	}
	return m, s
}

func stoppedUpdate(t *testing.T, dir string, msgs ...any) UpdateModel {
	t.Helper()
	m, _ := stoppedUpdateIn(t, dir, fakeEngine(nil, msgs...))
	return m
}

// rerunEngine stops on first the first time it is prepared; every later
// run is one still going (the stream stays open), so a restart shows as
// a run streaming on a fresh console.
func rerunEngine(t *testing.T, calls *int, first prepareEngineFunc) prepareEngineFunc {
	return func(ctx context.Context, dir, action string) (*engine.Stream, func() error, error) {
		*calls++
		if *calls == 1 {
			return first(ctx, dir, action)
		}
		return fakeEngineStreaming(t, ev("host", "host", "running", "host-tools", "check"))(ctx, dir, action)
	}
}

// cannotStart is a prepare that fails before the installer runs: the
// launcher could not fetch or start install.sh.
func cannotStart(err error) prepareEngineFunc {
	return func(context.Context, string, string) (*engine.Stream, func() error, error) {
		return nil, nil, err
	}
}

// --- what every stopped screen holds to -----------------------------------

// tokenRE matches tok as a whole engine token: not inside a longer
// word or a hyphenated token.
func tokenRE(tok string) *regexp.Regexp {
	return regexp.MustCompile(`(^|[^\w-])` + regexp.QuoteMeta(tok) + `($|[^\w-])`)
}

// noEngineWords fails if the screen carries the engine's own vocabulary:
// "next:", a key=value pair, docker-host, or the last event's reason,
// action or phase token (the phase outside the stage line, whose console
// word may contain it, as "Checking host" does).
func noEngineWords(t *testing.T, s string, last engine.Event) {
	t.Helper()
	for _, bad := range []string{"next:", "docker-host", "phase=", "reason=", "action=", "recovery=", "recovery-command:"} {
		if strings.Contains(s, bad) {
			t.Errorf("the stopped screen shows the engine's %q:\n%s", bad, s)
		}
	}
	var kept []string
	for _, line := range strings.Split(s, "\n") {
		if !strings.HasPrefix(screenRow(line), "stage:") {
			kept = append(kept, line)
		}
	}
	rest := strings.Join(kept, "\n")
	for _, tok := range []string{last.Reason, last.Action, last.Phase} {
		if tok != "" && tokenRE(tok).MatchString(rest) {
			t.Errorf("the stopped screen shows the raw token %q:\n%s", tok, s)
		}
	}
}

// stopCase is one way a run can stop, as the person sees it.
type stopCase struct {
	name  string
	build func(t *testing.T) (tea.Model, string) // the flow on the stopped screen, and its folder
	// sentence is the one sentence about the folder ("<dir>" for the
	// folder); steps are the next-step lines, verbatim; absent are
	// texts that must not show.
	sentence string
	steps    []string
	absent   []string
	// stage is the stage line, "" when no event arrived (then no
	// "stage:" line at all).
	stage string
	rows  []string
	// caret is the preselected row; "" where the design names none.
	caret string
	last  engine.Event
}

func installCase(entries []string, msgs []any) func(t *testing.T) (tea.Model, string) {
	return func(t *testing.T) (tea.Model, string) {
		dir := folderWith(t, entries...)
		return stoppedInstall(t, dir, msgs...), dir
	}
}

func updateCase(entries []string, msgs []any) func(t *testing.T) (tea.Model, string) {
	return func(t *testing.T) (tea.Model, string) {
		dir := folderWith(t, entries...)
		return stoppedUpdate(t, dir, msgs...), dir
	}
}

var (
	rowsAgain  = []string{"Install again", "Menu", "Exit"}
	rowsUpdate = []string{"Update again", "Menu", "Exit"}
	rowsRepair = []string{"Open Repair", "Menu", "Exit"}
	rowsGuided = []string{"Open the guided installer", "Menu", "Exit"}
	rowsLeave  = []string{"Menu", "Exit"}
)

func stopCases() []stopCase {
	docker := []string{"Orbit installer: Docker is required."}
	compose := []string{"Orbit installer: Orbit services could not be created or started."}
	web := []string{"Orbit installer: The web service did not become ready in time."}
	return []stopCase{
		{
			name:     "install refused before writing, folder not empty",
			build:    installCase([]string{"notes.txt", "old-site/"}, []any{hostRefused, exit1(notEmptyStderr...)}),
			sentence: stopNothingChanged, steps: []string{stepFixNamedAbove, stepAnotherFolder},
			stage: stageAfter(hostRefused), rows: rowsAgain, caret: "Install again", last: hostRefused.Event,
		},
		{
			name:     "install refused before writing, empty folder",
			build:    installCase(nil, []any{hostRefused, exit1(docker...)}),
			sentence: stopNothingChanged, steps: []string{stepFixNamedAbove, stepAnotherFolder},
			absent: []string{"in the folder", stepMoveThemOut},
			stage:  stageAfter(hostRefused), rows: rowsAgain, caret: "Install again", last: hostRefused.Event,
		},
		{
			name:     "install rolled back",
			build:    installCase([]string{"notes.txt"}, []any{hostDone, composeRunning, rollbackRunning, rollbackDone, composeFailed, exit1(compose...)}),
			sentence: stopUndone, steps: []string{stepFixNamedAbove},
			absent: []string{"in the folder", stepAnotherFolder, stepMoveThemOut},
			stage:  stageAfter(hostDone, composeRunning, composeFailed), // rollback events left out: the line names the step that failed
			rows:   rowsAgain, caret: "Install again", last: composeFailed.Event,
		},
		{
			name:     "install abort after a rollback",
			build:    installCase([]string{"notes.txt"}, []any{hostDone, rollbackRunning, appAborted, exit1("Orbit installer: One entry could not be moved back.")}),
			sentence: stopNotFullyUndone, steps: []string{stepDoWhatLinesSay},
			absent: []string{"in the folder", "Install again", "Open the guided installer"},
			stage:  stageAfter(hostDone, appAborted), rows: rowsLeave, last: appAborted.Event,
		},
		{
			name:     "install abort before writing",
			build:    installCase([]string{"notes.txt"}, []any{hostAborted, exit1("Orbit installer: orbit-install-backups is not a folder.")}),
			sentence: stopCannotGoAhead, steps: []string{stepDoWhatInstaller},
			absent: []string{"in the folder", "Install again", "Open the guided installer"},
			stage:  stageAfter(hostAborted), rows: rowsLeave, last: hostAborted.Event,
		},
		{
			name:     "install services left running",
			build:    installCase(nil, []any{hostDone, appRepair, exit1(web...)}),
			sentence: stopNotReady, steps: []string{stepRepairCanLook},
			absent: []string{"undone", "in the folder"},
			stage:  stageAfter(hostDone, appRepair), rows: rowsRepair, caret: "Open Repair", last: appRepair.Event,
		},
		{
			name:     "install died after events, stream closed early",
			build:    installCase(nil, []any{hostDone, composeRunning}),
			sentence: stopDied, steps: []string{stepInstallingAgain},
			stage: stageAfter(hostDone, composeRunning), rows: rowsAgain, caret: "Install again", last: composeRunning.Event,
		},
		{
			name:     "install died after events, exit 1 with no terminal event",
			build:    installCase(nil, []any{hostDone, composeRunning, exit1("Killed")}),
			sentence: stopDied, steps: []string{stepInstallingAgain},
			stage: stageAfter(hostDone, composeRunning), rows: rowsAgain, caret: "Install again", last: composeRunning.Event,
		},
		{
			name:     "install with no events, exit 1",
			build:    installCase(nil, []any{engine.RawLineMsg{Text: "Pulling ghcr.io/tomlawesome/orbit:latest"}, exit1("Orbit installer: something went wrong.")}),
			sentence: stopSaidNothing, rows: rowsGuided, caret: "Open the guided installer",
			absent: []string{"Install again"},
		},
		{
			name: "install the launcher could not start",
			build: func(t *testing.T) (tea.Model, string) {
				dir := folderWith(t)
				m, _ := stoppedInstallIn(t, dir, 80, 26, cannotStart(errors.New("fetch install.sh: connection refused")))
				return m, dir
			},
			sentence: stopCouldNotStart, rows: rowsAgain, caret: "Install again",
			absent: []string{"Open the guided installer"},
		},
		{
			name:  "install with an action the launcher does not know",
			build: installCase(nil, []any{hostDone, appUnknown, exit1(web...)}),
			stage: stageAfter(hostDone, appUnknown),
			rows:  rowsLeave, last: appUnknown.Event,
			absent: []string{"Install again", "Open the guided installer"},
		},
		{
			name:     "update retry",
			build:    updateCase([]string{"notes.txt"}, []any{hostDone, rollbackRunning, rollbackDone, imageRetry, exit1("Orbit installer: Could not pull the image.")}),
			sentence: stopUpdateNotChanged, stage: stageAfter(hostDone, imageRetry),
			absent: []string{"undone", "in the folder", "Install again"},
			rows:   rowsUpdate, caret: "Update again", last: imageRetry.Event,
		},
		{
			name:     "update repair",
			build:    updateCase([]string{"notes.txt"}, []any{hostDone, rollbackRunning, rollbackDone, appRepair, exit1(web...)}),
			sentence: stopUpdateChanged, stage: stageAfter(hostDone, appRepair),
			absent: []string{"undone", "in the folder"},
			rows:   rowsRepair, caret: "Open Repair", last: appRepair.Event,
		},
		{
			name:     "update abort",
			build:    updateCase([]string{"notes.txt"}, []any{hostAborted, exit1("Orbit installer: Update requires a recognized existing Orbit deployment.")}),
			sentence: stopUpdateCannot, stage: stageAfter(hostAborted),
			absent: []string{"undone", "in the folder", "Update again", "Open the guided installer"},
			rows:   rowsLeave, last: hostAborted.Event,
		},
		{
			name:     "update died after events",
			build:    updateCase([]string{"notes.txt"}, []any{hostDone, composeRunning}),
			sentence: stopDied, stage: stageAfter(hostDone, composeRunning),
			absent: []string{"undone", "in the folder", stepInstallingAgain},
			rows:   rowsUpdate, caret: "Update again", last: composeRunning.Event,
		},
		{
			name:     "update with no events, exit 1",
			build:    updateCase(nil, []any{exit1("Orbit installer: something went wrong.")}),
			sentence: stopSaidNothing, rows: rowsGuided, caret: "Open the guided installer",
			absent: []string{"undone", "Update again"},
		},
		{
			name: "update the launcher could not start",
			build: func(t *testing.T) (tea.Model, string) {
				dir := folderWith(t)
				m, _ := stoppedUpdateIn(t, dir, cannotStart(errors.New("fetch install.sh: connection refused")))
				return m, dir
			},
			sentence: stopCouldNotStart, rows: rowsUpdate, caret: "Update again",
			absent: []string{"undone", "Open the guided installer"},
		},
	}
}

// Each way a run can stop: its sentence, its stage line, its next-step
// lines, its rows exactly and in order, the forward row preselected, and
// none of the engine's vocabulary.
func TestStoppedScreen_EachCaseSaysWhatHappenedAndOffersItsRows(t *testing.T) {
	for _, c := range stopCases() {
		t.Run(c.name, func(t *testing.T) {
			m, dir := c.build(t)
			s := screen(m)
			words := flat(s)
			if c.sentence != "" {
				if want := strings.ReplaceAll(c.sentence, "<dir>", dir); !strings.Contains(words, want) {
					t.Errorf("the sentence %q is missing:\n%s", want, s)
				}
			}
			for _, step := range c.steps {
				if !strings.Contains(words, step) {
					t.Errorf("the next step %q is missing:\n%s", step, s)
				}
			}
			for _, gone := range c.absent {
				if strings.Contains(words, gone) {
					t.Errorf("the screen shows %q, which this case must not:\n%s", gone, s)
				}
			}
			if c.stage != "" && !strings.Contains(words, c.stage) {
				t.Errorf("the stage line %q is missing:\n%s", c.stage, s)
			}
			if c.stage == "" && strings.Contains(s, "stage:") {
				t.Errorf("a stage line though no event arrived:\n%s", s)
			}
			if got := screenLabels(s); !slices.Equal(got, c.rows) {
				t.Errorf("rows = %q, want %q:\n%s", got, c.rows, s)
			}
			if c.caret != "" {
				if got := caretLabel(s); got != c.caret {
					t.Errorf("the caret is on %q, want the forward row %q:\n%s", got, c.caret, s)
				}
			}
			noEngineWords(t, s, c.last)
			if n := screenRows(s); n > 26 {
				t.Errorf("the screen takes %d rows of 26:\n%s", n, s)
			}
		})
	}
}

// Owner answer 2a (issue #257): the stage line names the step that
// failed, never "Rolling back", even when rollback events arrived between.
func TestStoppedScreen_StageNamesTheStepThatFailedNotTheRollback(t *testing.T) {
	m := stoppedInstall(t, folderWith(t, "notes.txt"), hostDone, composeRunning, rollbackRunning, rollbackDone, composeFailed, exit1("Orbit installer: Orbit services could not be created or started."))
	s := screen(m)
	words := flat(s)
	if strings.Contains(words, "stage: Rolling back") {
		t.Errorf("the stage line names the rollback, not the step that failed:\n%s", s)
	}
	if want := stageAfter(composeFailed); !strings.Contains(words, want) {
		t.Errorf("the stage line %q is missing:\n%s", want, s)
	}
}

// Esc on every stopped screen goes back to the main menu.
func TestStoppedScreen_EscGoesToTheMenuFromEveryCase(t *testing.T) {
	for _, c := range stopCases() {
		t.Run(c.name, func(t *testing.T) {
			m, _ := c.build(t)
			after, cmd := m.Update(key(tea.KeyEsc))
			if isQuit(cmd) {
				t.Fatal("Esc quit the launcher")
			}
			o, ok := after.(interface{ Outcome() flowOutcome })
			if !ok {
				t.Fatalf("%T has no outcome", after)
			}
			if !leftForMenu(o.Outcome()) {
				t.Errorf("outcome after Esc = %+v, want back to the main menu", o.Outcome())
			}
		})
	}
}

// --- Install: refused before anything was written --------------------------

// The folder holds what the person left there plus the installer's own
// files: the screen names the folder, says nothing was changed, lists the
// person's entries (not .env-orbit, .orbit-secrets or a real
// orbit-install-backups directory), and says what to do, in the design's
// order.
func TestStoppedScreen_NotEmptyFolderNamesTheFolderAndListsWhatIsInIt(t *testing.T) {
	dir := folderWith(t, "notes.txt", "old-site/", ".env-orbit", ".orbit-secrets/", "orbit-install-backups/")
	m := stoppedInstall(t, dir, hostRefused, exit1(notEmptyStderr...))
	s := screen(m)
	words := flat(s)

	order := []string{
		"Install stopped",
		strings.ReplaceAll(stopNothingChanged, "<dir>", dir),
		stageAfter(hostRefused),
		"Orbit installer: The installation directory is not empty",
		"Refusing to install here.",
		"in the folder: notes.txt, old-site/ — the installer needs it empty " + stepFixNamedAbove,
		stepAnotherFolder,
	}
	at := -1
	for _, want := range order {
		i := strings.Index(words, want)
		if i < 0 {
			t.Fatalf("%q is missing from the screen:\n%s", want, s)
		}
		if i < at {
			t.Errorf("%q is out of order on the screen:\n%s", want, s)
		}
		at = i
	}
	for _, hidden := range []string{".env-orbit", ".orbit-secrets", "orbit-install-backups"} {
		if strings.Contains(words, hidden) {
			t.Errorf("the listing shows %q, which is the installer's own:\n%s", hidden, s)
		}
	}
	if got := screenLabels(s); !slices.Equal(got, rowsAgain) {
		t.Errorf("rows = %q, want %q", got, rowsAgain)
	}
	if got := caretLabel(s); got != "Install again" {
		t.Errorf("the caret is on %q, want Install again", got)
	}
}

// A file (or a link) named orbit-install-backups is not the installer's
// leftovers folder: Orbit refuses it, and the listing names it.
func TestStoppedScreen_AnOrbitInstallBackupsThatIsNotAFolderIsListed(t *testing.T) {
	t.Run("a file", func(t *testing.T) {
		dir := folderWith(t, "a", "orbit-install-backups")
		s := screen(stoppedInstall(t, dir, hostRefused, exit1(notEmptyStderr...)))
		if want := "in the folder: a, orbit-install-backups — the installer needs it empty " + stepFixNamedAbove; !strings.Contains(flat(s), want) {
			t.Errorf("want %q on the screen:\n%s", want, s)
		}
	})
	t.Run("a link", func(t *testing.T) {
		dir := folderWith(t, "a")
		if err := os.Symlink(filepath.Join(dir, "nowhere"), filepath.Join(dir, "orbit-install-backups")); err != nil {
			t.Fatal(err)
		}
		s := screen(stoppedInstall(t, dir, hostRefused, exit1(notEmptyStderr...)))
		if want := "in the folder: a, orbit-install-backups — the installer needs it empty " + stepFixNamedAbove; !strings.Contains(flat(s), want) {
			t.Errorf("want %q on the screen:\n%s", want, s)
		}
	})
}

// A few names, then "and N more": no long list, no scroll (owner, 15a).
func TestStoppedScreen_AFolderWithManyEntriesListsThreeAndCountsTheRest(t *testing.T) {
	dir := folderWith(t, "e/", "c.md", "a.md", "d.md", "b/", ".env-orbit")
	s := screen(stoppedInstall(t, dir, hostRefused, exit1(notEmptyStderr...)))
	if want := "in the folder: a.md, b/, c.md and 2 more — the installer needs it empty " + stepFixNamedAbove; !strings.Contains(flat(s), want) {
		t.Errorf("want %q on the screen:\n%s", want, s)
	}
	if strings.Contains(flat(s), "d.md") || strings.Contains(flat(s), " e/") {
		t.Errorf("the listing names more than three entries:\n%s", s)
	}
}

// A folder holding only the installer's own files has nothing to list:
// no listing line, and the next step is the general one.
func TestStoppedScreen_OnlyTheInstallersOwnFilesListsNothing(t *testing.T) {
	dir := folderWith(t, ".env-orbit", ".orbit-secrets/", "orbit-install-backups/")
	s := screen(stoppedInstall(t, dir, hostRefused, exit1("Orbit installer: Docker is required.")))
	if strings.Contains(s, "in the folder") {
		t.Errorf("a listing line with nothing to list:\n%s", s)
	}
	for _, want := range []string{strings.ReplaceAll(stopNothingChanged, "<dir>", dir), stepFixNamedAbove, stepAnotherFolder} {
		if !strings.Contains(flat(s), want) {
			t.Errorf("%q is missing:\n%s", want, s)
		}
	}
}

// installAgainRestarts presses Enter on the preselected Install again (or
// Update again) and checks the engine is prepared a second time and the
// run is streaming on a fresh console, with none of the stopped screen
// left.
func installAgainRestarts[M interface {
	tea.Model
	Outcome() flowOutcome
}](t *testing.T, m M, s *sink, calls *int, row string, gone ...string) M {
	t.Helper()
	if got := caretLabel(screen(m)); got != row {
		t.Fatalf("the caret is on %q, want %q:\n%s", got, row, screen(m))
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	// isQuit runs the command, and so would prepare the engine a second
	// time before drive did: the command runs once.
	cmd = onceCmd(cmd)
	if isQuit(cmd) {
		t.Fatalf("%s quit the launcher", row)
	}
	m = drive(t, m, cmd, s)
	if *calls != 2 {
		t.Errorf("the engine was prepared %d times, want twice (%s starts it again)", *calls, row)
	}
	if o := m.Outcome(); o.Done {
		t.Errorf("%s ended the flow (%+v); it reruns the engine in-flow", row, o)
	}
	after := screen(m)
	for _, old := range append([]string{"Install stopped", "Update stopped", "failed", "Menu"}, gone...) {
		if strings.Contains(after, old) {
			t.Errorf("after %s the screen still shows %q:\n%s", row, old, after)
		}
	}
	return m
}

// onceCmd runs cmd at most once, handing back its message each time.
func onceCmd(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	var msg tea.Msg
	ran := false
	return func() tea.Msg {
		if !ran {
			msg, ran = cmd(), true
		}
		return msg
	}
}

// Install again reruns the engine in-flow: no profile or notice again.
func TestStoppedScreen_InstallAgainAfterTheNotEmptyRefusalRunsTheEngineAgain(t *testing.T) {
	dir := folderWith(t, "notes.txt", "old-site/")
	calls := 0
	m, s := stoppedInstallIn(t, dir, 80, 26, rerunEngine(t, &calls, fakeEngine(nil, hostRefused, exit1(notEmptyStderr...))))
	m = installAgainRestarts(t, m, s, &calls, "Install again",
		"Nothing was changed", "in the folder", "Refusing to install here", stepMoveThemOut)
	if m.run.state != runStreaming {
		t.Errorf("run state = %v, want runStreaming", m.run.state)
	}
	if strings.Contains(screen(m), "A note before you install") || strings.Contains(screen(m), "Choose a deployment profile") {
		t.Errorf("Install again went back through the profile or the notice:\n%s", screen(m))
	}
}

// The launcher's own failure to start the installer: the error is shown
// under the sentence, and Install again restarts it.
func TestStoppedScreen_LauncherCouldNotStartTheInstallerAndInstallAgainRestarts(t *testing.T) {
	dir := folderWith(t)
	calls := 0
	m, s := stoppedInstallIn(t, dir, 80, 26, rerunEngine(t, &calls, cannotStart(errors.New("fetch install.sh: connection refused"))))
	words := flat(screen(m))
	if !strings.Contains(words, stopCouldNotStart+" fetch install.sh: connection refused") {
		t.Errorf("want the sentence then the error:\n%s", screen(m))
	}
	m = installAgainRestarts(t, m, s, &calls, "Install again", stopCouldNotStart, "connection refused")
	if m.run.state != runStreaming {
		t.Errorf("run state = %v, want runStreaming", m.run.state)
	}
}

// A second Install again that is refused again shows the same screen:
// each is a keypress, nothing loops.
func TestStoppedScreen_ASecondRefusalShowsTheSameScreen(t *testing.T) {
	dir := folderWith(t, "notes.txt")
	calls := 0
	refuse := fakeEngine(nil, hostRefused, exit1(notEmptyStderr...))
	m, s := stoppedInstallIn(t, dir, 80, 26, func(ctx context.Context, d, a string) (*engine.Stream, func() error, error) {
		calls++
		return refuse(ctx, d, a)
	})
	first := flat(screen(m))
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Install again
	m = drive(t, m, cmd, s)
	if calls != 2 {
		t.Fatalf("the engine was prepared %d times, want twice", calls)
	}
	if m.run.state != runFailed {
		t.Fatalf("run state = %v, want the stopped screen again", m.run.state)
	}
	if got := flat(screen(m)); !strings.Contains(got, "in the folder: notes.txt") || !strings.Contains(got, strings.ReplaceAll(stopNothingChanged, "<dir>", dir)) {
		t.Errorf("the second refusal's screen differs from the first:\nfirst: %s\nnow:   %s", first, got)
	}
	if got := screenLabels(screen(m)); !slices.Equal(got, rowsAgain) {
		t.Errorf("rows = %q, want %q", got, rowsAgain)
	}
}

// --- Install: abort keeps every line and the way out ------------------------

func abortLines(n int) []string {
	var lines []string
	for i := 1; i <= n; i++ {
		lines = append(lines, fmt.Sprintf("Orbit installer: cleanup step %02d is left to do", i))
	}
	return lines
}

// After a rollback that could not finish, twelve lines of instructions
// all fit at 80x26, under the sentence and above the next step, with
// Menu and Exit and no rerun.
func TestStoppedScreen_AbortAfterRollbackShowsAllTwelveLinesAt80x26(t *testing.T) {
	lines := abortLines(12)
	m := stoppedInstall(t, folderWith(t), append([]any{hostDone, rollbackRunning, appAborted}, exit1(lines...))...)
	s := screen(m)
	for _, line := range lines {
		if !strings.Contains(s, line) {
			t.Errorf("%q is missing at 80x26:\n%s", line, s)
		}
	}
	if dots := ellipsisRows(s); len(dots) != 0 {
		t.Errorf("a … row though all twelve lines fit:\n%s", s)
	}
	for _, want := range []string{stopNotFullyUndone, stepDoWhatLinesSay} {
		if !strings.Contains(flat(s), want) {
			t.Errorf("%q is missing:\n%s", want, s)
		}
	}
	if got := screenLabels(s); !slices.Equal(got, rowsLeave) {
		t.Errorf("rows = %q, want %q", got, rowsLeave)
	}
	if n := screenRows(s); n > 26 {
		t.Errorf("the screen takes %d rows of 26:\n%s", n, s)
	}
}

// Thirty lines do not fit: the oldest go behind one "…" row, the newest
// and the next step stay, and Menu and Exit are still on screen.
func TestStoppedScreen_AbortWithThirtyLinesKeepsTheWayOutAt80x26(t *testing.T) {
	lines := abortLines(30)
	m := stoppedInstall(t, folderWith(t), append([]any{hostDone, rollbackRunning, appAborted}, exit1(lines...))...)
	s := screen(m)
	if dots := ellipsisRows(s); len(dots) != 1 {
		t.Errorf("want one … row for the dropped lines, got %d:\n%s", len(dots), s)
	}
	if !strings.Contains(s, lines[29]) {
		t.Errorf("the newest line is not shown:\n%s", s)
	}
	if strings.Contains(s, lines[0]) {
		t.Errorf("the oldest line is shown though there is no room:\n%s", s)
	}
	if !strings.Contains(flat(s), stepDoWhatLinesSay) {
		t.Errorf("the next step was pushed off:\n%s", s)
	}
	if got := screenLabels(s); !slices.Equal(got, rowsLeave) {
		t.Errorf("rows = %q, want Menu and Exit on screen", got)
	}
	if n := screenRows(s); n > 26 {
		t.Errorf("the screen takes %d rows of 26:\n%s", n, s)
	}
}

// --- Install: a recovery command (#261's box, #257's sentences) -------------

const rollbackRunningLine = "phase=rollback component=installer state=running reason=rollback action=rollback elapsed=2s"

// retry with a recovery command: the folder was put back, something in
// Docker was not; the box, the line under it, and Copy command, Install
// again, Menu, Exit in that order.
func TestStoppedScreen_RetryWithARecoveryCommand(t *testing.T) {
	m, _ := installApp(t, failedScript(
		[]string{rollbackRunningLine, "recovery-command: " + engineCommand}, retryEvent, retryStderr(engineCommand)), 80, 40)
	s := screen(m)
	words := flat(s)
	for _, want := range []string{stopUndoneButDocker, stepRunThenInstall, engineCommand} {
		if !strings.Contains(words, want) {
			t.Errorf("%q is missing:\n%s", want, s)
		}
	}
	if want := []string{"Copy command", "Install again", "Menu", "Exit"}; !slices.Equal(screenLabels(s), want) {
		t.Errorf("rows = %q, want %q", screenLabels(s), want)
	}
	last := parsedEvent(t, retryEvent).Event
	noEngineWords(t, s, last)
}

// abort with a recovery command (#261 ruling 3, owner 16a): the box, the
// line "Run this in a terminal, then choose Install from the menu", and
// Copy command, Menu, Exit; no rerun.
func TestStoppedScreen_AbortWithARecoveryCommand(t *testing.T) {
	const abortRecovery = "phase=application component=application state=failed reason=application-health action=abort elapsed=3s recovery=command"
	stderr := []string{
		"Orbit installer: This install could not be rolled back: the folder could not be put back.",
		"Orbit installer: Run this in a terminal:",
		engineCommand,
		"Orbit installer: Then remove from " + rollbackFolder + " the entries named above, and install again.",
	}
	m, _ := installApp(t, failedScript(
		[]string{rollbackRunningLine, "recovery-command: " + engineCommand}, abortRecovery, stderr), 80, 40)
	s := screen(m)
	words := flat(s)
	for _, want := range []string{stopNotFullyUndone, stepRunThenMenu, engineCommand} {
		if !strings.Contains(words, want) {
			t.Errorf("%q is missing:\n%s", want, s)
		}
	}
	if strings.Contains(words, stepRunThenInstall) {
		t.Errorf("an abort tells the person to choose Install again:\n%s", s)
	}
	if want := []string{"Copy command", "Menu", "Exit"}; !slices.Equal(screenLabels(s), want) {
		t.Errorf("rows = %q, want %q", screenLabels(s), want)
	}
	noEngineWords(t, s, parsedEvent(t, abortRecovery).Event)
}

// --- Open Repair -----------------------------------------------------------

// Open Repair ends the flow asking for Repair.
func TestStoppedScreen_OpenRepairAsksForRepair(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		m := stoppedInstall(t, folderWith(t), hostDone, appRepair, exit1("Orbit installer: The web service did not become ready in time."))
		m, _ = updateAs(t, m, key(tea.KeyEnter)) // Open Repair, preselected
		if o := m.Outcome(); !o.Done || !o.WantsRepair || o.Succeeded || o.WantsMenu {
			t.Errorf("outcome after Open Repair = %+v, want Done and WantsRepair only", o)
		}
	})
	t.Run("update", func(t *testing.T) {
		m := stoppedUpdate(t, folderWith(t), hostDone, appRepair, exit1("Orbit installer: The web service did not become ready in time."))
		m, _ = updateAs(t, m, key(tea.KeyEnter)) // Open Repair, preselected
		if o := m.Outcome(); !o.Done || !o.WantsRepair || o.Succeeded || o.WantsMenu {
			t.Errorf("outcome after Open Repair = %+v, want Done and WantsRepair only", o)
		}
	})
}

// Through the app: Open Repair lands in Repair, on the same folder.
func TestStoppedScreen_OpenRepairLandsTheAppInRepairOnTheSameFolder(t *testing.T) {
	m, queue := installApp(t, failedScript(nil,
		"phase=application component=application state=failed reason=application-health action=repair elapsed=3s",
		[]string{"Orbit installer: The web service did not become ready in time."}), 80, 26)
	if s := screen(m); !strings.Contains(flat(s), stopNotReady) || caretLabel(s) != "Open Repair" {
		t.Fatalf("not on the services-left-running screen with Open Repair preselected:\n%s", s)
	}
	var repairDir string
	m.flowSeams.prepareRepair = func(_ context.Context, dir string, _ deploy.RepairMode) (*engine.Stream, error) {
		repairDir = dir
		return finishedStream(
			engine.RawLineMsg{Text: "diagnosis result=healthy checked=13 skipped=0"},
			engine.DoneMsg{ExitCode: 0},
		), nil
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if isQuit(cmd) {
		t.Fatal("Open Repair quit the launcher")
	}
	m = drive(t, m, cmd, queue)
	if m.state != appStateRepair {
		t.Fatalf("the app is in state %v, want Repair:\n%s", m.state, screen(m))
	}
	if repairDir != m.targetDir {
		t.Errorf("Repair ran on %q, want the install's folder %q", repairDir, m.targetDir)
	}
	if strings.Contains(screen(m), "Install stopped") {
		t.Errorf("the stopped screen is still showing:\n%s", screen(m))
	}
}

// --- Update ----------------------------------------------------------------

// Update again reruns the update in-flow.
func TestStoppedScreen_UpdateAgainRunsTheEngineAgain(t *testing.T) {
	calls := 0
	m, s := stoppedUpdateIn(t, folderWith(t), rerunEngine(t, &calls, fakeEngine(nil, hostDone, imageRetry, exit1("Orbit installer: Could not pull the image."))))
	m = installAgainRestarts(t, m, s, &calls, "Update again", stopUpdateNotChanged, "Could not pull")
	if m.run.state != runStreaming {
		t.Errorf("run state = %v, want runStreaming", m.run.state)
	}
}

// An update is never rolled back: no stopped Update screen says
// "undone", whatever events arrived, and none lists the folder.
func TestStoppedScreen_UpdateNeverSaysUndoneOrListsTheFolder(t *testing.T) {
	for _, c := range stopCases() {
		if !strings.HasPrefix(c.name, "update") {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			m, _ := c.build(t)
			s := screen(m)
			if strings.Contains(s, "undone") {
				t.Errorf("an Update stopped screen says undone:\n%s", s)
			}
			if strings.Contains(s, "in the folder") || strings.Contains(s, "notes.txt") {
				t.Errorf("an Update stopped screen lists the folder:\n%s", s)
			}
			if !strings.Contains(s, "Update stopped") {
				t.Errorf("not titled Update stopped:\n%s", s)
			}
		})
	}
}

// A retry whose event says recovery=command but whose recovery-command
// line never arrived is still a rollback that left something in Docker:
// the screen must not say the folder and Docker are as they were.
func TestStoppedScreen_RetryWithRecoveryButNoCommandLineIsNotUndone(t *testing.T) {
	m, _ := installApp(t, failedScript(
		[]string{rollbackRunningLine}, retryEvent, retryStderr(engineCommand)), 80, 40)
	s := screen(m)
	words := flat(s)
	if !strings.Contains(words, stopUndoneButDocker) {
		t.Errorf("%q is missing:\n%s", stopUndoneButDocker, s)
	}
	if strings.Contains(words, "are as they were") {
		t.Errorf("the screen says the install was fully undone:\n%s", s)
	}
	if want := []string{"Install again", "Menu", "Exit"}; !slices.Equal(screenLabels(s), want) {
		t.Errorf("rows = %q, want %q", screenLabels(s), want)
	}
}
