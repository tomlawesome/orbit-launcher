package ui

// #329: Remove makes its own running check when it opens, and "Stand down
// Orbit" waits for that answer, as Install does for its clash check (#318).
// The answer is removeRunningMsg{dir, otherFolder}; the splash's late
// runningResultMsg never releases the wait. A reader error, a timeout or no
// reader at all leave the stand-down as it was. Written from the design
// note's test specification (section 4) before the code.
//
// Mechanisms the note leaves open, chosen here:
//   - "the working line" is read off the spinner glyph: the confirm screen
//     shows none of style.SpinnerFrames otherwise, so the test does not
//     depend on the wording the fix picks for it;
//   - the stand-down fake counts calls and records the directory it was
//     given.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// standDownCounter is a stand-down fake that counts its calls and records
// the directory of the last one.
type standDownCounter struct {
	calls atomic.Int32
	dir   atomic.Value
}

func (c *standDownCounter) fn(_ context.Context, dir string) error {
	c.calls.Add(1)
	c.dir.Store(dir)
	return nil
}

// showsWorkingLine reports whether the screen carries a working line's
// spinner glyph.
func showsWorkingLine(s string) bool {
	for _, f := range style.SpinnerFrames {
		if strings.Contains(s, string(f)) {
			return true
		}
	}
	return false
}

// runningRemove is a Remove on its confirm in lateDir with the running
// reader read, and Init's two answers withheld: installed is the
// install-date answer, running is Remove's own running answer.
func runningRemove(t *testing.T, read runningReadFunc) (m RemoveModel, installed tea.Msg, running removeRunningMsg, sd *standDownCounter) {
	t.Helper()
	sd = &standDownCounter{}
	m = newTestRemoveModel(sd.fn)
	m.targetDir = lateDir
	m.runningRead = read
	m.lookupInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }
	var gotInstalled, gotRunning int
	for _, msg := range answersOf(m.Init()) {
		switch v := msg.(type) {
		case installedAtMsg:
			installed = v
			gotInstalled++
		case removeRunningMsg:
			running = v
			gotRunning++
		default:
			t.Fatalf("Init produced a %T, want the install date or Remove's running answer", msg)
		}
	}
	if gotInstalled != 1 || gotRunning != 1 {
		t.Fatalf("Init produced %d install-date answers and %d running answers, want one of each", gotInstalled, gotRunning)
	}
	return m, installed, running, sd
}

func assertStillWaiting(t *testing.T, m RemoveModel, cmd tea.Cmd, sd *standDownCounter) {
	t.Helper()
	if m.state != removeStateConfirm {
		t.Fatalf("state = %v, want the confirm to stay while the running answer is out", m.state)
	}
	if cmd != nil {
		t.Errorf("Enter started a command while the running answer was out")
	}
	if n := sd.calls.Load(); n != 0 {
		t.Errorf("the stand-down was called %d times while the running answer was out", n)
	}
	s := screen(m)
	if !strings.Contains(s, "Stand down Orbit") {
		t.Errorf("the confirm screen is gone:\n%s", s)
	}
	if !showsWorkingLine(s) {
		t.Errorf("the confirm screen shows no working line while it waits:\n%s", s)
	}
}

// --- (a) reproducing tests ---------------------------------------------

func TestRemoveRunning_EnterWaitsForTheAnswer(t *testing.T) {
	m, installed, running, sd := runningRemove(t, startedFrom(otherProduction))
	m, _ = updateAs(t, m, installed)

	m2, cmd := updateAs(t, m, key(tea.KeyEnter))

	assertStillWaiting(t, m2, cmd, sd)

	m2, cmd = updateAs(t, m2, running)

	assertRefusal(t, m2, otherProduction, refuseRemove, "Stand down Orbit")
	if cmd != nil {
		t.Errorf("the refusal started a command")
	}
	if n := sd.calls.Load(); n != 0 {
		t.Errorf("the stand-down was called %d times", n)
	}
}

func TestRemoveRunning_AnswerAfterTheSplashSaidNothing(t *testing.T) {
	dir := installedFolder(t)
	var calls atomic.Int32
	m := started(t, folderApp(t, dir, 80, 26, deploy.HealthResponding,
		reading(func(ctx context.Context, d *deploy.Deployment) (deploy.RunningApp, error) {
			if calls.Add(1) == 1 {
				return deploy.RunningApp{}, nil // the splash's check: no container
			}
			return startedFrom(otherProduction)(ctx, d)
		})))
	if s := screen(m); strings.Contains(s, otherFolderRow) {
		t.Fatalf("setup: the splash already knows the other folder:\n%s", s)
	}
	m = moveCaretTo(t, m, "Remove")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if m.state != appStateRemove {
		t.Fatalf("setup: Enter on Remove did not open Remove (state %v)", m.state)
	}
	sd := &standDownCounter{}
	m.remove.standDown = sd.fn

	// Remove's size arrives in its opening batch, so its confirm is drawn
	// only once that batch is fed, minus the running answer held back.
	var withheld []removeRunningMsg
	for _, msg := range answersOf(cmd) {
		if r, ok := msg.(removeRunningMsg); ok {
			withheld = append(withheld, r)
			continue
		}
		m, _ = updateAs(t, m, msg)
	}
	if len(withheld) != 1 {
		t.Fatalf("setup: Remove's opening produced %d running answers, want 1", len(withheld))
	}
	if !strings.Contains(screen(m), "Stand down Orbit") {
		t.Fatalf("setup: Remove did not draw its confirm:\n%s", screen(m))
	}

	m, cmd = updateAs(t, m, key(tea.KeyEnter))

	if m.state != appStateRemove || m.remove.state != removeStateConfirm {
		t.Fatalf("state = %v / %v, want the confirm to stay while the answer is out:\n%s", m.state, m.remove.state, screen(m))
	}
	if cmd != nil {
		t.Errorf("Enter started a command while the answer was out")
	}
	if n := sd.calls.Load(); n != 0 {
		t.Errorf("the stand-down was called %d times while the answer was out", n)
	}
	if s := screen(m); !strings.Contains(s, "Stand down Orbit") || !showsWorkingLine(s) {
		t.Errorf("the confirm should show its rows and a working line:\n%s", s)
	}

	m, _ = updateAs(t, m, withheld[0])

	if m.state != appStateRemove {
		t.Fatalf("state = %v, want the remove flow's refusal", m.state)
	}
	assertRefusal(t, m, otherProduction, refuseRemove, "Stand down Orbit")
	if n := sd.calls.Load(); n != 0 {
		t.Errorf("the stand-down was called %d times", n)
	}
}

// The splash's "nothing" answer is not Remove's: only removeRunningMsg
// releases the wait.
func TestRemoveRunning_SplashLateNoneDoesNotReleaseTheWait(t *testing.T) {
	m, _, running, sd := runningRemove(t, startedFrom(otherProduction))
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	assertStillWaiting(t, m, cmd, sd)

	m, cmd = updateAs(t, m, runningResultMsg{dir: lateDir})

	assertStillWaiting(t, m, cmd, sd)

	m, _ = updateAs(t, m, running)

	assertRefusal(t, m, otherProduction, refuseRemove, "Stand down Orbit")
	if n := sd.calls.Load(); n != 0 {
		t.Errorf("the stand-down was called %d times", n)
	}
}

// --- (b) legitimate paths still proceed --------------------------------

func TestRemoveRunning_SameFolderCarriesOnAsToday(t *testing.T) {
	readers := []struct {
		name string
		read runningReadFunc
	}{
		{"this folder", startedFrom(lateDir)},
		{"no container", clashAnswering(deploy.RunningApp{}, nil)},
		{"cannot run", clashAnswering(deploy.RunningApp{}, errors.New("no daemon"))},
	}
	assertStandsDown := func(t *testing.T, m RemoveModel, cmd tea.Cmd, sd *standDownCounter) {
		t.Helper()
		if m.state != removeStateStandingDown {
			t.Fatalf("state = %v, want the stand-down started:\n%s", m.state, screen(m))
		}
		if cmd == nil {
			t.Fatal("no command, want the stand-down")
		}
		cmd()
		if n := sd.calls.Load(); n != 1 {
			t.Errorf("the stand-down was called %d times, want once", n)
		}
		if got := sd.dir.Load(); got != "/opt/orbit" {
			t.Errorf("the stand-down was given %v, want /opt/orbit", got)
		}
	}
	for _, c := range readers {
		t.Run(c.name, func(t *testing.T) {
			m, installed, running, sd := runningRemove(t, c.read)
			m, _ = updateAs(t, m, installed, running)

			m, cmd := updateAs(t, m, key(tea.KeyEnter))

			assertStandsDown(t, m, cmd, sd)
		})
	}
	t.Run("a waiting Enter carries on", func(t *testing.T) {
		m, installed, running, sd := runningRemove(t, clashAnswering(deploy.RunningApp{}, nil))
		m, _ = updateAs(t, m, installed)
		m, cmd := updateAs(t, m, key(tea.KeyEnter))
		assertStillWaiting(t, m, cmd, sd)

		m, cmd = updateAs(t, m, running)

		assertStandsDown(t, m, cmd, sd)
		// The stand-down screen has its own working line, so look for
		// the confirm's waiting text, not any spinner.
		if strings.Contains(screen(m), removeChecking) {
			t.Errorf("the confirm's working line should be gone:\n%s", screen(m))
		}
	})
}

func TestRemoveRunning_NoReaderAsksNothing(t *testing.T) {
	sd := &standDownCounter{}
	m := newTestRemoveModel(sd.fn)
	m.lookupInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }

	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init must still start the install-date lookup")
	}
	msg := cmd()
	if _, batched := msg.(tea.BatchMsg); batched {
		t.Error("Init batched commands, want the install-date lookup alone")
	}
	if _, ok := msg.(installedAtMsg); !ok {
		t.Errorf("Init answered with a %T, want the install date", msg)
	}

	m2, run := updateAs(t, m, key(tea.KeyEnter))

	if m2.state != removeStateStandingDown || run == nil {
		t.Fatalf("state = %v, cmd nil = %v, want the stand-down started at once", m2.state, run == nil)
	}
	run()
	if n := sd.calls.Load(); n != 1 {
		t.Errorf("the stand-down was called %d times, want once", n)
	}
}

func TestRemoveRunning_LeavingWhileWaitingNeverStandsDown(t *testing.T) {
	answers := []struct {
		name string
		msg  func(removeRunningMsg) removeRunningMsg
	}{
		{"a clean answer", func(r removeRunningMsg) removeRunningMsg { r.otherFolder = ""; return r }},
		{"a folder answer", func(r removeRunningMsg) removeRunningMsg { r.otherFolder = otherProduction; return r }},
	}
	leaves := []struct {
		name  string
		keys  []tea.KeyPressMsg
		check func(*testing.T, RemoveModel, tea.Cmd)
	}{
		{"Esc", []tea.KeyPressMsg{key(tea.KeyEsc)}, func(t *testing.T, m RemoveModel, _ tea.Cmd) {
			if !leftForMenu(m.Outcome()) {
				t.Errorf("Outcome = %+v, want back to the menu", m.Outcome())
			}
		}},
		{"Cancel", []tea.KeyPressMsg{key(tea.KeyDown), key(tea.KeyEnter)}, func(t *testing.T, m RemoveModel, _ tea.Cmd) {
			if !leftForMenu(m.Outcome()) {
				t.Errorf("Outcome = %+v, want back to the menu", m.Outcome())
			}
		}},
		{"Ctrl-C", []tea.KeyPressMsg{ctrlC()}, func(t *testing.T, m RemoveModel, cmd tea.Cmd) {
			if !isQuit(cmd) || m.state != removeStateCancelled {
				t.Errorf("Ctrl-C: state = %v, quit = %v, want the flow cancelled and quitting", m.state, isQuit(cmd))
			}
		}},
	}
	for _, l := range leaves {
		for _, a := range answers {
			t.Run(l.name+" then "+a.name, func(t *testing.T) {
				m, installed, running, sd := runningRemove(t, startedFrom(otherProduction))
				m, _ = updateAs(t, m, installed)
				m, cmd := updateAs(t, m, key(tea.KeyEnter))
				assertStillWaiting(t, m, cmd, sd)

				var leaveCmd tea.Cmd
				for _, k := range l.keys {
					m, leaveCmd = updateAs(t, m, k)
				}
				l.check(t, m, leaveCmd)

				m, cmd = updateAs(t, m, a.msg(running))

				if cmd != nil {
					t.Errorf("the answer after leaving started a command")
				}
				if m.state == removeStateStandingDown {
					t.Errorf("the answer after leaving began the stand-down")
				}
				if n := sd.calls.Load(); n != 0 {
					t.Errorf("the stand-down was called %d times", n)
				}
			})
		}
	}
}

func TestRemoveRunning_CaretMoveDropsTheWait(t *testing.T) {
	m, installed, running, sd := runningRemove(t, clashAnswering(deploy.RunningApp{}, nil))
	m, _ = updateAs(t, m, installed)
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	assertStillWaiting(t, m, cmd, sd)

	m, _ = updateAs(t, m, key(tea.KeyDown)) // to Cancel
	if caretLabel(screen(m)) != "Cancel" {
		t.Fatalf("setup: the caret is on %q, want Cancel:\n%s", caretLabel(screen(m)), screen(m))
	}
	if showsWorkingLine(screen(m)) {
		t.Errorf("the working line should be gone once the caret moved:\n%s", screen(m))
	}

	m, cmd = updateAs(t, m, running)

	if cmd != nil || m.state != removeStateConfirm {
		t.Errorf("the clean answer after the caret moved: state = %v, cmd nil = %v, want the confirm left alone", m.state, cmd == nil)
	}
	if n := sd.calls.Load(); n != 0 {
		t.Errorf("the stand-down was called %d times", n)
	}
}

// The shared body's time.After branch: a reader that never answers is
// answered "" after deploy.RunningAppTimeout, and the stand-down proceeds.
func TestRemoveRunning_TimeoutProceeds(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for deploy.RunningAppTimeout")
	}
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	read := func(ctx context.Context, _ *deploy.Deployment) (deploy.RunningApp, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return deploy.RunningApp{Found: true, Image: recordedImage, Folder: otherProduction, State: "running"}, nil
	}

	sd := &standDownCounter{}
	m := newTestRemoveModel(sd.fn)
	m.targetDir = lateDir
	m.runningRead = read
	m.lookupInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }

	got := make(chan removeRunningMsg, 1)
	// Init is called here, not in the goroutine: the goroutine must not
	// read m while the Enter below replaces it.
	initCmd := m.Init()
	go func() {
		for _, msg := range answersOf(initCmd) {
			if r, ok := msg.(removeRunningMsg); ok {
				got <- r
			}
		}
	}()
	m, _ = updateAs(t, m, key(tea.KeyEnter))
	if m.state != removeStateConfirm {
		t.Fatalf("setup: Enter did not wait (state %v)", m.state)
	}

	select {
	case r := <-got:
		if r.otherFolder != "" {
			t.Fatalf("the timed-out check named %q, want no folder", r.otherFolder)
		}
		var cmd tea.Cmd
		m, cmd = updateAs(t, m, r)
		if m.state != removeStateStandingDown || cmd == nil {
			t.Errorf("state = %v, cmd nil = %v, want the stand-down started", m.state, cmd == nil)
		}
	case <-time.After(deploy.RunningAppTimeout + time.Second):
		t.Fatal("the running check did not answer within deploy.RunningAppTimeout + 1s")
	}
}
