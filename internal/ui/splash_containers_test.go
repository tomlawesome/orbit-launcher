package ui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// #341 (revised): the splash's state word also requires every expected
// container to be running and healthy per Docker.

var fullNames = []string{"orbit-db", "orbit-app", "orbit-clamav", "orbit-tika", "orbit-ollama"}

// allRunning lists the first n of the full profile's services running.
func allRunning(n int) []deploy.ServiceState {
	out := make([]deploy.ServiceState, 0, n)
	for _, name := range fullNames[:n] {
		out = append(out, deploy.ServiceState{Service: name, State: "running"})
	}
	return out
}

// sameHealthMsg compares two messages; the faults are a slice, so the
// message is not == comparable. No faults is nil or empty alike.
func sameHealthMsg(a, b healthResultMsg) bool {
	if len(a.faults) == 0 && len(b.faults) == 0 {
		a.faults, b.faults = nil, nil
	}
	return reflect.DeepEqual(a, b)
}

func fault(service, word string) deploy.ServiceFault {
	return deploy.ServiceFault{Service: service, Word: word}
}

// openDegradedDrawer is a degraded splash with the caret on the word and
// the drawer open.
func openDegradedDrawer(t *testing.T, msg healthResultMsg) string {
	t.Helper()
	m := degradedSplash(t, msg)
	if m.state != stateDegraded {
		t.Fatalf("state = %v, want degraded for %+v", m.state, msg)
	}
	m = onInstall(t, m)
	m, _ = updateAs(t, m, key(tea.KeyUp))
	return screen(openDrawer(t, m))
}

func assertInOrder(t *testing.T, s string, texts ...string) {
	t.Helper()
	prev := -1
	for _, text := range texts {
		i := lineIndex(s, text)
		if i < 0 || i <= prev {
			t.Fatalf("%q is not beneath the previous row (row %d, previous %d):\n%s", text, i, prev, s)
		}
		prev = i
	}
}

// Case 3: the services are read on every HTTP answer.
func TestSplashProbe_ServicesAreReadOnEveryAnswer(t *testing.T) {
	for _, reason := range []deploy.HealthReason{
		deploy.HealthResponding, deploy.HealthMaintenance, deploy.HealthServerError,
		deploy.HealthNotResponding, deploy.HealthUnexpectedAnswer, deploy.HealthUnchecked,
	} {
		t.Run(string(reason), func(t *testing.T) {
			m, _ := probeModel(t, "full")
			if reason == deploy.HealthUnchecked {
				m.localURL = ""
			}
			m.healthProbe = func(context.Context, string) deploy.HealthReason { return reason }
			reads := 0
			m.serviceStates = func(string) ([]deploy.ServiceState, error) { reads++; return allRunning(5), nil }
			msg, ok := m.probeHealthCmd()().(healthResultMsg)
			if !ok {
				t.Fatal("not a healthResultMsg")
			}
			if reads != 1 {
				t.Errorf("services read %d times for %q, want 1", reads, reason)
			}
			if !msg.asked || !msg.counted || msg.running != 5 || msg.expected != 5 || len(msg.faults) != 0 {
				t.Errorf("message = %+v, want asked, 5 of 5 counted and no faults", msg)
			}
		})
	}
}

// Case 4: the two reads run in parallel.
func TestSplashProbe_TheTwoReadsRunInParallel(t *testing.T) {
	m, _ := probeModel(t, "")
	m.healthProbe = func(context.Context, string) deploy.HealthReason {
		time.Sleep(300 * time.Millisecond)
		return deploy.HealthResponding
	}
	m.serviceStates = func(string) ([]deploy.ServiceState, error) {
		time.Sleep(300 * time.Millisecond)
		return allRunning(3), nil
	}
	start := time.Now()
	msg, ok := m.probeHealthCmd()().(healthResultMsg)
	if !ok {
		t.Fatal("not a healthResultMsg")
	}
	if took := time.Since(start); took >= 500*time.Millisecond {
		t.Errorf("message took %s, want under 500ms (reads in parallel)", took)
	}
	if !msg.counted || msg.reason != deploy.HealthResponding {
		t.Errorf("message = %+v, want both reads in it", msg)
	}
}

// Case 5: services outside the expected set never appear.
func TestSplashProbe_ServicesOutsideTheSetNeverAppear(t *testing.T) {
	m, _ := probeModel(t, "") // db, app, clamav
	m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthResponding }
	m.serviceStates = func(string) ([]deploy.ServiceState, error) {
		return []deploy.ServiceState{
			{Service: "orbit-db", State: "running"},
			{Service: "orbit-app", State: "running"},
			{Service: "orbit-clamav", State: "running"},
			{Service: "orbit-tika", State: "exited"},
			{Service: "orbit-ollama-model-pull", State: "exited"},
		}, nil
	}
	msg := m.probeHealthCmd()().(healthResultMsg)
	if len(msg.faults) != 0 || msg.running != 3 || msg.expected != 3 || !msg.counted || !msg.asked {
		t.Errorf("message = %+v, want 3 of 3 and no faults", msg)
	}
}

// Case 5: faults come in expected order, with Repair's words.
func TestSplashProbe_FaultsFollowExpectedOrderAndWords(t *testing.T) {
	m, _ := probeModel(t, "full")
	m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthResponding }
	m.serviceStates = func(string) ([]deploy.ServiceState, error) {
		return []deploy.ServiceState{
			{Service: "orbit-ollama", State: "running", Health: "starting"},
			{Service: "orbit-clamav", State: "running", Health: "unhealthy"},
			{Service: "orbit-db", State: "running", Health: "healthy"},
		}, nil
	}
	msg := m.probeHealthCmd()().(healthResultMsg)
	want := []deploy.ServiceFault{
		fault("orbit-app", "missing"), fault("orbit-clamav", "unhealthy"),
		fault("orbit-tika", "missing"), fault("orbit-ollama", "starting"),
	}
	if !reflect.DeepEqual(msg.faults, want) || msg.running != 1 || msg.expected != 5 || !msg.asked || !msg.counted {
		t.Errorf("message = %+v, want faults %+v, 1 of 5", msg, want)
	}
}

// Case 6 (amended 2026-11-08): unreadable Docker (asked, not counted) with
// an HTTP failure is still degraded on the HTTP reason, Repair
// preselected, no count row.
func TestSplashDegraded_UnreadableDockerWithAnHTTPFailureShowsNoCount(t *testing.T) {
	msg := healthResultMsg{reason: deploy.HealthNotResponding, asked: true}
	if got := degradedSplash(t, msg).menu.sel; got != menuRepair {
		t.Errorf("selected = %d, want Repair", got)
	}
	s := openDegradedDrawer(t, msg)
	if !strings.Contains(s, "not responding") || strings.Contains(s, "services") {
		t.Errorf("want the HTTP reason and no services row:\n%s", s)
	}
}

// Case 6: state from faults.
func TestSplashState_FromFaults(t *testing.T) {
	unhealthy := []deploy.ServiceFault{fault("orbit-clamav", "unhealthy")}
	for name, c := range map[string]struct {
		msg   healthResultMsg
		state deployState
	}{
		"ready + fault":       {healthResultMsg{reason: deploy.HealthResponding, faults: unhealthy, counted: true, running: 2, expected: 3}, stateDegraded},
		"maintenance + fault": {healthResultMsg{reason: deploy.HealthMaintenance, faults: unhealthy, counted: true, running: 2, expected: 3}, stateDegraded},
		"ready + none":        {healthResultMsg{reason: deploy.HealthResponding, counted: true, running: 3, expected: 3}, stateAlive},
		"maintenance + none":  {healthResultMsg{reason: deploy.HealthMaintenance, counted: true, running: 3, expected: 3}, stateMaintenance},
		"not responding":      {healthResultMsg{reason: deploy.HealthNotResponding}, stateDegraded},
		// Amended 2026-11-08: a check that could not run with no fault is
		// can't tell, no longer degraded.
		"unchecked + none": {healthResultMsg{reason: deploy.HealthUnchecked, asked: true, counted: true, running: 3, expected: 3}, stateCantTell},
		// Amended 2026-11-08: not responding over unreadable Docker is degraded.
		"not responding, docker unreadable": {healthResultMsg{reason: deploy.HealthNotResponding, asked: true}, stateDegraded},
	} {
		t.Run(name, func(t *testing.T) {
			if got := degradedSplash(t, c.msg).state; got != c.state {
				t.Errorf("state = %v, want %v", got, c.state)
			}
		})
	}
	t.Run("ready + fault preselects Repair", func(t *testing.T) {
		if got := degradedSplash(t, healthResultMsg{reason: deploy.HealthResponding, faults: unhealthy}).menu.sel; got != menuRepair {
			t.Errorf("selected = %d, want Repair", got)
		}
	})
}

// Case 7: starting containers degrade the word but leave the caret.
func TestSplashState_StartingContainers(t *testing.T) {
	starting := []deploy.ServiceFault{fault("orbit-clamav", "starting")}
	mixed := []deploy.ServiceFault{fault("orbit-app", "unhealthy"), fault("orbit-clamav", "starting")}
	for name, c := range map[string]struct {
		msg  healthResultMsg
		want int
	}{
		"ready + starting stays on Update":       {healthResultMsg{reason: deploy.HealthResponding, faults: starting}, menuUpdate},
		"maintenance + starting stays":           {healthResultMsg{reason: deploy.HealthMaintenance, faults: starting}, menuUpdate},
		"could not check + starting stays":       {healthResultMsg{reason: deploy.HealthUnchecked, faults: starting}, menuUpdate},
		"ready + starting and unhealthy repairs": {healthResultMsg{reason: deploy.HealthResponding, faults: mixed}, menuRepair},
		"server error + starting repairs":        {healthResultMsg{reason: deploy.HealthServerError, faults: starting}, menuRepair},
		"not responding + starting repairs":      {healthResultMsg{reason: deploy.HealthNotResponding, faults: starting}, menuRepair},
	} {
		t.Run(name, func(t *testing.T) {
			m := degradedSplash(t, c.msg)
			if m.state != stateDegraded {
				t.Errorf("state = %v, want degraded", m.state)
			}
			if m.menu.sel != c.want {
				t.Errorf("selected = %d, want %d", m.menu.sel, c.want)
			}
		})
	}
}

// Case 7: a fault never overrides the other-folder rest on Exit.
func TestSplashState_FaultKeepsExitWhenAnotherFolderStartedIt(t *testing.T) {
	faults := []deploy.ServiceFault{fault("orbit-clamav", "unhealthy")}
	m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26})
	m.targetDir = "/opt/orbit"
	for _, order := range []string{"health first", "folder first"} {
		t.Run(order, func(t *testing.T) {
			health := healthResultMsg{reason: deploy.HealthResponding, faults: faults}
			folder := runningResultMsg{dir: m.targetDir, mismatch: folderMismatch(otherProduction)}
			msgs := []tea.Msg{health, folder}
			if order == "folder first" {
				msgs = []tea.Msg{folder, health}
			}
			got, _ := updateAs(t, m, msgs...)
			if got.state != stateDegraded || got.menu.sel != menuExit {
				t.Errorf("state = %v, selected = %d, want degraded with the caret on Exit", got.state, got.menu.sel)
			}
		})
	}
}

// Case 8: drawer rows and their order.
func TestSplashDegraded_DrawerRowsFollowTheFaults(t *testing.T) {
	t.Run("server error, one fault", func(t *testing.T) {
		s := openDegradedDrawer(t, healthResultMsg{
			reason: deploy.HealthServerError, faults: []deploy.ServiceFault{fault("orbit-app", "unhealthy")},
			running: 2, expected: 3, counted: true,
		})
		assertInOrder(t, s, "server error", "orbit-app unhealthy", "2 of 3 services running")
	})
	t.Run("ready, two faults, no reason row", func(t *testing.T) {
		s := openDegradedDrawer(t, healthResultMsg{
			reason:  deploy.HealthResponding,
			faults:  []deploy.ServiceFault{fault("orbit-clamav", "unhealthy"), fault("orbit-tika", "missing")},
			running: 3, expected: 5, counted: true,
		})
		assertInOrder(t, s, "orbit-clamav unhealthy", "orbit-tika missing", "3 of 5 services running")
		for _, reason := range []string{"server error", "not responding", "unexpected answer", "could not check"} {
			if strings.Contains(s, reason) {
				t.Errorf("a ready answer shows the reason %q:\n%s", reason, s)
			}
		}
	})
	// Amended 2026-11-08: ready over unreadable Docker, and could not
	// check with no faults, are can't tell: no drawer, no reason row, no
	// count row. The old expectation (could not check / 3 of 3) is gone.
	t.Run("ready, not counted, is can't tell with no drawer", func(t *testing.T) {
		m := degradedSplash(t, healthResultMsg{reason: deploy.HealthResponding, asked: true})
		if m.state != stateCantTell {
			t.Fatalf("state = %v, want can't tell", m.state)
		}
		s := screen(m)
		for _, text := range []string{"degraded", "could not check", "services running"} {
			if strings.Contains(s, text) {
				t.Errorf("screen shows %q:\n%s", text, s)
			}
		}
	})
	t.Run("could not check, no faults, is can't tell with no drawer", func(t *testing.T) {
		m := degradedSplash(t, healthResultMsg{reason: deploy.HealthUnchecked, asked: true, running: 3, expected: 3, counted: true})
		if m.state != stateCantTell {
			t.Fatalf("state = %v, want can't tell", m.state)
		}
		s := screen(m)
		for _, text := range []string{"could not check", "3 of 3 services running"} {
			if strings.Contains(s, text) {
				t.Errorf("screen shows %q:\n%s", text, s)
			}
		}
	})
	t.Run("could not check, one fault", func(t *testing.T) {
		s := openDegradedDrawer(t, healthResultMsg{
			reason: deploy.HealthUnchecked, faults: []deploy.ServiceFault{fault("orbit-tika", "stopped")},
			asked: true, running: 4, expected: 5, counted: true,
		})
		assertInOrder(t, s, "could not check", "orbit-tika stopped", "4 of 5 services running")
	})
}

// Case 9: the full-profile worst case still fits a 24-row terminal.
func TestSplashDegraded_WorstDrawerKeepsTheMenuOnScreen(t *testing.T) {
	var faults []deploy.ServiceFault
	for _, name := range fullNames {
		faults = append(faults, fault(name, "unhealthy"))
	}
	msg := healthResultMsg{reason: deploy.HealthServerError, faults: faults, expected: 5, counted: true}
	m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = updateAs(t, m, msg)
	m = onInstall(t, m)
	m, _ = updateAs(t, m, key(tea.KeyUp))
	s := screen(openDrawer(t, m))
	for _, item := range MainMenu {
		if !strings.Contains(s, item.Label) {
			t.Errorf("menu item %s is off screen:\n%s", item.Label, s)
		}
	}
	for _, f := range faults {
		if !strings.Contains(s, f.Service+" "+f.Word) {
			t.Errorf("drawer lacks %q:\n%s", f.Service+" "+f.Word, s)
		}
	}
	if got := len(strings.Split(strings.TrimRight(s, "\n"), "\n")); got > 26 {
		t.Errorf("screen is %d rows, want at most 26", got)
	}
}

// Case 10: fault rows are centred on the axis like the reason row.
func TestSplashDegraded_FaultRowsAreCentredOnTheAxis(t *testing.T) {
	s := openDegradedDrawer(t, healthResultMsg{
		reason: deploy.HealthResponding, faults: []deploy.ServiceFault{fault("orbit-clamav", "unhealthy")},
		running: 2, expected: 3, counted: true,
	})
	assertCentredWith(t, s, "orbit-clamav unhealthy", "O R B I T")
	assertCentredWith(t, s, "orbit-clamav unhealthy", "degraded")
}

// Case 10: no word before the message.
func TestSplashDegraded_NoWordBeforeTheMessage(t *testing.T) {
	m, _ := updateAs(t, seedDeployment(NewSplashModel()), tea.WindowSizeMsg{Width: 80, Height: 26})
	s := screen(m)
	for _, word := range []string{"alive", "degraded", "maintenance"} {
		if strings.Contains(s, word) {
			t.Errorf("the screen shows %q before the health message:\n%s", word, s)
		}
	}
}

// Case 13: nothing public is passed to any seam.
func TestSplashProbe_NoSeamSeesTheAppURL(t *testing.T) {
	m, dir := probeModel(t, "")
	m.appURL = "https://mail.example.test"
	var seen []string
	m.healthProbe = func(_ context.Context, u string) deploy.HealthReason {
		seen = append(seen, u)
		return deploy.HealthResponding
	}
	m.serviceStates = func(d string) ([]deploy.ServiceState, error) {
		seen = append(seen, d)
		return allRunning(3), nil
	}
	m.probeHealthCmd()()
	want := map[string]bool{m.localURL: true, dir: true}
	for _, s := range seen {
		if !want[s] {
			t.Errorf("a seam was handed %q, want only the local URL and the folder", s)
		}
	}
	if len(seen) != 2 {
		t.Errorf("seams called %d times, want 2", len(seen))
	}
}
