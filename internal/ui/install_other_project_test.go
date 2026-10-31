package ui

// #321: Install asks, before it writes anything, whether an Orbit app
// container exists under another Compose project, and when there is
// exactly one it opens on an advisory in the stale-volume screen's shape:
// "There is already an Orbit on this machine", naming where that Orbit
// was started from, with "Continue anyway" and "Cancel". It is an
// advisory and not #318's dead end, because the act it predicts is the
// installer's own refusal (Compose's container-name conflict, rolled
// back), not a takeover. Written from the design note (issue note 47112,
// sections 2, 3 and 5) before the code.
//
// Mechanisms the note leaves open, chosen here as the simplest thing a
// test can observe:
//   - the three pre-flight answers are held back and delivered by hand;
//     the new one is installOtherProjectMsg{dir, folder, project}, what
//     checkOtherProjectCmd produces (an empty project is no finding),
//     so the tests run Init's commands rather than build the messages,
//     except where a message is delivered on purpose out of turn;
//   - the reader is InstallModel.otherRead, called with the project an
//     install here would use.

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
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

const (
	opTitle         = "There is already an Orbit on this machine"
	opBodyFirst     = "That Orbit holds the container names every Orbit uses,"
	opBodySecond    = "so the installer will stop when it starts services here."
	opAdviceFolder  = "Start the launcher from that folder instead."
	opAdviceProject = "Use that Orbit, or remove it, before installing here."
	opPromise       = "nothing has been changed, and nothing will be by this screen"
	opFolderLead    = "started from "
	opProjectLead   = "under the Compose project "
	opProject       = "production"
)

// opRows are the advisory's two rows, in order.
var opRows = []string{"Continue anyway", "Cancel"}

type opReadFunc = func(context.Context, string) (deploy.OtherProjectApp, error)

// opFinding is a reader that always finds an Orbit under project, started
// from folder.
func opFinding(project, folder string) opReadFunc {
	return func(context.Context, string) (deploy.OtherProjectApp, error) {
		return deploy.OtherProjectApp{Found: true, Project: project, Folder: folder, State: "running"}, nil
	}
}

// opAnswering is a reader that always answers app and err.
func opAnswering(app deploy.OtherProjectApp, err error) opReadFunc {
	return func(context.Context, string) (deploy.OtherProjectApp, error) { return app, err }
}

// opInstallWith is an Install flow in dir on its profile screen at 80x24,
// with the volume check answering volumes, running reading the same
// project's container and other the other projects'. The three answers
// Init's commands produce are returned withheld.
func opInstallWith(t *testing.T, dir string, running runningReadFunc, other opReadFunc, volumes ...deploy.DatabaseVolume) (m InstallModel, vol, clash, otherAnswer tea.Msg) {
	t.Helper()
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	m = NewInstallModel(dir, "v0.1.0")
	m.checkVolumes = staleVolumeCheck(volumes...)
	m.runningRead = running
	m.otherRead = other
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})
	for _, msg := range answersOf(m.Init()) {
		switch msg.(type) {
		case staleVolumesMsg:
			vol = msg
		case installClashMsg:
			clash = msg
		case installOtherProjectMsg:
			otherAnswer = msg
		default:
			t.Fatalf("Init produced a %T, want the volume, clash or other-project answer", msg)
		}
	}
	if vol == nil || clash == nil || otherAnswer == nil {
		t.Fatalf("Init produced the volume answer %v, the clash answer %v and the other-project answer %v, want all three", vol, clash, otherAnswer)
	}
	return m, vol, clash, otherAnswer
}

// opInstall is opInstallWith where the same project's container is not
// there.
func opInstall(t *testing.T, dir string, other opReadFunc, volumes ...deploy.DatabaseVolume) (m InstallModel, vol, clash, otherAnswer tea.Msg) {
	t.Helper()
	return opInstallWith(t, dir, clashAnswering(deploy.RunningApp{}, nil), other, volumes...)
}

// advisory is Install after all three answers, the last finding an Orbit
// under project started from folder.
func advisory(t *testing.T, folder, project string) InstallModel {
	t.Helper()
	m, vol, clash, other := opInstall(t, clashDir, opFinding(project, folder))
	m, _ = updateAs(t, m, vol, clash, other)
	if m.state != installStateOtherProject {
		t.Fatalf("setup: state = %v, want the advisory:\n%s", m.state, screen(m))
	}
	return m
}

// assertAdvisory checks the whole advisory: the title, the fact row, the
// reason in two rows, the advice, the promise and the two rows with the
// caret on Continue anyway, in that order down the screen; the amber
// mark, the muted advice and the faint promise; and none of the profile
// screen or the dead end.
func assertAdvisory(t *testing.T, m tea.Model, fact, advice string, notShown ...string) {
	t.Helper()
	s := screen(m)
	flat := oneLine(s)
	for _, want := range []string{opTitle, fact, opBodyFirst, opBodySecond, advice, opPromise} {
		if !strings.Contains(flat, want) {
			t.Errorf("the advisory lacks %q:\n%s", want, s)
		}
	}
	for _, not := range append([]string{profileTitle, "Menu", clashTitle}, notShown...) {
		if strings.Contains(s, not) {
			t.Errorf("the advisory shows %q:\n%s", not, s)
		}
	}
	if got := screenLabels(s); !sameLabels(got, opRows) || caretLabel(s) != "Continue anyway" || strings.Count(s, "▸") != 1 {
		t.Errorf("the advisory's rows are %q with the caret on %q, want %q with the caret on Continue anyway:\n%s", got, caretLabel(s), opRows, s)
	}

	// The order down the screen, and the reason in two rows of its own.
	order := []string{opTitle, fact, opBodyFirst, opBodySecond, advice, opPromise, "Continue anyway", "Cancel"}
	last := -1
	for _, want := range order {
		i := lineIndex(s, want)
		if i < 0 {
			continue // reported above
		}
		if i <= last {
			t.Errorf("%q is on row %d, not below the row before it (%d):\n%s", want, i, last, s)
		}
		last = i
	}
	if a, b := lineIndex(s, opBodyFirst), lineIndex(s, opBodySecond); a >= 0 && b >= 0 && b != a+1 {
		t.Errorf("the reason is on rows %d and %d, want two consecutive rows:\n%s", a, b, s)
	}

	raw := m.View().Content
	if amber := style.DegradedText.Render(style.SymbolMark); !strings.Contains(raw, amber) {
		t.Errorf("the mark is not amber (style.DegradedText)")
	}
	if row := rawRowOf(t, m, advice); !strings.Contains(row, style.MutedText.Render(advice)) {
		t.Errorf("the advice is not muted (style.MutedText): %q", row)
	}
	lead := opProjectLead
	if strings.HasPrefix(fact, opFolderLead) {
		lead = opFolderLead
	}
	if row := rawRowOf(t, m, lead); !strings.Contains(row, openingOf(style.Ink)+lead) {
		t.Errorf("the fact row is not plain ink (style.Ink): %q", row)
	}
	tag := openingOf(style.Tagline)
	if row := rawRowOf(t, m, "nothing has been"); !strings.Contains(row, tag+"nothing has been") {
		t.Errorf("the promise is not in the faint style (style.Tagline): %q", row)
	}
}

// --- the advisory ------------------------------------------------------

func TestInstallOtherProject_NamesTheFolderAndOffersContinueAnyway(t *testing.T) {
	m, vol, clash, other := opInstall(t, clashDir, opFinding(opProject, otherProduction))

	m, _ = updateAs(t, m, vol, clash, other)

	if m.state != installStateOtherProject {
		t.Fatalf("state = %v, want the advisory", m.state)
	}
	assertAdvisory(t, m, opFolderLead+otherProduction, opAdviceFolder, opProjectLead)
}

// With no folder label there is nothing to point at: the project is named
// and the advice changes with it.
func TestInstallOtherProject_NamesTheProjectWhenThereIsNoFolderLabel(t *testing.T) {
	m, vol, clash, other := opInstall(t, clashDir, opFinding(opProject, ""))

	m, _ = updateAs(t, m, vol, clash, other)

	if m.state != installStateOtherProject {
		t.Fatalf("state = %v, want the advisory", m.state)
	}
	assertAdvisory(t, m, opProjectLead+opProject, opAdviceProject, opFolderLead, opAdviceFolder)
}

// A container started from this very folder under another project name
// (a record edited or replaced after the stack started) has no other
// folder to name: "start the launcher from that folder" would point here.
func TestInstallOtherProject_ThisFolderUnderAnotherProjectNamesTheProject(t *testing.T) {
	t.Run("the same spelling", func(t *testing.T) {
		m, vol, clash, other := opInstall(t, clashDir, opFinding(opProject, clashDir))

		m, _ = updateAs(t, m, vol, clash, other)

		if m.state != installStateOtherProject {
			t.Fatalf("state = %v, want the advisory", m.state)
		}
		assertAdvisory(t, m, opProjectLead+opProject, opAdviceProject, opFolderLead, opAdviceFolder)
	})
	t.Run("a link to this folder", func(t *testing.T) {
		target, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		for name, c := range map[string]struct{ dir, label string }{
			"the label names the link": {target, link},
			"this folder is the link":  {link, target},
		} {
			t.Run(name, func(t *testing.T) {
				m, vol, clash, other := opInstall(t, c.dir, opFinding(opProject, c.label))

				m, _ = updateAs(t, m, vol, clash, other)

				if m.state != installStateOtherProject {
					t.Fatalf("state = %v, want the advisory", m.state)
				}
				assertAdvisory(t, m, opProjectLead+opProject, opAdviceProject, opFolderLead, opAdviceFolder)
			})
		}
	})
}

// The project asked about is the one an install here would use: the
// default for a dormant folder, the record's name for a pre-provisioned
// one, the shell's when the record has none.
func TestInstallOtherProject_AsksWithTheProjectAnInstallWouldUse(t *testing.T) {
	provisioned := t.TempDir()
	testutil.WriteEnvOrbit(t, filepath.Join(provisioned, deploy.EnvFile), "COMPOSE_PROJECT_NAME=production")

	for _, c := range []struct{ name, dir, shell, project string }{
		{"dormant folder", t.TempDir(), "", "orbit"},
		{"pre-provisioned folder", provisioned, "", "production"},
		{"the shell names a project", t.TempDir(), "mail", "mail"},
	} {
		t.Run(c.name, func(t *testing.T) {
			var asked atomic.Value
			read := func(_ context.Context, project string) (deploy.OtherProjectApp, error) {
				asked.Store(project)
				return deploy.OtherProjectApp{}, nil
			}

			opInstallWithShell(t, c.dir, c.shell, read)

			if got := asked.Load(); got != c.project {
				t.Errorf("the reader was asked for project %v, want %q", got, c.project)
			}
		})
	}
}

// opInstallWithShell is opInstall with the shell's COMPOSE_PROJECT_NAME
// set after the fixture clears it.
func opInstallWithShell(t *testing.T, dir, shell string, read opReadFunc) {
	t.Helper()
	m := NewInstallModel(dir, "v0.1.0")
	m.checkVolumes = staleVolumeCheck()
	m.runningRead = clashAnswering(deploy.RunningApp{}, nil)
	m.otherRead = read
	t.Setenv("COMPOSE_PROJECT_NAME", shell)
	answersOf(m.Init())
}

// --- Enter waits for all three answers ---------------------------------

func TestInstallOtherProject_EnterWaitsForTheAnswer(t *testing.T) {
	m, vol, clash, other := opInstall(t, clashDir, opFinding(opProject, otherProduction))

	m, _ = updateAs(t, m, vol, clash, key(tea.KeyEnter))

	if m.state != installStateProfile {
		t.Fatalf("Enter moved on to state %v before the other-project check had answered", m.state)
	}
	if s := screen(m); !strings.Contains(s, clashChecking) {
		t.Errorf("the waiting screen lacks %q:\n%s", clashChecking, s)
	}

	m, _ = updateAs(t, m, other)

	if m.state != installStateOtherProject {
		t.Fatalf("state = %v, want the advisory once the finding arrives", m.state)
	}
	assertAdvisory(t, m, opFolderLead+otherProduction, opAdviceFolder)
}

// The line is true whichever check is still out: with the other two
// answered clean, Enter still waits for this one.
func TestInstallOtherProject_WorkingLineStaysWhileOnlyTheOtherCheckIsOut(t *testing.T) {
	m, vol, clash, other := opInstall(t, clashDir, opAnswering(deploy.OtherProjectApp{}, nil))
	m, _ = updateAs(t, m, vol, clash, key(tea.KeyEnter))

	if m.state != installStateProfile || !strings.Contains(screen(m), clashChecking) {
		t.Fatalf("with the other-project check still out the flow should wait (state %v):\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, other)

	if m.state != installStateNotice {
		t.Errorf("state = %v after the clean answer, want the notice", m.state)
	}
	if strings.Contains(screen(m), clashChecking) {
		t.Errorf("the working line should be gone once all three have answered:\n%s", screen(m))
	}
}

func TestInstallOtherProject_NoFindingCarriesOnAsToday(t *testing.T) {
	t.Run("all three clean show the profile screen", func(t *testing.T) {
		m, vol, clash, other := opInstall(t, clashDir, opAnswering(deploy.OtherProjectApp{}, nil))
		m, _ = updateAs(t, m, vol, clash, other)
		if m.state != installStateProfile || !strings.Contains(screen(m), profileTitle) {
			t.Errorf("state = %v, want the profile screen:\n%s", m.state, screen(m))
		}
		if strings.Contains(screen(m), opTitle) {
			t.Errorf("the advisory shows:\n%s", screen(m))
		}
	})
	t.Run("a waiting Enter opens the notice", func(t *testing.T) {
		m, vol, clash, other := opInstall(t, clashDir, opAnswering(deploy.OtherProjectApp{}, nil))
		m, _ = updateAs(t, m, key(tea.KeyEnter), vol, clash, other)
		if m.state != installStateNotice {
			t.Errorf("state = %v, want the notice:\n%s", m.state, screen(m))
		}
	})
}

// --- Continue anyway and leaving ---------------------------------------

func TestInstallOtherProject_ContinueAnywayReachesTheProfileScreen(t *testing.T) {
	m := advisory(t, otherProduction, opProject)

	m, cmd := updateAs(t, m, key(tea.KeyEnter))

	if cmd != nil {
		t.Errorf("Continue anyway started a command")
	}
	if m.state != installStateProfile || !strings.Contains(screen(m), profileTitle) {
		t.Fatalf("state = %v, want the profile screen:\n%s", m.state, screen(m))
	}
	if strings.Contains(screen(m), opTitle) {
		t.Errorf("the advisory is still showing:\n%s", screen(m))
	}
	if m.Outcome().Done {
		t.Errorf("Outcome = %+v, want the flow still open", m.Outcome())
	}

	// Every check has answered, so the person's next choice goes through.
	m, _ = updateAs(t, m, key(tea.KeyEnter))

	if m.state != installStateNotice {
		t.Errorf("state = %v after choosing a profile, want the notice", m.state)
	}
}

// "Continue anyway" ends this check's say, not the others': Enter on the
// profile screen after it still waits for any check still out, as it does
// after the stale-volume screen (#318).
func TestInstallOtherProject_ContinueAnywayStillWaitsForAnotherCheck(t *testing.T) {
	m, vol, clash, other := opInstall(t, clashDir, opFinding(opProject, otherProduction))
	m, _ = updateAs(t, m, vol, other)
	if m.state != installStateOtherProject {
		t.Fatalf("setup: state = %v, want the advisory:\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if m.state != installStateProfile || !strings.Contains(screen(m), profileTitle) {
		t.Fatalf("setup: Continue anyway left state %v, want the profile screen:\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, key(tea.KeyEnter))

	if m.state != installStateProfile {
		t.Fatalf("Enter moved on to state %v while the clash check was still out", m.state)
	}
	if s := screen(m); !strings.Contains(s, clashChecking) {
		t.Errorf("the waiting screen lacks %q:\n%s", clashChecking, s)
	}

	m, _ = updateAs(t, m, clash)

	if m.state != installStateNotice {
		t.Errorf("state = %v once the clean clash answer arrives, want the notice the waiting Enter chose", m.state)
	}
}

func TestInstallOtherProject_CancelAndEscReturnToTheMenuWithoutInstalling(t *testing.T) {
	for _, c := range []struct {
		name string
		keys func(t *testing.T, m InstallModel) (InstallModel, tea.Cmd)
	}{
		{"Esc", func(t *testing.T, m InstallModel) (InstallModel, tea.Cmd) { return updateAs(t, m, key(tea.KeyEsc)) }},
		{"the Cancel row", func(t *testing.T, m InstallModel) (InstallModel, tea.Cmd) {
			m = moveCaretTo(t, m, "Cancel")
			return updateAs(t, m, key(tea.KeyEnter))
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			var prepared atomic.Int32
			m := advisory(t, otherProduction, opProject)
			m.seams.prepareEngine = func(context.Context, string, string) (*engine.Stream, func() error, error) {
				prepared.Add(1)
				return nil, nil, errors.New("must not be called")
			}

			m, cmd := c.keys(t, m)

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

// Arrows move the caret between the two rows; nothing else does anything.
func TestInstallOtherProject_OtherKeysDoNothing(t *testing.T) {
	m := advisory(t, otherProduction, opProject)

	t.Run("arrows move the caret", func(t *testing.T) {
		m, cmd := updateAs(t, m, key(tea.KeyDown))
		if cmd != nil || m.state != installStateOtherProject || caretLabel(screen(m)) != "Cancel" {
			t.Fatalf("Down: caret on %q, state %v:\n%s", caretLabel(screen(m)), m.state, screen(m))
		}
		m, cmd = updateAs(t, m, key(tea.KeyUp))
		if cmd != nil || m.state != installStateOtherProject || caretLabel(screen(m)) != "Continue anyway" {
			t.Fatalf("Up: caret on %q, state %v:\n%s", caretLabel(screen(m)), m.state, screen(m))
		}
	})
	t.Run("other keys leave the screen as it is", func(t *testing.T) {
		others := map[string]tea.KeyPressMsg{
			"q": runeKey('q'), "x": runeKey('x'), "y": runeKey('y'), "d": runeKey('d'), "1": runeKey('1'), "space": runeKey(' '),
			"tab": key(tea.KeyTab), "left": key(tea.KeyLeft), "right": key(tea.KeyRight), "backspace": key(tea.KeyBackspace),
		}
		want := screen(m)
		for name, k := range others {
			var cmd tea.Cmd
			m, cmd = updateAs(t, m, k)
			if cmd != nil {
				t.Fatalf("%s started a command", name)
			}
			if m.state != installStateOtherProject || screen(m) != want || m.Outcome().Done {
				t.Fatalf("%s changed the advisory (state %v):\n%s", name, m.state, screen(m))
			}
		}
	})
	t.Run("Ctrl-C quits", func(t *testing.T) {
		if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
			t.Error("Ctrl+C on the advisory did not quit")
		}
	})
}

// --- precedence --------------------------------------------------------

// The advisory beats the stale volume: it names the folder the volume
// screen cannot, and the database the volume screen worries about is
// that Orbit's.
func TestInstallOtherProject_WinsOverTheStaleVolumeScreen(t *testing.T) {
	m, vol, _, other := opInstall(t, clashDir, opFinding(opProject, otherProduction), deploy.DatabaseVolume{Name: "orbit-db-data"})

	m, _ = updateAs(t, m, vol)
	if m.state != installStateStaleVolume || !strings.Contains(screen(m), "This machine already has an Orbit database") {
		t.Fatalf("setup: state = %v, want the stale-volume screen:\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, other)

	if m.state != installStateOtherProject {
		t.Fatalf("state = %v, want the advisory to replace the stale-volume screen", m.state)
	}
	assertAdvisory(t, m, opFolderLead+otherProduction, opAdviceFolder, "orbit-db-data")
}

func TestInstallOtherProject_ArrivingFirstDropsAVolumeFinding(t *testing.T) {
	m, vol, _, other := opInstall(t, clashDir, opFinding(opProject, otherProduction), deploy.DatabaseVolume{Name: "orbit-db-data"})

	m, _ = updateAs(t, m, other, vol)

	if m.state != installStateOtherProject {
		t.Fatalf("state = %v, want the advisory kept", m.state)
	}
	assertAdvisory(t, m, opFolderLead+otherProduction, opAdviceFolder, "orbit-db-data")
}

// #318's dead end is above this advisory.
func TestInstallOtherProject_LosesToTheClash(t *testing.T) {
	m, _, clash, other := opInstallWith(t, clashDir, startedFrom(otherProduction), opFinding(opProject, otherProduction))

	m, _ = updateAs(t, m, other)
	if m.state != installStateOtherProject {
		t.Fatalf("setup: state = %v, want the advisory:\n%s", m.state, screen(m))
	}

	m, _ = updateAs(t, m, clash)

	if m.state != installStateOtherFolder {
		t.Fatalf("state = %v, want the clash's refusal to replace the advisory", m.state)
	}
	assertClashRefusal(t, m, otherProduction)
	if strings.Contains(screen(m), opTitle) {
		t.Errorf("the advisory shows on the refusal:\n%s", screen(m))
	}
}

func TestInstallOtherProject_ArrivingOnTheClashIsDropped(t *testing.T) {
	m, _, clash, other := opInstallWith(t, clashDir, startedFrom(otherProduction), opFinding(opProject, otherProduction))

	m, _ = updateAs(t, m, clash, other)

	if m.state != installStateOtherFolder {
		t.Fatalf("state = %v, want the refusal kept", m.state)
	}
	assertClashRefusal(t, m, otherProduction)
	s := screen(m)
	for _, not := range []string{opTitle, "Continue anyway"} {
		if strings.Contains(s, not) {
			t.Errorf("the refusal shows %q:\n%s", not, s)
		}
	}
}

// --- silence -----------------------------------------------------------

func TestInstallOtherProject_ReaderErrorIsSilent(t *testing.T) {
	m, vol, clash, other := opInstall(t, clashDir, opAnswering(deploy.OtherProjectApp{}, errors.New("Cannot connect to the Docker daemon")))

	m, _ = updateAs(t, m, vol, clash, other)

	if m.state != installStateProfile || !strings.Contains(screen(m), profileTitle) {
		t.Errorf("state = %v, want the profile screen:\n%s", m.state, screen(m))
	}
	if s := screen(m); strings.Contains(s, opTitle) || strings.Contains(strings.ToLower(s), "could not check") {
		t.Errorf("a check that could not run said so:\n%s", s)
	}
}

// With Enter waiting, the answer cannot arrive late; this is the guard
// for an answer that does: in the notice (or the run) it is dropped.
func TestInstallOtherProject_LateAnswerNeverInterruptsALaterScreen(t *testing.T) {
	m, vol, clash, other := opInstall(t, clashDir, opAnswering(deploy.OtherProjectApp{}, nil))
	m, _ = updateAs(t, m, vol, clash, other, key(tea.KeyEnter))
	if m.state != installStateNotice {
		t.Fatalf("setup: state = %v, want the notice", m.state)
	}

	m, _ = updateAs(t, m, installOtherProjectMsg{dir: clashDir, folder: otherProduction, project: opProject})

	if m.state != installStateNotice {
		t.Errorf("a late answer pulled the flow to state %v", m.state)
	}
	if strings.Contains(screen(m), opTitle) {
		t.Errorf("the notice turned into the advisory:\n%s", screen(m))
	}
}

// --- the switch --------------------------------------------------------

// No reader means no check, no wait and no screen: Init batches the
// volume and clash commands of #318 alone, and Enter does not wait on a
// third.
func TestInstallOtherProject_WithoutAReaderAsksNothing(t *testing.T) {
	m := NewInstallModel(clashDir, "v0.1.0")
	m.checkVolumes = staleVolumeCheck()
	m.runningRead = clashAnswering(deploy.RunningApp{}, nil)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 24})

	msgs := answersOf(m.Init())
	if len(msgs) != 2 {
		t.Fatalf("Init produced %d messages (%v), want the volume and clash answers alone", len(msgs), msgs)
	}
	for _, msg := range msgs {
		if _, ok := msg.(installOtherProjectMsg); ok {
			t.Fatalf("Init asked the other-project question without a reader")
		}
	}

	// Enter waits for the two answers and for nothing else.
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	m, _ = updateAs(t, m, msgs...)
	if m.state != installStateNotice {
		t.Errorf("state = %v, want the notice once the two answers are in", m.state)
	}
}

// --- the screen as drawn -----------------------------------------------

func TestInstallOtherProject_LongFolderWrapsToFourRows(t *testing.T) {
	long := "/srv" + strings.Repeat("/orbit-deployments-x", 16) + strings.Repeat("/more", 8)
	m := advisory(t, long, opProject)

	s := screen(m)
	rows := rowsOf(s)
	first := lineIndex(s, opFolderLead)
	if first < 0 {
		t.Fatalf("no fact row:\n%s", s)
	}
	end := first
	for end < len(rows) && screenRow(rows[end]) != "" {
		end++
	}
	if n := end - first; n > folderRows {
		t.Errorf("the fact is %d rows, want at most folderRows (%d):\n%s", n, folderRows, s)
	} else if n < 2 {
		t.Errorf("the fact is %d row, want the long folder wrapped:\n%s", n, s)
	}
	if got := screenLabels(s); !sameLabels(got, opRows) {
		t.Errorf("the rows are %q, want both still on a 24-row screen:\n%s", got, s)
	}
	for _, want := range []string{opBodyFirst, opBodySecond, opAdviceFolder, opPromise} {
		if !strings.Contains(oneLine(s), want) {
			t.Errorf("the advisory lacks %q:\n%s", want, s)
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

func TestInstallOtherProject_ControlRunesShowAsEscapes(t *testing.T) {
	for _, c := range []struct{ name, folder, project, lead, want string }{
		{"folder", "/srv/a\nb\x1b[999mZZ", opProject, opFolderLead, `started from /srv/a\nb\x1b[999mZZ`},
		{"project", "", "p\nq\x1b[999mZZ", opProjectLead, `under the Compose project p\nq\x1b[999mZZ`},
	} {
		t.Run(c.name, func(t *testing.T) {
			m := advisory(t, c.folder, c.project)

			if raw := m.View().Content; strings.Contains(raw, "\x1b[999m") {
				t.Errorf("the escape sequence reached the terminal: %q", raw)
			}
			s := screen(m)
			if !strings.Contains(oneLine(s), c.want) {
				t.Errorf("the fact row does not show the control runes as escapes (%q):\n%s", c.want, s)
			}
			rows := rowsOf(s)
			first := lineIndex(s, c.lead)
			if first < 0 || first+1 >= len(rows) || screenRow(rows[first+1]) != "" {
				t.Errorf("the newline made an extra row under the fact:\n%s", s)
			}
		})
	}
}

// --- the app -----------------------------------------------------------

// otherReading is the app's other-project reader, supplied by the test.
func otherReading(read opReadFunc) appOpt {
	return func(m AppModel) AppModel { return m.WithOtherProjectReader(read) }
}

// A dormant folder: the splash shows no rows and asks nothing about other
// projects, and Install, preselected, asks on its own and opens on the
// advisory.
func TestApp_InstallFromADormantMenuOpensOnTheOtherProjectAdvisory(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	var asked atomic.Int32
	var project atomic.Value
	m := started(t, folderApp(t, shortTempDir(t), 80, 26, deploy.HealthResponding,
		runningAs(deploy.RunningApp{}, nil),
		otherReading(func(ctx context.Context, p string) (deploy.OtherProjectApp, error) {
			asked.Add(1)
			project.Store(p)
			return opFinding(opProject, otherProduction)(ctx, p)
		})))
	noRows(t, screen(m))
	if got := caretLabel(screen(m)); got != "Install" {
		t.Fatalf("setup: the caret is on %q, want Install:\n%s", got, screen(m))
	}
	if n := asked.Load(); n != 0 {
		t.Fatalf("setup: the splash asked the other-project question %d times", n)
	}

	m = openFlow(t, m, "Install")

	if m.state != appStateInstall {
		t.Fatalf("state = %v, want the install flow", m.state)
	}
	if got := project.Load(); got != "orbit" {
		t.Errorf("Install asked for project %v, want orbit", got)
	}
	assertAdvisory(t, m, opFolderLead+otherProduction, opAdviceFolder)
}

// WithoutRunningCheck is the one switch for both questions (the same
// docker ps, the same daemon): the reader is cleared with the running
// one, so Install asks nothing, waits for nothing and shows nothing.
func TestApp_WithoutRunningCheckAsksNoOtherProject(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	var asked atomic.Int32
	counting := otherReading(func(ctx context.Context, p string) (deploy.OtherProjectApp, error) {
		asked.Add(1)
		return opFinding(opProject, otherProduction)(ctx, p)
	})
	off := func(m AppModel) AppModel { return m.WithoutRunningCheck() }
	dir := shortTempDir(t)

	t.Run("setup: the check is on without the switch", func(t *testing.T) {
		m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{}, nil), counting))

		m = openFlow(t, m, "Install")

		if m.install.otherRead == nil {
			t.Error("Install has no other-project reader although one was given")
		}
	})

	asked.Store(0)
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding, runningAs(deploy.RunningApp{}, nil), counting, off))

	m = openFlow(t, m, "Install")

	if m.state != appStateInstall {
		t.Fatalf("state = %v, want the install flow", m.state)
	}
	if m.install.otherRead != nil {
		t.Error("Install has an other-project reader although the running check is off")
	}
	if n := asked.Load(); n != 0 {
		t.Errorf("the other-project question was asked %d times with the check off", n)
	}
	if s := screen(m); m.install.state != installStateProfile || strings.Contains(s, opTitle) || !strings.Contains(s, profileTitle) {
		t.Errorf("Install did not open on the profile screen (state %v):\n%s", m.install.state, s)
	}
}
