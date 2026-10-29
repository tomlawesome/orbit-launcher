package ui

// #295: written from the issue alone, without the code. Two cases of the
// configuration handoff (Orbit contract docs/engine-events.md
// "Non-interactive contract", #1519) that config_handoff_test.go does not
// cover:
//
//   - The collected answers live for the launcher session, not for one
//     Install flow: choosing Install from the main menu again re-seeds the
//     new run's configuration tree without asking.
//   - A folder that already holds a .env-orbit of the person's own: Orbit
//     ignores the handed-in tree there and uses the folder's own, so
//     handing the answers in could never complete the install. In this
//     one case the launcher adopts the completed configuration into the
//     folder, as it did before #295, and then retries.
//
// The harness is config_handoff_test.go's: a fake install.sh behind the
// launcher's local script override, the real engine preparation and the
// real adoption, and only the configuration session faked.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// handoffApp is one launcher session (the whole AppModel) on the
// fixture's folder.
func (f *handoffFixture) handoffApp(seams engineRunSeams) (AppModel, *sink) {
	f.t.Helper()
	queue := newSink()
	m := NewAppModelNoAnimation().WithSender(queue.Send).WithoutVolumeCheck().WithVersion("v9.9.9")
	m.targetDir = f.dir
	m.flowSeams = seams
	m.flowNoticeDuration = time.Millisecond
	shortNoticeTick(f.t)
	m.flowReadClaim = func(context.Context, string, string, time.Time) (string, error) { return "", nil }
	m, _ = updateAs(f.t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
	return m, queue
}

// appSettled drives the app, taking pushed stream messages first, until
// quiet; it fails the test if a screen showing one of never comes up.
func appSettled(t *testing.T, m AppModel, cmd tea.Cmd, s *sink, never []string) AppModel {
	t.Helper()
	return driveWatching(t, m, cmd, never, &sink{msgs: s.msgs, first: true})
}

// appStartInstall chooses Install on the menu row the caret is on, then
// Standard, then accepts the notice if it is shown, and drives the run.
func appStartInstall(t *testing.T, m AppModel, s *sink, never []string) AppModel {
	t.Helper()
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Install
	m = drive(t, m, cmd)
	if sc := screen(m); !strings.Contains(sc, "Choose a deployment profile") {
		t.Fatalf("setup: Install did not open the profile screen:\n%s", sc)
	}
	m, cmd = updateAs(t, m, key(tea.KeyEnter)) // Standard
	if strings.Contains(screen(m), "A note before you install") {
		m = drive(t, m, cmd)
		m, cmd = updateAs(t, m, key(tea.KeyEnd), tea.KeyPressMsg{Text: noticePhrase}, key(tea.KeyEnter))
	}
	return appSettled(t, m, cmd, s, never)
}

// appAnswerGuided answers guided configuration as a person would: local
// sign-in, then the origin, and drives the retry to its end.
func appAnswerGuided(t *testing.T, m AppModel, s *sink) AppModel {
	t.Helper()
	if sc := screen(m); !strings.Contains(sc, "How will people sign in?") {
		t.Fatalf("setup: guided configuration did not open:\n%s", sc)
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Local accounts
	m = appSettled(t, m, cmd, s, nil)
	if sc := screen(m); !strings.Contains(sc, "Public Orbit origin") {
		t.Fatalf("setup: the origin was not asked:\n%s", sc)
	}
	for _, r := range handoffOrigin {
		m, cmd = updateAs(t, m, runeKey(r))
		m = drive(t, m, cmd)
	}
	m, cmd = updateAs(t, m, key(tea.KeyEnter))
	return appSettled(t, m, cmd, s, nil)
}

// A. Install from the main menu, after guided configuration in the same
// launcher session, re-seeds the new run's tree and asks nothing.
//
// Why it fails today: the answers are kept on the InstallModel, and the
// app builds a new InstallModel for each menu choice, so the second
// Install starts with no answers: its first run's tree is empty and the
// engine refuses again. The fake installer's refusal bound (one) ends
// that run there, so the first failure is run 3's unseeded tree; without
// the bound guided configuration would open again, which the never list
// and the prepare/start counts also catch.
func TestConfigHandoff_InstallFromTheMainMenuReseedsWithoutAsking(t *testing.T) {
	f := newHandoffFixture(t, 1, tailSucceeds)
	m, s := f.handoffApp(f.seams(true, nil))

	m = appStartInstall(t, m, s, nil)
	m = appAnswerGuided(t, m, s)
	if got := f.runs(); got != "2" {
		t.Fatalf("setup: the installer ran %q times, want 2 (the refusal, then the retry); screen:\n%s", got, screen(m))
	}
	f.requireSeeded(2)
	if m.state != appStateSuccess {
		t.Fatalf("setup: the retry did not reach the success screen (state %v):\n%s", m.state, screen(m))
	}
	if t.Failed() {
		t.FailNow()
	}

	// Back to the main menu, as a person does from the success screen.
	m, cmd := updateAs(t, m, key(tea.KeyEsc))
	m = drive(t, m, cmd)
	if m.state != appStateSplash {
		t.Fatalf("setup: Esc did not bring the main menu back (state %v):\n%s", m.state, screen(m))
	}
	m = caretToRow(t, m, "Install")

	never := []string{"How will people sign in?", "Public Orbit origin"}
	m = appStartInstall(t, m, s, never)
	if sc := screen(m); strings.Contains(sc, "How will people sign in?") || strings.Contains(sc, "Public Orbit origin") {
		t.Errorf("Install from the main menu asked for configuration again in the same launcher session:\n%s", sc)
	}
	if n := f.prepares.Load(); n != 1 {
		t.Errorf("a configuration session was prepared %d times in one launcher session, want once", n)
	}
	if n := f.starts.Load(); n != 1 {
		t.Errorf("guided configuration ran %d times in one launcher session, want once", n)
	}
	if got := f.runs(); got != "3" {
		t.Fatalf("Install from the menu ran the installer %q times in all, want 3 (one seeded run); screen:\n%s", got, screen(m))
	}
	f.requireSeeded(3)
	if m.state != appStateSuccess {
		t.Errorf("the re-seeded install did not succeed (state %v):\n%s", m.state, screen(m))
	}
}

// ownEnvInstaller is the fake install.sh for a folder with a .env-orbit
// of its own. As Orbit's contract says, the handed-in tree is ignored
// there: only the folder's .env-orbit counts. It refuses with a
// configuration failure while that file lacks the answer (at most @MAX@
// times, then a bound), and succeeds once it holds it.
const ownEnvInstaller = `#!/usr/bin/env bash
out=@OUT@
n=$(( $(cat "$out/runs" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$out/runs"
tree="$@TREEENV@"
ls -A "$PWD" > "$out/folder-$n"
cat .env-orbit > "$out/folderenv-$n" 2>/dev/null
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
if grep -qx 'APP_URL=@ORIGIN@' .env-orbit 2>/dev/null; then
  echo "phase=application component=application state=healthy reason=application-health action=health elapsed=1s"
  echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=2s"
  exit 0
fi
r=$(( $(cat "$out/refusals" 2>/dev/null || echo 0) + 1 )); echo "$r" > "$out/refusals"
if [ "$r" -le @MAX@ ] && [ -n "$tree" ]; then
  mkdir -p "$tree/scripts" && printf '#!/usr/bin/env bash\nexit 0\n' > "$tree/scripts/configure.sh"
  echo "phase=configuration component=configuration state=failed reason=configuration-failure action=retry elapsed=0s"
  echo "Orbit installer: configuration fields requiring attention: APP_URL." >&2
  exit 1
fi
echo "phase=host component=host state=failed reason=docker-host action=abort elapsed=0s"
echo "Orbit installer: test bound: the folder's own .env-orbit is still incomplete." >&2
exit 1
`

// B. A fresh Install into a folder that already holds an incomplete
// .env-orbit of the person's own: the completed configuration is adopted
// into the folder, then the retry runs and succeeds.
//
// Why it fails today: since #295 the launcher only hands the answers in
// through the retry's tree, which Orbit ignores when the folder has its
// own configuration, so the folder's .env-orbit is still the old
// incomplete one after the retry (and the retry is refused again).
func TestConfigHandoff_FolderWithItsOwnEnvFileIsAdoptedInto(t *testing.T) {
	f := &handoffFixture{t: t, dir: t.TempDir(), out: t.TempDir()}
	script := strings.NewReplacer("@OUT@", shQuote(f.out), "@MAX@", fmt.Sprint(1),
		"@TREEENV@", deploy.ConfigTreeEnv, "@ORIGIN@", handoffOrigin).Replace(ownEnvInstaller)
	installScriptAt(t, script)
	own := "# the person's own settings\nAPP_URL=\n"
	if err := os.WriteFile(filepath.Join(f.dir, ".env-orbit"), []byte(own), 0o600); err != nil {
		t.Fatal(err)
	}

	m, s := f.model(f.seams(false, nil)) // nothing owed: the answers are collected at once
	m, cmd := startInstallRun(t, m)
	m = handoffSettled(t, m, cmd, s)

	if got := f.runs(); got != "2" {
		t.Fatalf("the installer ran %q times, want 2 (the refusal, then the retry); screen:\n%s", got, screen(m))
	}
	if first := f.read("folderenv-1"); first != own {
		t.Fatalf("setup: the first run saw the folder's .env-orbit as %q, want the person's own %q", first, own)
	}
	body, err := os.ReadFile(filepath.Join(f.dir, ".env-orbit"))
	if err != nil {
		t.Fatalf("the folder's .env-orbit is gone after the retry: %v", err)
	}
	if !strings.Contains(string(body), "APP_URL="+handoffOrigin+"\n") {
		t.Errorf("after the retry the folder's .env-orbit = %q, want the completed configuration holding APP_URL=%s (it must be adopted into a folder with its own .env-orbit)", body, handoffOrigin)
	}
	if got := f.read("folderenv-2"); !strings.Contains(got, "APP_URL="+handoffOrigin+"\n") {
		t.Errorf("the retry started with the folder's .env-orbit = %q, want the adopted configuration", got)
	}
	secret := filepath.Join(f.dir, ".orbit-secrets", "oidc-client-secret")
	if b, err := os.ReadFile(secret); err != nil || string(b) != "session-secret\n" {
		t.Errorf(".orbit-secrets/ was not written beside the adopted .env-orbit (%s: %q, %v)", secret, b, err)
	}
	if out := m.Outcome(); !out.Done || !out.Succeeded {
		t.Errorf("the retry after adopting into the folder did not succeed (outcome %+v):\n%s", out, screen(m))
	}
}
