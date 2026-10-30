package ui

// #277: Esc that lands while the deployment's configuration is being
// imported into the run's configuration tree (in TMPDIR) must leave
// nothing of the launcher's behind there. Before, the run's files were
// released on Esc while the import was still under way, and an empty
// configuration folder could stay in TMPDIR.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// launcherEntries lists the launcher's staged entries in dir: the
// install.sh copy and the configuration tree.
func launcherEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read TMPDIR: %v", err)
	}
	var found []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "orbit-launcher-install-") || strings.HasPrefix(e.Name(), "orbit-launcher-config-") {
			found = append(found, e.Name())
		}
	}
	return found
}

// importFixture is a TMPDIR holding the configuration tree install.sh
// handed over on a configuration refusal, and a deployment folder whose
// configuration (a .env-orbit and secrets) the session imports into it.
type importFixture struct {
	tmp, tree, target string
}

func newImportFixture(t *testing.T, secrets int) importFixture {
	t.Helper()
	f := importFixture{tmp: t.TempDir(), target: t.TempDir()}
	tree, err := os.MkdirTemp(f.tmp, "orbit-launcher-config-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(tree, 0o700); err != nil {
		t.Fatal(err)
	}
	f.tree = tree
	testutil.StageTree(t, tree, 0o700,
		testutil.File{Path: "scripts/configure.sh", Body: "#!/usr/bin/env bash\necho 'missing APP_URL'; exit 1\n", Mode: 0o600},
		testutil.File{Path: ".env-orbit.example", Body: "APP_URL=\n", Mode: 0o600})

	if err := os.WriteFile(filepath.Join(f.target, ".env-orbit"), []byte("ORBIT_IMAGE=x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(f.target, ".orbit-secrets"), 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < secrets; i++ {
		name := filepath.Join(f.target, ".orbit-secrets", fmt.Sprintf("secret-%04d", i))
		if err := os.WriteFile(name, []byte("secret\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// refusedRunIn is an Install of f.target whose engine handed over f.tree
// (the run's cleanup removes it, as the real one does) and refused for
// configuration: the run is in the session, and prepare is the command
// that prepares it.
func refusedRunIn(t *testing.T, f importFixture, seams engineRunSeams) (r engineRun, prepare tea.Cmd) {
	t.Helper()
	r = newEngineRun(deploy.Action("install"), f.target, "Install — Standard", "v9.9.9").withSeams(seams)
	r, _ = r.start(80, 26) // the engine's own start is not run: the ready message below stands for it
	r, _ = runUpdate(t, r, engineReadyMsg{
		stream:     &engine.Stream{C: make(chan any)},
		configTree: f.tree,
		cleanup:    func() error { return os.RemoveAll(f.tree) },
	})
	r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", "configuration-failure", "retry")})
	r, prepare = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{Err: errors.New("exit status 1"), ExitCode: 1}})
	if r.state != runConfigCollect || prepare == nil {
		t.Fatalf("setup: state = %v (prepare %v), want the configuration session being prepared", r.state, prepare != nil)
	}
	return r, prepare
}

// preparation is the session's preparation running as the event loop
// would run it: whatever it yields (a late plan may be dealt with inside
// and yield nothing) arrives on out, and done closes when it returns.
type preparation struct {
	out  chan tea.Msg
	done chan struct{}
}

func startPreparation(prepare tea.Cmd) preparation {
	p := preparation{out: make(chan tea.Msg, 256), done: make(chan struct{})}
	go func() {
		defer close(p.done)
		if msg := prepare(); msg != nil {
			p.out <- msg
		}
	}()
	return p
}

// awaitMoment waits until moment says Esc should land, failing if the
// preparation finishes first. The deadline is only for a preparation
// that hangs: a slow machine (CI under -race) can take well over 5s to
// copy the secrets a later round waits for.
func awaitMoment(t *testing.T, p preparation, moment func() bool) {
	t.Helper()
	for deadline := time.Now().Add(60 * time.Second); !moment(); time.Sleep(time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("setup: the moment for Esc never came")
		}
		select {
		case <-p.done:
			t.Fatal("setup: the preparation finished before Esc could land")
		default:
		}
	}
}

// pressEscAndSettle presses Esc and plays the loop until the preparation
// has finished and a little longer, so a late removal or re-creation is
// seen. afterEsc, if set, runs once Esc has been handled.
func pressEscAndSettle(t *testing.T, r engineRun, p preparation, afterEsc *gate) {
	t.Helper()
	r, next := r.update(key(tea.KeyEsc))
	runCmdInto(next, p.out)
	if afterEsc != nil {
		afterEsc.open()
	}
	done := p.done
	var settled <-chan time.Time
	deadline := time.After(10 * time.Second)
	for {
		select {
		case msg := <-p.out:
			if _, tick := msg.(tickMsg); tick {
				continue
			}
			var cmd tea.Cmd
			r, cmd = r.update(msg)
			runCmdInto(cmd, p.out)
		case <-done:
			done = nil
			settled = time.After(500 * time.Millisecond)
		case <-settled:
			return
		case <-deadline:
			t.Fatal("the preparation never finished after Esc")
		}
	}
}

func checkNothingLeft(t *testing.T, tmp string) {
	t.Helper()
	left := launcherEntries(t, tmp)
	if len(left) == 0 {
		return
	}
	var what []string
	for _, name := range left {
		inside, _ := os.ReadDir(filepath.Join(tmp, name))
		var names []string
		for _, e := range inside {
			names = append(names, e.Name())
		}
		if len(names) > 5 {
			names = append(names[:5], fmt.Sprintf("and %d more", len(names)-5))
		}
		what = append(what, name+" ["+strings.Join(names, " ")+"]")
	}
	t.Errorf("Esc during the configuration import left the launcher's files in TMPDIR: %s", strings.Join(what, ", "))
}

// Esc in the middle of the import: the deployment holds enough secrets
// that copying them takes a while, and Esc lands once the copy has
// begun, at a different point in each round (repeated, because one
// round can miss the moment by luck). The real preparation runs (no
// prepareConfig seam).
func TestLows_EscInTheMiddleOfTheConfigurationImportLeavesNothingInTMPDIR(t *testing.T) {
	for round := 0; round < 10; round++ {
		f := newImportFixture(t, 3000)
		r, prepare := refusedRunIn(t, f, engineRunSeams{})

		copied := 1 + round*250
		importing := func() bool {
			entries, err := os.ReadDir(filepath.Join(f.tree, ".orbit-secrets"))
			return err == nil && len(entries) >= copied
		}
		p := startPreparation(prepare)
		awaitMoment(t, p, importing)
		pressEscAndSettle(t, r, p, nil)
		if left := launcherEntries(t, f.tmp); len(left) > 0 {
			t.Logf("round %d (Esc after %d secrets copied):", round, copied)
			checkNothingLeft(t, f.tmp)
			return
		}
	}
}

// Esc just before the import: the real preparation is held until Esc has
// landed, then runs.
func TestLows_EscJustBeforeTheConfigurationImportLeavesNothingInTMPDIR(t *testing.T) {
	f := newImportFixture(t, 1)
	hold := newGate()
	var preparing atomic.Int32
	r, prepare := refusedRunIn(t, f, engineRunSeams{
		prepareConfig: func(ctx context.Context, target, configTree string) configPlanMsg {
			preparing.Add(1)
			hold.wait()
			return defaultPrepareConfig(ctx, target, configTree)
		},
	})
	p := startPreparation(prepare)
	awaitMoment(t, p, func() bool { return preparing.Load() > 0 })
	pressEscAndSettle(t, r, p, hold)
	if hold.timedOut() {
		t.Fatal("setup: the preparation was released before Esc")
	}
	checkNothingLeft(t, f.tmp)
}
