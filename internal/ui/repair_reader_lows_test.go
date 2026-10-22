package ui

// Pins the Repair and services-reader items of #277 (v0.4.3 Lows and
// Notes), from the findings alone:
//
//   - reading the services never holds the screen: with a reader that
//     takes two seconds, Repair's update and render return at once and the
//     services block says it is still checking; the rows replace it when
//     the answer comes;
//   - the splash shows a healthy deployment's status without waiting for
//     the services reader;
//   - Repair's refusal reason and an unknown plan result word reach the
//     screen as visibleText, never raw;
//   - the app takes the services reader as an option, WithServicesReader,
//     and both Repair and the splash read through it.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// readerSlow is how long a slow services reader takes; readerPrompt is
// how soon the screen must answer while it does.
const (
	readerSlow   = 2 * time.Second
	readerPrompt = time.Second
)

// checkingRow is the services block's row while the reader has not
// answered: it names the services and says they are still being checked.
func checkingRow(s string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "services") && strings.Contains(line, "checking") && !strings.Contains(line, "could not") {
			return strings.Trim(line, sky), true
		}
	}
	return "", false
}

// A services reader that takes two seconds does not hold Repair's screen:
// the diagnosis lands, renders and takes a key at once, showing that the
// services are still being checked; when the reader answers, its rows
// replace that row.
func TestRepairServices_ASlowReaderNeverHoldsTheScreen(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, deploy.EnvFile), []byte("APP_URL=http://localhost\nCOMPOSE_PROFILES=standard\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewRepairModel(dir, "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	var calls atomic.Int32
	m.serviceStates = func(string) ([]deploy.ServiceState, error) {
		calls.Add(1)
		time.Sleep(readerSlow)
		return up("orbit-db", "orbit-app", "orbit-clamav"), nil
	}

	start := time.Now()
	m = repairLines(t, m, "diagnosis result=healthy checked=15 skipped=0")
	m, cmd := repairDone(t, m, engine.DoneMsg{ExitCode: engine.ExitHealthy})
	s := screen(m)
	pressed, _ := updateAs(t, m, key(tea.KeyDown))
	_ = screen(pressed)
	if took := time.Since(start); took > readerPrompt {
		t.Errorf("the diagnosis took %v to land, render and take a key with a %v services reader; want under %v", took, readerSlow, readerPrompt)
	}
	if _, ok := checkingRow(s); !ok {
		t.Errorf("no services row saying they are still being checked:\n%s", s)
	}
	if rows := svcRows(t, m); len(rows) != 0 {
		t.Errorf("service rows shown before the reader answered: %v", rows)
	}

	m = drive(t, m, cmd)
	if n := calls.Load(); n != 1 {
		t.Errorf("the services were read %d times, want 1", n)
	}
	if row, ok := checkingRow(screen(m)); ok {
		t.Errorf("the checking row %q stayed after the reader answered:\n%s", row, screen(m))
	}
	got := fmt.Sprint(svcRows(t, m))
	if want := fmt.Sprint([]svcRow{{"✓", "orbit-db", "running"}, {"✓", "orbit-app", "running"}, {"✓", "orbit-clamav", "running"}}); got != want {
		t.Errorf("services rows after the answer = %v, want %v:\n%s", got, want, screen(m))
	}
}

// When the health probe says the deployment is healthy, the splash's
// status does not wait for a slow services reader.
func TestSplashProbe_HealthyDoesNotWaitForTheServicesReader(t *testing.T) {
	m, _ := probeModel(t, "full")
	m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthResponding }
	m.serviceStates = func(string) ([]deploy.ServiceState, error) {
		time.Sleep(readerSlow)
		return nil, nil
	}
	cmd := m.probeHealthCmd()
	if cmd == nil {
		t.Fatal("probeHealthCmd returned nil")
	}
	got := make(chan tea.Msg, 1)
	start := time.Now()
	go func() { got <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-got:
	case <-time.After(readerPrompt):
		t.Fatalf("the healthy probe's answer was still waiting after %v on a %v services reader", readerPrompt, readerSlow)
	}
	if took := time.Since(start); took > readerPrompt {
		t.Errorf("the healthy probe took %v", took)
	}
	res, ok := msg.(healthResultMsg)
	if !ok {
		t.Fatalf("probe produced %#v, want a healthResultMsg", msg)
	}
	if res.reason != deploy.HealthResponding {
		t.Errorf("reason = %q, want responding", res.reason)
	}
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26}, msg)
	if !strings.Contains(screen(m), "alive") {
		t.Errorf("the splash does not show the deployment alive:\n%s", screen(m))
	}
}

// The other side: a degraded deployment still counts its services.
func TestSplashProbe_DegradedStillCountsServices(t *testing.T) {
	m, _ := probeModel(t, "")
	m.healthProbe = func(context.Context, string) deploy.HealthReason { return deploy.HealthNotResponding }
	m.serviceStates = func(string) ([]deploy.ServiceState, error) { return up("orbit-db", "orbit-app"), nil }
	want := healthResultMsg{reason: deploy.HealthNotResponding, running: 2, expected: 3, counted: true}
	if got := m.probeHealthCmd()(); got != want {
		t.Errorf("message = %+v, want %+v", got, want)
	}
}

// ---- foreign words in Repair's outcome rows reach the screen escaped ----

// readerEscapeValues are the control and format runes of the finding:
// ESC (with a sequence after it), BEL and the right-to-left override.
// CR is left out: the engine's line parser splits a line at it.
var readerEscapeValues = []string{"\x1b[2JEVIL", "\aEVIL", "\u202eEVIL"}

// repairRefusedScreen is the outcome of a run the engine refused with
// reason, at width.
func repairRefusedScreen(t *testing.T, reason string, width int) RepairModel {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: screenEscapeHeight})
	m.state = repairExecuting
	m = repairLines(t, m, "execution result=refused done=0 failed=0 reason="+reason)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 6, Err: errors.New("exit status 6")})
	if !strings.Contains(screen(m), "Repair refused") {
		t.Fatalf("setup: the refused run did not reach its outcome screen:\n%s", screen(m))
	}
	return m
}

// repairPlanResultScreen is the plan screen for a plan whose summary
// carries the result word result, at width.
func repairPlanResultScreen(t *testing.T, result string, width int) RepairModel {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: screenEscapeHeight})
	m = repairLines(t, m,
		"plan action=restart-services resolves=stale-container mutation=reversible backup=not-required",
		"plan result="+result+" actions=0 manual=0",
	)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4})
	if !strings.Contains(screen(m), "nothing has run yet") {
		t.Fatalf("setup: the plan did not reach the plan screen:\n%s", screen(m))
	}
	return m
}

func TestRepairOutcome_ForeignWordsShowAsVisibleText(t *testing.T) {
	sites := []struct {
		name string
		base string
		draw func(t *testing.T, v string, width int) RepairModel
	}{
		{"refusal reason", "lock-held", repairRefusedScreen},
		{"unknown plan result", "blocked", repairPlanResultScreen},
	}
	for _, site := range sites {
		for _, width := range screenEscapeWidths {
			t.Run(fmt.Sprintf("%s/%d/ordinary", site.name, width), func(t *testing.T) {
				s := screen(site.draw(t, site.base, width))
				if !strings.Contains(s, site.base) {
					t.Errorf("the ordinary word %q is not on the screen:\n%s", site.base, s)
				}
				if strings.Contains(s, `\x`) || strings.Contains(s, `\u`) || strings.Contains(s, `\a`) {
					t.Errorf("an ordinary word gained escape text:\n%s", s)
				}
			})
			for _, suffix := range readerEscapeValues {
				v := site.base + suffix
				t.Run(fmt.Sprintf("%s/%d/%q", site.name, width, suffix), func(t *testing.T) {
					m := site.draw(t, v, width)
					s := screen(m)
					if want := visibleText(v); !strings.Contains(s, want) {
						t.Errorf("the screen lacks %q as text %q:\n%q", v, want, s)
					}
					if r := identityStray(s); r != 0 {
						t.Errorf("%U reached the screen:\n%q", r, s)
					}
					if strings.Contains(m.View().Content, suffix) {
						t.Errorf("the raw view holds %q unescaped", suffix)
					}
				})
			}
		}
	}
}

// ---- the app's services reader ----

// readerRecord is a fake services reader that notes every folder it was
// asked about and answers with have.
type readerRecord struct {
	mu    sync.Mutex
	asked []string
	have  []deploy.ServiceState
}

func (r *readerRecord) read(dir string) ([]deploy.ServiceState, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.asked = append(r.asked, dir)
	return r.have, nil
}

func (r *readerRecord) folders() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.asked)
}

var readerAppHave = []deploy.ServiceState{
	{Service: "orbit-db", State: "running", Health: "healthy"},
	{Service: "orbit-app", State: "restarting"},
	{Service: "orbit-clamav", State: "running", Health: "healthy"},
}

// Repair opened from the app reads the services through the reader the
// app was given, never the real `docker compose ps`.
func TestAppModel_WithServicesReaderIsWhatRepairReads(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
	reader := &readerRecord{have: readerAppHave}
	m := NewAppModelNoAnimation().WithServicesReader(reader.read)
	m.targetDir = dir
	m = m.WithDeploymentStatus(func(context.Context, string) deploy.HealthReason { return deploy.HealthResponding })
	m.flowSeams = engineRunSeams{prepareRepair: fakeRepairStream(`echo 'diagnosis result=healthy checked=1 skipped=0'; exit 0`)}
	queue := newSink()
	m = m.WithSender(queue.Send)
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})

	m = moveCaretTo(t, m, "Repair")
	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	m = drive(t, m, cmd)
	m = appDrainStream(t, m, queue)
	s := screen(m)
	if !strings.Contains(s, "Diagnosis clear") {
		t.Fatalf("repair did not finish:\n%s", s)
	}
	if got := reader.folders(); !slices.Contains(got, dir) || slices.ContainsFunc(got, func(d string) bool { return d != dir }) {
		t.Errorf("the app's reader was asked about %v, want only the deployment folder %q", got, dir)
	}
	if !strings.Contains(s, "orbit-app") || !strings.Contains(s, "restarting") {
		t.Errorf("the services block does not show the app reader's answer (orbit-app restarting):\n%s", s)
	}
}

// The splash's probe counts the services through the app's reader too.
func TestAppModel_WithServicesReaderIsWhatTheSplashCounts(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "APP_URL=https://mail.example.test\n")
	reader := &readerRecord{have: readerAppHave}
	m := NewAppModelNoAnimation().WithServicesReader(reader.read)
	m.targetDir = dir
	m = m.WithDeploymentStatus(func(context.Context, string) deploy.HealthReason { return deploy.HealthNotResponding })
	cmd := m.splash.probeHealthCmd()
	if cmd == nil {
		t.Fatal("the splash has no probe")
	}
	want := healthResultMsg{reason: deploy.HealthNotResponding, running: 2, expected: 3, counted: true}
	if got := cmd(); got != want {
		t.Errorf("probe message = %+v, want %+v", got, want)
	}
	if got := reader.folders(); len(got) != 1 || got[0] != dir {
		t.Errorf("the app's reader was asked about %v, want the deployment folder %q once", got, dir)
	}
}
