package ui

// #295: written from the issue alone, without the code. Guided
// configuration no longer writes into the deployment folder: the
// launcher hands the person's answers to the installer in the retry's
// fresh configuration tree (ORBIT_LAUNCHER_CONFIG_TREE), and the
// installer's own transaction writes them. Whenever the install does not
// happen the folder ends as it started. The answers are kept for the
// launcher session (InstallModel), so Install again re-seeds the next
// run's tree without asking; a new session asks again.
//
// Every flow test here runs the REAL engine preparation (a fake
// install.sh behind the launcher's local script override, so the real
// stage-and-run path and its real configuration tree are used) and the
// REAL adoption (no adoptConfig seam). Only the configuration session
// itself is faked: prepareConfig hands back the run's own tree as the
// session tree, and the fake configure step writes its answers there, as
// configure.sh --init does. The fake install.sh records, at its start,
// the deployment folder's listing and its configuration tree's contents
// and modes.
//
// Why each fails against the old code: the old adoptAndRetry wrote
// .env-orbit and .orbit-secrets/ into the deployment folder before the
// retry, and the retry's tree was empty.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// handoffInstallerHead is the fake install.sh's common start. @OUT@ is
// where it records; @MAX@ is how many configuration refusals it gives
// before it stops refusing (a bound, so the old code's empty retry tree
// ends the run instead of looping).
const handoffInstallerHead = `#!/usr/bin/env bash
out=@OUT@
n=$(( $(cat "$out/runs" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$out/runs"
tree="$ORBIT_LAUNCHER_CONFIG_TREE"
ls -A "$PWD" > "$out/folder-$n"
if [ -n "$tree" ] && [ -d "$tree" ]; then
  (cd "$tree" && find . -mindepth 1 -printf '%m %y %p\n' | LC_ALL=C sort) > "$out/tree-$n"
  cat "$tree/.env-orbit" > "$out/env-$n" 2>/dev/null
fi
echo "phase=host component=host state=completed reason=host-tools action=check elapsed=0s"
if [ ! -f "$tree/.env-orbit" ]; then
  r=$(( $(cat "$out/refusals" 2>/dev/null || echo 0) + 1 )); echo "$r" > "$out/refusals"
  if [ "$r" -le @MAX@ ]; then
    mkdir -p "$tree/scripts" && printf '#!/usr/bin/env bash\nexit 0\n' > "$tree/scripts/configure.sh"
    echo "phase=configuration component=configuration state=failed reason=configuration-failure action=retry elapsed=0s"
    echo "Orbit installer: configuration fields requiring attention: APP_URL." >&2
    exit 1
  fi
  echo "phase=host component=host state=failed reason=docker-host action=abort elapsed=0s"
  echo "Orbit installer: test bound: no configuration was handed in." >&2
  exit 1
fi
`

// The tails: what the fake installer does once a configuration was
// handed in.
const (
	tailSucceeds = `echo "phase=application component=application state=healthy reason=application-health action=health elapsed=1s"
echo "phase=complete component=installer state=completed reason=deployment-ready action=complete elapsed=2s"
exit 0
`
	// A refusal before anything is written, as the engine gives for a
	// folder it cannot install into: the "can't go ahead" ending.
	tailRefusedBeforeWriting = `echo "phase=host component=host state=failed reason=docker-host action=abort elapsed=0s"
echo "Orbit installer: orbit-install-backups is not a folder." >&2
exit 1
`
	// The install transaction writes the handed-in configuration (only
	// where the folder has none, as the contract says), then fails after
	// the commit and rolls back exactly what it wrote.
	tailFailsAfterCommit = `wrote=; wrotes=
if [ ! -e .env-orbit ]; then cp "$tree/.env-orbit" .env-orbit; wrote=1; fi
if [ ! -e .orbit-secrets ] && [ -d "$tree/.orbit-secrets" ]; then cp -R "$tree/.orbit-secrets" .orbit-secrets; wrotes=1; fi
ls -A "$PWD" > "$out/during-$n"
echo "phase=compose component=compose state=running reason=compose-validation action=validate elapsed=1s"
echo "phase=rollback component=installer state=running reason=rollback action=rollback elapsed=2s"
[ -n "$wrote" ] && rm -f .env-orbit
[ -n "$wrotes" ] && rm -rf .orbit-secrets
echo "phase=rollback component=installer state=completed reason=rollback action=rollback elapsed=3s"
echo "phase=compose component=compose state=failed reason=docker-host action=retry elapsed=3s"
echo "Orbit installer: Orbit services could not be created or started." >&2
exit 1
`
)

// guidedInitStep is configure.sh --init under the machine protocol for
// local sign-in: APP_URL asked, then the answers written into the
// session tree @TREE@, owner-only.
const guidedInitStep = `
echo "prompt field=APP_URL kind=url required=true attempt=1"
read -r a || { echo "prompt-abort field=APP_URL"; exit 1; }
echo "prompt-accept field=APP_URL"
umask 077
printf 'APP_URL=%s\n' "$a" > @TREE@/.env-orbit
mkdir -p @TREE@/.orbit-secrets
printf 'session-secret\n' > @TREE@/.orbit-secrets/oidc-client-secret
exit 0`

const handoffOrigin = "https://orbit.example.test"

// What the retry's tree must hold: the collected files, owner-only.
var seededTreeLines = []string{
	"600 f ./.env-orbit",
	"700 d ./.orbit-secrets",
	"600 f ./.orbit-secrets/oidc-client-secret",
}

type handoffFixture struct {
	t   *testing.T
	dir string // the deployment folder
	out string // where the fake install.sh records

	mu      sync.Mutex
	session string // the configuration session's tree

	prepares atomic.Int32 // configuration sessions prepared (asked)
	starts   atomic.Int32 // guided steps started
}

func newHandoffFixture(t *testing.T, maxRefusals int, tail string) *handoffFixture {
	t.Helper()
	f := &handoffFixture{t: t, dir: t.TempDir(), out: t.TempDir()}
	script := strings.NewReplacer("@OUT@", shQuote(f.out), "@MAX@", fmt.Sprint(maxRefusals)).Replace(handoffInstallerHead) + tail
	installScriptAt(t, script)
	return f
}

func (f *handoffFixture) sessionTree() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.session
}

// writeAnswers is the session's result, as configure.sh leaves it.
func writeAnswers(t *testing.T, tree string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(tree, ".env-orbit"), []byte("APP_URL="+handoffOrigin+"\n"), 0o600); err != nil {
		t.Error(err)
	}
	if err := os.MkdirAll(filepath.Join(tree, ".orbit-secrets"), 0o700); err != nil {
		t.Error(err)
	}
	if err := os.WriteFile(filepath.Join(tree, ".orbit-secrets", "oidc-client-secret"), []byte("session-secret\n"), 0o600); err != nil {
		t.Error(err)
	}
}

// prepareConfig opens a configuration session in the tree the run handed
// over. With guided false the answers are already there (nothing owed),
// so adoption and the retry follow at once; with guided true the person
// is asked (sign-in mode, then APP_URL). Ending the session clears what
// it collected from that tree, as the real session's cleanup does.
func (f *handoffFixture) prepareConfig(guided bool, also func()) prepareConfigFunc {
	return func(_ context.Context, _ string, tree string) configPlanMsg {
		f.prepares.Add(1)
		time.Sleep(200 * time.Millisecond) // configure.sh --check takes a moment
		if tree == "" {
			return configPlanMsg{err: errors.New("test: the run handed over no configuration tree")}
		}
		f.mu.Lock()
		f.session = tree
		f.mu.Unlock()
		if !guided {
			writeAnswers(f.t, tree)
		}
		if also != nil {
			also()
		}
		return configPlanMsg{plan: configPlan{
			configTree: tree,
			needInit:   guided,
			cleanup: func() {
				_ = os.Remove(filepath.Join(tree, ".env-orbit"))
				_ = os.RemoveAll(filepath.Join(tree, ".orbit-secrets"))
			},
		}}
	}
}

func (f *handoffFixture) startConfig() startConfigFunc {
	return func(string, deploy.ConfigStep, deploy.AuthMode) (*engine.Stream, io.WriteCloser, error) {
		f.starts.Add(1)
		script := strings.ReplaceAll(guidedInitStep, "@TREE@", shQuote(f.sessionTree()))
		return engine.StartInteractive(exec.Command("bash", "-c", script))
	}
}

// seams: real engine preparation and real adoption; only the session is
// faked.
func (f *handoffFixture) seams(guided bool, also func()) engineRunSeams {
	return engineRunSeams{
		prepareConfig: f.prepareConfig(guided, also),
		startConfig:   f.startConfig(),
		recheckConfig: secretNotOwed,
		detect:        fakeDetect(handoffOrigin),
	}
}

// model is one launcher session's Install flow on the fixture's folder.
func (f *handoffFixture) model(seams engineRunSeams) (InstallModel, *sink) {
	f.t.Helper()
	m := NewInstallModel(f.dir, "v9.9.9")
	m.seams = seams
	s := newSink()
	m.send = s.Send
	m, _ = updateAs(f.t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
	return m, s
}

func (f *handoffFixture) read(name string) string {
	body, _ := os.ReadFile(filepath.Join(f.out, name))
	return string(body)
}

func (f *handoffFixture) runs() string { return strings.TrimSpace(f.read("runs")) }

// requireSeeded fails unless install run n saw the collected answers in
// its configuration tree, owner-only, and the deployment folder still
// empty.
func (f *handoffFixture) requireSeeded(n int) {
	f.t.Helper()
	if folder := strings.Fields(f.read(fmt.Sprintf("folder-%d", n))); len(folder) != 0 {
		f.t.Errorf("install run %d started with the deployment folder holding %v; the launcher must write nothing there", n, folder)
	}
	tree := f.read(fmt.Sprintf("tree-%d", n))
	for _, want := range seededTreeLines {
		if !strings.Contains(tree, want+"\n") {
			f.t.Errorf("install run %d's configuration tree lacks %q; it held:\n%s", n, want, tree)
		}
	}
	if env := f.read(fmt.Sprintf("env-%d", n)); env != "APP_URL="+handoffOrigin+"\n" {
		f.t.Errorf("install run %d's handed-in .env-orbit = %q, want the person's answer", n, env)
	}
}

// guidedConfiguration starts Install and answers guided configuration as
// a person would: local sign-in, then the origin. It returns once the
// retry that follows has run to its end.
func (f *handoffFixture) guidedConfiguration(m InstallModel, s *sink) InstallModel {
	f.t.Helper()
	t := f.t
	m, cmd := startInstallRun(t, m)
	m = handoffSettled(t, m, cmd, s)
	if sc := screen(m); !strings.Contains(sc, "How will people sign in?") {
		t.Fatalf("setup: guided configuration did not open:\n%s", sc)
	}
	m, cmd = updateAs(t, m, key(tea.KeyEnter)) // Local accounts
	m = handoffSettled(t, m, cmd, s)
	if sc := screen(m); !strings.Contains(sc, "Public Orbit origin") {
		t.Fatalf("setup: the origin was not asked:\n%s", sc)
	}
	for _, r := range handoffOrigin {
		m, cmd = updateAs(t, m, runeKey(r))
		m = drive(t, m, cmd)
	}
	m, cmd = updateAs(t, m, key(tea.KeyEnter))
	return handoffSettled(t, m, cmd, s)
}

// handoffDrive is drive with one difference: a message the engine
// stream pushed is taken before the next queued command runs, as the
// program's own loop would see it arriving while a command works. (drive
// runs the whole command chain first, so the first run's stream end would
// only reach the model after the retry had started.) It fails the test if
// a screen showing one of never comes up on the way.
func handoffDrive(t *testing.T, m InstallModel, cmd tea.Cmd, s *sink, never []string) InstallModel {
	t.Helper()
	cmds := []tea.Cmd{cmd}
	for i := 0; ; i++ {
		if i > 1000 {
			t.Fatal("command chain did not settle")
		}
		var msg tea.Msg
		select {
		case msg = <-s.msgs:
		default:
			if len(cmds) > 0 {
				next := cmds[0]
				cmds = cmds[1:]
				if next == nil {
					continue
				}
				msg = next()
				break
			}
			select {
			case msg = <-s.msgs:
			case <-time.After(2 * time.Second):
				return m
			}
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			cmds = append(cmds, batch...)
			continue
		}
		if _, ok := msg.(tickMsg); ok || msg == nil {
			continue
		}
		var more tea.Cmd
		m, more = updateAs(t, m, msg)
		for _, gone := range never {
			if shown := screen(m); strings.Contains(shown, gone) {
				t.Errorf("a screen showing %q came up after %T:\n%s", gone, msg, shown)
			}
		}
		cmds = append(cmds, more)
	}
}

// handoffSettled drives m until it has been quiet for two seconds.
func handoffSettled(t *testing.T, m InstallModel, cmd tea.Cmd, s *sink) InstallModel {
	t.Helper()
	return handoffDrive(t, m, cmd, s, nil)
}

// handoffListing is every entry under dir with its mode, sorted.
func handoffListing(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, info.Mode().String()+" "+rel)
		return nil
	})
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	sort.Strings(out)
	return out
}

func requireSameListing(t *testing.T, what string, before, after []string) {
	t.Helper()
	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("%s: the deployment folder changed\nbefore: %v\nafter:  %v", what, before, after)
	}
}

// 1. After guided configuration the deployment folder is untouched until
// the installer runs; the answers are in the tree handed to the retry,
// 0600 / 0700. Old code: the folder held .env-orbit and .orbit-secrets
// when the retry started, and the retry's tree was empty.
func TestConfigHandoff_GuidedAnswersGoToTheNextRunsTreeNotTheFolder(t *testing.T) {
	f := newHandoffFixture(t, 1, tailSucceeds)
	before := handoffListing(t, f.dir)
	m, s := f.model(f.seams(true, nil))
	m = f.guidedConfiguration(m, s)

	if got := f.runs(); got != "2" {
		t.Fatalf("the installer ran %q times, want 2 (the refusal, then the retry); screen:\n%s", got, screen(m))
	}
	f.requireSeeded(2)
	if sc := screen(m); !strings.Contains(sc, "Get into Orbit") {
		t.Errorf("the retry with the handed-in configuration did not succeed:\n%s", sc)
	}
	// The fake installer's success writes nothing, so anything in the
	// folder now was written by the launcher.
	requireSameListing(t, "after a successful retry", before, handoffListing(t, f.dir))
}

// 2. A retry refused before anything is written shows the existing
// sentence, which is now true: the folder's listing equals its listing
// before the run. Old code: the launcher's .env-orbit and .orbit-secrets
// are left in the folder under "nothing was changed".
func TestConfigHandoff_RetryRefusedBeforeWritingChangesNothing(t *testing.T) {
	f := newHandoffFixture(t, 1, tailRefusedBeforeWriting)
	before := handoffListing(t, f.dir)
	m, s := f.model(f.seams(false, nil))
	m, cmd := startInstallRun(t, m)
	m = handoffSettled(t, m, cmd, s)

	if got := f.runs(); got != "2" {
		t.Fatalf("the installer ran %q times, want 2; screen:\n%s", got, screen(m))
	}
	if sc := flat(screen(m)); !strings.Contains(sc, "The install can't go ahead here; nothing was changed.") {
		t.Errorf("the refusal's sentence is not shown:\n%s", screen(m))
	}
	requireSameListing(t, "after a retry refused before writing", before, handoffListing(t, f.dir))
}

// 3. A failure after the installer's commit leaves the folder as it was
// found: the installer wrote the handed-in configuration and its
// rollback removed exactly that. Old code: the folder already had the
// launcher's .env-orbit, so the installer neither wrote nor removed it,
// and it is still there.
func TestConfigHandoff_FailureAfterTheCommitLeavesTheFolderAsFound(t *testing.T) {
	f := newHandoffFixture(t, 1, tailFailsAfterCommit)
	before := handoffListing(t, f.dir)
	m, s := f.model(f.seams(false, nil))
	m, cmd := startInstallRun(t, m)
	m = handoffSettled(t, m, cmd, s)

	if got := f.runs(); got != "2" {
		t.Fatalf("the installer ran %q times, want 2; screen:\n%s", got, screen(m))
	}
	if during := f.read("during-2"); !strings.Contains(during, ".env-orbit") {
		t.Errorf("the installer's own transaction did not write the handed-in configuration (folder during the run: %q)", during)
	}
	if sc := screen(m); !strings.Contains(sc, "Install stopped") {
		t.Errorf("the failed install is not on the stopped screen:\n%s", sc)
	}
	requireSameListing(t, "after a failure after the commit", before, handoffListing(t, f.dir))
}

// 4. Install again in the same launcher session asks nothing: the next
// run's tree is seeded with the same answers and no configuration screen
// is shown. Old code: the retry's tree was empty (the installer's bound
// then ends the run with no Install again), and a later run's tree is
// empty too.
func TestConfigHandoff_InstallAgainReseedsWithoutAsking(t *testing.T) {
	f := newHandoffFixture(t, 1, tailFailsAfterCommit)
	m, s := f.model(f.seams(true, nil))
	m = f.guidedConfiguration(m, s)
	if got := f.runs(); got != "2" {
		t.Fatalf("setup: the installer ran %q times, want 2; screen:\n%s", got, screen(m))
	}
	f.requireSeeded(2)
	if t.Failed() {
		t.FailNow()
	}

	m = caretToRow(t, m, "Install again")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = handoffDrive(t, m, cmd, s, []string{"How will people sign in?", "Public Orbit origin"})
	if sc := screen(m); strings.Contains(sc, "How will people sign in?") || strings.Contains(sc, "Public Orbit origin") {
		t.Errorf("Install again asked for configuration again:\n%s", sc)
	}

	if got := f.runs(); got != "3" {
		t.Fatalf("Install again did not run the installer again (runs %q); screen:\n%s", got, screen(m))
	}
	f.requireSeeded(3)
	if n := f.prepares.Load(); n != 1 {
		t.Errorf("a configuration session was prepared %d times in one launcher session, want once", n)
	}
	if n := f.starts.Load(); n != 1 {
		t.Errorf("guided configuration ran %d times in one launcher session, want once", n)
	}
}

// 5. A new launcher session asks again: nothing is kept between
// sessions, neither in the folder nor in the next session's first tree.
// Old code: the first session left .env-orbit and .orbit-secrets in the
// folder.
func TestConfigHandoff_ANewSessionAsksAgain(t *testing.T) {
	f := newHandoffFixture(t, 2, tailFailsAfterCommit)
	before := handoffListing(t, f.dir)
	first, s := f.model(f.seams(true, nil))
	first = f.guidedConfiguration(first, s)
	if got := f.runs(); got != "2" {
		t.Fatalf("setup: the first session's installer ran %q times, want 2; screen:\n%s", got, screen(first))
	}
	requireSameListing(t, "after the first launcher session", before, handoffListing(t, f.dir))

	second, s2 := f.model(f.seams(true, nil))
	second, cmd := startInstallRun(t, second)
	second = handoffSettled(t, second, cmd, s2)
	if got := f.runs(); got != "3" {
		t.Fatalf("the second session's install did not run (runs %q); screen:\n%s", got, screen(second))
	}
	if tree := f.read("tree-3"); strings.Contains(tree, ".env-orbit") || strings.Contains(tree, ".orbit-secrets") {
		t.Errorf("a new launcher session handed in the previous session's answers:\n%s", tree)
	}
	if sc := screen(second); !strings.Contains(sc, "How will people sign in?") {
		t.Errorf("a new launcher session did not ask again:\n%s", sc)
	}
	if n := f.prepares.Load(); n != 2 {
		t.Errorf("configuration sessions prepared = %d over two launcher sessions, want 2", n)
	}
}

// 6. When the retry cannot be staged (its install.sh and configuration
// tree cannot be created, so nothing can be seeded) the folder is
// untouched. The session makes the temporary directory unusable once the
// first run is staged. Old code: adoption had already written into the
// folder before the retry's staging failed.
func TestConfigHandoff_StagingFailureLeavesTheFolderUntouched(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	t.Setenv("TMPDIR", os.TempDir()) // restored after the test
	f := newHandoffFixture(t, 1, tailSucceeds)
	before := handoffListing(t, f.dir)
	breakStaging := func() { os.Setenv("TMPDIR", missing) }
	m, s := f.model(f.seams(false, breakStaging))
	m, cmd := startInstallRun(t, m)
	m = handoffSettled(t, m, cmd, s)

	if got := f.runs(); got != "1" {
		t.Errorf("the installer ran %q times, want once (the retry could not be staged)", got)
	}
	if sc := screen(m); !strings.Contains(sc, "Install stopped") {
		t.Errorf("the staging failure is not on the stopped screen:\n%s", sc)
	}
	requireSameListing(t, "after a retry that could not be staged", before, handoffListing(t, f.dir))
}

// --- the configuration session's end, on a plain engineRun ----------------

// settleRun plays cmd and what follows through r, the way the program
// would, for a bounded number of steps.
func settleRun(t *testing.T, r engineRun, cmd tea.Cmd) engineRun {
	t.Helper()
	cmds := []tea.Cmd{cmd}
	for i := 0; len(cmds) > 0 && i < 50; i++ {
		next := cmds[0]
		cmds = cmds[1:]
		if next == nil {
			continue
		}
		msg := next()
		if batch, ok := msg.(tea.BatchMsg); ok {
			cmds = append(cmds, batch...)
			continue
		}
		if _, ok := msg.(tickMsg); ok || msg == nil {
			continue
		}
		var more tea.Cmd
		r, more = r.update(msg)
		cmds = append(cmds, more)
	}
	return r
}

// The session's end with nothing owed, through the real adoption: the
// collected files never reach the deployment folder. This is the inverse
// of TestConfigCollect_NothingOwedAdoptsIntoTheTargetAndRetries, which
// pins the old behaviour. Old code: .env-orbit is in the folder.
func TestConfigHandoff_NothingOwedLeavesTheFolderAlone(t *testing.T) {
	tree := t.TempDir()
	writeAnswers(t, tree)
	r := collectingRun(t, engineRunSeams{ // no adopt seam: the real one
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			return nil, nil, errors.New("stop after the retry starts")
		},
	})
	before := handoffListing(t, r.targetDir)
	r, cmd := r.update(configPlanMsg{plan: configPlan{configTree: tree, cleanup: func() {}}})
	r = settleRun(t, r, cmd)
	requireSameListing(t, "after the session's end", before, handoffListing(t, r.targetDir))
}

// Where a test injects adoptConfig, it must never be given the
// deployment folder as its destination. Old code: adoptConfig(tree,
// targetDir).
func TestConfigHandoff_InjectedAdoptIsNeverGivenTheFolder(t *testing.T) {
	tree := t.TempDir()
	writeAnswers(t, tree)
	var mu sync.Mutex
	var dests []string
	r := collectingRun(t, engineRunSeams{
		adoptConfig: func(_, dest string) error {
			mu.Lock()
			dests = append(dests, dest)
			mu.Unlock()
			return nil
		},
		prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
			return nil, nil, errors.New("stop after the retry starts")
		},
	})
	r, cmd := r.update(configPlanMsg{plan: configPlan{configTree: tree, cleanup: func() {}}})
	r = settleRun(t, r, cmd)
	mu.Lock()
	defer mu.Unlock()
	for _, d := range dests {
		if filepath.Clean(d) == filepath.Clean(r.targetDir) {
			t.Errorf("adoptConfig was given the deployment folder %s as its destination", d)
		}
	}
}
