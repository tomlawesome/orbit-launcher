package ui

// Pins #227 (#200 judgement, finding C4; owner answer 9a): while Remove
// stands Orbit down, the launcher cannot be left. Ctrl-C and Esc do
// nothing and the working line carries Repair's second line, "can't be
// stopped from here". Before this, Ctrl-C quit and left `docker compose
// down` running with nobody watching. Once the result arrives keys work
// again, and the five-minute limit still ends on the failed screen.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// cannotStop is the sentence Repair's running screen carries; Remove's
// working line says the same thing in the same words.
const cannotStop = "can't be stopped from here"

// standDownKeys are the two keys a stand-down must ignore.
var standDownKeys = []struct {
	name string
	k    tea.KeyPressMsg
}{
	{"ctrl+c", ctrlC()},
	{"esc", key(tea.KeyEsc)},
}

// removeStandingDown is Remove with the stand-down started and its
// result not yet delivered; run is the command that would deliver it.
func removeStandingDown(t *testing.T, standDown func(context.Context, string) error) (RemoveModel, tea.Cmd) {
	t.Helper()
	m, run := updateAs(t, newTestRemoveModel(standDown), key(tea.KeyEnter)) // Stand down Orbit is preselected
	if m.state != removeStateStandingDown || run == nil {
		t.Fatalf("the stand-down did not start (state %v):\n%s", m.state, screen(m))
	}
	return m, run
}

// appStandingDown is the same through AppModel: Remove opened from the
// menu, the stand-down started, the result not yet delivered.
func appStandingDown(t *testing.T, standDown func(context.Context, string) error) (AppModel, tea.Cmd) {
	t.Helper()
	m := openRemove(t, leaveApp(t))
	if m.state != appStateRemove {
		t.Fatalf("Remove did not open (state %v):\n%s", m.state, screen(m))
	}
	m.remove.standDown = standDown
	m.remove.clipboard = io.Discard
	m, run := updateAs(t, m, key(tea.KeyEnter))
	if m.remove.state != removeStateStandingDown || run == nil {
		t.Fatalf("the stand-down did not start (state %v):\n%s", m.remove.state, screen(m))
	}
	return m, run
}

func standDownOK(context.Context, string) error { return nil }

// Ctrl-C quits nothing and Esc leaves nothing while the stand-down runs.
func TestStandDown_CtrlCAndEscDoNothingWhileItRuns(t *testing.T) {
	for _, c := range standDownKeys {
		t.Run(c.name, func(t *testing.T) {
			m, _ := removeStandingDown(t, standDownOK)
			before := screen(m)
			m, cmd := updateAs(t, m, c.k)
			if cmd != nil {
				t.Errorf("%s produced a command (quit = %v)", c.name, isQuit(cmd))
			}
			if m.state != removeStateStandingDown {
				t.Errorf("%s changed the state to %v", c.name, m.state)
			}
			if o := m.Outcome(); o.Done || o.WantsMenu {
				t.Errorf("%s ended the flow: %+v", c.name, o)
			}
			if s := screen(m); s == "" || s != before {
				t.Errorf("%s changed the screen:\n%s", c.name, s)
			}
		})
	}
}

// Pressing them again, and in any order, is no different.
func TestStandDown_RepeatedPressesStillDoNothing(t *testing.T) {
	m, _ := removeStandingDown(t, standDownOK)
	for i := 0; i < 3; i++ {
		var cmd tea.Cmd
		m, cmd = updateAs(t, m, ctrlC(), key(tea.KeyEsc), ctrlC())
		if cmd != nil || m.state != removeStateStandingDown || m.Outcome().Done {
			t.Fatalf("press %d: state = %v, quit = %v, outcome = %+v", i, m.state, isQuit(cmd), m.Outcome())
		}
	}
}

// The working line is Repair's: the stand-down line, then a second line
// saying it can't be stopped from here.
func TestStandDown_WorkingLineSaysItCannotBeStoppedFromHere(t *testing.T) {
	m, _ := removeStandingDown(t, standDownOK)
	lines := strings.Split(screen(m), "\n")
	working, second := -1, -1
	for i, l := range lines {
		if working < 0 && strings.Contains(l, "standing down Orbit…") {
			working = i
		}
		if strings.Contains(l, cannotStop) {
			second = i
		}
	}
	if working < 0 || second < 0 {
		t.Fatalf("working screen lacks the stand-down line or %q:\n%s", cannotStop, screen(m))
	}
	if second != working+1 {
		t.Errorf("%q is on line %d, want the line right under the stand-down line (%d):\n%s", cannotStop, second, working, screen(m))
	}
}

// The sentence is still there after the keys were pressed: refusing a
// key is not the same as having said why.
func TestStandDown_ScreenStillSaysItAfterTheKeys(t *testing.T) {
	m, _ := removeStandingDown(t, standDownOK)
	m, _ = updateAs(t, m, ctrlC(), key(tea.KeyEsc))
	if s := screen(m); !strings.Contains(s, "standing down Orbit…") || !strings.Contains(s, cannotStop) {
		t.Fatalf("after Ctrl-C and Esc the working screen reads:\n%s", s)
	}
}

// The confirm screen and the finished screens say nothing about being
// unable to stop: the sentence belongs to the working line alone.
func TestStandDown_OtherScreensDoNotSayIt(t *testing.T) {
	if s := screen(newTestRemoveModel(standDownOK)); strings.Contains(s, cannotStop) {
		t.Errorf("the confirm screen says it can't be stopped:\n%s", s)
	}
	for name, err := range map[string]error{"removed": nil, "failed": errors.New("docker daemon not running")} {
		m := removeFinished(t, err)
		if s := screen(m); strings.Contains(s, cannotStop) {
			t.Errorf("the %s screen says it can't be stopped:\n%s", name, s)
		}
	}
}

// Ctrl-C must not reach the running command either: the context the
// stand-down was given is never cancelled by the key, and the result of
// a stand-down that finishes after the keys is delivered as normal.
func TestStandDown_KeysDuringARealWaitNeitherCancelNorLoseTheResult(t *testing.T) {
	release := make(chan struct{})
	gotCtx := make(chan context.Context, 1)
	m, run := removeStandingDown(t, func(ctx context.Context, _ string) error {
		gotCtx <- ctx
		<-release
		return nil
	})

	result := make(chan tea.Msg, 1)
	go func() { result <- run() }()
	ctx := <-gotCtx

	m, cmd := updateAs(t, m, ctrlC(), key(tea.KeyEsc))
	if cmd != nil || m.state != removeStateStandingDown {
		t.Fatalf("keys during the wait acted: state = %v, quit = %v", m.state, isQuit(cmd))
	}
	select {
	case <-ctx.Done():
		t.Fatal("a key cancelled the running stand-down")
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	var msg tea.Msg
	select {
	case msg = <-result:
	case <-time.After(2 * time.Second):
		t.Fatal("the stand-down's result never arrived")
	}
	m, _ = updateAs(t, m, msg)
	if m.state != removeStateDone {
		t.Fatalf("state after the result = %v, want removeStateDone:\n%s", m.state, screen(m))
	}
}

// Keys work again once the result has arrived: on the removed screen
// Ctrl-C quits and Esc goes back to the menu.
func TestStandDown_KeysWorkAgainOnceItSucceeds(t *testing.T) {
	t.Run("ctrl+c quits", func(t *testing.T) {
		m, run := removeStandingDown(t, standDownOK)
		m, _ = updateAs(t, m, run())
		if m.state != removeStateDone {
			t.Fatalf("state = %v, want removeStateDone", m.state)
		}
		if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
			t.Fatal("Ctrl-C did not quit on the removed screen")
		}
	})
	t.Run("esc goes back to the menu", func(t *testing.T) {
		m, run := removeStandingDown(t, standDownOK)
		m, _ = updateAs(t, m, run())
		m, cmd := updateAs(t, m, key(tea.KeyEsc))
		if isQuit(cmd) || !leftForMenu(m.Outcome()) {
			t.Fatalf("Esc on the removed screen: quit = %v, outcome = %+v", isQuit(cmd), m.Outcome())
		}
	})
}

// A press made while it ran does not linger: it is not replayed when
// the result arrives.
func TestStandDown_APressDuringItIsNotRememberedAfterward(t *testing.T) {
	m, run := removeStandingDown(t, standDownOK)
	m, _ = updateAs(t, m, ctrlC(), key(tea.KeyEsc))
	m, cmd := updateAs(t, m, run())
	if cmd != nil || m.state != removeStateDone || m.Outcome().Done {
		t.Fatalf("after the result: state = %v, quit = %v, outcome = %+v", m.state, isQuit(cmd), m.Outcome())
	}
}

// The five-minute limit still ends on the failed screen, and the keys
// work there: the stand-down's own timeout error is a result like any.
func TestStandDown_TheTimeLimitStillEndsOnTheFailedScreen(t *testing.T) {
	limit := fmt.Errorf("docker compose down did not finish within 5m0s: %w", context.DeadlineExceeded)
	for _, c := range standDownKeys {
		t.Run(c.name, func(t *testing.T) {
			m, run := removeStandingDown(t, func(context.Context, string) error { return limit })
			m, _ = updateAs(t, m, c.k) // pressed while it ran: ignored
			m, _ = updateAs(t, m, run())
			if m.state != removeStateFailed {
				t.Fatalf("state = %v, want removeStateFailed:\n%s", m.state, screen(m))
			}
			s := screen(m)
			if !strings.Contains(s, "Could not stand down Orbit") || !strings.Contains(s, "did not finish within 5m0s") {
				t.Errorf("failed screen:\n%s", s)
			}
			if strings.Contains(s, cannotStop) {
				t.Errorf("the failed screen still says it can't be stopped:\n%s", s)
			}
		})
	}
	m, run := removeStandingDown(t, func(context.Context, string) error { return limit })
	m, _ = updateAs(t, m, run())
	if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
		t.Error("Ctrl-C did not quit on the failed screen")
	}
	if m, cmd := updateAs(t, m, key(tea.KeyEsc)); isQuit(cmd) || !leftForMenu(m.Outcome()) {
		t.Errorf("Esc on the failed screen: quit = %v, outcome = %+v", isQuit(cmd), m.Outcome())
	}
}

// --- Through AppModel -----------------------------------------------------

// Through the app the keys are the same nothing: the program is not
// quit and the menu does not come back.
func TestStandDown_AppIgnoresCtrlCAndEscWhileItRuns(t *testing.T) {
	for _, c := range standDownKeys {
		t.Run(c.name, func(t *testing.T) {
			m, _ := appStandingDown(t, standDownOK)
			before := screen(m)
			m, cmd := updateAs(t, m, c.k)
			if cmd != nil {
				t.Errorf("%s produced a command (quit = %v)", c.name, isQuit(cmd))
			}
			if m.state != appStateRemove || m.remove.state != removeStateStandingDown {
				t.Errorf("%s moved the app to state %v (remove %v)", c.name, m.state, m.remove.state)
			}
			if s := screen(m); s != before || strings.Contains(s, "O R B I T") {
				t.Errorf("%s changed the screen:\n%s", c.name, s)
			}
		})
	}
}

func TestStandDown_AppWorkingLineSaysItCannotBeStoppedFromHere(t *testing.T) {
	m, _ := appStandingDown(t, standDownOK)
	m, _ = updateAs(t, m, ctrlC())
	if s := screen(m); !strings.Contains(s, "standing down Orbit…") || !strings.Contains(s, cannotStop) {
		t.Fatalf("working screen through the app:\n%s", s)
	}
}

func TestStandDown_AppKeysWorkAgainOnceTheResultArrives(t *testing.T) {
	t.Run("ctrl+c quits", func(t *testing.T) {
		m, run := appStandingDown(t, standDownOK)
		m, _ = updateAs(t, m, ctrlC())
		m = drive(t, m, run)
		if m.remove.state != removeStateDone {
			t.Fatalf("remove state = %v, want removeStateDone", m.remove.state)
		}
		if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
			t.Fatal("Ctrl-C did not quit the program on the removed screen")
		}
	})
	t.Run("esc returns to the menu", func(t *testing.T) {
		m, run := appStandingDown(t, standDownOK)
		m, _ = updateAs(t, m, key(tea.KeyEsc))
		m = drive(t, m, run)
		m, cmd := updateAs(t, m, key(tea.KeyEsc))
		m = drive(t, m, cmd)
		if m.state != appStateSplash {
			t.Fatalf("Esc on the removed screen left the app in state %v:\n%s", m.state, screen(m))
		}
	})
}

func TestStandDown_AppTheTimeLimitStillEndsOnTheFailedScreen(t *testing.T) {
	limit := fmt.Errorf("docker compose down did not finish within 5m0s: %w", context.DeadlineExceeded)
	m, run := appStandingDown(t, func(context.Context, string) error { return limit })
	m, _ = updateAs(t, m, ctrlC(), key(tea.KeyEsc))
	m = drive(t, m, run)
	if m.state != appStateRemove || m.remove.state != removeStateFailed {
		t.Fatalf("app state %v, remove state %v; want the failed screen:\n%s", m.state, m.remove.state, screen(m))
	}
	if _, cmd := updateAs(t, m, ctrlC()); !isQuit(cmd) {
		t.Fatal("Ctrl-C did not quit the program on the failed screen")
	}
}
