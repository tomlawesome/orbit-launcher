package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// RepairModel is the real read-only diagnosis and plan (orbit#261
// slices 1–3): the launcher runs the deployment's own repair.sh — the
// copy install.sh placed there from the image, never a download (#190)
// — with `--plan` (diagnosis plus
// the classified proposed actions — still zero mutation by the
// script's own contract), and renders the finding/diagnosis/plan
// grammar — enums in, honest words out, outcome keyed off exit codes,
// never prose. A repair.sh too old for --plan rejects it as a usage
// error, and the flow falls back to `--check` — capability detected by
// behaviour. When the plan proposes executable actions the menu offers
// them (orbit#261 slice 4): the safe batch runs unattended (the
// script's documented automation path — reversible actions, per-file
// backups, full re-diagnosis after), and the guarded database-
// credential rotation is driven in-console over the same machine
// prompt grammar as configuration (typed action word + checkpoint
// passphrase, never automatable by the script's own contract). The
// menu never offers an action the plan didn't propose.
type RepairModel struct {
	width, height int
	star          starfield.Model
	targetDir     string
	version       string

	state       repairState
	mode        deploy.RepairMode
	findings    []engine.Finding
	diagnosis   *engine.Diagnosis
	planActions []engine.PlanAction
	planSummary *engine.PlanSummary
	manualNotes []string
	exitCode    int
	runErr      error
	// stoppedIn is the state a failure interrupted, which the error
	// screen titles itself by (see fail).
	stoppedIn repairState

	// Execution evidence (orbit#261 slice 4): what ran, how it ended,
	// with findings/diagnosis holding the post-execution re-diagnosis.
	executeResults []engine.ExecuteResult
	execSummary    *engine.ExecutionSummary
	dangerous      *engine.Dangerous

	// The dangerous rotation's machine-prompt session.
	stdin io.WriteCloser
	rot   promptSession

	stream *engine.Stream

	// serviceStates reads the deployment's containers for the services
	// block (#255). Nil shows no block: the c9 harness leaves it unset,
	// which keeps its goldens as they were. services holds the one
	// reading; see readServices. servicesLimit is how long the read
	// may take before the block says it could not check (#277); zero is
	// deploy.ServiceStatesTimeout, the production reader's own limit.
	serviceStates func(dir string) ([]deploy.ServiceState, error)
	services      *serviceReading
	servicesLimit time.Duration
	// foreignOwned says which of the deployment's configuration files
	// another account owns (#265); asked at every run start (see
	// startDiagnosis). Nil checks nothing: the c9 harness leaves it
	// unset. foreign is the answer that stopped the run.
	foreignOwned func(dir string) (deploy.Ownership, error)
	foreign      deploy.Ownership
	// clipboard is where the stop screen's Copy command writes its OSC 52
	// sequence: nil in production, which means os.Stdout. copied is that
	// row having copied.
	clipboard io.Writer
	copied    bool
	// frame is the working lines' spinner frame, advanced on every tick.
	frame int
	// menu is the cursor on whichever menu screen is showing; each screen
	// names its own rows (planMenu, executedMenu, repairMenu).
	menu menu

	// ctx scopes every start this model launches; cancel
	// stops one in flight. A Ctrl-C before a run exists cancels it and
	// sets quitting; the model then quits only when the preparation
	// reports back, killing a diagnosis it managed to start. A mutation
	// that started anyway is kept instead, on a fresh context (see
	// keepRunAfterCancel).
	ctx      context.Context
	cancel   context.CancelFunc
	quitting bool
	// signalQuit is a SignalQuitMsg that arrived during the safe repairs
	// or the rotation: the launcher quits as soon as the engine has ended.
	signalQuit bool

	// Done/WantsMenu surface the outcome to AppModel, exactly like a
	// flow's engine run.
	Done      bool
	WantsMenu bool

	// send is the flow's way back into the event loop, for the engine
	// stream reader; set by AppModel from the program's sender.
	send func(tea.Msg)

	// Test seams (prepareRepair, prepareRotate); nil gets the real
	// implementations.
	engineRunSeams
}

type repairState int

const (
	repairPreparing repairState = iota
	repairDiagnosis
	repairUnavailable
	repairError
	repairExecuting
	repairRotating
	repairExecuted
	// repairNotFound is the engine's exit 5: no Orbit deployment in the
	// directory. It is the one no-deployment screen (#229), a dead end.
	repairNotFound
	// repairForeign is the stop screen for files another account owns
	// (#265): no engine ran.
	repairForeign
)

type (
	prepareRepairFunc func(ctx context.Context, targetDir string, mode deploy.RepairMode) (*engine.Stream, error)
	prepareRotateFunc func(ctx context.Context, targetDir string) (*engine.Stream, io.WriteCloser, error)
)

// repairReadyMsg carries the started diagnosis run (or why there is
// none).
type repairReadyMsg struct {
	stream *engine.Stream
	err    error
}

// repairRotateReadyMsg carries the started rotation session.
type repairRotateReadyMsg struct {
	stream *engine.Stream
	stdin  io.WriteCloser
	err    error
}

// repairStreamMsg wraps one message from the diagnosis stream.
type repairStreamMsg struct{ msg any }

// repairStreamEndedMsg reports a repair stream closing; stream says
// which run's, since a --plan rejected for --check, a fresh diagnosis
// after an execution and the execution itself each have their own.
type repairStreamEndedMsg struct {
	stream *engine.Stream
	reason error
}

// repairStream is Repair's wrapping for the engine stream reader.
var repairStream = streamWrap{
	msg: func(msg any) tea.Msg { return repairStreamMsg{msg: msg} },
	ended: func(s *engine.Stream, reason error) tea.Msg {
		return repairStreamEndedMsg{stream: s, reason: reason}
	},
}

// NewRepairModel constructs the Repair flow for targetDir.
func NewRepairModel(targetDir, version string) RepairModel {
	m := RepairModel{targetDir: targetDir, version: version, mode: deploy.RepairPlan, services: &serviceReading{}}
	// One context for the model's whole life: Init cannot store state,
	// and the model quits once it is cancelled.
	m.ctx, m.cancel = context.WithCancel(context.Background())
	return m
}

// prepareContext is the context preparations run on; a model built
// without NewRepairModel falls back to one that is never cancelled.
func (m RepairModel) prepareContext() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

// Outcome surfaces the flow result to AppModel.
func (m RepairModel) Outcome() flowOutcome {
	return flowOutcome{Done: m.Done, WantsMenu: m.WantsMenu}
}

// Init implements tea.Model: the diagnosis starts immediately — it is
// read-only by the script's own contract, so there is nothing to
// confirm first.
func (m RepairModel) Init() tea.Cmd {
	return m.startDiagnosis(m.mode)
}

// startRun launches one read-only repair invocation in the given mode.
func (m RepairModel) startRun(mode deploy.RepairMode) tea.Cmd {
	prepare := m.prepareRepair
	if prepare == nil {
		prepare = defaultPrepareRepair
	}
	targetDir, ctx := m.targetDir, m.prepareContext()
	return func() tea.Msg {
		stream, err := prepare(ctx, targetDir, mode)
		return repairReadyMsg{stream: stream, err: err}
	}
}

// defaultPrepareRepair starts one repair run of the deployment's own
// scripts/repair.sh — nothing is fetched or written (#190).
func defaultPrepareRepair(ctx context.Context, targetDir string, mode deploy.RepairMode) (*engine.Stream, error) {
	cmd, err := deploy.RepairCommand(targetDir, mode)
	if err != nil {
		return nil, err
	}
	// A cancel that has already landed must still stop the start.
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return engine.Start(cmd)
}

// defaultPrepareRotate starts the dangerous rotation with its stdin
// piped — the machine-prompt transport the script demands.
func defaultPrepareRotate(ctx context.Context, targetDir string) (*engine.Stream, io.WriteCloser, error) {
	cmd, err := deploy.RepairCommand(targetDir, deploy.RepairExecuteDangerous)
	if err != nil {
		return nil, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return engine.StartInteractive(cmd)
}

// startExecution resets the evidence a fresh run will replace and
// launches it. The re-diagnosis the executor prints lands in the same
// findings/diagnosis fields the plan screen used.
func (m *RepairModel) startExecution() {
	m.findings, m.diagnosis = nil, nil
	m.executeResults, m.execSummary = nil, nil
	m.dangerous = nil
	m.manualNotes = nil
	m.stream, m.stdin = nil, nil
	m.rot = promptSession{}
	m.exitCode = 0
	m.stoppedIn = repairPreparing
	// A fresh pointer rather than clearing loaded on the old one: a view
	// still holding the old reading is undisturbed, and the next
	// diagnosis reads Docker again instead of showing the stale states.
	m.services = &serviceReading{}
}

// keepRunAfterCancel keeps a mutating run the engine started just as
// Ctrl-C landed: it is never signalled, because a kill milliseconds
// after start is exactly stopping halfway. The launcher stays and
// watches it; the fresh context lets a later "Diagnose again" start.
func (m *RepairModel) keepRunAfterCancel() {
	m.quitting = false
	m.ctx, m.cancel = context.WithCancel(context.Background())
}

func (m RepairModel) startRotate() tea.Cmd {
	prepare := m.prepareRotate
	if prepare == nil {
		prepare = defaultPrepareRotate
	}
	targetDir, ctx := m.targetDir, m.prepareContext()
	return func() tea.Msg {
		stream, stdin, err := prepare(ctx, targetDir)
		return repairRotateReadyMsg{stream: stream, stdin: stdin, err: err}
	}
}

// Update implements tea.Model.
func (m RepairModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(SignalQuitMsg); ok {
		// The safe repairs ignore Ctrl-C and the rotation only closes
		// its input (see handleKey), but nobody may be left to see the
		// result, so the quit waits for the engine to end.
		if m.inMutation() {
			m.signalQuit = true
		}
		return m.handleKey(ctrlCPress)
	}
	updated, cmd := m.update(msg)
	// Not while the step runs: that would drop the update's command
	// (the tick chain, the stream reader) with it.
	if r := updated.(RepairModel); r.signalQuit && !r.inMutation() {
		return r.handleKey(ctrlCPress)
	}
	return updated, cmd
}

// inMutation says a step that changes the deployment is running: the
// safe repairs or the credential rotation.
func (m RepairModel) inMutation() bool {
	return m.state == repairExecuting || m.state == repairRotating
}

func (m RepairModel) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// See RemoveModel.Update: the sky is built here, the tick chain
		// is not — there is exactly one, and it is already running.
		m.width, m.height = msg.Width, msg.Height
		m.star = newSky(msg.Width, msg.Height)
		return m, nil

	case tickMsg:
		m.star = m.star.Advance()
		m.frame = nextFrame(m.frame)
		return m, tick()

	case repairReadyMsg:
		if m.quitting {
			if msg.stream == nil {
				return m, tea.Quit
			}
			if m.state == repairPreparing {
				msg.stream.Kill()
				return m, tea.Quit
			}
			m.keepRunAfterCancel()
		}
		if msg.err != nil {
			m.fail(msg.err)
			switch {
			case errors.Is(msg.err, deploy.ErrNoDeployment):
				m.state = repairNotFound
			case errors.Is(msg.err, deploy.ErrRepairUnavailable):
				m.state = repairUnavailable
			}
			return m, nil
		}
		m.stream = msg.stream
		return m, readEngineStream(m.send, m.stream, repairStream)

	case repairForeignMsg:
		if m.quitting {
			// The start was cancelled, and it started nothing.
			return m, tea.Quit
		}
		m.state = repairForeign
		m.foreign = msg.own
		m.copied = false
		m.menu = menu{}
		return m, nil

	case repairRotateReadyMsg:
		if m.quitting {
			if msg.stream == nil {
				if msg.stdin != nil {
					_ = msg.stdin.Close()
				}
				return m, tea.Quit
			}
			// Keep the rotation and close its input at once: the
			// engine backs out at its first prompt.
			m.keepRunAfterCancel()
			m.stream = msg.stream
			if msg.stdin != nil {
				_ = msg.stdin.Close()
			}
			m.stdin = nil
			return m, readEngineStream(m.send, m.stream, repairStream)
		}
		if msg.err != nil {
			m.fail(msg.err)
			if errors.Is(msg.err, deploy.ErrNoDeployment) {
				m.state = repairNotFound
			}
			return m, nil
		}
		m.stream = msg.stream
		m.stdin = msg.stdin
		return m, readEngineStream(m.send, m.stream, repairStream)

	case repairStreamMsg:
		return m.handleStream(msg.msg)

	case repairServicesMsg:
		return m.gotServices(msg), nil

	case repairStreamEndedMsg:
		return m.handleStreamEnded(msg)

	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		// Only the rotation prompt takes a paste (#222).
		if m.state == repairRotating {
			m.rot = m.rot.paste(msg.Content)
		}
		return m, nil
	}
	return m, nil
}

func (m RepairModel) handleStream(msg any) (tea.Model, tea.Cmd) {
	switch m.state {
	case repairPreparing, repairExecuting, repairRotating:
	default:
		// An abandoned session's process still delivers its queued
		// messages; they belong to nothing now.
		return m, nil
	}
	switch s := msg.(type) {
	case engine.RawLineMsg:
		if m.state == repairRotating {
			var ok bool
			if m.rot, ok = m.rot.applyLine(s.Text); ok {
				return m, nil
			}
		}
		if f, ok := engine.ParseFinding(s.Text); ok {
			m.findings = append(m.findings, f)
		} else if e, ok := engine.ParseExecuteResult(s.Text); ok {
			m.executeResults = append(m.executeResults, e)
		} else if es, ok := engine.ParseExecutionSummary(s.Text); ok {
			summary := es
			m.execSummary = &summary
		} else if dg, ok := engine.ParseDangerous(s.Text); ok {
			summary := dg
			m.dangerous = &summary
		} else if p, ok := engine.ParsePlanAction(s.Text); ok {
			m.planActions = append(m.planActions, p)
		} else if ps, ok := engine.ParsePlanSummary(s.Text); ok {
			summary := ps
			m.planSummary = &summary
		} else if d, ok := engine.ParseDiagnosis(s.Text); ok {
			diag := d
			m.diagnosis = &diag
		}
		return m, nil
	case engine.EventMsg:
		// repair.sh emits no phase events; tolerate and move on.
		return m, nil
	case engine.DoneMsg:
		return m.handleDone(s)
	}
	return m, nil
}

// handleStreamEnded closes out a run whose stream has stopped. The
// ending of a stream that is not the current one — a --plan replaced
// by --check, a run already resolved and left behind — belongs to
// nothing. The current stream ending after its DoneMsg is the ordinary
// case and changes nothing either. Only a run still waiting on its
// stream has lost it: the engine stopped without reporting an outcome,
// and the screen says so (#206).
func (m RepairModel) handleStreamEnded(msg repairStreamEndedMsg) (tea.Model, tea.Cmd) {
	if msg.stream != m.stream {
		return m, nil
	}
	switch m.state {
	case repairPreparing, repairExecuting, repairRotating:
	default:
		return m, nil
	}
	logDiag("repair stream: closed before the run finished")
	err := msg.reason
	if err == nil {
		err = errEngineStoppedEarly
	}
	m.stdin = nil
	m.rot = promptSession{}
	m.fail(err)
	return m, nil
}

func (m RepairModel) handleDone(s engine.DoneMsg) (tea.Model, tea.Cmd) {
	m.exitCode = s.ExitCode
	m.stdin = nil

	switch m.state {
	case repairExecuting, repairRotating:
		// Execution outcomes come from the parsed execution summary,
		// with the exit code as the honest fallback. Exit 6 is the
		// dangerous batch refused: the operator said no, or there was
		// no terminal to ask. Nothing was attempted, so it ends on the
		// after-picture like any other outcome, not on the error screen.
		if m.execSummary == nil && s.ExitCode == engine.ExitNotInstalled {
			// Not an Orbit installation, in every mode: the one
			// no-deployment screen, as for a diagnosis.
			m.state = repairNotFound
			m.menu = menu{}
			return m, nil
		}
		if m.execSummary == nil && s.ExitCode != engine.ExitHealthy && s.ExitCode != engine.ExitDangerousRefused {
			m.fail(s.Err)
			return m, nil
		}
		m.state = repairExecuted
		m.menu = menu{}
		return m, nil
	}

	var readCmd tea.Cmd
	switch s.ExitCode {
	case engine.ExitNotInstalled:
		// "This isn't an Orbit installation", in every mode: the one
		// no-deployment screen Update and Remove show, not a diagnosis
		// (#229). The finding and tally that came with it say no more.
		m.state = repairNotFound
	case engine.ExitHealthy, engine.ExitAttention, engine.ExitFailed:
		// The contract's outcomes, shared by --check and --plan.
		m.state = repairDiagnosis
		readCmd = m.readServices()
		if m.mode == deploy.RepairPlan {
			// The manual guidance lines ride stderr, value-free by
			// the script's contract.
			m.manualNotes = s.StderrTail
		}
	case engine.ExitUsage:
		if m.mode == deploy.RepairPlan {
			// A repair.sh too old for --plan: usage error. Fall back
			// to the diagnosis it does speak.
			m.mode = deploy.RepairCheck
			m.findings, m.diagnosis = nil, nil
			m.planActions, m.planSummary, m.manualNotes = nil, nil, nil
			m.stream = nil
			m.menu = menu{}
			return m, m.startRun(deploy.RepairCheck)
		}
		m.fail(s.Err)
	default:
		m.fail(s.Err)
	}
	m.menu = menu{}
	return m, readCmd
}

// fail ends the run on the error screen, which is titled by the state
// it interrupted: a safe repair or a rotation that stopped is not a
// failed read-only check.
func (m *RepairModel) fail(err error) {
	m.runErr = err
	m.stoppedIn = m.state
	m.state = repairError
	m.menu = menu{}
}

var repairMenu = []string{"Menu", "Exit"}

// planMenu is the diagnosis screen's menu: execution choices appear
// only when the plan actually proposes them — a menu item that could
// do nothing would be a lie.
func (m RepairModel) planMenu() []string {
	var items []string
	if m.planHasSafe() {
		items = append(items, "Run the safe repairs")
	}
	if m.planHasDangerous() {
		items = append(items, "Rotate database credentials")
	}
	return append(items, "Menu", "Exit")
}

func (m RepairModel) planHasSafe() bool {
	for _, p := range m.planActions {
		switch p.Action {
		case "fix-permissions", "restore-transaction", "restart-services":
			return true
		}
	}
	return false
}

func (m RepairModel) planHasDangerous() bool {
	for _, p := range m.planActions {
		if p.Action == "rotate-database-credential" {
			return true
		}
	}
	return false
}

// executedMenu closes the loop after an execution: fresh diagnosis or
// out.
var executedMenu = []string{"Diagnose again", "Menu", "Exit"}

func (m RepairModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if isCtrlC(msg) {
		if m.stream == nil && (m.state == repairPreparing || m.state == repairExecuting || m.state == repairRotating) {
			// The run is still being started: cancel that
			// and quit when it reports back (see the ready cases).
			m.quitting = true
			if m.cancel != nil {
				m.cancel()
			}
			return m, nil
		}
		switch m.state {
		case repairExecuting:
			// A running mutation is never signalled: stopping it halfway
			// is the one thing more dangerous than letting it finish.
			return m, nil
		case repairRotating:
			return m.stopRotation()
		}
		if m.stream != nil {
			m.stream.Kill()
		}
		return m, tea.Quit
	}

	switch m.state {
	case repairPreparing, repairExecuting:
		if msg.Code == tea.KeyEsc && m.state == repairPreparing {
			// The read-only run can be abandoned freely; a running
			// execution is left to finish — killing a mutation
			// mid-flight is the one thing more dangerous than running
			// it.
			// Still being started: cancel the start, as Ctrl-C does,
			// or the process it launches outlives the screen (#221).
			if m.stream != nil {
				m.stream.Kill()
			} else if m.cancel != nil {
				m.cancel()
			}
			m.Done = true
			m.WantsMenu = true
		}
		return m, nil

	case repairRotating:
		return m.handleRotateKey(msg)

	case repairNotFound:
		// The dead end: its one row and Esc both go back to the menu,
		// and no other key does anything (#228), as in Update and Remove.
		if _, chosen, back := newMenu(deadEndRows...).onScreen(msg); back || chosen == "Menu" {
			m.Done = true
			m.WantsMenu = true
		}
		return m, nil

	case repairDiagnosis:
		menu := m.planMenu()
		return m.handleMenu(msg, menu, func(label string) (tea.Model, tea.Cmd) {
			switch label {
			case "Run the safe repairs":
				m.startExecution()
				m.state = repairExecuting
				return m, m.startRun(deploy.RepairExecuteSafe)
			case "Rotate database credentials":
				m.startExecution()
				m.state = repairRotating
				return m, m.startRotate()
			case "Menu":
				m.Done = true
				m.WantsMenu = true
				return m, nil
			default:
				return m, tea.Quit
			}
		})

	case repairExecuted:
		return m.handleExecutedMenu(msg)

	case repairForeign:
		return m.handleForeignKey(msg)

	case repairError:
		if m.stoppedMutation() {
			return m.handleExecutedMenu(msg)
		}
	}

	return m.handleMenu(msg, repairMenu, func(label string) (tea.Model, tea.Cmd) {
		if label == "Menu" {
			m.Done = true
			m.WantsMenu = true
			return m, nil
		}
		return m, tea.Quit
	})
}

// handleExecutedMenu is executedMenu's keys, for the after-picture and
// for a mutation that stopped partway.
func (m RepairModel) handleExecutedMenu(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	return m.handleMenu(msg, executedMenu, func(label string) (tea.Model, tea.Cmd) {
		switch label {
		case "Diagnose again":
			return m.diagnoseAgain()
		case "Menu":
			m.Done = true
			m.WantsMenu = true
			return m, nil
		default:
			return m, tea.Quit
		}
	})
}

// diagnoseAgain starts a fresh diagnosis, owner check first, from the
// after-picture, a stopped mutation or the foreign-files stop.
func (m RepairModel) diagnoseAgain() (tea.Model, tea.Cmd) {
	m.startExecution()
	m.planActions, m.planSummary = nil, nil
	m.state = repairPreparing
	m.mode = deploy.RepairPlan
	return m, m.startDiagnosis(deploy.RepairPlan)
}

// stoppedMutation says the error screen is showing a safe repair or
// rotation whose script had started: the deployment may be partly
// changed, so it gets the after-picture's menu, not the read-only one.
// A start failure ran nothing and keeps the plain menu.
func (m RepairModel) stoppedMutation() bool {
	// A usage error is the script refusing before it did anything.
	return m.state == repairError && m.stream != nil && m.exitCode != engine.ExitUsage &&
		(m.stoppedIn == repairExecuting || m.stoppedIn == repairRotating)
}

// handleMenu drives a menu screen over items: Esc is back to the main
// menu, and Enter hands choose the selected row's label (menu.go), so
// contextual menus can't drift out of sync with selection indexes.
func (m RepairModel) handleMenu(msg tea.KeyPressMsg, items []string, choose func(string) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	var chosen string
	var back bool
	m.menu, chosen, back = m.menu.over(items).onScreen(msg)
	if back {
		m.Done = true
		m.WantsMenu = true
		return m, nil
	}
	if chosen == "" {
		return m, nil
	}
	return choose(chosen)
}

// handleRotateKey is the rotation session's typing surface — the same
// promptSession as the in-console configuration prompts. Esc closes the
// session's input, which the engine treats as its documented abort at
// its next prompt; a rotation already past its prompts is left to
// finish. Either way the run ends on the after-picture.
func (m RepairModel) handleRotateKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.Code == tea.KeyEsc {
		return m.stopRotation()
	}
	m.rot, _, _ = m.rot.key(msg, m.stdin)
	return m, nil
}

// stopRotation closes the rotation's input and waits: the engine is
// never signalled, because what it already began must be backed out or
// finished by the engine itself. With input already closed, or not yet
// open, there is nothing to do.
func (m RepairModel) stopRotation() (tea.Model, tea.Cmd) {
	if m.stdin == nil {
		return m, nil
	}
	m.stdin.Close()
	m.stdin = nil
	m.rot = promptSession{}
	return m, nil
}

// View implements tea.Model.
func (m RepairModel) View() tea.View { return tea.NewView(m.view()) }

// cannotStopLine is the second line of a working screen whose step
// changes the machine and so ignores Ctrl-C and Esc while it runs.
// Remove's stand-down has its own line, since stopping it is not unsafe.
const cannotStopLine = "can't be stopped from here — stopping halfway is the one unsafe step"

// view renders the screen's content.
func (m RepairModel) view() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case repairPreparing:
		return skyBlock(m.star, m.width, m.height, workingLine(m.frame, "reading the deployment", "nothing will be changed"))
	case repairExecuting:
		return skyBlock(m.star, m.width, m.height, workingLine(m.frame, "running the safe repairs — every step reversible, backups first", cannotStopLine))
	case repairRotating:
		return m.viewRotating()
	case repairExecuted:
		return m.viewExecuted()
	case repairUnavailable:
		return m.viewUnavailable()
	case repairForeign:
		return m.viewForeign()
	case repairError:
		return m.viewError()
	case repairNotFound:
		return viewNoDeployment(m.star, m.width, m.height, m.targetDir,
			"There's nothing here for Repair to read — nothing was changed.")
	}
	return m.viewDiagnosis()
}

// viewRotating is the rotation session: the same input grammar as the
// in-console configuration prompts, under a title that says exactly
// what is at stake.
func (m RepairModel) viewRotating() string {
	var b strings.Builder
	writeHeader(&b, style.DegradedText.Render(style.SymbolMark), "Rotate database credentials")
	fmt.Fprintln(&b, style.MutedText.Render("A passphrase-protected backup of the current credential is"))
	fmt.Fprintln(&b, style.MutedText.Render("taken and verified before anything changes."))
	fmt.Fprintln(&b)

	if m.stream != nil && m.stdin == nil {
		// Input closed, engine still running: it is backing out at its
		// next prompt or finishing a step it already began.
		fmt.Fprintln(&b, workingLine(m.frame, "stopping", "waiting for the engine to back out or finish what it began"))
		return skyBlock(m.star, m.width, m.height, b.String())
	}

	m.rot.view(&b, "", m.width, m.frame)
	return skyBlock(m.star, m.width, m.height, b.String())
}

// viewExecuted is the after-picture: what ran, how it ended, and the
// fresh re-diagnosis the executor printed.
func (m RepairModel) viewExecuted() string {
	var b strings.Builder
	var glyph, title string

	result := ""
	if m.execSummary != nil {
		result = m.execSummary.Result
	}
	switch result {
	case engine.ResultComplete:
		glyph, title = style.SuccessText.Render(style.SymbolMark), "Repairs applied"
	case engine.ResultDeclined:
		glyph, title = style.DegradedText.Render(style.SymbolMark), "Nothing was changed"
	case engine.ResultUnactionable:
		glyph, title = style.DegradedText.Render(style.SymbolMark), "Nothing safe to run"
	case engine.ResultEmpty:
		glyph, title = style.SuccessText.Render(style.SymbolMark), "Nothing to repair"
	case engine.ResultFailed:
		glyph, title = style.ErrorText.Render(style.SymbolFailure), "Some repairs failed"
	case engine.ResultRefused:
		glyph, title = style.DegradedText.Render(style.SymbolMark), "Repair refused"
	default:
		glyph, title = style.DegradedText.Render(style.SymbolMark), "Repair run ended"
	}
	writeInlineHeader(&b, glyph, title)
	// The refusal lines wrap at the screen's width, never past it (#287).
	limit := measure(m.width, errorWidth)

	for _, e := range m.executeResults {
		fmt.Fprintln(&b, executeLine(e))
	}
	if len(m.executeResults) > 0 {
		fmt.Fprintln(&b)
	}
	if m.execSummary != nil && (m.execSummary.Done > 0 || m.execSummary.Failed > 0) {
		fmt.Fprintln(&b, style.Tagline.Render(fmt.Sprintf("%d done%s%d failed", m.execSummary.Done, style.Separator, m.execSummary.Failed)))
	}
	if m.execSummary != nil && m.execSummary.Result == engine.ResultRefused {
		// The whole run was refused before any batch ran; the reason
		// enum says why, and the exit code alone cannot.
		switch m.execSummary.Reason {
		case "deployment-version-unsupported":
			writeWrappedIn(&b, style.DegradedText, "nothing was changed — this deployment's Orbit release is too old to repair here", limit, textArea(m.width))
			writeWrappedIn(&b, style.DegradedText, "upgrade or reinstall it with a supported release, then diagnose again", limit, textArea(m.width))
		default:
			refusal := "nothing was changed — the engine refused to run"
			if m.execSummary.Reason != "" {
				// The engine's word, escaped like any other foreign
				// text before it reaches the terminal (#277).
				refusal += " (" + visibleText(m.execSummary.Reason) + ")"
			}
			writeWrappedIn(&b, style.DegradedText, refusal, limit, textArea(m.width))
		}
	}
	if m.dangerous != nil && m.dangerous.Result == engine.ResultRefused {
		// The dangerous batch's gate was never passed. Nothing was
		// rotated and nothing failed — say only that, and say why when
		// the stream told us, because "you declined" and "there was no
		// terminal to ask" send the operator to different next steps.
		refusal := "credentials left as they were — nothing was rotated"
		if m.dangerous.Reason == "non-interactive" {
			refusal += " (no terminal was available to approve it)"
		}
		writeWrappedIn(&b, style.DegradedText, refusal, limit, textArea(m.width))
	}

	// The after-picture: the executor's own honest re-diagnosis.
	if m.diagnosis != nil {
		if m.diagnosis.Result == engine.ResultHealthy {
			fmt.Fprintln(&b, style.SuccessText.Render("diagnosis clear after repairs"))
		} else {
			fmt.Fprintln(&b, style.Tagline.Render("still standing after repairs:"))
			for _, f := range sortedFindings(m.findings) {
				fmt.Fprintln(&b, findingLine(f))
			}
		}
	}
	fmt.Fprintln(&b)
	m.menu.over(executedMenu).write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}

// executeLine renders one execution outcome in honest words.
func executeLine(e engine.ExecuteResult) string {
	var glyph string
	switch e.Result {
	case engine.ResultDone:
		glyph = style.SuccessText.Render(style.SymbolSuccess)
	case engine.ResultFailed:
		glyph = style.ErrorText.Render(style.SymbolFailure)
	default:
		glyph = style.Tagline.Render(style.SymbolQueued)
	}
	words := style.Ink.Render(actionWords(e.Action))
	if e.Result == engine.ResultSkipped {
		words = style.Tagline.Render(actionWords(e.Action))
	}
	return glyph + " " + words + style.Tagline.Render(" — "+classWords(e.Resolves))
}

func (m RepairModel) viewUnavailable() string {
	var b strings.Builder
	writeHeader(&b, style.AccentText.Render(style.SymbolMark), "Diagnosis needs a newer Orbit")
	fmt.Fprintln(&b, style.MutedText.Render("This Orbit release doesn't publish its repair diagnosis"))
	fmt.Fprintln(&b, style.MutedText.Render("yet. Nothing on the deployment was touched."))
	fmt.Fprintln(&b)
	m.menu.over(repairMenu).write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m RepairModel) viewError() string {
	var b strings.Builder
	title := "Diagnosis stopped"
	switch m.stoppedIn {
	case repairExecuting:
		title = "Repairs stopped"
	case repairRotating:
		title = "Credential rotation stopped"
	}
	writeInlineHeader(&b, style.ErrorText.Render(style.SymbolFailure), title)
	if m.runErr != nil {
		// Docker's words, at Docker's length.
		writeWrappedIn(&b, style.Tagline, errorWords(m.runErr), measure(m.width, errorWidth), textArea(m.width))
	}
	rows := repairMenu
	if m.stoppedMutation() {
		writeWrappedIn(&b, style.DegradedText, "it may have changed the deployment partway — backups were taken first; diagnose again to see where it stands", measure(m.width, errorWidth), textArea(m.width))
		rows = executedMenu
	}
	if m.runErr != nil || m.stoppedMutation() {
		fmt.Fprintln(&b)
	}
	m.menu.over(rows).write(&b)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m RepairModel) viewDiagnosis() string {
	var b strings.Builder

	result := ""
	if m.diagnosis != nil {
		result = m.diagnosis.Result
	}
	var glyph, title string
	switch {
	case m.planSummary != nil && m.planSummary.Result == engine.ResultEmpty,
		result == engine.ResultHealthy,
		m.planSummary != nil && m.exitCode == engine.ExitHealthy:
		glyph, title = style.SuccessText.Render(style.SymbolMark), "Diagnosis clear"
	case m.planSummary != nil && m.exitCode == engine.ExitPlanAvailable:
		// Plan mode's stdout carries only the plan (verified against
		// the real script): the proposal is the story.
		glyph, title = style.DegradedText.Render(style.SymbolMark), "Repairs proposed"
	case m.planSummary != nil:
		glyph, title = style.DegradedText.Render(style.SymbolMark), "Needs your attention"
	case result == engine.ResultAttention:
		glyph, title = style.DegradedText.Render(style.SymbolMark), "Needs attention"
	default:
		glyph, title = style.ErrorText.Render(style.SymbolFailure), "Problems found"
	}
	writeInlineHeader(&b, glyph, title)
	head := b.String()
	b.Reset()

	var summary strings.Builder
	if len(m.findings) > 0 {
		fmt.Fprintln(&summary)
	}
	if m.diagnosis != nil {
		fmt.Fprintln(&summary, style.Tagline.Render(fmt.Sprintf("%d checked%s%d skipped", m.diagnosis.Checked, style.Separator, m.diagnosis.Skipped)))
	}

	steps, after := m.planParts(result)
	var tail strings.Builder
	for _, row := range after {
		fmt.Fprintln(&tail, row)
	}
	fmt.Fprintln(&tail)
	m.menu.over(m.planMenu()).write(&tail)

	// The services block gives way first (#290): it takes the rows the
	// rest leaves with the findings and the whole plan at full length,
	// and shrinks in steps (servicesBlock) before anything else. Only
	// then do the findings shorten, taking the rows the header, the
	// block, the summary, the whole plan and the menu leave, so an older
	// repair.sh with many findings never pushes the menu off the screen.
	// The last row is left to the sky, as on every scene screen.
	stepRows := 0
	for _, step := range steps {
		stepRows += len(step)
	}
	var found [][]string
	for _, f := range sortedFindings(m.findings) {
		found = append(found, []string{findingLine(f)})
	}
	fixed := 1 + strings.Count(head, "\n") + strings.Count(summary.String(), "\n") + strings.Count(tail.String(), "\n") + stepRows
	svcRoom := math.MaxInt
	if m.height > 0 {
		svcRoom = m.height - fixed - len(found)
	}
	block := m.servicesBlock(svcRoom)
	findRoom := math.MaxInt
	if m.height > 0 {
		findRoom = m.height - fixed - len(block)
	}
	b.WriteString(head)
	for _, row := range block {
		fmt.Fprintln(&b, row)
	}
	m.writeSteps(&b, found, findRoom)
	b.WriteString(summary.String())

	// The steps take whatever rows remain, so a short terminal
	// never pushes the summary and menu off the bottom.
	room := math.MaxInt
	if m.height > 0 {
		room = m.height - 1 - strings.Count(b.String(), "\n") - strings.Count(tail.String(), "\n")
	}
	m.writeSteps(&b, steps, room)
	b.WriteString(tail.String())
	return skyBlock(m.star, m.width, m.height, b.String())
}

// writeSteps writes the plan's steps (or the findings, one row each)
// into room rows: all of them when they fit, otherwise as many whole
// steps as fit and then one row saying how many are not shown.
func (m RepairModel) writeSteps(b *strings.Builder, steps [][]string, room int) {
	total := 0
	for _, step := range steps {
		total += len(step)
	}
	// With no steps there is nothing to count as "more", however
	// little room the findings left.
	if total <= room || len(steps) == 0 {
		for _, step := range steps {
			for _, row := range step {
				fmt.Fprintln(b, row)
			}
		}
		return
	}
	shown := len(steps)
	var more []string
	for {
		more = wrapWordsIn(fmt.Sprintf("and %d more — make the terminal taller to see them", len(steps)-shown), measure(m.width, proseWidth), textArea(m.width))
		used := len(more)
		for _, step := range steps[:shown] {
			used += len(step)
		}
		if used <= room || shown == 0 {
			break
		}
		shown--
	}
	for _, step := range steps[:shown] {
		for _, row := range step {
			fmt.Fprintln(b, row)
		}
	}
	for _, row := range more {
		fmt.Fprintln(b, style.Tagline.Render(row))
	}
}

// planParts lays out the proposed plan (orbit#261 slice 3): what the
// engine would do, classified — and the plain truth that nothing has
// run yet. steps holds each plan line and manual step as its screen
// rows; after is the rows that follow them, which are always shown.
func (m *RepairModel) planParts(result string) (steps [][]string, after []string) {
	if len(m.planActions) == 0 {
		// No plan lines: healthy, a --check fallback run, or nothing
		// plannable. Keep the honest note whenever something's wrong.
		if result != engine.ResultHealthy {
			after = append(after, m.taglineRows("repair actions arrive with a later Orbit release")...)
		}
		return nil, after
	}

	// The plan rows' text wraps at the same measure as the manual
	// steps below, so the two never break at different widths.
	for _, p := range m.planActions {
		steps = append(steps, planRowsIn(p, measure(m.width, proseWidth), textArea(m.width)))
	}
	for _, note := range m.manualNotes {
		if step, ok := strings.CutPrefix(note, "manual step: "); ok {
			// The engine's guidance is unsized prose with a machine tag
			// on the end: drop the tag (the plan line above already
			// names the problem) and wrap the rest, so it is never cut
			// at the screen's edge (#253). A step can name a path or a
			// command to run: a word that fits the text area stays whole,
			// a wider one is cut at the measure, every character kept
			// (#277 L4).
			lines := wrapWordsIn(manualStepWords(step), measure(m.width, proseWidth), textArea(m.width))
			if len(lines) == 0 {
				lines = []string{""}
			}
			rows := make([]string, len(lines))
			for i, line := range lines {
				rows[i] = style.Tagline.Render(line)
			}
			steps = append(steps, rows)
		}
	}
	return steps, append([]string{""}, m.taglineRows(planSummaryWords(m.planSummary, m.planHasSafe() || m.planHasDangerous()))...)
}

// taglineRows is a line under the plan, one row where it fits the text
// area and wrapped to it where it does not, so no row is wider than the
// text area (#277 L4: the 48-cell lines wrap at 50 columns).
func (m RepairModel) taglineRows(text string) []string {
	lines := wrapWordsIn(text, textArea(m.width), textArea(m.width))
	rows := make([]string, len(lines))
	for i, line := range lines {
		rows[i] = style.Tagline.Render(line)
	}
	return rows
}

// planRows renders one proposed action: the action in plain words and
// what it resolves, with the backup requirement when the contract
// demands one. The mutation class is deliberately not repeated — the
// action words already carry it. Unknown enum values render as
// visibleText. Plan mode's stdout carries no separate finding lines, so
// the resolves class is the reader's only pointer at the underlying
// problem — always shown. Its text wraps at limit cells: a line
// too long for the row wraps as wrapWords does, its continuation rows
// indented under the text after the glyph, so it is never cut at the
// edge and "backup first" stays on screen. The action words keep their
// ink on whichever rows they land. A screen draws through planRowsIn.
func planRows(p engine.PlanAction, limit int) []string {
	return planRowsIn(p, limit, limit)
}

// planRowsIn is planRows wrapped as wrapWordsIn wraps, for a screen whose
// text area is edge cells wide.
func planRowsIn(p engine.PlanAction, limit, edge int) []string {
	glyph := style.AccentText.Render(style.SymbolQueued)
	if p.Action == "manual" {
		glyph = style.DegradedText.Render(style.SymbolQueued)
	}
	lines := wrapWordsIn(planText(p), limit, edge)
	// The action words lead the text; wrapWords drops only the space at
	// each break, so each row's ink is what it shares with the action
	// words not yet drawn (matched, not counted, as an unknown action
	// comes verbatim and cleaning may change it).
	ink := actionWords(p.Action)
	rows := make([]string, len(lines))
	for i, line := range lines {
		lead := "  "
		if i == 0 {
			lead = glyph + " "
		}
		n := 0
		for n < len(line) && n < len(ink) && line[n] == ink[n] {
			n++
		}
		for n > 0 && n < len(line) && !utf8.RuneStart(line[n]) {
			n--
		}
		rows[i] = lead + style.Ink.Render(line[:n]) + style.Tagline.Render(line[n:])
		ink = strings.TrimPrefix(ink[n:], " ")
		if n == 0 {
			ink = ""
		}
	}
	// Every row is centred on its own, so pad a wrapped line's rows to
	// one width: their left edges then align and the indent shows.
	blockWidth := 0
	for _, row := range rows {
		blockWidth = max(blockWidth, lipgloss.Width(row))
	}
	for i, row := range rows {
		rows[i] = row + strings.Repeat(" ", blockWidth-lipgloss.Width(row))
	}
	return rows
}

// planText is a plan line's words after its glyph, unstyled.
func planText(p engine.PlanAction) string {
	context := planReasonWords(p.Action, p.Resolves)
	if p.Backup == "required" {
		context += style.Separator + "backup first"
	}
	return actionWords(p.Action) + " — " + context
}

// manualStepWords is a manual step's guidance without the engine's
// trailing "(resolves=<class>)" tag, which is protocol, not words.
func manualStepWords(step string) string {
	if i := strings.LastIndex(step, "(resolves="); i >= 0 && strings.HasSuffix(step, ")") {
		step = strings.TrimSpace(step[:i])
	}
	return step
}

// planReasonWords is what a plan line resolves, with its subject named.
// A finding line puts its target before classWords ("database — can't
// be reached"); a plan line has none, so a class whose words lack a
// subject gets one (#253) — unless the action already names it
// ("rotate database credentials"), where repeating it would only push
// the line past 80 cells.
func planReasonWords(action, class string) string {
	var noun, lead string
	switch class {
	case "database-unreachable", "database-credential-mismatch":
		noun, lead = "database", "the database"
	case "database-schema-mismatch":
		noun, lead = "database", "the database was"
	case "application-unhealthy":
		noun, lead = "application", "the application"
	case "stale-container":
		noun, lead = "container", "a container is"
	case "secret-missing":
		noun, lead = "secret", "a secret is"
	case "managed-file-missing", "managed-file-symlink":
		noun, lead = "managed file", "a managed file"
		if class == "managed-file-missing" {
			lead += " is"
		}
	default:
		return classWords(class)
	}
	if strings.Contains(actionWords(action), noun) {
		return classWords(class)
	}
	return lead + " " + classWords(class)
}

// actionWords maps plan action classes to plain words.
func actionWords(action string) string {
	switch action {
	case "fix-permissions":
		return "restore safe permissions"
	case "rerun-configuration":
		return "re-run guided configuration"
	case "restore-transaction":
		return "restore the interrupted install transaction"
	case "rotate-database-credential":
		return "rotate database credentials"
	case "regenerate-secret":
		return "regenerate the secret"
	case "restart-services":
		return "restart Orbit's services"
	case "manual":
		return "needs your hands"
	default:
		return visibleText(action)
	}
}

// planSummaryWords is the one-line truth under the plan. runnable says
// whether the menu below offers a repair to run, so the line points at
// it when it does and never claims execution is unavailable.
func planSummaryWords(s *engine.PlanSummary, runnable bool) string {
	if s == nil {
		if runnable {
			return "nothing has run yet — pick a repair below to run it"
		}
		return "nothing has run yet"
	}
	switch s.Result {
	case engine.ResultReady:
		if runnable {
			return "a safe plan is ready — pick a repair below to run it"
		}
		return "a safe plan is ready — nothing has run yet"
	case engine.ResultManualRequired:
		if runnable {
			return "some steps need your hands — the rest can run from the menu below"
		}
		return "some steps need your hands — nothing has run yet"
	case engine.ResultEmpty:
		return "nothing to plan"
	default:
		return visibleText(s.Result) + " — nothing has run yet"
	}
}

// sortedFindings orders fail before warn before info, preserving the
// engine's own order within a severity.
func sortedFindings(findings []engine.Finding) []engine.Finding {
	rank := map[string]int{"fail": 0, "warn": 1, "info": 2}
	out := make([]engine.Finding, len(findings))
	copy(out, findings)
	sort.SliceStable(out, func(i, j int) bool {
		ri, ok := rank[out[i].Severity]
		if !ok {
			ri = 3
		}
		rj, ok := rank[out[j].Severity]
		if !ok {
			rj = 3
		}
		return ri < rj
	})
	return out
}

// findingLine renders one finding: severity glyph, the target in plain
// words, the reason class in plain words. Unknown enum values render
// verbatim as visibleText — honest, never guessed at.
func findingLine(f engine.Finding) string {
	glyph := style.Tagline.Render(style.SymbolQueued)
	switch f.Severity {
	case "fail":
		glyph = style.ErrorText.Render(style.SymbolFailure)
	case "warn":
		glyph = style.DegradedText.Render(style.SymbolWarn)
	}
	return glyph + " " + style.Ink.Render(targetWords(f.Target)) + style.Tagline.Render(" — "+classWords(f.Class))
}

// targetWords maps target classes to the thing a person knows.
func targetWords(target string) string {
	switch target {
	case "directory":
		return "deployment directory"
	case "env-file":
		return deploy.EnvFile
	case "compose-file":
		return "docker-compose.yml"
	case "compose":
		return "compose configuration"
	case "configuration":
		return "configuration"
	case "secrets-directory":
		return "secrets directory"
	case "session-secret", "postgres-password", "document-kek", "oidc-client-secret":
		return target + " secret"
	case "staging":
		return "installer staging"
	case "container":
		return "containers"
	case "database-volume":
		return "database volume"
	case "database":
		return "database"
	case "application":
		return "application"
	default:
		// The engine's value as given, escaped: it is drawn on screen.
		return visibleText(target)
	}
}

// classWords maps reason classes to honest words.
func classWords(class string) string {
	switch class {
	case "not-orbit-directory":
		return "no recognisable Orbit installation"
	case "managed-file-missing":
		return "missing"
	case "managed-file-symlink":
		return "is a symlink, refusing to trust it"
	case "managed-file-permissions":
		return "permissions aren't restricted to the owner"
	case "secrets-directory-invalid":
		return "missing, symlinked or permissions too open"
	case "secret-missing":
		return "absent or empty"
	case "secret-permissions":
		return "wrong type or permissions"
	case "configuration-incomplete":
		return "required fields aren't ready"
	case "configuration-invalid":
		return "unreadable or structurally broken"
	case "staging-evidence-present":
		return "an interrupted install left staging behind"
	case "configuration-migration-interrupted":
		return "a configuration change was interrupted"
	case "document-volume-retained-without-key":
		return "document data kept but its encryption key is gone"
	case "deployment-version-unsupported":
		return "no valid recorded Orbit version"
	case "compose-interpolation-failed":
		return "compose files don't resolve"
	case "docker-unavailable":
		return "docker couldn't be reached for this check"
	case "container-foreign-owner":
		return "a container in this project isn't Orbit's"
	case "volume-retained-without-credentials":
		return "database data kept but its credentials are gone"
	case "unrelated-resource-present":
		return "an unrelated Orbit-like volume exists"
	// The database/container layer (orbit#261 slice 2), plus the
	// classes its contract documents as reserved for the executor
	// slice — named now so they render honestly the day they ship.
	case "database-unreachable":
		return "can't be reached"
	case "database-credential-mismatch":
		return "rejects the stored credentials"
	case "database-credential-unverifiable":
		// Orbit's application restarts faster than repair can read the
		// credential it presents, so nothing was compared.
		return "password couldn't be checked: the application restarts too fast"
	case "stale-container":
		return "running an older image than configured"
	case "application-unhealthy":
		return "reports unhealthy"
	case "unsupported-schema":
		return "schema newer than this engine supports"
	case "migration-failed":
		return "a migration failed"
	case "image-identity-mismatch":
		return "image identity doesn't match the record"
	case "database-schema-mismatch":
		return "migrated by a different Orbit version"
	default:
		// The engine's value as given, escaped: it is drawn on screen.
		return visibleText(class)
	}
}
