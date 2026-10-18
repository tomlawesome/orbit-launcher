package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
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
	URL       string
	Elapsed   time.Duration
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
		Done:      r.Done,
		Succeeded: r.Succeeded,
		WantsMenu: r.WantsMenu,
		URL:       r.URL,
		Elapsed:   r.Elapsed,
		ExitNote:  r.exitNote,
	}
}

// engineReadyMsg carries the started engine stream (or the error that
// prevented it — fetch failure, unwritable temp dir).
type engineReadyMsg struct {
	stream *engine.Stream
	// configTree is the directory the engine was handed for its
	// configuration tree (deploy.ConfigTreeDir); cleanup removes it.
	configTree string
	cleanup    func() error
	err        error
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

	// cfg is the in-console configuration session, live while state is
	// runConfigCollect.
	cfg configCollect

	// Outcome evidence, gathered from events and the exit code.
	configRefused bool
	lastFailed    *engine.Event
	stderrTail    []string
	runErr        error

	// exitNote is set only when the launcher killed an installer that
	// was still undoing its work; main prints it after the screen closes
	// and exits 1. Empty means a clean stop, or no stop.
	exitNote string

	// menu is the cursor on whichever post-run screen is showing; each
	// screen names its own rows (failedMenu).
	menu menu

	// Done/Succeeded/WantsMenu are read by the owning flow model and
	// AppModel. URL and Elapsed feed the success screen.
	Done      bool
	Succeeded bool
	WantsMenu bool
	URL       string
	Elapsed   time.Duration

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
	targetDir, action := r.targetDir, string(r.action)
	ctx, cancel := context.WithCancel(context.Background())
	r.cancelPrepare = cancel
	return r, func() tea.Msg {
		if prepare == nil {
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
	return engineReadyMsg{stream: stream, configTree: deploy.ConfigTreeDir(cmd), cleanup: cleanup}
}

// releaseRunFiles removes what the engine run staged — install.sh and
// its configuration tree — once nothing needs them any more. A
// configuration refusal holds them past the run's end, for the
// configuration session; every way out of that (a retry, the terminal
// handoff, leaving, quitting, a failure) comes through here.
func (r *engineRun) releaseRunFiles() {
	if r.cleanup != nil {
		_ = r.cleanup()
		r.cleanup = nil
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
				msg.stream.Kill()
			}
			if msg.cleanup != nil {
				_ = msg.cleanup()
			}
			return r, tea.Quit
		}
		if msg.err != nil {
			r.runErr = msg.err
			r.state = runFailed
			return r, nil
		}
		r.stream = msg.stream
		r.cleanup = msg.cleanup
		r.configTree = msg.configTree
		r.state = runStreaming
		return r, readEngineStream(r.send, r.stream, engineRunStream)

	case engineStreamMsg:
		if _, done := msg.msg.(engine.DoneMsg); done && r.state == runStopping {
			// The installer finished its own stop. Whatever it exited
			// with, the operator asked for this: not a failure.
			return r.quitNow(false)
		}
		return r.handleStream(msg.msg)

	case engineStoppedMsg:
		if r.state != runStopping {
			return r, nil
		}
		return r.quitNow(msg.forced)

	case engineStreamEndedMsg:
		return r.handleStreamEnded(msg)

	case configPlanMsg, configStepMsg, configStreamMsg, configStreamEndedMsg, configRecheckMsg, configAdoptedMsg:
		return r.handleConfigMsg(msg)

	case installPreparedMsg:
		if msg.err != nil {
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
			r.runErr = msg.err
			r.state = runFailed
			return r, nil
		}
		return r.succeed()

	case tea.KeyPressMsg:
		return r.handleKey(msg)

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
		if r.configRefused {
			// The engine's documented configuration-required refusal:
			// the target was rolled back, and the fix is the guided
			// configuration, which starts at once — in-console from the
			// configuration tree the engine just handed over, which is
			// why the run's files are kept until that is over.
			return r.beginConfigCollect()
		}
		r.state = runFailed
		r.menu = menu{}
		return r, nil
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
	r.state = runFailed
	r.menu = menu{}
	return r, nil
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
	r.Elapsed = r.console.elapsed()
	r.state = runSucceeded
	r.Done = true
	r.Succeeded = true
	return r, nil
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
	// The handoff stages its own install.sh and configuration tree; a
	// refused run's are done with.
	r.releaseRunFiles()
	if requireInConsoleConfig() {
		logDiag("refusing the terminal handoff: " + requireInConsoleEnv + " is set")
		// The strict refusal is now the reason this run stopped, so
		// the earlier evidence must not render in its place —
		// viewFailed prefers lastFailed over runErr.
		r.lastFailed = nil
		r.stderrTail = nil
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
	return r, prepareInstallCmd(prepare, r.targetDir)
}

func (r engineRun) handleKey(msg tea.KeyPressMsg) (engineRun, tea.Cmd) {
	if isCtrlC(msg) {
		if r.state == runPreparing {
			// Nothing to kill yet: cancel the preparation and quit when
			// it reports back (see the engineReadyMsg case).
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
		return r.handleMenuKey(msg, failedMenu, func(label string) (engineRun, tea.Cmd) {
			switch label {
			case "Open the guided installer":
				return r.beginHandoff()
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

// The failure screen offers three exits, in this order: forward into
// the real interactive installer, back to the menu, or out. The forward
// option matters most for a legacy engine (orbit main today), which
// reports no telemetry — its non-interactive refusal is indistinguishable
// from any other exit 1, and the honest remedy is the guided installer.
var failedMenu = []string{"Open the guided installer", "Menu", "Exit"}

// view renders whichever screen the run is on.
func (r engineRun) view(width, height int) string {
	if width == 0 {
		return ""
	}
	switch r.state {
	case runPreparing, runStreaming:
		return r.console.view(width, height)
	case runStopping:
		return skyBlock(r.console.sky, width, height,
			workingLine(r.console.spinner, "stopping the "+string(r.action), "the installer is undoing what it started — a few seconds"))
	case runConfigSignInMode:
		return r.viewConfigSignInMode(width, height)
	case runConfigCollect:
		return r.viewConfigCollect(width, height)
	case runHandoffRunning:
		return lipgloss.NewStyle().Padding(1, 2).Render(style.Tagline.Render("in the installer — you'll return here when it finishes"))
	case runFailed:
		return r.viewFailed(width, height)
	}
	return ""
}

func (r engineRun) viewFailed(width, height int) string {
	var b strings.Builder
	writeInlineHeader(&b, style.ErrorText.Render(style.SymbolFailure), flowStoppedTitle(r.action))
	// Docker's words, at Docker's length: wrapped, never cut at the edge.
	limit := measure(width, errorWidth)
	if r.lastFailed != nil {
		word := r.lastFailed.Phase
		if idx := phaseIndex(word); idx >= 0 {
			word = consolePhases[idx].word
		}
		// The phase word leads in the error colour; the rest wraps.
		lines := wrapWords(word+" — "+r.lastFailed.Reason+style.Separator+"next: "+r.lastFailed.Action, limit)
		for i, line := range lines {
			if rest, ok := strings.CutPrefix(line, word); i == 0 && ok {
				line = style.ErrorText.Render(word) + style.Tagline.Render(rest)
			} else {
				line = style.Tagline.Render(line)
			}
			fmt.Fprintln(&b, line)
		}
	} else if r.runErr != nil {
		writeWrapped(&b, style.Tagline, r.runErr.Error(), limit)
	}
	for _, line := range tailLines(r.stderrTail, 6) {
		writeWrapped(&b, style.Tagline, line, limit)
	}
	fmt.Fprintln(&b)
	r.menu.over(failedMenu).write(&b)
	return skyBlock(r.console.sky, width, height, b.String())
}

// writeStackedMenu writes menu rows in the centred grammar: each label
// centres on the screen axis (via the per-line centring downstream),
// the selected row's caret riding two cells left of its label.
func writeStackedMenu(b *strings.Builder, items []string, selected int) {
	for i, label := range items {
		fmt.Fprintln(b, menuRow(label, i == selected))
	}
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
