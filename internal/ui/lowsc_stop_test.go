package ui

// v0.4.3-c audit Lows (issue #277), written from the specification alone:
//
//   - B. The Install stopping line fits at every width from 40 to 80.
//   - D. The stream ending while the stopping screen shows leaves it for
//     the stopped screen at once.
//   - E. A signal after the installer disarmed its rollback still quits
//     the launcher once the install finishes.

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// B (#277): on the Install stopping screen before any phase=optional
// event, no rendered row is wider than the screen, from 40 to 80 columns.
func TestLowsC_B_TheStoppingLineFitsAtEveryWidthFrom40To80(t *testing.T) {
	for w := 40; w <= 80; w++ {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			r := startedRun(t, "install", engineRunSeams{})
			r, _ = runUpdate(t, r, tea.WindowSizeMsg{Width: w, Height: 26})
			r.state = runStopping
			s := ansi.Strip(r.view(w, 26))
			if !strings.Contains(s, "stopping the install") {
				t.Fatalf("not the stopping screen:\n%s", s)
			}
			for i, line := range strings.Split(s, "\n") {
				if got := lipgloss.Width(line); got > w {
					t.Errorf("row %d is %d cells at %d columns: %q", i, got, w, line)
				}
			}
		})
	}
}

// D (#277): the person asked the install to stop, the engine exits by
// itself and its stream closes without a final done message. The
// launcher leaves "stopping" for the stopped screen at once, with no
// second Ctrl-C.
func TestLowsC_D_StreamEndingWhileStoppingLeavesTheStoppingScreen(t *testing.T) {
	s := stoppableStream(t, stubbornScript)
	cleaned := 0
	r := streamingRunOn(t, "install", s, &cleaned)
	r, _ = runUpdate(t, r, ctrlC())
	if r.state != runStopping {
		t.Fatalf("state after Ctrl+C = %v, want runStopping", r.state)
	}

	r, _ = runUpdate(t, r, engineStreamEndedMsg{})
	screen := runScreen(r)
	if r.state == runStopping || strings.Contains(screen, "stopping the install") {
		t.Errorf("still on the stopping screen after the stream closed (state %v):\n%s", r.state, screen)
	}
	if r.state != runFailed {
		t.Errorf("state = %v, want the stopped screen (runFailed)", r.state)
	}
	if !strings.Contains(screen, "Install stopped") {
		t.Errorf("the stopped screen is not shown:\n%s", screen)
	}
}

// lowsCQuits reports whether cmd, or any command in the batches it
// expands to, yields tea.Quit. Each command gets a second to answer.
func lowsCQuits(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	got := make(chan tea.Msg, 1)
	go func() { got <- cmd() }()
	select {
	case msg := <-got:
		if msg == tea.Quit() {
			return true
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			var wg sync.WaitGroup
			var mu sync.Mutex
			quit := false
			for _, c := range batch {
				wg.Add(1)
				go func() {
					defer wg.Done()
					if lowsCQuits(c) {
						mu.Lock()
						quit = true
						mu.Unlock()
					}
				}()
			}
			wg.Wait()
			return quit
		}
	case <-time.After(time.Second):
	}
	return false
}

// E (#277): the installer has disarmed its rollback (a phase=optional
// event arrived) and a SignalQuitMsg (SIGHUP, SIGTERM or a first outside
// SIGINT) follows. It does not quit while the install still runs, but
// once the engine completes successfully the launcher quits instead of
// staying on the success screen.
func TestLowsC_E_ASignalAfterTheRollbackIsDisarmedQuitsWhenTheInstallCompletes(t *testing.T) {
	s := stoppableStream(t, optionalWaitScript)
	cleaned := 0
	run := streamingRunOn(t, "install", s, &cleaned)

	inst := NewInstallModel(t.TempDir(), "v9.9.9")
	inst.state = installStateRunning
	inst.run = run
	app := NewAppModelNoAnimation()
	app.state = appStateInstall
	app.install = inst

	app, cmd := updateAs(t, app, engineStreamMsg{msg: clamavStarting()})
	if lowsCQuits(cmd) {
		t.Fatal("setup: the optional event quit the app")
	}
	app, cmd = updateAs(t, app, SignalQuitMsg{})
	if lowsCQuits(cmd) {
		t.Fatal("the signal quit the app while the install was still running")
	}
	for _, msg := range []any{clamavWaiting(), installCompleted()} {
		app, cmd = updateAs(t, app, engineStreamMsg{msg: msg})
		if lowsCQuits(cmd) {
			t.Fatalf("the app quit on %#v, before the engine's final message", msg)
		}
	}

	app, cmd = updateAs(t, app, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 0}})
	if !lowsCQuits(cmd) {
		t.Errorf("the install completed after a signal and the app did not quit (state %v):\n%s", app.state, screen(app))
	}
}
