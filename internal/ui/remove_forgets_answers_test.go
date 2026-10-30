package ui

// #303 P1, written from the brief alone, without the code. Owner answer
// 13a (2026-10-28): the guided answers the launcher holds for its
// session are forgotten when a Remove stand-down completes. A person who
// stands Orbit down and then deletes its configuration, as the "fully
// remove" command does, and installs again in the same launcher session
// is asked again; nothing they answered before the Remove is handed in.
// A Remove that did nothing (cancelled, or a stand-down that failed)
// keeps the answers, as before.
//
// The harness is config_handoff_menu_test.go's: the whole AppModel, a
// fake install.sh behind the local script override, the real engine
// preparation and adoption, and only the configuration session and the
// stand-down faked.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// tailCommitsAndSucceeds is a successful install whose transaction
// writes the handed-in configuration into the folder (only where the
// folder has none, as the contract says), as a real install leaves it.
const tailCommitsAndSucceeds = `if [ ! -e .env-orbit ]; then cp "$tree/.env-orbit" .env-orbit; fi
if [ ! -e .orbit-secrets ] && [ -d "$tree/.orbit-secrets" ]; then cp -R "$tree/.orbit-secrets" .orbit-secrets; fi
` + tailSucceeds

// folderOwnEnvWins is put in front of the head's tree check: a folder
// holding a complete .env-orbit of its own is installed from that, and
// the handed-in tree is ignored, as Orbit's contract says.
const folderOwnEnvWins = `cat .env-orbit > "$out/folderenv-$n" 2>/dev/null
if grep -qx 'APP_URL=@ORIGIN@' .env-orbit 2>/dev/null; then
  echo "phase=application component=application state=healthy reason=application-health action=health elapsed=1s"
  echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=2s"
  exit 0
fi
`

const treeCheckLine = `if [ ! -f "$tree/.env-orbit" ]; then`

// removeApp is one launcher session on the fixture's folder, ready to
// open Remove without reaching Docker.
func (f *handoffFixture) removeApp() (AppModel, *sink) {
	f.t.Helper()
	m, s := f.handoffApp(f.seams(true, nil))
	m.flowInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }
	return m, s
}

// installGuidedToMenu runs the session's first Install through guided
// configuration to the success screen, then Esc back to the main menu.
func installGuidedToMenu(t *testing.T, f *handoffFixture, m AppModel, s *sink) AppModel {
	t.Helper()
	m = appStartInstall(t, m, s, nil, appAtRest)
	m = appAnswerGuided(t, m, s)
	if got := f.runs(); got != "2" {
		t.Fatalf("setup: the installer ran %q times, want 2 (the refusal, then the retry); screen:\n%s", got, screen(m))
	}
	f.requireSeeded(2)
	if m.state != appStateSuccess {
		t.Fatalf("setup: the retry did not reach the success screen (state %v):\n%s", m.state, screen(m))
	}
	if _, err := os.Stat(filepath.Join(f.dir, deploy.EnvFile)); err != nil {
		t.Fatalf("setup: the install left no .env-orbit in the folder: %v", err)
	}
	if t.Failed() {
		t.FailNow()
	}
	m, cmd := updateAs(t, m, key(tea.KeyEsc))
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Fatalf("setup: Esc did not bring the main menu back (state %v):\n%s", m.state, screen(m))
	}
	return m
}

// openRemoveFromMenu chooses Remove on the menu and fakes its
// stand-down with standDown.
func openRemoveFromMenu(t *testing.T, m AppModel, standDown func(context.Context, string) error) AppModel {
	t.Helper()
	m = caretToRow(t, m, "Remove")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateRemove || !strings.Contains(screen(m), "Stand down Orbit") {
		t.Fatalf("setup: Remove did not open its confirm screen (state %v):\n%s", m.state, screen(m))
	}
	m.remove.standDown = standDown
	m.remove.clipboard = io.Discard
	return m
}

// standDownTo confirms the stand-down and drives it to its result
// screen, which must be want.
func standDownTo(t *testing.T, m AppModel, want removeState) AppModel {
	t.Helper()
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Stand down Orbit is preselected
	m = drive(t, m, cmd)
	if m.state != appStateRemove || m.remove.state != want {
		t.Fatalf("setup: the stand-down did not end on the expected screen (app state %v, remove state %v, want %v):\n%s", m.state, m.remove.state, want, screen(m))
	}
	return m
}

// removeToMenu chooses Menu on a finished Remove's screen.
func removeToMenu(t *testing.T, m AppModel) AppModel {
	t.Helper()
	m = caretToRow(t, m, "Menu")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Fatalf("setup: Menu did not bring the main menu back (state %v):\n%s", m.state, screen(m))
	}
	return m
}

// deleteConfiguration removes the folder's .env-orbit and .orbit-secrets,
// as the "fully remove" command shown after the stand-down would.
func deleteConfiguration(t *testing.T, dir string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, deploy.EnvFile)); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(dir, deploy.SecretsDir)); err != nil {
		t.Fatal(err)
	}
}

// requireNotSeeded fails if install run n was handed any configuration.
func (f *handoffFixture) requireNotSeeded(n int) {
	f.t.Helper()
	if tree := f.read(fmt.Sprintf("tree-%d", n)); strings.Contains(tree, ".env-orbit") || strings.Contains(tree, ".orbit-secrets") {
		f.t.Errorf("install run %d after a completed Remove was handed the answers held from before it; its tree held:\n%s", n, tree)
	}
	if env := f.read(fmt.Sprintf("env-%d", n)); env != "" {
		f.t.Errorf("install run %d after a completed Remove was handed .env-orbit %q, want none", n, env)
	}
}

// 1. Remove completes, the configuration is deleted, Install again: the
// person is asked again, and the first run is handed nothing.
//
// Fails before: the held answers seed the run, so nothing is asked.
func TestRemoveForgets_InstallAfterACompletedRemoveAsksAgain(t *testing.T) {
	// Two refusals, so the unseeded run after the Remove opens guided
	// configuration instead of hitting the bound.
	f := newHandoffFixture(t, 2, tailCommitsAndSucceeds)
	m, s := f.removeApp()
	m = installGuidedToMenu(t, f, m, s)

	m = openRemoveFromMenu(t, m, standDownOK)
	m = standDownTo(t, m, removeStateDone)
	m = removeToMenu(t, m)
	deleteConfiguration(t, f.dir)

	m = caretToRow(t, m, "Install")
	m = appStartInstall(t, m, s, nil, appAtRest)
	if got := f.runs(); got != "3" {
		t.Errorf("Install after the Remove ran the installer %q times in all, want 3 (one refused, unseeded run, then the questions)", got)
	}
	f.requireNotSeeded(3)
	if sc := screen(m); !strings.Contains(sc, "How will people sign in?") {
		t.Fatalf("Install after a completed Remove and a deleted configuration did not ask how people will sign in:\n%s", sc)
	}
	m = appAnswerGuided(t, m, s) // also requires "Public Orbit origin"
	if n := f.prepares.Load(); n != 2 {
		t.Errorf("a configuration session was prepared %d times, want twice (once per Install, the Remove between them)", n)
	}
	if n := f.starts.Load(); n != 2 {
		t.Errorf("guided configuration ran %d times, want twice", n)
	}
	if got := f.runs(); got != "4" {
		t.Fatalf("the installer ran %q times in all, want 4; screen:\n%s", got, screen(m))
	}
	f.requireSeeded(4)
	if m.state != appStateSuccess {
		t.Errorf("the install with the new answers did not succeed (state %v):\n%s", m.state, screen(m))
	}
}

// 2. Once the stand-down completes, the private temporary copy of the
// held answers is gone.
//
// Fails before: it remains until the launcher exits.
func TestRemoveForgets_CompletedStandDownDeletesTheHeldCopy(t *testing.T) {
	tmp := collectedTemp(t)
	f := newHandoffFixture(t, 1, tailCommitsAndSucceeds)
	m, s := f.removeApp()
	m = installGuidedToMenu(t, f, m, s)
	if held := listCollected(t, tmp); len(held) == 0 {
		t.Fatalf("setup: no orbit-launcher-collected-* directory under TMPDIR after guided configuration; the test proves nothing")
	}

	m = openRemoveFromMenu(t, m, standDownOK)
	standDownTo(t, m, removeStateDone)
	if held := listCollected(t, tmp); len(held) != 0 {
		t.Errorf("after the stand-down completed the held answers are still on disk: %v", held)
	}
}

// 3. A Remove that did nothing keeps the answers: Install from the menu
// asks nothing and its run is seeded. The folder's configuration is
// deleted as in 1, so the stand-down's result is the only difference.
//
// Passes before and after.
func TestRemoveForgets_RemoveThatDidNothingKeepsTheAnswers(t *testing.T) {
	cases := []struct {
		name   string
		remove func(t *testing.T, m AppModel) AppModel
	}{
		{"cancelled on the confirm screen", func(t *testing.T, m AppModel) AppModel {
			m = openRemoveFromMenu(t, m, func(context.Context, string) error {
				t.Error("the stand-down ran although Remove was cancelled")
				return nil
			})
			m = caretToRow(t, m, "Cancel")
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			m = drive(t, m, cmd)
			if m.state != appStateSplash {
				t.Fatalf("setup: Cancel did not bring the main menu back (state %v):\n%s", m.state, screen(m))
			}
			return m
		}},
		{"stand-down fails", func(t *testing.T, m AppModel) AppModel {
			m = openRemoveFromMenu(t, m, func(context.Context, string) error {
				return errors.New("docker daemon not running")
			})
			m = standDownTo(t, m, removeStateFailed)
			return removeToMenu(t, m)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newHandoffFixture(t, 1, tailCommitsAndSucceeds)
			m, s := f.removeApp()
			m = installGuidedToMenu(t, f, m, s)
			m = c.remove(t, m)
			deleteConfiguration(t, f.dir)

			m = caretToRow(t, m, "Install")
			never := []string{"How will people sign in?", "Public Orbit origin"}
			m = appStartInstall(t, m, s, never, appAtRest)
			if n := f.prepares.Load(); n != 1 {
				t.Errorf("a configuration session was prepared %d times, want once (the Remove did nothing)", n)
			}
			if n := f.starts.Load(); n != 1 {
				t.Errorf("guided configuration ran %d times, want once", n)
			}
			if got := f.runs(); got != "3" {
				t.Fatalf("the installer ran %q times in all, want 3 (one seeded run); screen:\n%s", got, screen(m))
			}
			f.requireSeeded(3)
			if m.state != appStateSuccess {
				t.Errorf("the re-seeded install did not succeed (state %v):\n%s", m.state, screen(m))
			}
		})
	}
}

// 5. Remove completes and the folder is left intact: Install from the
// menu installs from the folder's own .env-orbit and asks nothing.
// Dropping the held answers changes nothing in this path.
//
// Passes before and after.
func TestRemoveForgets_InstallIntoTheIntactFolderAsksNothing(t *testing.T) {
	f := &handoffFixture{t: t, dir: t.TempDir(), out: t.TempDir()}
	head := strings.Replace(handoffInstallerHead, treeCheckLine, folderOwnEnvWins+treeCheckLine, 1)
	if head == handoffInstallerHead {
		t.Fatal("setup: the fake installer's tree check was not found")
	}
	script := strings.NewReplacer("@OUT@", shQuote(f.out), "@MAX@", fmt.Sprint(1),
		"@TREEENV@", deploy.ConfigTreeEnv, "@ORIGIN@", handoffOrigin).Replace(head + tailCommitsAndSucceeds)
	installScriptAt(t, script)

	m, s := f.removeApp()
	m = installGuidedToMenu(t, f, m, s)
	m = openRemoveFromMenu(t, m, standDownOK)
	m = standDownTo(t, m, removeStateDone)
	m = removeToMenu(t, m)

	m = caretToRow(t, m, "Install")
	never := []string{"How will people sign in?", "Public Orbit origin"}
	m = appStartInstall(t, m, s, never, appAtRest)
	if n := f.prepares.Load(); n != 1 {
		t.Errorf("a configuration session was prepared %d times, want once", n)
	}
	if n := f.starts.Load(); n != 1 {
		t.Errorf("guided configuration ran %d times, want once", n)
	}
	if got := f.runs(); got != "3" {
		t.Fatalf("the installer ran %q times in all, want 3; screen:\n%s", got, screen(m))
	}
	if got := f.read("folderenv-3"); !strings.Contains(got, "APP_URL="+handoffOrigin+"\n") {
		t.Errorf("Install after the Remove started with the folder's .env-orbit = %q, want the folder's own configuration", got)
	}
	if m.state != appStateSuccess {
		t.Errorf("the install from the folder's own configuration did not succeed (state %v):\n%s", m.state, screen(m))
	}
}
