package ui

// #318: Install refuses, before it writes anything, when this machine's
// Orbit was started from another folder: under one Compose project name
// `compose up` here would adopt or recreate that folder's containers
// under this folder's files. The refusal is #316's dead end with the
// title "This machine's Orbit was started from", and it has no
// "Continue anyway". Written from the design note (issue note 46957,
// sections 2 to 5) before the code.
//
// Mechanisms the note leaves open, chosen here as the simplest thing a
// test can observe:
//   - the two pre-flight answers are held back and delivered by hand: the
//     volume answer is staleVolumesMsg, the clash answer is
//     installClashMsg{dir, otherFolder}; both are what Init's commands
//     produce, so the tests run the commands rather than build the
//     messages, except where a message is delivered on purpose out of
//     turn;
//   - "Init returns only the volume command" is read off Init's message:
//     a lone command answers with staleVolumesMsg, a batch with
//     tea.BatchMsg.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

const (
	clashDir      = "/opt/orbit"
	clashTitle    = "This machine's Orbit was started from "
	clashBody     = "Install here would take over that folder's Orbit."
	clashChecking = "Checking for an earlier Orbit on this machine…"
	profileTitle  = "Choose a deployment profile"
)

type runningReadFunc = func(context.Context, *deploy.Deployment) (deploy.RunningApp, error)

// clashAnswering is a reader that always answers app and err.
func clashAnswering(app deploy.RunningApp, err error) runningReadFunc {
	return func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) { return app, err }
}

// startedFrom is a reader that answers an app container started from
// folder.
func startedFrom(folder string) runningReadFunc {
	return func(context.Context, *deploy.Deployment) (deploy.RunningApp, error) {
		return deploy.RunningApp{Found: true, Image: recordedImage, Folder: folder, State: "running"}, nil
	}
}

// answersOf runs a command and the commands it batches, and returns the
// messages they produce.
func answersOf(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range batch {
			out = append(out, answersOf(c)...)
		}
		return out
	}
	if msg == nil {
		return nil
	}
	return []tea.Msg{msg}
}

// clashInstall is an Install flow in dir on its profile screen at 80x24,
// with the volume check answering volumes and the running reader read.
// The two answers Init's commands produce are returned withheld: vol is
// the volume answer, clash the clash answer.
func clashInstall(t *testing.T, dir string, read runningReadFunc, volumes ...deploy.DatabaseVolume) (m InstallModel, vol, clash tea.Msg) {
	t.Helper()
	m = NewInstallModel(dir, "v0.1.0")
	m.checkVolumes = staleVolumeCheck(volumes...)
	m.runningRead = read
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, msg := range answersOf(m.Init()) {
		switch msg.(type) {
		case staleVolumesMsg:
			vol = msg
		case installClashMsg:
			clash = msg
		default:
			t.Fatalf("Init produced a %T, want the volume answer or the clash answer", msg)
		}
	}
	if vol == nil || clash == nil {
		t.Fatalf("Init produced the volume answer %v and the clash answer %v, want both", vol, clash)
	}
	return m, vol, clash
}

// clashed is Install after both answers, the clash answer naming other.
func clashed(t *testing.T, other string) InstallModel {
	t.Helper()
	m, vol, clash := clashInstall(t, clashDir, startedFrom(other))
	m, _ = updateAs(t, m, vol, clash)
	if m.state != installStateOtherFolder {
		t.Fatalf("setup: state = %v, want the refusal:\n%s", m.state, screen(m))
	}
	return m
}

func assertClashRefusal(t *testing.T, m tea.Model, other string) {
	t.Helper()
	s := screen(m)
	flat := oneLine(s)
	for _, want := range []string{clashTitle + other, clashBody, "Start the launcher from that folder instead.", "nothing has been changed, and nothing will be by this screen"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, s)
		}
	}
	for _, not := range []string{profileTitle, "Continue anyway", "Cancel"} {
		if strings.Contains(s, not) {
			t.Errorf("the refusal shows %q:\n%s", not, s)
		}
	}
	if got := screenLabels(s); !sameLabels(got, []string{"Menu"}) || caretLabel(s) != "Menu" {
		t.Errorf("the refusal's rows are %q with the caret on %q, want Menu alone:\n%s", got, caretLabel(s), s)
	}
}

// --- the refusal -------------------------------------------------------

func TestInstallClash_RefusesBeforeTheProfileScreen(t *testing.T) {
	m, vol, clash := clashInstall(t, clashDir, startedFrom(otherProduction))

	m, _ = updateAs(t, m, vol, clash)

	if m.state != installStateOtherFolder {
		t.Fatalf("state = %v, want the refusal", m.state)
	}
	assertClashRefusal(t, m, otherProduction)
}

// The project asked about is the one an install here would use: the
// default for a dormant folder, the record's name for a pre-provisioned
// one.
func TestInstallClash_AsksForTheProjectAnInstallWouldUse(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	provisioned := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(provisioned, deploy.EnvFile), "COMPOSE_PROJECT_NAME=production")

	for _, c := range []struct{ name, dir, project string }{
		{"dormant folder", t.TempDir(), "orbit"},
		{"pre-provisioned folder", provisioned, "production"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var project, target atomic.Value
			read := func(_ context.Context, d *deploy.Deployment) (deploy.RunningApp, error) {
				project.Store(d.Project)
				target.Store(d.TargetDir)
				return deploy.RunningApp{}, nil
			}

			clashInstall(t, c.dir, read)

			if got := project.Load(); got != c.project {
				t.Errorf("the reader was asked for project %v, want %q", got, c.project)
			}
			if got := target.Load(); got != c.dir {
				t.Errorf("the reader was asked for folder %v, want %q", got, c.dir)
			}
		})
	}
}

// --- Enter waits for both answers --------------------------------------

func TestInstallClash_EnterWaitsForTheAnswer(t *testing.T) {
	m, _, clash := clashInstall(t, clashDir, startedFrom(otherProduction))

	m, _ = updateAs(t, m, key(tea.KeyEnter))

	if m.state != installStateProfile {
		t.Fatalf("Enter moved on to state %v before either check had answered", m.state)
	}
	if s := screen(m); !strings.Contains(s, clashChecking) {
		t.Errorf("the waiting screen lacks %q:\n%s", clashChecking, s)
	}

	m, _ = updateAs(t, m, clash)

	assertClashRefusal(t, m, otherProduction)
}

// The line is true whichever check is still out: with the volume check
// answered clean and the clash check not, Enter still waits.
func TestInstallClash_WorkingLineStaysWhileOnlyTheClashIsOut(t *testing.T) {
	m, vol, clash := clashInstall(t, clashDir, clashAnswering(deploy.RunningApp{}, nil))
	m, _ = updateAs(t, m, vol, key(tea.KeyEnter))

	if m.state != installStateProfile || !strings.Contains(screen(m), clashChecking) {
		t.Fatalf("with the clash check still out the flow should wait (state %v):\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, clash)

	if m.state != installStateNotice {
		t.Errorf("state = %v after the clean clash answer, want the notice", m.state)
	}
	if strings.Contains(screen(m), clashChecking) {
		t.Errorf("the working line should be gone once both have answered:\n%s", screen(m))
	}
}

// The other way round: the clash answered clean, the volume check out.
func TestInstallClash_WorkingLineStaysWhileOnlyTheVolumeCheckIsOut(t *testing.T) {
	m, vol, clash := clashInstall(t, clashDir, clashAnswering(deploy.RunningApp{}, nil))
	m, _ = updateAs(t, m, clash, key(tea.KeyEnter))

	if m.state != installStateProfile || !strings.Contains(screen(m), clashChecking) {
		t.Fatalf("with the volume check still out the flow should wait (state %v):\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, vol)

	if m.state != installStateNotice {
		t.Errorf("state = %v after the clean volume answer, want the notice", m.state)
	}
}

func TestInstallClash_NoClashCarriesOnAsToday(t *testing.T) {
	t.Run("both answers clean show the profile screen", func(t *testing.T) {
		m, vol, clash := clashInstall(t, clashDir, clashAnswering(deploy.RunningApp{}, nil))
		m, _ = updateAs(t, m, vol, clash)
		if m.state != installStateProfile || !strings.Contains(screen(m), profileTitle) {
			t.Errorf("state = %v, want the profile screen:\n%s", m.state, screen(m))
		}
	})
	t.Run("a waiting Enter opens the notice", func(t *testing.T) {
		m, vol, clash := clashInstall(t, clashDir, clashAnswering(deploy.RunningApp{}, nil))
		m, _ = updateAs(t, m, key(tea.KeyEnter), vol, clash)
		if m.state != installStateNotice {
			t.Errorf("state = %v, want the notice:\n%s", m.state, screen(m))
		}
	})
}

// --- precedence --------------------------------------------------------

func TestInstallClash_WinsOverTheStaleVolumeScreen(t *testing.T) {
	m, vol, clash := clashInstall(t, clashDir, startedFrom(otherProduction), deploy.DatabaseVolume{Name: "orbit-db-data"})

	m, _ = updateAs(t, m, vol)
	if m.state != installStateStaleVolume || !strings.Contains(screen(m), "This machine already has an Orbit database") {
		t.Fatalf("setup: state = %v, want the stale-volume screen:\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, clash)

	if m.state != installStateOtherFolder {
		t.Fatalf("state = %v, want the refusal to replace the stale-volume screen", m.state)
	}
	assertClashRefusal(t, m, otherProduction)
}

func TestInstallClash_ArrivingFirstIsKeptWhenAVolumeFollows(t *testing.T) {
	m, vol, clash := clashInstall(t, clashDir, startedFrom(otherProduction), deploy.DatabaseVolume{Name: "orbit-db-data"})

	m, _ = updateAs(t, m, clash, vol)

	if m.state != installStateOtherFolder {
		t.Fatalf("state = %v, want the refusal kept", m.state)
	}
	assertClashRefusal(t, m, otherProduction)
	if strings.Contains(screen(m), "orbit-db-data") {
		t.Errorf("the stale volume shows on the refusal:\n%s", screen(m))
	}
}

// --- what is no clash --------------------------------------------------

// assertNoClash checks that Install in dir, with the reader read, ends
// both answers on the profile screen.
func assertNoClash(t *testing.T, dir string, read runningReadFunc) {
	t.Helper()
	m, vol, clash := clashInstall(t, dir, read)
	m, _ = updateAs(t, m, vol, clash)
	if m.state != installStateProfile || !strings.Contains(screen(m), profileTitle) {
		t.Errorf("state = %v, want the profile screen:\n%s", m.state, screen(m))
	}
	if strings.Contains(screen(m), clashTitle) {
		t.Errorf("a refusal shows:\n%s", screen(m))
	}
}

func TestInstallClash_SameFolderIsNoClash(t *testing.T) {
	assertNoClash(t, clashDir, startedFrom(clashDir))
}

// Docker records the path Compose was given, which may be the folder or
// a link to it, whichever the launcher was started through.
func TestInstallClash_SymlinkedFolderIsNoClash(t *testing.T) {
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	t.Run("the label names the link", func(t *testing.T) { assertNoClash(t, target, startedFrom(link)) })
	t.Run("this folder is the link", func(t *testing.T) { assertNoClash(t, link, startedFrom(target)) })
}

func TestInstallClash_NoLabelIsNoClash(t *testing.T) {
	assertNoClash(t, clashDir, startedFrom(""))
}

func TestInstallClash_NoContainerIsNoClash(t *testing.T) {
	assertNoClash(t, clashDir, clashAnswering(deploy.RunningApp{}, nil))
}

func TestInstallClash_ReaderErrorIsSilent(t *testing.T) {
	assertNoClash(t, clashDir, clashAnswering(deploy.RunningApp{}, errors.New("Cannot connect to the Docker daemon")))
}

// --- the switch --------------------------------------------------------

// No reader (the switch is set, or no running check was given) means no
// check, no wait and no refusal: Init is the volume command alone.
func TestInstallClash_WithoutRunningCheckAsksNothing(t *testing.T) {
	m := NewInstallModel(clashDir, "v0.1.0")
	m.checkVolumes = staleVolumeCheck()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	msgs := answersOf(m.Init())
	if len(msgs) != 1 {
		t.Fatalf("Init produced %d messages, want the volume answer alone", len(msgs))
	}
	if _, ok := msgs[0].(staleVolumesMsg); !ok {
		t.Fatalf("Init produced a %T, want the volume answer", msgs[0])
	}
	if _, batched := m.Init()().(tea.BatchMsg); batched {
		t.Error("Init batched commands, want the volume command alone")
	}

	// Enter waits for the volume answer and for nothing else.
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	m, _ = updateAs(t, m, msgs[0])
	if m.state != installStateNotice {
		t.Errorf("state = %v, want the notice once the volume answer is in", m.state)
	}
}

// --- later answers -----------------------------------------------------

// With Enter waiting, the answer cannot arrive late; this is the guard
// for an answer that does.
func TestInstallClash_LateAnswerNeverInterruptsALaterScreen(t *testing.T) {
	m, vol, clash := clashInstall(t, clashDir, clashAnswering(deploy.RunningApp{}, nil))
	m, _ = updateAs(t, m, vol, clash, key(tea.KeyEnter))
	if m.state != installStateNotice {
		t.Fatalf("setup: state = %v, want the notice", m.state)
	}

	m, _ = updateAs(t, m, installClashMsg{dir: clashDir, otherFolder: otherProduction})

	if m.state != installStateNotice {
		t.Errorf("a late clash answer pulled the flow to state %v", m.state)
	}
	if strings.Contains(screen(m), clashTitle) {
		t.Errorf("the notice turned into the refusal:\n%s", screen(m))
	}
}

// --- leaving -----------------------------------------------------------

func TestInstallClash_EscAndMenuReturnToTheMenuWithoutInstalling(t *testing.T) {
	for _, c := range []struct {
		name string
		k    tea.KeyPressMsg
	}{{"Esc", key(tea.KeyEsc)}, {"the Menu row", key(tea.KeyEnter)}} {
		t.Run(c.name, func(t *testing.T) {
			var prepared atomic.Int32
			m := clashed(t, otherProduction)
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

func TestInstallClash_OtherKeysDoNothing(t *testing.T) {
	others := map[string]tea.KeyPressMsg{
		"q": runeKey('q'), "x": runeKey('x'), "y": runeKey('y'), "1": runeKey('1'), "space": runeKey(' '),
		"tab": key(tea.KeyTab), "left": key(tea.KeyLeft), "right": key(tea.KeyRight),
		"up": key(tea.KeyUp), "down": key(tea.KeyDown), "backspace": key(tea.KeyBackspace),
	}
	m := clashed(t, otherProduction)
	want := screen(m)

	for name, k := range others {
		var cmd tea.Cmd
		m, cmd = updateAs(t, m, k)
		if cmd != nil {
			t.Fatalf("%s started a command", name)
		}
		if m.state != installStateOtherFolder || screen(m) != want || m.Outcome().Done {
			t.Fatalf("%s changed the refusal (state %v):\n%s", name, m.state, screen(m))
		}
	}
}

// --- the folder as drawn -----------------------------------------------

func TestInstallClash_LongFolderWrapsToFourRows(t *testing.T) {
	long := "/srv" + strings.Repeat("/orbit-deployments-x", 16) + strings.Repeat("/more", 8)
	m := clashed(t, long)

	s := screen(m)
	rows := rowsOf(s)
	first := lineIndex(s, "This machine's Orbit was started from")
	if first < 0 {
		t.Fatalf("no title:\n%s", s)
	}
	end := first
	for end < len(rows) && screenRow(rows[end]) != "" {
		end++
	}
	if n := end - first; n > folderRows {
		t.Errorf("the title is %d rows, want at most folderRows (%d):\n%s", n, folderRows, s)
	} else if n < 2 {
		t.Errorf("the title is %d row, want the long folder wrapped:\n%s", n, s)
	}
	if got := screenLabels(s); !sameLabels(got, []string{"Menu"}) {
		t.Errorf("the rows are %q, want Menu still on a 24-row screen:\n%s", got, s)
	}
	for _, want := range []string{clashBody, "Start the launcher from that folder instead."} {
		if !strings.Contains(oneLine(s), want) {
			t.Errorf("the refusal lacks %q:\n%s", want, s)
		}
	}
	for i, r := range rows {
		if n := len([]rune(strings.TrimRight(r, " "))); n > 80 {
			t.Errorf("row %d is %d cells on an 80-cell screen: %q", i, n, r)
		}
	}
	if len(rows) > 24 {
		t.Errorf("the screen is %d rows, want at most 24", len(rows))
	}
}

func TestInstallClash_ControlRunesShowAsEscapes(t *testing.T) {
	m := clashed(t, "/srv/a\nb\x1b[999mZZ")

	if raw := m.View().Content; strings.Contains(raw, "\x1b[999m") {
		t.Errorf("the other folder's escape sequence reached the terminal: %q", raw)
	}
	s := screen(m)
	if !strings.Contains(oneLine(s), `This machine's Orbit was started from /srv/a\nb\x1b[999mZZ`) {
		t.Errorf("the title does not show the control runes as escapes:\n%s", s)
	}
	rows := rowsOf(s)
	first := lineIndex(s, "This machine's Orbit was started from")
	if first < 0 || first+1 >= len(rows) || screenRow(rows[first+1]) != "" {
		t.Errorf("the newline in the folder made an extra row under the title:\n%s", s)
	}
}

// --- the app -----------------------------------------------------------

// A dormant folder: the splash shows no rows, and Install, preselected,
// asks on its own and opens on the refusal.
func TestApp_InstallFromADormantMenuOpensOnTheRefusal(t *testing.T) {
	var asked atomic.Int32
	m := started(t, folderApp(t, shortTempDir(t), 80, 26, deploy.HealthResponding,
		reading(func(ctx context.Context, d *deploy.Deployment) (deploy.RunningApp, error) {
			asked.Add(1)
			return startedFrom(otherProduction)(ctx, d)
		})))
	noRows(t, screen(m))
	if got := caretLabel(screen(m)); got != "Install" {
		t.Fatalf("setup: the caret is on %q, want Install:\n%s", got, screen(m))
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("setup: the splash asked %d times for a dormant folder", n)
	}

	m = openFlow(t, m, "Install")

	if m.state != appStateInstall {
		t.Fatalf("state = %v, want the install flow", m.state)
	}
	if n := asked.Load(); n == 0 {
		t.Error("Install did not ask which folder started the container")
	}
	assertClashRefusal(t, m, otherProduction)
}

// An installed folder in state A: the splash says the container was
// started from another folder, and Install opens on the refusal with its
// own body line.
func TestApp_InstallOnAnInstalledFolderInStateAOpensOnTheRefusal(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{
		Found: true, Image: recordedImage, Folder: otherProduction, State: "running",
	}, nil)))
	if s := screen(m); !strings.Contains(s, otherFolderRow) {
		t.Fatalf("setup: the splash does not show the other-folder row:\n%s", s)
	}

	m = openFlow(t, m, "Install")

	if m.state != appStateInstall {
		t.Fatalf("state = %v, want the install flow", m.state)
	}
	assertClashRefusal(t, m, otherProduction)
}

// The splash's answer is not Install's: a runningResultMsg forwarded
// while Install is open changes nothing.
func TestApp_SplashAnswerDoesNotTouchInstall(t *testing.T) {
	dir := installedFolder(t)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{}, nil)))
	m = openFlow(t, m, "Install")
	if s := screen(m); m.state != appStateInstall || !strings.Contains(s, profileTitle) {
		t.Fatalf("setup: Install is not on its profile screen (state %v):\n%s", m.state, s)
	}
	before := screen(m)

	m, _ = updateAs(t, m, runningResultMsg{dir: dir, mismatch: folderMismatch(otherProduction)})

	if got := screen(m); got != before {
		t.Errorf("the splash's answer changed Install's screen.\nbefore:\n%s\nafter:\n%s", before, got)
	}
	if m.state != appStateInstall {
		t.Errorf("state = %v, want the install flow left alone", m.state)
	}
}
