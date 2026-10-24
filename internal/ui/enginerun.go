package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// engineRun drives one engine run for the Install and Update flows:
// the mission console over the piped plain-mode event stream first,
// and — on the engine's configuration-required refusal — guided
// configuration, then the outcome.
//
// Configuration stays the engine's domain (the #51 principle,
// orbit-launcher#73): the launcher never invents a field name, a
// validation rule, or a file format. With a contract-era engine
// (orbit#297) the collection happens in-console over the machine
// prompt protocol — configure.sh names each field, validates each
// answer with its own validators, and writes its own files, with the
// launcher relaying answers over stdin and adopting the produced
// configuration into the target (see configcollect.go). A legacy
// engine that doesn't speak the protocol fails fast with no prompt
// line, and the flow falls back to the same tea.ExecProcess terminal
// handoff as before — capability detected by behaviour, never by
// version sniffing. The piped engine run itself cannot prompt by
// construction (deploy.BuildEngineCommand detaches it from the
// controlling terminal, engaging the engine's documented
// non-interactive contract), and success and failure are keyed off
// events plus exit codes, never scraped prose.
type engineRunState int

const (
	runPreparing engineRunState = iota
	runStreaming
	runConfigSignInMode
	runConfigCollect
	runHandoffRunning
	runFailed
	runSucceeded
	// runStopping is Ctrl-C mid-run: the engine has been asked to stop
	// and the launcher waits, bounded by stopGrace, for its own rollback.
	runStopping
	// runLeft is a configuration session cancelled with Esc: the run is
	// on its way to the main menu and draws nothing more.
	runLeft
)

// stopGrace is how long a Ctrl-C mid-run waits for the installer to undo
// what it started before killing it. Its cleanup is a docker rm, a file
// rollback and an rm -rf — seconds; the bound is for a wedged daemon.
// A variable so tests can shorten it.
var stopGrace = 15 * time.Second

// Seam types: the overridable dependencies an engine run needs, so
// flow tests never touch the network or a real terminal.
type (
	prepareEngineFunc  func(ctx context.Context, targetDir, action string) (*engine.Stream, func() error, error)
	prepareInstallFunc func(ctx context.Context, targetDir string) (*exec.Cmd, func() error, error)
	runHandoffFunc     func(cmd *exec.Cmd) tea.Cmd
	detectFunc         func(dir string) (*deploy.Deployment, error)
	nowFunc            func() time.Time
)

// flowOutcome is what a flow's engine run reports upward to AppModel.
type flowOutcome struct {
	Done      bool
	Succeeded bool
	WantsMenu bool
	// WantsRepair is the stopped screen's Open Repair: the app opens
	// Repair on the same folder.
	WantsRepair bool
	URL         string
	Elapsed     time.Duration
	// ExitNote is printed by main after the screen closes.
	ExitNote string
}

// backToMenu is leaving a flow for the main menu before anything has
// run: Esc on the flow's first screen, or the Cancel or Back row there,
// which is the same act (#210, owner decision 4a). Ctrl-C is never this;
// it quits.
var backToMenu = flowOutcome{Done: true, WantsMenu: true}

func outcomeOf(r engineRun) flowOutcome {
	return flowOutcome{
		Done:        r.Done,
		Succeeded:   r.Succeeded,
		WantsMenu:   r.WantsMenu,
		WantsRepair: r.WantsRepair,
		URL:         r.URL,
		Elapsed:     r.Elapsed,
		ExitNote:    r.exitNote,
	}
}

// serviceLabels are the names install.sh's completion lines give the
// optional services (its optional_service_not_ready labels), by the
// component their events carry. The lines are prose, so they are only
// ever shown, and only for a service whose events say it ended waiting.
var serviceLabels = map[string]string{
	"clamav": "Scanner: ",
	"tika":   "Document processing: ",
	"ollama": "Local model service: ",
}

// optionalStillWaiting reports whether e is an optional service's
// ending that did not fail the install: still starting, or stopped.
func optionalStillWaiting(e engine.Event) bool {
	return e.State == engine.StateWaiting && e.Reason == "optional-status" && e.Action == engine.ActionContinue
}

// engineReadyMsg carries the started engine stream (or the error that
// prevented it — fetch failure, unwritable temp dir).
type engineReadyMsg struct {
	stream *engine.Stream
	// configTree is the directory the engine was handed for its
	// configuration tree (deploy.ConfigTreeDir); cleanup removes it.
	configTree string
	cleanup    func() error
	// script is the install.sh the engine was started from, kept for
	// the run's retry.
	script []byte
	err    error
}

// engineStoppedMsg ends the wait after Ctrl-C: the engine has exited
// (forced false) or the grace ran out with it still running (forced).
type engineStoppedMsg struct{ forced bool }

// engineStreamMsg wraps one message from the engine stream's channel.
type engineStreamMsg struct{ msg any }

// engineStreamEndedMsg reports the engine stream closing. Arriving
// while the run is still streaming, it means the engine stopped without
// ever saying how the run went.
type engineStreamEndedMsg struct{ reason error }

// errNoEngineSender is what a run reports when it was built without a
// way to deliver engine output. It names the launcher, not the engine:
// the engine did nothing wrong.
var errNoEngineSender = errors.New("the launcher couldn't read the engine's output")

// errEngineStoppedEarly is the one reason every flow gives for a stream
// that closed before its DoneMsg.
var errEngineStoppedEarly = errors.New("the engine stopped without reporting how the run finished")

// errStillIncomplete is the failure when the engine refuses for
// configuration again on the retry that followed guided configuration.
//
//lint:ignore ST1005 shown as a whole sentence on the failure screen
var errStillIncomplete = errors.New("Orbit still says the configuration is incomplete.")

// streamWrap names the owning flow's messages for what the engine
// stream reader sees: one message from the stream, and the stream's
// end (reason is nil for an ordinary close). ended is handed the
// stream so a flow that starts several in turn can tell a superseded
// one's ending from its current one's.
type streamWrap struct {
	msg   func(msg any) tea.Msg
	ended func(s *engine.Stream, reason error) tea.Msg
}

// engineRunStream is the install/update run's wrapping.
var engineRunStream = streamWrap{
	msg:   func(msg any) tea.Msg { return engineStreamMsg{msg: msg} },
	ended: func(_ *engine.Stream, reason error) tea.Msg { return engineStreamEndedMsg{reason: reason} },
}

type engineRun struct {
	action    deploy.Action // the engine flag
	targetDir string
	title     string
	version   string

	state   engineRunState
	console ConsoleModel

	// width/height are remembered so an in-flow restart (the retry
	// after in-console configuration) rebuilds the console at size.
	width, height int

	stream  *engine.Stream
	cleanup func() error
	// script is the install.sh this run fetched. The retry after guided
	// configuration starts from it rather than fetching again, so a
	// network blip then cannot read as bad answers, and the retry runs
	// the same script that asked for the configuration.
	script []byte
	// configTree is where this run's engine copies its verified
	// configuration tree on a configuration refusal (ai/orbit#1225). It
	// lives until cleanup runs, which a refusal defers until the
	// configuration that follows it is over (releaseRunFiles).
	configTree string

	// cancelPrepare cancels the in-flight fetch-and-start. A Ctrl-C
	// while preparing cancels it and sets quitting; the run then quits
	// only when the preparation reports back, killing anything it
	// managed to start, so no engine is ever left running unobserved.
	cancelPrepare context.CancelFunc
	quitting      bool
	// handoffPending is the terminal handoff's preparation still
	// running: its staged files arrive only in installPreparedMsg, so a
	// Ctrl-C meanwhile waits for that message, as one while preparing
	// the engine does.
	handoffPending bool

	// cfg is the in-console configuration session, live while state is
	// runConfigCollect.
	cfg configCollect

	// retriedAfterConfig is set once guided configuration has led to an
	// engine retry in this run. A retry that refuses for configuration
	// again goes to the failure screen rather than round the same
	// session for ever; a fresh start from the menu is a new run.
	retriedAfterConfig bool

	// Outcome evidence, gathered from events and the exit code.
	// waiting holds the optional services whose last event was
	// optionalStillWaiting; completed is the success event seen, after
	// which completion holds the stdout lines that may name them (a few:
	// one per service is what the installer prints).
	waiting       map[string]bool
	completed     bool
	completion    []string
	configRefused bool
	lastFailed    *engine.Event
	stderrTail    []string
	runErr        error

	// pendingCommand is the command of a recovery-command line that was
	// the last stdout line; recoveryCommand is it adopted by the terminal
	// failure that followed with recovery=command (orbit
	// docs/engine-events.md, "Recovery command (v0)"). copiedCommand is
	// the failure screen's Copy command row having copied it.
	pendingCommand  string
	recoveryCommand string
	copiedCommand   bool

	// How the run stopped, as far as the launcher can tell (stopKind):
	// sawEvent is any event having arrived; rollbackSeen a rollback-phase
	// one; optionalSeen an optional-phase one; startFailed the launcher's own failure to fetch or start the
	// installer; exited the installer's failing exit ending the run, and
	// streamCut its stream closing before it said how the run went.
	// handoffRefused is the strict mode's refusal of the terminal
	// handoff. folderEntries is the folder's listing, read once by a
	// command on entering the screen when nothing was written to it:
	// folderRead is that read (nil when none was started), folderPending
	// it not having answered yet, folderFailed its error or no answer in
	// time (readFolderCmd).
	sawEvent       bool
	rollbackSeen   bool
	optionalSeen   bool
	startFailed    bool
	exited         bool
	streamCut      bool
	handoffRefused bool
	folderEntries  []string
	folderRead     *folderRead
	folderPending  bool
	folderFailed   bool

	// handoffByHand is the terminal handoff chosen from the failure
	// screen rather than reached as a fallback; handoffErr is why its
	// preparation failed, shown below the failure it was meant to answer.
	handoffByHand bool
	handoffErr    error

	// exitNote is set only when the launcher killed an installer that
	// was still undoing its work; main prints it after the screen closes
	// and exits 1. Empty means a clean stop, or no stop.
	exitNote string

	// menu is the cursor on whichever post-run screen is showing; each
	// screen names its own rows (stoppedRows).
	menu menu

	// Done/Succeeded/WantsMenu/WantsRepair are read by the owning flow
	// model and AppModel. URL, Elapsed and ServiceNotes feed the success
	// screen.
	Done         bool
	Succeeded    bool
	WantsMenu    bool
	WantsRepair  bool
	URL          string
	Elapsed      time.Duration
	ServiceNotes []string

	// send pushes a message into the event loop from outside it, for
	// the engine stream reader. Production wires this from the program;
	// a run without it cannot read the engine at all and says so.
	send func(tea.Msg)

	// Test seams; nil gets the real implementations.
	engineRunSeams
}

func newEngineRun(action deploy.Action, targetDir, title, version string) engineRun {
	return engineRun{
		action:    action,
		targetDir: targetDir,
		title:     title,
		version:   version,
	}
}

// start begins the run: the console starts its clock now, and the
// engine is fetched and launched in the background.
func (r engineRun) start(width, height int) (engineRun, tea.Cmd) {
	r.state = runPreparing
	r.width, r.height = width, height
	r.console = newConsole(r.title, r.version, r.now)
	r.console = r.console.setSize(width, height)
	prepare := r.prepareEngine
	targetDir, action, script := r.targetDir, string(r.action), r.script
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelPrepare = cancel
	return r, func() tea.Msg {
		if prepare == nil {
			if script != nil {
				return startEngine(ctx, script, targetDir, action)
			}
			return defaultPrepareEngine(ctx, targetDir, action)
		}
		stream, cleanup, err := prepare(ctx, targetDir, action)
		return engineReadyMsg{stream: stream, cleanup: cleanup, err: err}
	}
}

// defaultPrepareEngine fetches install.sh and starts it as the
// detached, piped, plain-mode engine run, reporting the configuration tree
// directory the engine was handed.
func defaultPrepareEngine(ctx context.Context, targetDir, action string) engineReadyMsg {
	script, err := deploy.FetchInstallScript(ctx)
	if err != nil {
		return engineReadyMsg{err: err}
	}
	return startEngine(ctx, script, targetDir, action)
}

// startEngine stages script and starts it as the engine run.
func startEngine(ctx context.Context, script []byte, targetDir, action string) engineReadyMsg {
	// A cancel that lands after the script arrived must still stop the
	// engine from starting: once started, only the model can kill it.
	if err := ctx.Err(); err != nil {
		return engineReadyMsg{err: err}
	}
	cmd, cleanup, err := deploy.BuildEngineCommand(script, targetDir, deploy.Action(action))
	if err != nil {
		return engineReadyMsg{err: err}
	}
	stream, err := engine.Start(cmd)
	if err != nil {
		cleanup()
		return engineReadyMsg{err: err}
	}
	return engineReadyMsg{stream: stream, configTree: deploy.ConfigTreeDir(cmd), cleanup: cleanup, script: script}
}

// releaseRunFiles removes what the engine run staged — install.sh and
// its configuration tree — once nothing needs them any more. A
// configuration refusal holds them past the run's end, for the
// configuration session; every way out of that (a retry, the terminal
// handoff, leaving, quitting, a failure) comes through here. While the
// configuration session's preparation is still writing into the tree,
// the removal waits for it to finish (configLate).
func (r *engineRun) releaseRunFiles() {
	if r.cleanup != nil {
		cleanup := r.cleanup
		r.cleanup = nil
		release := func() { _ = cleanup() }
		if r.cfg.late == nil || !r.cfg.late.releaseRunFilesAfter(release) {
			release()
		}
	}
	r.configTree = ""
}

// readEngineStream starts the one long-lived reader for an engine
// stream, and is the only code that reads one: the install/update run,
// Repair and in-console configuration all come through here, each with
// its own wrapping (#206).
//
// It replaces a chain of one-shot commands, where each message was
// fetched by a command that had to hand back a fresh one (#159). That
// chain held a single token, and every path that failed to pass it on
// stopped the run for good: the engine kept writing, the channel kept
// filling, and nothing ever read it again. Because the launcher runs
// with no tick chain under ORBIT_LAUNCHER_NO_ANIMATION, the program
// then had nothing at all left to wake it, and looked frozen while
// being merely idle.
//
// A re-arm inside the message handler could not fix that, as the
// compat run on 2026-09-10 showed: the handler only runs when a message
// arrives, and the only thing that delivers messages is the pump it was
// meant to revive.
//
// A goroutine reading until the channel closes holds no token to lose.
// It ends by saying so, so a stream that stops early becomes a failure
// the operator can see rather than a screen that never changes.
//
// Messages reach the flow in the order the engine wrote them, because
// send delivers each one before the next is read; the flows' state
// guards depend on that order.
func readEngineStream(send func(tea.Msg), s *engine.Stream, wrap streamWrap) tea.Cmd {
	if send == nil {
		// Every production path sets this. A nil sender means the flow
		// was built without one, and the run would otherwise wait in
		// silence for messages nothing can deliver — the exact failure
		// this reader exists to remove, so it fails loudly instead.
		logDiag("engine stream: no sender wired for this run")
		return func() tea.Msg {
			return wrap.ended(s, errNoEngineSender)
		}
	}
	return func() tea.Msg {
		go func() {
			for msg := range s.C {
				send(wrap.msg(msg))
			}
			send(wrap.ended(s, nil))
		}()
		return nil
	}
}

// update handles every message while an engine run owns the screen.
func (r engineRun) update(msg tea.Msg) (engineRun, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		r.width, r.height = msg.Width, msg.Height
		r.console = r.console.setSize(msg.Width, msg.Height)
		return r, nil

	case tickMsg:
		r.console = r.console.advance()
		return r, tick()

	case engineReadyMsg:
		if r.quitting {
			if msg.stream != nil {
				// The engine started after all: stop it as Ctrl-C stops a
				// running one, so its EXIT trap can roll back what it began.
				r.stream, r.cleanup, r.configTree = msg.stream, msg.cleanup, msg.configTree
				r, stop := r.beginStop()
				return r, tea.Batch(stop, readEngineStream(r.send, r.stream, engineRunStream))
			}
			if msg.cleanup != nil {
				_ = msg.cleanup()
			}
			return r, tea.Quit
		}
		if msg.err != nil {
			r.runErr = msg.err
			r.startFailed = true
			return r.enterStopped()
		}
		r.stream = msg.stream
		r.cleanup = msg.cleanup
		r.configTree = msg.configTree
		if msg.script != nil {
			r.script = msg.script
		}
		r.state = runStreaming
		return r, readEngineStream(r.send, r.stream, engineRunStream)

	case engineStreamMsg:
		if d, done := msg.msg.(engine.DoneMsg); done && r.state == runStopping {
			// A stop that reaches the installer after it disarmed its
			// rollback (database and application ready — orbit
			// docs/engine-events.md) only ends the optional waits: the
			// run finishes with its completion event and exit 0 and
			// nothing is undone, so it is the success it is, keyed as
			// every outcome is off the event plus the exit code.
			if d.Err == nil && r.completed {
				return r.handleStream(msg.msg)
			}
			// The installer finished its own stop. Whatever it exited
			// with, the operator asked for this: not a failure.
			return r.quitNow(false)
		}
		return r.handleStream(msg.msg)

	case engineStoppedMsg:
		if r.state != runStopping {
			return r, nil
		}
		if !msg.forced {
			// The installer's exit is not the end of its stream: its
			// last lines and its DoneMsg are still on the way (bounded
			// by the command's WaitDelay), and that DoneMsg says
			// whether the stop undid the run or finished it.
			return r, nil
		}
		return r.quitNow(true)

	case engineStreamEndedMsg:
		return r.handleStreamEnded(msg)

	case folderReadMsg:
		return r.gotFolder(msg), nil

	case configPlanMsg, configStepMsg, configStreamMsg, configStreamEndedMsg, configRecheckMsg, configAdoptedMsg:
		return r.handleConfigMsg(msg)

	case installPreparedMsg:
		r.handoffPending = false
		if r.quitting {
			if msg.cleanup != nil {
				_ = msg.cleanup()
			}
			return r, tea.Quit
		}
		if msg.err != nil && r.handoffByHand {
			// Chosen on the failure screen: that failure still stands,
			// and the preparation's error goes beneath it.
			r.handoffErr = msg.err
			r.state = runFailed
			return r, nil
		}
		if msg.err != nil {
			// The preparation's own error, not an earlier refusal the
			// handoff was meant to answer.
			r.forgetStop()
			r.runErr = msg.err
			r.state = runFailed
			return r, nil
		}
		r.cleanup = msg.cleanup
		handoff := r.runHandoff
		if handoff == nil {
			handoff = defaultRunHandoff
		}
		return r, handoff(msg.cmd)

	case installFinishedMsg:
		if r.cleanup != nil {
			r.cleanup()
			r.cleanup = nil
		}
		if msg.err != nil {
			// The handoff's own result, not the failure it answered.
			r.forgetStop()
			r.runErr = msg.err
			r.state = runFailed
			return r, nil
		}
		return r.succeed()

	case tea.KeyPressMsg:
		return r.handleKey(msg)

	case SignalQuitMsg:
		// A hangup or terminate that repeats while the stop it asked for
		// is running is the same request again, not the force a second
		// Ctrl-C is: the installer keeps its grace to roll back, and the
		// grace timer still kills one that ignores it.
		if r.state == runStopping {
			return r, nil
		}
		return r.handleKey(ctrlCPress)

	case tea.PasteMsg:
		// Only the configuration prompt takes a paste (#222).
		if r.state == runConfigCollect {
			r.cfg.session = r.cfg.session.paste(msg.Content)
		}
		return r, nil
	}
	return r, nil
}

func (r engineRun) handleStream(msg any) (engineRun, tea.Cmd) {
	switch m := msg.(type) {
	case engine.EventMsg:
		r.console = r.console.observeEvent(m.Event)
		r.sawEvent = true
		if m.Event.Phase == engine.PhaseRollback {
			r.rollbackSeen = true
		}
		if m.Event.Phase == engine.PhaseOptional {
			r.optionalSeen = true
		}
		// The command counts only when its line came just before the
		// terminal failure that says it was printed.
		if m.Event.IsTerminalFailure() && m.Event.Recovery == engine.RecoveryHasCommand {
			r.recoveryCommand = r.pendingCommand
		}
		r.pendingCommand = ""
		if _, optional := serviceLabels[m.Event.Component]; optional {
			if r.waiting == nil {
				r.waiting = map[string]bool{}
			}
			r.waiting[m.Event.Component] = optionalStillWaiting(m.Event)
		}
		if m.Event.IsSuccess() {
			r.completed = true
		}
		if m.Event.NeedsConfiguration() {
			r.configRefused = true
		}
		if m.Event.IsTerminalFailure() {
			failed := m.Event
			r.lastFailed = &failed
		}
		return r, nil

	case engine.RawLineMsg:
		r.console = r.console.observeRaw(m.Text)
		r.pendingCommand, _ = engine.RecoveryCommand(m.Text)
		if r.completed && len(r.completion) < 2*len(serviceLabels) && serviceLine(m.Text) {
			r.completion = append(r.completion, m.Text)
		}
		return r, nil

	case engine.DoneMsg:
		r.stderrTail = m.StderrTail
		if m.Err == nil || !r.configRefused {
			r.releaseRunFiles()
		}
		if m.Err == nil {
			return r.succeed()
		}
		r.runErr = m.Err
		if r.configRefused && r.retriedAfterConfig {
			// Guided configuration already ran and the engine still
			// refuses: another session would ask the same again.
			r.releaseRunFiles()
			// The refusal event would otherwise be shown instead.
			r.lastFailed = nil
			r.runErr = errStillIncomplete
			r.state = runFailed
			r.menu = menu{}
			return r, nil
		}
		if r.configRefused {
			// The engine's documented configuration-required refusal:
			// the target was rolled back, and the fix is the guided
			// configuration, which starts at once — in-console from the
			// configuration tree the engine just handed over, which is
			// why the run's files are kept until that is over.
			return r.beginConfigCollect()
		}
		r.exited = true
		return r.enterStopped()
	}
	return r, nil
}

// handleStreamEnded closes out a run whose stream has stopped.
//
// Arriving after the run resolved, it is the ordinary case: DoneMsg was
// handled and the channel closed behind it, so there is nothing to do.
// Arriving while the run is still streaming, the engine stopped without
// reporting an outcome, and the honest thing is to say so — a run that
// cannot be reported on has failed, whatever it managed to do first.
func (r engineRun) handleStreamEnded(msg engineStreamEndedMsg) (engineRun, tea.Cmd) {
	if r.state != runStreaming {
		return r, nil
	}
	logDiag("engine stream: closed before the run finished")
	if r.cleanup != nil {
		r.cleanup()
		r.cleanup = nil
	}
	r.runErr = msg.reason
	if r.runErr == nil {
		r.runErr = errEngineStoppedEarly
	}
	r.streamCut = true
	return r.enterStopped()
}

// succeed resolves the deployment's URL and finishes the run.
func (r engineRun) succeed() (engineRun, tea.Cmd) {
	detect := r.detect
	if detect == nil {
		detect = deploy.Detect
	}
	if d, err := detect(r.targetDir); err == nil && d != nil {
		r.URL = d.AppURL
	}
	r.ServiceNotes = r.serviceNotes()
	r.Elapsed = r.console.elapsed()
	r.state = runSucceeded
	r.Done = true
	r.Succeeded = true
	return r, nil
}

// serviceLine reports whether line starts with an optional service's label.
func serviceLine(line string) bool {
	for _, label := range serviceLabels {
		if strings.HasPrefix(line, label) {
			return true
		}
	}
	return false
}

// serviceNotes are the completion lines naming an optional service its
// events say is still starting or stopped, in the installer's order.
func (r engineRun) serviceNotes() []string {
	var notes []string
	for _, line := range r.completion {
		for component, label := range serviceLabels {
			if r.waiting[component] && strings.HasPrefix(line, label) {
				notes = append(notes, line)
			}
		}
	}
	return notes
}

// beginHandoff hands the real terminal to the interactive installer —
// the config collection stretch. Deliberately flagless: install.sh's
// own menus pick the right action for whatever state the target is in,
// on any engine generation.
//
// Under ORBIT_LAUNCHER_REQUIRE_IN_CONSOLE_CONFIG the handoff is
// refused outright and the run stops on the failure screen instead.
// The gate sits here rather than at each fallback so that the menu
// row offering the handoff by hand cannot quietly reopen the path the
// variable exists to close — with it set, this process never reaches
// install.sh's interactive prompts, which is what lets CI treat one of
// those prompts appearing as proof of the in-console path.
func (r engineRun) beginHandoff() (engineRun, tea.Cmd) {
	r.handoffByHand = false
	r.handoffErr = nil
	// The handoff stages its own install.sh and configuration tree; a
	// refused run's are done with.
	r.releaseRunFiles()
	if requireInConsoleConfig() {
		logDiag("refusing the terminal handoff: " + requireInConsoleEnv + " is set")
		// The strict refusal is now the reason this run stopped, so
		// the earlier evidence must not render in its place.
		r.forgetStop()
		r.handoffRefused = true
		// The variable's name is in the diagnostic line above.
		r.runErr = errors.New("in-console configuration required — terminal handoff refused")
		r.state = runFailed
		r.menu = menu{}
		return r, nil
	}
	r.state = runHandoffRunning
	prepare := r.prepareInstall
	if prepare == nil {
		prepare = defaultPrepareInstall
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelPrepare = cancel
	r.handoffPending = true
	return r, prepareInstallCmd(ctx, prepare, r.targetDir)
}

func (r engineRun) handleKey(msg tea.KeyPressMsg) (engineRun, tea.Cmd) {
	if isCtrlC(msg) {
		if r.state == runPreparing || (r.state == runHandoffRunning && r.handoffPending) {
			// Nothing to kill yet: cancel the preparation and quit when
			// it reports back (the engineReadyMsg and installPreparedMsg
			// cases), which is when its staged files can be removed.
			r.quitting = true
			if r.cancelPrepare != nil {
				r.cancelPrepare()
			}
			return r, nil
		}
		if r.state == runStreaming && r.stream != nil {
			return r.beginStop()
		}
		// Anything else, including a second Ctrl-C while stopping, is
		// the usual force.
		return r.quitNow(true)
	}

	switch r.state {
	case runConfigSignInMode:
		return r.handleConfigSignInModeKey(msg)

	case runConfigCollect:
		return r.handleConfigKey(msg)

	case runFailed:
		return r.handleMenuKey(msg, r.stoppedRows(), func(label string) (engineRun, tea.Cmd) {
			switch label {
			case removeCopy:
				// Stays on the screen and copies again on every Enter,
				// as Remove's does.
				r.copiedCommand = true
				return r, copyToClipboard(os.Stdout, r.recoveryCommand)
			case installAgain, updateAgain:
				// The same run again, in-flow: the profile and the
				// notice stand. After an unfinished rollback the
				// installer finishes putting the folder back first.
				return r.retryEngine()
			case openRepair:
				// The services are in place: Repair, on this folder.
				r.releaseRunFiles()
				r.Done = true
				r.WantsRepair = true
				return r, nil
			case guidedInstaller:
				r, cmd := r.beginHandoff()
				r.handoffByHand = true
				return r, cmd
			case "Menu":
				r.Done = true
				r.WantsMenu = true
				return r, nil
			default: // Exit
				return r, tea.Quit
			}
		})
	}
	return r, nil
}

// beginStop is Ctrl-C mid-run: SIGTERM so install.sh's EXIT trap can
// roll the run back, then a wait on the engine's exit bounded by
// stopGrace (the pattern of docker stop and systemd's TimeoutStopSec).
// Nothing here cancels a context the engine was built on: its
// detachedCommand cancel would SIGKILL the group before the trap ran,
// and BuildEngineCommand builds it on context.Background().
func (r engineRun) beginStop() (engineRun, tea.Cmd) {
	stream, grace := r.stream, stopGrace
	stream.Terminate()
	r.state = runStopping
	return r, func() tea.Msg {
		select {
		case <-stream.Done():
			return engineStoppedMsg{}
		case <-time.After(grace):
			return engineStoppedMsg{forced: true}
		}
	}
}

// quitNow ends the run and the launcher, killing the engine first if
// force is set (it is a no-op once the engine has been reaped).
func (r engineRun) quitNow(force bool) (engineRun, tea.Cmd) {
	killed := false
	if r.stream != nil && force {
		killed = r.stream.Kill()
	}
	// The alt screen is gone by the time anyone could read this, so the
	// note rides out on the final model and main prints it. Only a kill
	// that landed on a live installer earns the note: a second Ctrl-C or
	// the grace timer can arrive after the installer has already exited
	// by itself, and that stop was clean. The note is never overwritten:
	// the killed installer's own DoneMsg can still be handled before the
	// quit.
	if killed && r.state == runStopping && r.exitNote == "" {
		r.exitNote = forcedStopNote(r.action)
	}
	r.cfg.close()
	// A removal this defers to a configuration preparation still
	// running is finished before the process exits
	// (FinishDeferredCleanup, from cmd/orbit-launcher).
	r.releaseRunFiles()
	return r, tea.Quit
}

// forcedStopNote is the owner's wording for a stop whose installer was
// still undoing its work when the launcher ended it. A stopped update
// says "before updating again" (decision 21a).
func forcedStopNote(action deploy.Action) string {
	again := "installing"
	if action == deploy.ActionUpdate {
		again = "updating"
	}
	return "the " + string(action) + " was stopped, but Orbit's installer was still undoing its work when the launcher stopped waiting and ended it. " +
		"The folder and the Docker containers and volumes it created may be half restored — check them before " + again + " again."
}

// handleMenuKey drives a post-run screen's menu over items: Esc is back
// to the main menu, and Enter hands choose the selected row's label.
func (r engineRun) handleMenuKey(msg tea.KeyPressMsg, items []string, choose func(string) (engineRun, tea.Cmd)) (engineRun, tea.Cmd) {
	var chosen string
	var back bool
	r.menu, chosen, back = r.menu.over(items).onScreen(msg)
	if back {
		r.releaseRunFiles()
		r.Done = true
		r.WantsMenu = true
		return r, nil
	}
	if chosen == "" {
		return r, nil
	}
	return choose(chosen)
}

// The stopped screen's rows (#257). Each way a run can stop offers only
// the rows that can move the person on, then Menu and Exit.
const (
	// installAgain and updateAgain run the same step again, in-flow.
	installAgain = "Install again"
	updateAgain  = "Update again"
	// openRepair is offered when the services were left in place.
	openRepair = "Open Repair"
	// guidedInstaller hands the terminal to install.sh's own prompts:
	// the honest remedy only for an installer that reports nothing, or
	// for configuration still refused, since anywhere else it runs the
	// same install.sh into the same refusal.
	guidedInstaller = "Open the guided installer"
)

// stopKind is how a run stopped, as far as the launcher can tell from
// the facts it has: the terminal failure's action, a rollback seen, a
// recovery command, any event at all, its own failure to start the
// installer. Never the installer's prose, which the contract says is
// unstable.
type stopKind int

const (
	// stoppedOther is a failure of the configuration session or of the
	// guided installer: its own words, as before.
	stoppedOther stopKind = iota
	// stoppedBeforeWriting is a retry with no rollback: the installer arms
	// its rollback only once it may write, so nothing was written.
	stoppedBeforeWriting
	// stoppedUndone is a retry after a rollback: the folder and Docker are
	// as they were.
	stoppedUndone
	// stoppedUndoneButDocker is a retry whose rollback left one command to
	// run (#261).
	stoppedUndoneButDocker
	// stoppedNotFullyUndone is an abort after a rollback, or with a
	// recovery command: the folder could not be put back.
	stoppedNotFullyUndone
	// stoppedCannotGoAhead is an abort before anything was written, or an
	// action the launcher does not know.
	stoppedCannotGoAhead
	// stoppedNotReady is a repair: the services were left in place.
	stoppedNotReady
	// stoppedDied is events, then the installer ending without a terminal
	// event.
	stoppedDied
	// stoppedSaidNothing is a failing exit with no event at all: an older
	// install.sh.
	stoppedSaidNothing
	// stoppedCouldNotStart is the launcher's own failure to fetch or start
	// the installer.
	stoppedCouldNotStart
	// stoppedHandoffRefused is the strict mode's refusal of the terminal
	// handoff.
	stoppedHandoffRefused
)

// stopKind reads how the run stopped.
func (r engineRun) stopKind() stopKind {
	switch {
	case r.handoffRefused:
		return stoppedHandoffRefused
	case r.startFailed:
		return stoppedCouldNotStart
	case r.lastFailed != nil:
		return r.failedKind(*r.lastFailed)
	case r.sawEvent && (r.exited || r.streamCut):
		return stoppedDied
	case r.exited:
		return stoppedSaidNothing
	}
	return stoppedOther
}

// failedKind reads a terminal failure by its action. An action the
// launcher does not know is read as abort: never a rerun it cannot
// vouch for.
//
// recovery=command alone counts as a command left to run, even when its
// recovery-command line never arrived: the folder or Docker is not as it
// was, so the screen must not say it is.
func (r engineRun) failedKind(e engine.Event) stopKind {
	command := r.recoveryCommand != "" || e.Recovery == engine.RecoveryHasCommand
	switch e.Action {
	case engine.ActionRetry:
		switch {
		case command:
			return stoppedUndoneButDocker
		case r.rollbackSeen:
			return stoppedUndone
		}
		return stoppedBeforeWriting
	case engine.ActionRepair:
		return stoppedNotReady
	}
	if command || r.rollbackSeen {
		return stoppedNotFullyUndone
	}
	return stoppedCannotGoAhead
}

// forgetStop drops what a stop left behind, for a failure that is the
// launcher's or the guided installer's own, or a fresh run.
func (r *engineRun) forgetStop() {
	r.lastFailed = nil
	r.stderrTail = nil
	r.sawEvent, r.rollbackSeen, r.startFailed = false, false, false
	r.exited, r.streamCut, r.handoffRefused = false, false, false
	r.pendingCommand, r.recoveryCommand, r.copiedCommand = "", "", false
	r.folderEntries, r.folderRead, r.folderPending, r.folderFailed = nil, nil, false, false
}

// enterStopped puts the run on the stopped screen, the caret on its
// first row. An Install that stopped before writing reads the folder
// once, by the command it returns, so the screen can say what is in it
// (#277 L3): never on the screen's own thread.
func (r engineRun) enterStopped() (engineRun, tea.Cmd) {
	r.state = runFailed
	r.menu = menu{}
	r.folderEntries, r.folderRead, r.folderPending, r.folderFailed = nil, nil, false, false
	if r.action == deploy.ActionInstall && r.stopKind() == stoppedBeforeWriting {
		r.folderRead = &folderRead{dir: r.targetDir}
		r.folderPending = true
		return r, r.readFolderCmd()
	}
	return r, nil
}

// folderReadLimit is how long the stopped screen waits for the folder's
// listing, as long as Repair waits for the services (A4).
const folderReadLimit = 4 * time.Second

// folderRead names one read of the folder: its answer is kept only while
// the run still holds the same read, so one landing after Install again
// or Menu is dropped. It is never zero-sized, so two reads never share
// an address.
type folderRead struct{ dir string }

// folderReadMsg is a folder read's answer: the names the screen lists,
// or failed for an error or no answer within the limit.
type folderReadMsg struct {
	read   *folderRead
	names  []string
	failed bool
}

// readFolderCmd lists the folder off the screen's thread, waiting at most
// folderLimit, as readServices does: the reader runs on a goroutine of
// its own with a buffered slot for its answer, so one that is late (or
// never returns) costs only that goroutine, and its answer is dropped.
func (r engineRun) readFolderCmd() tea.Cmd {
	list, read := r.listFolder, r.folderRead
	if list == nil {
		list = os.ReadDir
	}
	limit := r.folderLimit
	if limit <= 0 {
		limit = folderReadLimit
	}
	return func() tea.Msg {
		type answer struct {
			entries []os.DirEntry
			err     error
		}
		answered := make(chan answer, 1)
		go func() {
			entries, err := list(read.dir)
			answered <- answer{entries, err}
		}()
		select {
		case a := <-answered:
			if a.err != nil {
				return folderReadMsg{read: read, failed: true}
			}
			return folderReadMsg{read: read, names: folderNames(a.entries)}
		case <-time.After(limit):
			return folderReadMsg{read: read, failed: true}
		}
	}
}

// gotFolder keeps a folder read's answer if the run still holds that
// read and is still on its stopped screen.
func (r engineRun) gotFolder(msg folderReadMsg) engineRun {
	if msg.read == nil || msg.read != r.folderRead || !r.folderPending || r.state != runFailed || r.Done {
		return r
	}
	r.folderPending = false
	r.folderEntries, r.folderFailed = msg.names, msg.failed
	return r
}

// folderNames lists the folder's entries as the stopped screen names
// them: sorted, directories ending "/", without the installer's own
// files (.env-orbit, .orbit-secrets, and orbit-install-backups when it
// is a real directory; a file or link of that name the installer
// refuses, so it is listed).
func folderNames(entries []os.DirEntry) []string {
	var names []string
	for _, e := range entries {
		name := e.Name()
		if name == deploy.EnvFile || name == deploy.SecretsDir || (name == deploy.BackupsDir && e.IsDir()) {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// folderListing is the stopped screen's line about the folder: a few
// names, then how many more (owner, 15a), never a long list, ending in
// the fact that holds whichever check refused (#277 W5). It names as
// many entries, up to three, as keep the whole line within limit, so it
// takes the one row "reading the folder" held for it (#277 L3). When
// even one name does not fit, it keeps one name and the line wraps
// between words.
func folderListing(names []string, limit int) string {
	const most, tail = 3, " — the installer needs it empty"
	line := func(shown int) string {
		if shown >= len(names) {
			return "in the folder: " + strings.Join(names, ", ") + tail
		}
		return fmt.Sprintf("in the folder: %s and %d more", strings.Join(names[:shown], ", "), len(names)-shown) + tail
	}
	for shown := min(most, len(names)); shown > 1; shown-- {
		if l := line(shown); lipgloss.Width(visibleText(l)) <= limit {
			return l
		}
	}
	return line(1)
}

// stoppedRows are the stopped screen's rows for how the run stopped.
func (r engineRun) stoppedRows() []string {
	var rows []string
	if r.recoveryCommand != "" {
		rows = append(rows, removeCopy)
	}
	switch r.stopKind() {
	case stoppedCouldNotStart:
		if errors.Is(r.runErr, deploy.ErrFolderNameControl) {
			// A rerun from the same folder fails the same way (#277 W3).
			break
		}
		fallthrough
	case stoppedBeforeWriting, stoppedUndone, stoppedUndoneButDocker, stoppedDied:
		again := installAgain
		if r.action == deploy.ActionUpdate {
			again = updateAgain
		}
		rows = append(rows, again)
	case stoppedNotReady:
		rows = append(rows, openRepair)
	case stoppedSaidNothing, stoppedOther:
		rows = append(rows, guidedInstaller)
	}
	return append(rows, "Menu", "Exit")
}

// stopSentence is the one sentence saying what happened to the folder
// and Docker; "" where the failure's own words stand instead. An update
// is never rolled back, so its sentences key off the action alone and
// never say "undone".
func (r engineRun) stopSentence(kind stopKind) string {
	switch kind {
	case stoppedDied:
		return "The installer stopped without saying how it ended."
	case stoppedSaidNothing:
		return "The installer stopped without saying what went wrong."
	case stoppedCouldNotStart:
		return "The launcher couldn't start the installer."
	}
	if r.action == deploy.ActionUpdate {
		switch kind {
		case stoppedBeforeWriting, stoppedUndone, stoppedUndoneButDocker:
			return "The update did not finish. Orbit's services were not changed."
		case stoppedNotReady:
			return "The update did not finish. Orbit's services were changed and may not all be running."
		case stoppedNotFullyUndone, stoppedCannotGoAhead:
			return "The update can't go ahead; nothing was changed."
		}
		return ""
	}
	switch kind {
	case stoppedBeforeWriting:
		return "Nothing was changed in " + r.targetDir + "."
	case stoppedUndone:
		return "The install was undone: " + r.targetDir + " and Docker are as they were."
	case stoppedUndoneButDocker:
		return "The install was undone in the folder, but something it started in Docker is still there."
	case stoppedNotFullyUndone:
		return "The install couldn't be fully undone; the lines below say what is left."
	case stoppedCannotGoAhead:
		return "The install can't go ahead here; nothing was changed."
	case stoppedNotReady:
		return "Orbit is installed but not ready; its services are still running."
	}
	return ""
}

// nextSteps are the muted lines saying what to do next, one per line;
// an Update's screen has none. After a recovery command the first is
// the line under its box.
func (r engineRun) nextSteps(kind stopKind) []string {
	if r.action == deploy.ActionUpdate {
		return nil
	}
	const fixNamed = "fix what the installer names above, then install again"
	switch kind {
	case stoppedBeforeWriting:
		// Never "move them out" as the cause: the launcher can't tell a
		// Docker refusal from a not-empty one, so the listing carries the
		// folder's fact and this line stays the general one (#277 W5).
		return []string{fixNamed, "to use another folder, exit and run the launcher from there"}
	case stoppedUndone:
		return []string{fixNamed}
	case stoppedUndoneButDocker:
		return []string{"Run this in a terminal, then choose Install again"}
	case stoppedNotFullyUndone:
		if r.recoveryCommand != "" {
			return []string{"Run this in a terminal, then choose Install from the menu"}
		}
		return []string{"do what the lines above say in a terminal, then choose Install from the menu"}
	case stoppedCannotGoAhead:
		return []string{"do what the installer says above, then choose Install from the menu"}
	case stoppedNotReady:
		return []string{"Repair can look at it"}
	case stoppedDied:
		return []string{"installing again is safe: the installer checks the folder first"}
	}
	return nil
}

// view renders whichever screen the run is on.
func (r engineRun) view(width, height int) string {
	if width == 0 {
		return ""
	}
	switch r.state {
	case runPreparing, runStreaming:
		return r.console.view(width, height)
	case runStopping:
		detail := "the installer is undoing what it started — a few seconds"
		// phase=optional is only emitted after the installer has disarmed
		// its rollback, so a stop then undoes nothing (orbit
		// docs/engine-events.md).
		if r.action == deploy.ActionInstall && r.optionalSeen {
			detail = "Orbit is already installed; the installer is finishing without waiting for the optional services"
		}
		return skyBlock(r.console.sky, width, height,
			workingLine(r.console.spinner, "stopping the "+string(r.action), detail))
	case runConfigSignInMode:
		return r.viewConfigSignInMode(width, height)
	case runConfigCollect:
		return r.viewConfigCollect(width, height)
	case runHandoffRunning:
		// The line wraps inside the padding, never past the screen (#288).
		lines := wrapWordsIn("in the installer — you'll return here when it finishes", measure(width, proseWidth), textArea(width))
		return lipgloss.NewStyle().Padding(1, 2).Render(style.Tagline.Render(strings.Join(lines, "\n")))
	case runFailed:
		return r.viewFailed(width, height)
	}
	return ""
}

// viewFailed is the stopped screen (#257): the title, one sentence
// saying what happened to the folder and Docker, the stage the run
// stopped in, the installer's own lines, the folder's listing, the next
// step, then the rows. Never the engine's reason, action or phase
// tokens.
func (r engineRun) viewFailed(width, height int) string {
	// b is the rows above the installer's message; after is every row
	// below it, laid out first so the message knows its room.
	var b, after strings.Builder
	writeInlineHeader(&b, style.ErrorText.Render(style.SymbolFailure), flowStoppedTitle(r.action))
	// Docker's words, at Docker's length: wrapped, never cut at the edge.
	limit := measure(width, errorWidth)
	kind := r.stopKind()
	if sentence := r.stopSentence(kind); sentence != "" {
		// Plain ink: the ✗ carries the red (owner, 17a). The sentence can
		// hold the folder, so it is laid out as a folder's sentence is.
		for _, row := range wrapHard(sentence, limit, width-4, folderRows) {
			fmt.Fprintln(&b, style.Ink.Render(row))
		}
	}
	switch kind {
	case stoppedCouldNotStart, stoppedOther, stoppedHandoffRefused:
		if r.runErr != nil {
			writeWrappedIn(&b, style.Tagline, errorWords(r.runErr), limit, textArea(width))
		}
	default:
		if r.sawEvent {
			// The step that failed, not "Rolling back" (#257, owner 2a).
			writeWrappedIn(&b, style.MutedText, "stage: "+r.console.progressWord(), limit, textArea(width))
		}
	}
	if r.handoffErr != nil {
		writeWrappedIn(&after, style.Tagline, visibleLines(r.handoffErr.Error()), limit, textArea(width))
	}
	// The listing's slot: one muted row while the read is out, so the
	// menu sits where a one-row listing leaves it (#277 L3).
	switch {
	case r.folderPending:
		writeWrappedIn(&after, style.MutedText, "reading the folder", limit, textArea(width))
	case r.folderFailed:
		writeWrappedIn(&after, style.MutedText, "the folder could not be read", limit, textArea(width))
	case len(r.folderEntries) > 0:
		for _, row := range wrapHard(folderListing(r.folderEntries, limit), limit, width-4, folderRows) {
			fmt.Fprintln(&after, style.MutedText.Render(row))
		}
	}
	if r.recoveryCommand != "" {
		// Verbatim, as the contract asks: framed like Remove's box and
		// broken between its words only, so it reads whole; Copy command
		// copies the raw line.
		fmt.Fprintln(&after)
		for _, line := range commandBox(r.recoveryCommand, width) {
			fmt.Fprintln(&after, line)
		}
	}
	for _, step := range r.nextSteps(kind) {
		writeWrappedIn(&after, style.MutedText, step, limit, textArea(width))
	}
	fmt.Fprintln(&after)
	// The copy row says so once it has copied; the label Enter answers
	// with stays the same.
	rows := r.stoppedRows()
	if r.copiedCommand {
		rows = append([]string{removeCopy + style.Separator + "copied"}, rows[1:]...)
	}
	r.menu.over(rows).write(&after)

	// The installer's message gets the rows the screen has left once
	// every other row has its place, as Remove's box does: newest lines
	// kept, one "…" row standing for those dropped (#261 ruling 1). A
	// line that is the recovery command is not repeated: the box shows it.
	var stderr []string
	for _, line := range r.stderrTail {
		if r.recoveryCommand != "" && strings.TrimSpace(line) == strings.TrimSpace(r.recoveryCommand) {
			continue
		}
		stderr = append(stderr, line)
	}
	room := max(height-strings.Count(b.String(), "\n")-strings.Count(after.String(), "\n")-2, 1)
	// The installer's lines carry the backup folder's path: a word that
	// fits the text area stays whole, a wider one is cut at the measure,
	// every character kept (#277 L4).
	for _, line := range fitTail(stderr, limit, textArea(width), room) {
		fmt.Fprintln(&b, style.Tagline.Render(line))
	}
	b.WriteString(after.String())
	return skyBlock(r.console.sky, width, height, b.String())
}

// errorWords is an error's text as a screen shows it: visibleLines,
// except for text escaped as it was built, since visibleText is not
// idempotent (a backslash is shown doubled). That is a shownText, and
// deploy's refusal of a folder whose name holds a control character,
// which names the character as its escape and holds no raw one.
func errorWords(err error) string {
	var shown shownText
	if errors.As(err, &shown) || errors.Is(err, deploy.ErrFolderNameControl) {
		return err.Error()
	}
	return visibleLines(err.Error())
}

// fitTail lays out stderr lines, wrapped at limit within a text area edge
// cells wide (wrapWordsIn), in at most room rows.
// When they need more, the newest whole lines are kept behind one "…"
// row; a newest line that alone needs more keeps its last rows.
func fitTail(lines []string, limit, edge, room int) []string {
	wrapped := make([][]string, len(lines))
	total := 0
	for i, line := range lines {
		wrapped[i] = wrapWordsIn(line, limit, edge)
		if len(wrapped[i]) == 0 {
			wrapped[i] = []string{""}
		}
		total += len(wrapped[i])
	}
	if total <= room {
		var out []string
		for _, w := range wrapped {
			out = append(out, w...)
		}
		return out
	}
	keep := room - 1 // the "…" row takes one
	var kept []string
	for i := len(wrapped) - 1; i >= 0; i-- {
		if len(kept)+len(wrapped[i]) > keep {
			if len(kept) == 0 {
				kept = wrapped[i][len(wrapped[i])-keep:]
			}
			break
		}
		kept = append(slices.Clone(wrapped[i]), kept...)
	}
	return append([]string{"…"}, kept...)
}

// commandBox frames a shell command the way printf %q quotes it, like
// Remove's box: the text gets width-8, and rows break only between
// words (shellWords), each continued row ending " \", "&&" starting its
// own row, continuation rows indented two cells and an option kept with
// its value where it fits. Words are shown escaped for the terminal,
// backslashes as written. It always boxes (#277 L4): a command holding a
// word too wide for a row is laid out by wrapShellCommand, which cuts
// that word with the shell's own continuation, so the box still reads
// back as the command.
func commandBox(command string, width int) []string {
	limit := width - 8
	const indent, cont = "  ", " \\"
	words := shellWords(command)
	for i, w := range words {
		words[i] = visibleCommand(w)
	}
	var lines []string
	if one := strings.Join(words, " "); lipgloss.Width(one) <= limit {
		lines = []string{one}
	} else {
		fits := func(u string) bool { return lipgloss.Width(indent+u)+len(cont) <= limit }
		var units []string
		for i := 0; i < len(words); i++ {
			u := words[i]
			if strings.HasPrefix(u, "-") && i+1 < len(words) && !strings.HasPrefix(words[i+1], "-") && words[i+1] != "&&" && fits(u+" "+words[i+1]) {
				u += " " + words[i+1]
				i++
			}
			if !fits(u) {
				units = nil
				break
			}
			units = append(units, u)
		}
		if units == nil {
			lines = wrapShellCommand(words, limit)
		} else {
			line := ""
			for _, u := range units {
				if line != "" && line != indent && (u == "&&" || lipgloss.Width(line)+1+lipgloss.Width(u)+len(cont) > limit) {
					lines = append(lines, line+cont)
					line = indent
				}
				if line != "" && line != indent {
					line += " "
				}
				line += u
			}
			lines = append(lines, line)
		}
	}
	boxW := 0
	for i, l := range lines {
		boxW = max(boxW, lipgloss.Width(l))
		lines[i] = style.Ink.Render(l)
	}
	return drawBox(lipgloss.NewStyle().Foreground(style.Border), boxW+4, lines)
}

// shellWords splits a shell command at its word breaks: the spaces that
// are not escaped by a backslash (a space after an odd run of
// backslashes is part of the word) and not inside quotes ('…', "…" or
// $'…'), so a quoted folder with spaces in it is one word, as the shell
// reads it. Runs of spaces are one break; an unclosed quote runs to the
// end.
func shellWords(command string) []string {
	var words []string
	start := -1
	for i := 0; i < len(command); {
		c := command[i]
		if c == ' ' {
			if start >= 0 {
				words = append(words, command[start:i])
				start = -1
			}
			i++
			continue
		}
		if start < 0 {
			start = i
		}
		switch {
		case c == '\\':
			i += 2
		case c == '\'':
			i = quoteEnd(command, i+1, '\'', false)
		case c == '"':
			i = quoteEnd(command, i+1, '"', true)
		case c == '$' && i+1 < len(command) && command[i+1] == '\'':
			i = quoteEnd(command, i+2, '\'', true)
		default:
			i++
		}
	}
	if start >= 0 && start < len(command) {
		words = append(words, command[start:])
	}
	return words
}

// quoteEnd is the index just past the quote closing at or after i, a
// backslash escaping the next byte when escapes is set; len(s) when the
// quote never closes.
func quoteEnd(s string, i int, quote byte, escapes bool) int {
	for i < len(s) {
		switch {
		case escapes && s[i] == '\\':
			i += 2
		case s[i] == quote:
			return i + 1
		default:
			i++
		}
	}
	return len(s)
}

// writeStackedMenu writes menu rows in the centred grammar: each label
// centres on the screen axis (via the per-line centring downstream),
// the selected row's caret riding two cells left of its label.
func writeStackedMenu(b *strings.Builder, items []string, selected int) {
	for i, label := range items {
		fmt.Fprintln(b, menuRow(label, i == selected))
	}
}

// visibleCommand is visibleText for a shell-quoted command, keeping its
// backslashes as they are: they are the quoting printf %q wrote, and
// doubling them would show a different command from the one copied.
func visibleCommand(command string) string {
	parts := strings.Split(command, `\`)
	for i, p := range parts {
		parts[i] = visibleText(p)
	}
	return strings.Join(parts, `\`)
}

func flowStoppedTitle(action deploy.Action) string {
	if action == deploy.ActionUpdate {
		return "Update stopped"
	}
	return "Install stopped"
}

func tailLines(lines []string, n int) []string {
	if len(lines) <= n {
		return lines
	}
	return lines[len(lines)-n:]
}

// centreBlock places a plain content block on an empty screen using
// the same vertical rhythm as the scene screens.
func centreBlock(width, height int, content string) string {
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	topOffset := topOffsetFor(height-1, len(lines))
	var b strings.Builder
	for i := 0; i < topOffset; i++ {
		b.WriteString("\n")
	}
	for _, line := range lines {
		pad := (width - lipgloss.Width(line)) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad) + line + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
