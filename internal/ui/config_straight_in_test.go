package ui

// Pins #251 from the issue and the owner's decisions, not from the code.
// When the engine stops because it needs configuration only the person
// can give, the launcher goes STRAIGHT into guided configuration: there
// is no "Orbit needs your configuration" screen and no "Continue —
// guided configuration" row, and no key press is needed. Esc in the
// session (the sign-in-mode screen or a field) goes to the main menu and
// releases the run's files and the configuration process. The engine
// giving up mid-session lands on the failure screen, naming the field it
// stopped asking for and, if it had rejected an answer, the rejection's
// words.

import (
	"context"
	"errors"
	"io"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// The two flows that reach the engine's configuration stop; Repair never
// does.
var straightInFlows = []string{"Install", "Update"}

// removedConfigScreenText is what the removed screen showed.
var removedConfigScreenText = []string{"The engine stopped before touching anything", "Continue — guided configuration"}

func notShowingRemovedScreen(t *testing.T, s string) {
	t.Helper()
	for _, gone := range removedConfigScreenText {
		if strings.Contains(s, gone) {
			t.Errorf("the removed configuration screen is shown (%q):\n%s", gone, s)
		}
	}
}

// straightFlow is an Install or Update flow, wired the way AppModel wires
// it, that has been started and run against an engine that stops for
// configuration.
type straightFlow struct {
	t       *testing.T
	name    string
	model   tea.Model
	loop    *flowLoop
	cleaned atomic.Int32 // the run's own cleanup (the engine's files)
}

func (f *straightFlow) screen() string { return screen(f.model) }

func (f *straightFlow) runState() engineRunState {
	switch m := f.model.(type) {
	case InstallModel:
		return m.run.state
	case UpdateModel:
		return m.run.state
	}
	f.t.Fatalf("unexpected model %T", f.model)
	return 0
}

func (f *straightFlow) outcome() flowOutcome {
	return f.model.(interface{ Outcome() flowOutcome }).Outcome()
}

// press delivers one message to the flow and lets everything it sets off
// settle.
func (f *straightFlow) press(msg tea.Msg) {
	f.t.Helper()
	f.loop.settle(f.loop.update(msg))
}

// typeLine types text and presses Enter.
func (f *straightFlow) typeLine(text string) {
	f.t.Helper()
	for _, r := range text {
		f.loop.run(f.loop.update(runeKey(r)))
	}
	f.press(key(tea.KeyEnter))
}

// startStraightFlow starts the named flow against an engine that stops
// for configuration and settles; it presses nothing after the run starts.
// seams supplies the configuration session's seams; the engine and the
// handoff are filled in here.
func startStraightFlow(t *testing.T, name string, seams engineRunSeams) *straightFlow {
	t.Helper()
	f := &straightFlow{t: t, name: name}
	seams.prepareEngine = func(context.Context, string, string) (*engine.Stream, func() error, error) {
		msgs := configRefusalStream()
		ch := make(chan any, len(msgs))
		for _, m := range msgs {
			ch <- m
		}
		close(ch)
		return &engine.Stream{C: ch}, func() error { f.cleaned.Add(1); return nil }, nil
	}
	if seams.prepareInstall == nil {
		seams.prepareInstall = func(context.Context, string) (*exec.Cmd, func() error, error) {
			return exec.Command("true"), func() error { return nil }, nil
		}
		seams.runHandoff = fakeHandoff(nil)
	}
	seams.detect = fakeDetect("https://orbit.example.test")

	var queue *sink
	var cmd tea.Cmd
	switch name {
	case "Install":
		var m InstallModel
		m, queue = newTestInstallModel(seams)
		m, cmd = startInstallRun(t, m)
		f.model = m
	case "Update":
		var m UpdateModel
		m, queue = newTestUpdateModel(testDeployment(), seams)
		var updated tea.Model
		updated, cmd = m.Update(key(tea.KeyEnter))
		f.model = updated
	default:
		t.Fatalf("unknown flow %q", name)
	}
	f.loop = newFlowLoop(t, queue, func(msg tea.Msg) tea.Cmd {
		var next tea.Cmd
		f.model, next = f.model.Update(msg)
		return next
	})
	f.loop.settle(cmd)
	return f
}

// countingPlan is planned() with a count of the session's own cleanup
// (its staged .env-orbit), separate from the run's.
func countingPlan(needInit, needSecret bool, cleaned *atomic.Int32) prepareConfigFunc {
	return func(context.Context, string, string) configPlanMsg {
		return configPlanMsg{plan: configPlan{
			configTree: "/nonexistent-tree-for-tests",
			cleanup:    func() { cleaned.Add(1) },
			needInit:   needInit,
			needSecret: needSecret,
		}}
	}
}

// recordedConfig wraps scriptedConfig to keep every stream it starts.
type recordedConfig struct {
	inner   startConfigFunc
	streams []*engine.Stream
}

func (c *recordedConfig) start(dir string, step deploy.ConfigStep, mode deploy.AuthMode) (*engine.Stream, io.WriteCloser, error) {
	s, stdin, err := c.inner(dir, step, mode)
	if err == nil {
		c.streams = append(c.streams, s)
	}
	return s, stdin, err
}

func waitStopped(t *testing.T, s *engine.Stream) {
	t.Helper()
	if !closed(s.Done(), 5*time.Second) {
		t.Fatal("the configuration process was left running")
	}
}

func waitForRunCleanup(t *testing.T, f *straightFlow, want int32) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for f.cleaned.Load() != want && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := f.cleaned.Load(); got != want {
		t.Fatalf("the run's files were released %d times, want %d", got, want)
	}
}

// --- 1: straight in -----------------------------------------------------------

// The run itself: the engine's configuration stop is the end of the run
// and the start of the session, in one update, with no key press between.
func TestStraightIn_TheEnginesStopStartsTheSessionWithoutAKeyPress(t *testing.T) {
	for _, action := range []string{"install", "update"} {
		t.Run(action, func(t *testing.T) {
			r := startedRun(t, action, engineRunSeams{prepareConfig: planned(true, false)})
			r.state = runStreaming
			r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("configuration", "configuration", "failed", "configuration-failure", "retry")})
			r, cmd := runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1")}})
			if r.state != runConfigCollect {
				t.Fatalf("state = %v, want runConfigCollect (the session is being prepared)", r.state)
			}
			if cmd == nil {
				t.Fatal("no session preparation was started")
			}
			if r.Done {
				t.Fatal("the run ended at the stop instead of going on into the session")
			}
			notShowingRemovedScreen(t, runScreen(r))

			r, _ = runUpdate(t, r, cmd())
			if r.state != runConfigSignInMode {
				t.Fatalf("state = %v, want the sign-in-mode screen", r.state)
			}
			s := runScreen(r)
			if !strings.Contains(s, "How will people sign in?") {
				t.Fatalf("not on the sign-in question:\n%s", s)
			}
			notShowingRemovedScreen(t, s)
		})
	}
}

func TestStraightIn_GoesIntoGuidedConfigurationWithNoKeyPressed(t *testing.T) {
	for _, name := range straightInFlows {
		t.Run(name+" sign-in mode first", func(t *testing.T) {
			f := startStraightFlow(t, name, engineRunSeams{
				prepareConfig: planned(true, false),
				startConfig:   scriptedConfig(machineInitScript, machineSecretScript),
			})
			if f.runState() != runConfigSignInMode {
				t.Fatalf("run state = %v, want the sign-in-mode screen", f.runState())
			}
			s := f.screen()
			if !strings.Contains(s, "How will people sign in?") {
				t.Fatalf("not in guided configuration:\n%s", s)
			}
			notShowingRemovedScreen(t, s)
			if f.outcome().Done {
				t.Fatal("the flow finished instead of going into guided configuration")
			}

			// And on from there as before: the first field, still without
			// the removed screen.
			f.press(key(tea.KeyEnter)) // Local accounts
			s = f.screen()
			if !strings.Contains(s, "Public Orbit origin") {
				t.Fatalf("the first field never arrived:\n%s", s)
			}
			notShowingRemovedScreen(t, s)
		})

		t.Run(name+" first field when only the secret is owed", func(t *testing.T) {
			f := startStraightFlow(t, name, engineRunSeams{
				prepareConfig: planned(false, true),
				startConfig:   scriptedConfig(machineInitScript, machineSecretScript),
			})
			if f.runState() != runConfigCollect {
				t.Fatalf("run state = %v, want the session collecting", f.runState())
			}
			s := f.screen()
			if !strings.Contains(s, "OIDC client secret") {
				t.Fatalf("the first field never arrived:\n%s", s)
			}
			notShowingRemovedScreen(t, s)
		})
	}
}

// --- 2: Esc goes to the main menu ------------------------------------------------

func TestStraightIn_EscOnTheSignInModeGoesToTheMainMenu(t *testing.T) {
	for _, name := range straightInFlows {
		t.Run(name, func(t *testing.T) {
			var planCleaned atomic.Int32
			cfg := &recordedConfig{inner: scriptedConfig(machineInitScript, machineSecretScript)}
			f := startStraightFlow(t, name, engineRunSeams{
				prepareConfig: countingPlan(true, false, &planCleaned),
				startConfig:   cfg.start,
			})
			if !strings.Contains(f.screen(), "How will people sign in?") {
				t.Fatalf("not on the sign-in question:\n%s", f.screen())
			}
			if n := f.cleaned.Load(); n != 0 {
				t.Fatalf("the run's files were released %d times before Esc; the session still needs them", n)
			}

			f.press(key(tea.KeyEsc))

			if o := f.outcome(); !leftForMenu(o) {
				t.Fatalf("outcome after Esc = %+v, want back to the main menu", o)
			}
			notShowingRemovedScreen(t, f.screen())
			waitForRunCleanup(t, f, 1)
			if planCleaned.Load() == 0 {
				t.Error("the session's staged configuration was not cleaned up")
			}
			if len(cfg.streams) != 0 {
				t.Error("a configuration process was started although no sign-in mode was chosen")
			}
		})
	}
}

func TestStraightIn_EscOnAFieldGoesToTheMainMenuAndStopsTheProcess(t *testing.T) {
	for _, name := range straightInFlows {
		t.Run(name, func(t *testing.T) {
			var planCleaned atomic.Int32
			cfg := &recordedConfig{inner: scriptedConfig(machineInitScript, machineSecretScript)}
			f := startStraightFlow(t, name, engineRunSeams{
				prepareConfig: countingPlan(true, false, &planCleaned),
				startConfig:   cfg.start,
			})
			f.press(key(tea.KeyEnter)) // Local accounts
			if !strings.Contains(f.screen(), "Public Orbit origin") {
				t.Fatalf("the first field never arrived:\n%s", f.screen())
			}
			if len(cfg.streams) != 1 {
				t.Fatalf("%d configuration processes started, want 1", len(cfg.streams))
			}
			f.press(runeKey('h')) // half an answer, never to be sent

			f.press(key(tea.KeyEsc))

			if o := f.outcome(); !leftForMenu(o) {
				t.Fatalf("outcome after Esc = %+v, want back to the main menu", o)
			}
			notShowingRemovedScreen(t, f.screen())
			if s := f.screen(); strings.Contains(s, "Public Orbit origin") {
				t.Errorf("the field is still on screen after Esc:\n%s", s)
			}
			waitStopped(t, cfg.streams[0])
			waitForRunCleanup(t, f, 1)
			if planCleaned.Load() == 0 {
				t.Error("the session's staged configuration was not cleaned up")
			}
		})
	}
}

// --- 3: the engine gives up ------------------------------------------------------

// giveUpOnAppURLScript speaks the protocol and then gives up on a field
// without being rejected: the configure step exits with an error after
// its prompt, its input closed.
const giveUpOnAppURLScript = `
echo "prompt field=APP_URL kind=url required=true attempt=1"
echo "prompt-abort field=APP_URL"
exit 1`

// giveUpLaterScript accepts the origin and gives up on the issuer.
const giveUpLaterScript = `
echo "prompt field=APP_URL kind=url required=true attempt=1"
read -r a
echo "prompt-accept field=APP_URL"
echo "prompt field=OIDC_ISSUER kind=url required=true attempt=1"
echo "prompt-abort field=OIDC_ISSUER"
exit 1`

func labelOf(field string) string {
	label, _, _ := promptWords(field, "")
	return label
}

func TestStraightIn_TheEngineGivingUpShowsTheFailureScreen(t *testing.T) {
	cases := []struct {
		name string
		// script is the configure step; sso picks the SSO row first.
		script string
		sso    bool
		// answers are typed, in order, at the field shown.
		answers []string
		field   string
		// rejection is the words shown under the prompt after a rejection
		// ("" when the engine rejected nothing).
		rejection string
	}{
		{"no rejection", giveUpOnAppURLScript, false, nil, "APP_URL", ""},
		{"after rejections", machineInitScript, false, []string{"nope", "nope", "nope"}, "APP_URL", "must start with https://"},
		{"on a later field", giveUpLaterScript, true, []string{"https://orbit.example.test"}, "OIDC_ISSUER", ""},
	}
	for _, name := range straightInFlows {
		for _, c := range cases {
			t.Run(name+" "+c.name, func(t *testing.T) {
				cfg := &recordedConfig{inner: scriptedConfig(c.script, machineSecretScript)}
				f := startStraightFlow(t, name, engineRunSeams{
					prepareConfig: planned(true, false),
					startConfig:   cfg.start,
				})
				if c.sso {
					f.press(key(tea.KeyDown))
				}
				f.press(key(tea.KeyEnter)) // sign-in mode
				for _, a := range c.answers {
					f.typeLine(a)
				}

				s := f.screen()
				if f.runState() != runFailed {
					t.Fatalf("run state = %v, want the failure screen:\n%s", f.runState(), s)
				}
				if want := name + " stopped"; !strings.Contains(s, want) {
					t.Errorf("the failure screen is not titled %q:\n%s", want, s)
				}
				if want := "Orbit's setup stopped asking for " + labelOf(c.field) + "."; !strings.Contains(s, want) {
					t.Errorf("the failure screen lacks the reason %q:\n%s", want, s)
				}
				if c.rejection != "" && !strings.Contains(s, c.rejection) {
					t.Errorf("the rejection's words %q are missing:\n%s", c.rejection, s)
				}
				if c.rejection == "" && strings.Contains(s, "must start with") {
					t.Errorf("a rejection is reported although the engine rejected nothing:\n%s", s)
				}
				for _, row := range []string{"Open the guided installer", "Menu", "Exit"} {
					if !strings.Contains(s, row) {
						t.Errorf("the failure menu lacks %q:\n%s", row, s)
					}
				}
				notShowingRemovedScreen(t, s)
				if f.outcome().Done {
					t.Errorf("the flow finished on its own: %+v", f.outcome())
				}
				waitForRunCleanup(t, f, 1)
				if len(cfg.streams) == 0 {
					t.Fatal("no configuration process was started")
				}
				waitStopped(t, cfg.streams[len(cfg.streams)-1])
			})
		}
	}
}

// The failure screen's own menu still works: Menu goes to the main menu.
func TestStraightIn_TheGiveUpScreensMenuIsTheUsualOne(t *testing.T) {
	f := startStraightFlow(t, "Install", engineRunSeams{
		prepareConfig: planned(true, false),
		startConfig:   scriptedConfig(giveUpOnAppURLScript, machineSecretScript),
	})
	f.press(key(tea.KeyEnter)) // sign-in mode
	if f.runState() != runFailed {
		t.Fatalf("run state = %v, want the failure screen:\n%s", f.runState(), f.screen())
	}
	if got := screenLabels(f.screen()); len(got) != 3 || got[0] != "Open the guided installer" || got[1] != "Menu" || got[2] != "Exit" {
		t.Fatalf("failure rows = %q, want the usual three", got)
	}
	f.press(key(tea.KeyDown)) // Menu
	f.press(key(tea.KeyEnter))
	if o := f.outcome(); !leftForMenu(o) {
		t.Fatalf("outcome after Menu = %+v, want back to the main menu", o)
	}
}

// --- 4: other engine failures are unchanged --------------------------------------

func TestStraightIn_AnEngineFailureThatIsNotAConfigurationRefusalStillShowsTheFailureScreen(t *testing.T) {
	for _, action := range []string{"install", "update"} {
		t.Run(action, func(t *testing.T) {
			r := startedRun(t, action, engineRunSeams{})
			r.state = runStreaming
			r, _ = runUpdate(t, r, engineStreamMsg{msg: ev("identity", "image", "failed", "image-registry", "retry")})
			r, _ = runUpdate(t, r, engineStreamMsg{msg: engine.DoneMsg{ExitCode: 1, Err: errors.New("exit status 1")}})
			if r.state != runFailed {
				t.Fatalf("state = %v, want runFailed", r.state)
			}
			s := runScreen(r)
			if !strings.Contains(s, strings.ToUpper(action[:1])+action[1:]+" stopped") {
				t.Fatalf("not the failure screen:\n%s", s)
			}
			notShowingRemovedScreen(t, s)
		})
	}
}
