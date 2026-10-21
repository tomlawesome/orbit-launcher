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
	"sync"
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
	// engineStarts counts the engine runs this flow asked for. Past
	// maxEngineStarts the engine fails to prepare, so a flow that keeps
	// retrying stops instead of looping for ever.
	engineStarts atomic.Int32
}

const maxEngineStarts = 4

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

// run is the engine run the flow holds.
func (f *straightFlow) run() engineRun {
	switch m := f.model.(type) {
	case InstallModel:
		return m.run
	case UpdateModel:
		return m.run
	}
	f.t.Fatalf("unexpected model %T", f.model)
	return engineRun{}
}

// atRest reports whether the flow is waiting for the operator or is over:
// the sign-in question or a prompt is open, the failure screen is up, or
// the flow has left. A key that opens the next prompt is not at rest until
// that prompt has arrived, so a test types the next answer only once it
// can be taken (a keystroke sent before the prompt opens is dropped).
func (f *straightFlow) atRest() bool {
	if f.outcome().Done {
		return true
	}
	r := f.run()
	switch r.state {
	case runConfigSignInMode, runFailed, runSucceeded, runLeft:
		return true
	case runConfigCollect:
		return r.cfg.session.prompt != nil
	}
	return false
}

const atRestWhat = "the flow to be waiting for the operator again"

// press delivers one message to the flow and waits until it is at rest
// again. Where the key's effect is a different place (the failure screen
// after an engine that gives up, say, which passes through an open prompt)
// use pressUntil.
func (f *straightFlow) press(msg tea.Msg) {
	f.t.Helper()
	f.pressUntil(msg, atRestWhat, f.atRest)
}

// pressUntil delivers one message to the flow and waits for cond.
func (f *straightFlow) pressUntil(msg tea.Msg, what string, cond func() bool) {
	f.t.Helper()
	f.loop.settle(f.loop.update(msg), what, cond)
}

// typeLine types text and presses Enter, waiting for the flow to be at
// rest again.
func (f *straightFlow) typeLine(text string) {
	f.t.Helper()
	f.typeLineUntil(text, atRestWhat, f.atRest)
}

// typeLineUntil types text and presses Enter, then waits for cond.
func (f *straightFlow) typeLineUntil(text, what string, cond func() bool) {
	f.t.Helper()
	for _, r := range text {
		f.loop.run(f.loop.update(runeKey(r)))
	}
	f.pressUntil(key(tea.KeyEnter), what, cond)
}

// failed reports whether the flow is on its failure screen.
func (f *straightFlow) failed() bool { return f.runState() == runFailed }

// driveToFailure picks the sign-in row, types answers at the prompts as
// they open, and waits for the failure screen the engine's giving up ends
// on. The last step waits for the failure itself, not for a rest: the
// engine's prompt can be on screen just before it gives up.
func (f *straightFlow) driveToFailure(sso bool, answers []string) {
	f.t.Helper()
	if sso {
		f.press(key(tea.KeyDown))
	}
	if len(answers) == 0 {
		f.pressUntil(key(tea.KeyEnter), "the failure screen", f.failed) // sign-in mode
		return
	}
	f.press(key(tea.KeyEnter)) // sign-in mode: the first prompt opens
	for i, a := range answers {
		if i < len(answers)-1 {
			f.typeLine(a)
		} else {
			f.typeLineUntil(a, "the failure screen", f.failed)
		}
	}
}

// startStraightFlow starts the named flow against an engine that stops
// for configuration and waits until it is at rest; it presses nothing
// after the run starts. seams supplies the configuration session's seams;
// the engine and the handoff are filled in here.
func startStraightFlow(t *testing.T, name string, seams engineRunSeams) *straightFlow {
	t.Helper()
	return startStraightFlowUntil(t, name, seams, atRestWhat, (*straightFlow).atRest)
}

// startStraightFlowUntil is startStraightFlow waiting for cond instead of
// for rest, for a test whose seam holds the flow somewhere else.
func startStraightFlowUntil(t *testing.T, name string, seams engineRunSeams, what string, cond func(*straightFlow) bool) *straightFlow {
	t.Helper()
	f := &straightFlow{t: t, name: name}
	seams.prepareEngine = func(context.Context, string, string) (*engine.Stream, func() error, error) {
		if f.engineStarts.Add(1) > maxEngineStarts {
			return nil, nil, errors.New("test bound: the engine was started too many times")
		}
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
		// Every screen the flow shows, not only the ones a test stops to
		// look at: the removed screen would be on the way, not at the end.
		notShowingRemovedScreen(t, screen(f.model))
		return next
	})
	f.loop.settle(cmd, what, func() bool { return cond(f) })
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
				f.driveToFailure(c.sso, c.answers)

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
	f.driveToFailure(false, nil)
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

// --- 5: a second refusal after configuration (review F1) -------------------------

// stillIncomplete is the failure screen's sentence when the engine
// refuses for configuration again after guided configuration.
const stillIncomplete = "Orbit still says the configuration is incomplete."

// countedPlan is planned() with a count of how many guided-configuration
// sessions were prepared.
func countedPlan(needInit, needSecret bool, sessions *atomic.Int32) prepareConfigFunc {
	inner := planned(needInit, needSecret)
	return func(ctx context.Context, dir, tree string) configPlanMsg {
		sessions.Add(1)
		return inner(ctx, dir, tree)
	}
}

func countedAdopt(adopted *atomic.Int32) func(string, string) error {
	return func(string, string) error { adopted.Add(1); return nil }
}

// expectStillIncomplete fails unless the flow is on the failure screen
// with the second-refusal sentence and the usual menu.
func expectStillIncomplete(t *testing.T, f *straightFlow) {
	t.Helper()
	s := f.screen()
	if f.runState() != runFailed {
		t.Fatalf("run state = %v, want the failure screen:\n%s", f.runState(), s)
	}
	if want := f.name + " stopped"; !strings.Contains(s, want) {
		t.Errorf("the failure screen is not titled %q:\n%s", want, s)
	}
	if !strings.Contains(s, stillIncomplete) {
		t.Errorf("the failure screen lacks %q:\n%s", stillIncomplete, s)
	}
	if got := screenLabels(s); len(got) != 3 || got[0] != "Open the guided installer" || got[1] != "Menu" || got[2] != "Exit" {
		t.Errorf("failure rows = %q, want the usual three", got)
	}
	if strings.Contains(s, "How will people sign in?") {
		t.Errorf("guided configuration started again:\n%s", s)
	}
	if f.outcome().Done {
		t.Errorf("the flow finished on its own: %+v", f.outcome())
	}
}

// Guided configuration found nothing left to collect, adopted and
// retried; the retry refused for configuration again. One retry, then
// the failure screen: no second session, no third engine run.
func TestStraightIn_ASecondRefusalAfterNothingToCollectShowsTheFailureScreen(t *testing.T) {
	for _, name := range straightInFlows {
		t.Run(name, func(t *testing.T) {
			var sessions, adopted atomic.Int32
			f := startStraightFlow(t, name, engineRunSeams{
				prepareConfig: countedPlan(false, false, &sessions),
				adoptConfig:   countedAdopt(&adopted),
				startConfig:   scriptedConfig(machineInitScript, machineSecretScript),
			})
			expectStillIncomplete(t, f)
			if n := sessions.Load(); n != 1 {
				t.Errorf("guided configuration was prepared %d times, want once", n)
			}
			if n := adopted.Load(); n != 1 {
				t.Errorf("the configuration was adopted %d times, want once", n)
			}
			if n := f.engineStarts.Load(); n != 2 {
				t.Errorf("the engine ran %d times, want twice (the first run and one retry)", n)
			}
		})
	}
}

// The same rule after a session where the person answered fields.
func TestStraightIn_ASecondRefusalAfterAnsweredFieldsShowsTheFailureScreen(t *testing.T) {
	for _, name := range straightInFlows {
		t.Run(name, func(t *testing.T) {
			var sessions, adopted atomic.Int32
			f := startStraightFlow(t, name, engineRunSeams{
				prepareConfig: countedPlan(true, false, &sessions),
				adoptConfig:   countedAdopt(&adopted),
				recheckConfig: secretNotOwed,
				startConfig:   scriptedConfig(machineInitScriptLocal, machineSecretScript),
			})
			if !strings.Contains(f.screen(), "How will people sign in?") {
				t.Fatalf("the first refusal did not go straight into guided configuration:\n%s", f.screen())
			}
			f.press(key(tea.KeyEnter)) // Local accounts
			if !strings.Contains(f.screen(), "Public Orbit origin") {
				t.Fatalf("the first field never arrived:\n%s", f.screen())
			}
			f.typeLine("https://orbit.example.test")

			expectStillIncomplete(t, f)
			if n := sessions.Load(); n != 1 {
				t.Errorf("guided configuration was prepared %d times, want once", n)
			}
			if n := adopted.Load(); n != 1 {
				t.Errorf("the configuration was adopted %d times, want once", n)
			}
			if n := f.engineStarts.Load(); n != 2 {
				t.Errorf("the engine ran %d times, want twice (the first run and one retry)", n)
			}
		})
	}
}

// A fresh start of the flow begins clean: its first refusal goes
// straight into guided configuration again, with the same seams.
func TestStraightIn_AFreshStartAfterTheSecondRefusalGoesStraightInAgain(t *testing.T) {
	for _, name := range straightInFlows {
		t.Run(name, func(t *testing.T) {
			var sessions, adopted atomic.Int32
			seams := engineRunSeams{
				prepareConfig: countedPlan(false, false, &sessions),
				adoptConfig:   countedAdopt(&adopted),
				startConfig:   scriptedConfig(machineInitScript, machineSecretScript),
			}
			first := startStraightFlow(t, name, seams)
			expectStillIncomplete(t, first)

			second := startStraightFlow(t, name, seams)
			if n := sessions.Load(); n != 2 {
				t.Errorf("guided configuration was prepared %d times over two starts, want twice (once per start)", n)
			}
			if n := adopted.Load(); n != 2 {
				t.Errorf("the configuration was adopted %d times over two starts, want twice", n)
			}
			if n := second.engineStarts.Load(); n != 2 {
				t.Errorf("the fresh start ran the engine %d times, want twice (its first run and one retry)", n)
			}
			expectStillIncomplete(t, second)
		})
	}
}

// --- 6: Esc while guided configuration is still starting (review F2) -------------

// closeTrackingStdin is a configuration process's stdin that records,
// safely across goroutines, whether it was closed.
type closeTrackingStdin struct{ closed atomic.Bool }

func (c *closeTrackingStdin) Write(p []byte) (int, error) { return len(p), nil }
func (c *closeTrackingStdin) Close() error                { c.closed.Store(true); return nil }

// gate holds a seam until the test opens it, or until a fallback timer
// does so a seam called inside Update cannot hang the test.
type gate struct {
	ch       chan struct{}
	once     sync.Once
	fallback atomic.Bool
}

func newGate() *gate {
	g := &gate{ch: make(chan struct{})}
	time.AfterFunc(3*time.Second, func() { g.once.Do(func() { g.fallback.Store(true); close(g.ch) }) })
	return g
}

func (g *gate) open()          { g.once.Do(func() { close(g.ch) }) }
func (g *gate) wait()          { <-g.ch }
func (g *gate) timedOut() bool { return g.fallback.Load() }

func waitCount(t *testing.T, n *atomic.Int32, what string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for n.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if n.Load() == 0 {
		t.Fatalf("%s never happened", what)
	}
}

// Esc while the plan is being prepared leaves for the menu; the plan that
// arrives afterwards has its cleanup run, and nothing else changes.
func TestStraightIn_EscWhileThePlanIsPreparedCleansUpTheLatePlan(t *testing.T) {
	for _, name := range straightInFlows {
		t.Run(name, func(t *testing.T) {
			var planCleaned, preparing atomic.Int32
			hold := newGate()
			late := countingPlan(true, false, &planCleaned)
			cfg := &recordedConfig{inner: scriptedConfig(machineInitScript, machineSecretScript)}
			f := startStraightFlowUntil(t, name, engineRunSeams{
				prepareConfig: func(ctx context.Context, dir, tree string) configPlanMsg {
					preparing.Add(1)
					hold.wait()
					return late(ctx, dir, tree)
				},
				startConfig: cfg.start,
			}, "the plan to be held in preparation", func(*straightFlow) bool { return preparing.Load() > 0 })
			if hold.timedOut() {
				t.Fatal("setup: the plan was prepared inside Update, so there is no preparing phase")
			}
			if s := f.screen(); strings.Contains(s, "How will people sign in?") {
				t.Fatalf("setup: the plan is already on screen:\n%s", s)
			}

			f.press(key(tea.KeyEsc))
			if o := f.outcome(); !leftForMenu(o) {
				t.Fatalf("outcome after Esc = %+v, want back to the main menu", o)
			}
			afterEsc := f.screen()

			hold.open()
			f.loop.until("the late plan's cleanup", func() bool { return planCleaned.Load() > 0 })

			waitCount(t, &planCleaned, "the late plan's cleanup")
			if o := f.outcome(); !leftForMenu(o) {
				t.Errorf("the late plan changed the outcome to %+v, want still back to the main menu", o)
			}
			if s := f.screen(); s != afterEsc {
				t.Errorf("the late plan changed the screen:\nafter Esc:\n%s\nnow:\n%s", afterEsc, s)
			}
			if len(cfg.streams) != 0 {
				t.Error("a configuration process was started after Esc")
			}
		})
	}
}

// Esc while the configuration process is being started leaves for the
// menu; the process that arrives afterwards is killed and its stdin
// closed, and nothing else changes.
func TestStraightIn_EscWhileTheProcessStartsKillsTheLateProcess(t *testing.T) {
	for _, name := range straightInFlows {
		t.Run(name, func(t *testing.T) {
			var planCleaned atomic.Int32
			hold := newGate()
			proc := sleepingStream(t)
			stdin := &closeTrackingStdin{}
			var starts atomic.Int32
			f := startStraightFlow(t, name, engineRunSeams{
				prepareConfig: countingPlan(true, false, &planCleaned),
				startConfig: func(string, deploy.ConfigStep, deploy.AuthMode) (*engine.Stream, io.WriteCloser, error) {
					starts.Add(1)
					hold.wait()
					return proc, stdin, nil
				},
			})
			if !strings.Contains(f.screen(), "How will people sign in?") {
				t.Fatalf("not on the sign-in question:\n%s", f.screen())
			}
			// Local accounts: the process starts, and is held.
			f.pressUntil(key(tea.KeyEnter), "the configuration process to be asked for", func() bool { return starts.Load() > 0 })
			if hold.timedOut() {
				t.Fatal("setup: the process was started inside Update, so there is no starting phase")
			}
			if starts.Load() != 1 {
				t.Fatalf("setup: the configuration process was asked for %d times, want once", starts.Load())
			}

			f.press(key(tea.KeyEsc))
			if o := f.outcome(); !leftForMenu(o) {
				t.Fatalf("outcome after Esc = %+v, want back to the main menu", o)
			}
			afterEsc := f.screen()

			hold.open()
			f.loop.until("the late process's stdin to be closed", stdin.closed.Load)

			waitStopped(t, proc)
			deadline := time.Now().Add(3 * time.Second)
			for !stdin.closed.Load() && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if !stdin.closed.Load() {
				t.Error("the late process's stdin was never closed")
			}
			if o := f.outcome(); !leftForMenu(o) {
				t.Errorf("the late process changed the outcome to %+v, want still back to the main menu", o)
			}
			if s := f.screen(); s != afterEsc {
				t.Errorf("the late process changed the screen:\nafter Esc:\n%s\nnow:\n%s", afterEsc, s)
			}
		})
	}
}

// --- 7: the give-up reason names only a field still being asked (review F3) ------

// acceptThenFailScript accepts the origin and exits with an error, no
// abort line.
const acceptThenFailScript = `
echo "prompt field=APP_URL kind=url required=true attempt=1"
read -r a
echo "prompt-accept field=APP_URL"
exit 1`

// acceptTwoThenFailScript accepts the origin and the issuer, then exits
// with an error, no abort line.
const acceptTwoThenFailScript = `
echo "prompt field=APP_URL kind=url required=true attempt=1"
read -r a
echo "prompt-accept field=APP_URL"
echo "prompt field=OIDC_ISSUER kind=url required=true attempt=1"
read -r a
echo "prompt-accept field=OIDC_ISSUER"
exit 1`

// openPromptThenFailScript exits with an error while its prompt is still
// open, no abort line.
const openPromptThenFailScript = `
echo "prompt field=APP_URL kind=url required=true attempt=1"
exit 1`

const stoppedBeforeFinished = "Orbit's setup stopped before the configuration was finished."

func TestStraightIn_TheGiveUpReasonNamesOnlyAFieldStillBeingAsked(t *testing.T) {
	cases := []struct {
		name    string
		script  string
		sso     bool
		answers []string
		// field is the field the reason must name ("" for the reason
		// with no field); never lists fields it must not name.
		field string
		never []string
	}{
		{"accepted the only answer, then failed", acceptThenFailScript, false, []string{"https://orbit.example.test"}, "", []string{"APP_URL"}},
		{"accepted two answers, then failed", acceptTwoThenFailScript, true, []string{"https://orbit.example.test", "https://accounts.example.test"}, "", []string{"APP_URL", "OIDC_ISSUER"}},
		// The cases it guards: a field still being asked is named.
		{"open prompt, no abort line", openPromptThenFailScript, false, nil, "APP_URL", nil},
		{"abort line after an accepted field", giveUpLaterScript, true, []string{"https://orbit.example.test"}, "OIDC_ISSUER", []string{"APP_URL"}},
	}
	for _, name := range straightInFlows {
		for _, c := range cases {
			t.Run(name+" "+c.name, func(t *testing.T) {
				f := startStraightFlow(t, name, engineRunSeams{
					prepareConfig: planned(true, false),
					startConfig:   scriptedConfig(c.script, machineSecretScript),
				})
				f.driveToFailure(c.sso, c.answers)

				s := f.screen()
				if f.runState() != runFailed {
					t.Fatalf("run state = %v, want the failure screen:\n%s", f.runState(), s)
				}
				if want := name + " stopped"; !strings.Contains(s, want) {
					t.Errorf("the failure screen is not titled %q:\n%s", want, s)
				}
				if c.field == "" {
					if !strings.Contains(s, stoppedBeforeFinished) {
						t.Errorf("the failure screen lacks the reason %q:\n%s", stoppedBeforeFinished, s)
					}
					if strings.Contains(s, "stopped asking for") {
						t.Errorf("the reason names a field although the engine accepted every answer:\n%s", s)
					}
				} else if want := "Orbit's setup stopped asking for " + labelOf(c.field) + "."; !strings.Contains(s, want) {
					t.Errorf("the failure screen lacks the reason %q:\n%s", want, s)
				}
				for _, accepted := range c.never {
					if bad := "stopped asking for " + labelOf(accepted); strings.Contains(s, bad) {
						t.Errorf("the reason names %s, which the engine accepted:\n%s", accepted, s)
					}
				}
			})
		}
	}
}
