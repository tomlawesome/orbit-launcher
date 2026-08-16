package ui

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// RepairModel is the real read-only diagnosis and plan (orbit#261
// slices 1–3): the launcher fetches orbit's standalone repair.sh
// fresh, stages it into the deployment, runs `--plan` (diagnosis plus
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

	// Execution evidence (orbit#261 slice 4): what ran, how it ended,
	// with findings/diagnosis holding the post-execution re-diagnosis.
	executeResults []engine.ExecuteResult
	execSummary    *engine.ExecutionSummary

	// The dangerous rotation's machine-prompt session.
	stdin     io.WriteCloser
	rotPrompt *engine.Prompt
	rotReason string
	rotInput  []rune

	stream  *engine.Stream
	menuSel int

	// Done/WantsMenu surface the outcome to AppModel, exactly like a
	// flow's engine run.
	Done      bool
	WantsMenu bool

	// Test seams; nil gets the real implementations.
	prepare       prepareRepairFunc
	prepareRotate prepareRotateFunc
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

// NewRepairModel constructs the Repair flow for targetDir.
func NewRepairModel(targetDir, version string) RepairModel {
	return RepairModel{targetDir: targetDir, version: version, mode: deploy.RepairPlan}
}

// Outcome surfaces the flow result to AppModel.
func (m RepairModel) Outcome() flowOutcome {
	return flowOutcome{Done: m.Done, WantsMenu: m.WantsMenu}
}

// Init implements tea.Model: the diagnosis starts immediately — it is
// read-only by the script's own contract, so there is nothing to
// confirm first.
func (m RepairModel) Init() tea.Cmd {
	return m.startRun(m.mode)
}

// startRun launches one read-only repair invocation in the given mode.
func (m RepairModel) startRun(mode deploy.RepairMode) tea.Cmd {
	prepare := m.prepare
	if prepare == nil {
		prepare = defaultPrepareRepair
	}
	targetDir := m.targetDir
	return func() tea.Msg {
		stream, err := prepare(context.Background(), targetDir, mode)
		return repairReadyMsg{stream: stream, err: err}
	}
}

// defaultPrepareRepair fetches, stages and starts one repair run.
func defaultPrepareRepair(ctx context.Context, targetDir string, mode deploy.RepairMode) (*engine.Stream, error) {
	script, err := deploy.FetchRepairScript(ctx)
	if err != nil {
		return nil, err
	}
	if err := deploy.StageRepairScript(targetDir, script); err != nil {
		return nil, err
	}
	return engine.Start(deploy.BuildRepairCommand(targetDir, mode))
}

// defaultPrepareRotate starts the dangerous rotation with its stdin
// piped — the machine-prompt transport the script demands.
func defaultPrepareRotate(ctx context.Context, targetDir string) (*engine.Stream, io.WriteCloser, error) {
	script, err := deploy.FetchRepairScript(ctx)
	if err != nil {
		return nil, nil, err
	}
	if err := deploy.StageRepairScript(targetDir, script); err != nil {
		return nil, nil, err
	}
	return engine.StartInteractive(deploy.BuildRepairCommand(targetDir, deploy.RepairExecuteDangerous))
}

// startExecution resets the evidence a fresh run will replace and
// launches it. The re-diagnosis the executor prints lands in the same
// findings/diagnosis fields the plan screen used.
func (m *RepairModel) startExecution() {
	m.findings, m.diagnosis = nil, nil
	m.executeResults, m.execSummary = nil, nil
	m.manualNotes = nil
	m.stream, m.stdin = nil, nil
	m.rotPrompt, m.rotReason, m.rotInput = nil, "", nil
}

func (m RepairModel) startRotate() tea.Cmd {
	prepare := m.prepareRotate
	if prepare == nil {
		prepare = defaultPrepareRotate
	}
	targetDir := m.targetDir
	return func() tea.Msg {
		stream, stdin, err := prepare(context.Background(), targetDir)
		return repairRotateReadyMsg{stream: stream, stdin: stdin, err: err}
	}
}

func pumpRepair(s *engine.Stream) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-s.C
		if !ok {
			return nil
		}
		return repairStreamMsg{msg: msg}
	}
}

// Update implements tea.Model.
func (m RepairModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		// See RemoveModel.Update: the sky is built here, the tick chain
		// is not — there is exactly one, and it is already running.
		m.width, m.height = msg.Width, msg.Height
		m.star = starfield.New(msg.Width, msg.Height, 1)
		return m, nil

	case tickMsg:
		m.star = m.star.Advance()
		return m, tick()

	case repairReadyMsg:
		if msg.err != nil {
			m.runErr = msg.err
			m.state = repairError
			if msg.err == deploy.ErrRepairUnavailable {
				m.state = repairUnavailable
			}
			return m, nil
		}
		m.stream = msg.stream
		return m, pumpRepair(m.stream)

	case repairRotateReadyMsg:
		if msg.err != nil {
			m.runErr = msg.err
			m.state = repairError
			return m, nil
		}
		m.stream = msg.stream
		m.stdin = msg.stdin
		return m, pumpRepair(m.stream)

	case repairStreamMsg:
		return m.handleStream(msg.msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
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
			if parsed, ok := engine.ParsePromptLine(s.Text); ok {
				switch p := parsed.(type) {
				case engine.Prompt:
					prompt := p
					m.rotPrompt = &prompt
					m.rotInput = nil
				case engine.PromptReject:
					m.rotReason = rejectionWords(p.Reason)
					m.rotPrompt = nil
				case engine.PromptAccept:
					m.rotReason = ""
					m.rotPrompt = nil
				case engine.PromptAbort:
					m.rotPrompt = nil
				}
				return m, pumpRepair(m.stream)
			}
		}
		if f, ok := engine.ParseFinding(s.Text); ok {
			m.findings = append(m.findings, f)
		} else if e, ok := engine.ParseExecuteResult(s.Text); ok {
			m.executeResults = append(m.executeResults, e)
		} else if es, ok := engine.ParseExecutionSummary(s.Text); ok {
			summary := es
			m.execSummary = &summary
		} else if p, ok := engine.ParsePlanAction(s.Text); ok {
			m.planActions = append(m.planActions, p)
		} else if ps, ok := engine.ParsePlanSummary(s.Text); ok {
			summary := ps
			m.planSummary = &summary
		} else if d, ok := engine.ParseDiagnosis(s.Text); ok {
			diag := d
			m.diagnosis = &diag
		}
		return m, pumpRepair(m.stream)
	case engine.EventMsg:
		// repair.sh emits no phase events; tolerate and move on.
		return m, pumpRepair(m.stream)
	case engine.DoneMsg:
		return m.handleDone(s)
	}
	return m, nil
}

func (m RepairModel) handleDone(s engine.DoneMsg) (tea.Model, tea.Cmd) {
	m.exitCode = s.ExitCode
	m.stdin = nil

	switch m.state {
	case repairExecuting, repairRotating:
		// Execution outcomes come from the parsed execution summary,
		// with the exit code as the honest fallback.
		if m.execSummary == nil && s.ExitCode != 0 {
			m.runErr = s.Err
			m.state = repairError
			m.menuSel = 0
			return m, nil
		}
		m.state = repairExecuted
		m.menuSel = 0
		return m, nil
	}

	switch s.ExitCode {
	case 0, 3, 4, 5:
		// The contract's outcomes — shared by --check and --plan,
		// including "this isn't an orbit installation", which arrives
		// with its own finding line and deserves the same honest
		// rendering.
		m.state = repairDiagnosis
		if m.mode == deploy.RepairPlan {
			// The manual guidance lines ride stderr, value-free by
			// the script's contract.
			m.manualNotes = s.StderrTail
		}
	case 2:
		if m.mode == deploy.RepairPlan {
			// A repair.sh too old for --plan: usage error. Fall back
			// to the diagnosis it does speak.
			m.mode = deploy.RepairCheck
			m.findings, m.diagnosis = nil, nil
			m.planActions, m.planSummary, m.manualNotes = nil, nil, nil
			m.stream = nil
			m.menuSel = 0
			return m, m.startRun(deploy.RepairCheck)
		}
		m.runErr = s.Err
		m.state = repairError
	default:
		m.runErr = s.Err
		m.state = repairError
	}
	m.menuSel = 0
	return m, nil
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

func (m RepairModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		if m.stream != nil {
			m.stream.Kill()
		}
		return m, tea.Quit
	}

	switch m.state {
	case repairPreparing, repairExecuting:
		if msg.Type == tea.KeyEsc && m.state == repairPreparing {
			// The read-only run can be abandoned freely; a running
			// execution is left to finish — killing a mutation
			// mid-flight is the one thing more dangerous than running
			// it.
			if m.stream != nil {
				m.stream.Kill()
			}
			m.Done = true
			m.WantsMenu = true
		}
		return m, nil

	case repairRotating:
		return m.handleRotateKey(msg)

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
		return m.handleMenu(msg, executedMenu, func(label string) (tea.Model, tea.Cmd) {
			switch label {
			case "Diagnose again":
				m.startExecution()
				m.planActions, m.planSummary = nil, nil
				m.state = repairPreparing
				m.mode = deploy.RepairPlan
				return m, m.startRun(deploy.RepairPlan)
			case "Menu":
				m.Done = true
				m.WantsMenu = true
				return m, nil
			default:
				return m, tea.Quit
			}
		})
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

// handleMenu is the shared stacked-menu key handling, dispatching by
// label so contextual menus can't drift out of sync with selection
// indexes.
func (m RepairModel) handleMenu(msg tea.KeyMsg, items []string, choose func(string) (tea.Model, tea.Cmd)) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.Done = true
		m.WantsMenu = true
		return m, nil
	case tea.KeyUp:
		m.menuSel = (m.menuSel - 1 + len(items)) % len(items)
		return m, nil
	case tea.KeyDown:
		m.menuSel = (m.menuSel + 1) % len(items)
		return m, nil
	case tea.KeyEnter:
		return choose(items[m.menuSel])
	}
	return m, nil
}

// handleRotateKey is the rotation session's typing surface — the same
// grammar as the in-console configuration prompts. Esc abandons the
// session; the engine treats closed input as its documented abort and
// changes nothing.
func (m RepairModel) handleRotateKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyEsc {
		if m.stdin != nil {
			m.stdin.Close()
			m.stdin = nil
		}
		if m.stream != nil {
			m.stream.Kill()
		}
		m.state = repairDiagnosis
		m.menuSel = 0
		return m, nil
	}
	if m.rotPrompt == nil {
		return m, nil
	}
	switch msg.Type {
	case tea.KeyRunes:
		m.rotInput = append(m.rotInput, msg.Runes...)
	case tea.KeySpace:
		m.rotInput = append(m.rotInput, ' ')
	case tea.KeyBackspace:
		if len(m.rotInput) > 0 {
			m.rotInput = m.rotInput[:len(m.rotInput)-1]
		}
	case tea.KeyEnter:
		if m.stdin != nil {
			fmt.Fprintln(m.stdin, string(m.rotInput))
		}
		m.rotPrompt = nil
		m.rotInput = nil
	}
	return m, nil
}

// View implements tea.Model.
func (m RepairModel) View() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case repairPreparing:
		return skyBlock(m.star, m.width, m.height, style.MutedText.Render("reading the deployment — nothing will be changed"))
	case repairExecuting:
		return skyBlock(m.star, m.width, m.height, style.WarmText.Render("⠋")+" "+style.MutedText.Render("running the safe repairs — every step reversible, backups first"))
	case repairRotating:
		return m.viewRotating()
	case repairExecuted:
		return m.viewExecuted()
	case repairUnavailable:
		return m.viewUnavailable()
	case repairError:
		return m.viewError()
	}
	return m.viewDiagnosis()
}

// viewRotating is the rotation session: the same input grammar as the
// in-console configuration prompts, under a title that says exactly
// what is at stake.
func (m RepairModel) viewRotating() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Rotate database credentials"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("A passphrase-protected backup of the current credential is"))
	fmt.Fprintln(&b, style.MutedText.Render("taken and verified before anything changes."))
	fmt.Fprintln(&b)

	p := m.rotPrompt
	if p == nil {
		fmt.Fprintln(&b, style.MutedText.Render("talking to the engine…"))
		return skyBlock(m.star, m.width, m.height, b.String())
	}

	label, hint, notes := promptWords(p.Field, "")
	fmt.Fprintln(&b, lipgloss.NewStyle().Foreground(style.Text).Render(label))
	if hint != "" {
		fmt.Fprintln(&b, style.Tagline.Render(hint))
	}
	for _, note := range notes {
		fmt.Fprintln(&b, style.MutedText.Render(note))
	}
	fmt.Fprintln(&b)
	shown := string(m.rotInput)
	if p.Kind == "secret" {
		shown = ""
	}
	fmt.Fprintln(&b, style.MenuCaret.Render(style.SymbolSelected)+" "+lipgloss.NewStyle().Foreground(style.Text).Render(shown)+style.AccentText.Render("▏"))
	if m.rotReason != "" {
		fmt.Fprintln(&b)
		fmt.Fprintln(&b, style.DegradedText.Render(m.rotReason))
	}
	if p.Attempt > 1 {
		fmt.Fprintln(&b, style.Tagline.Render(fmt.Sprintf("attempt %d of 3", p.Attempt)))
	}
	return skyBlock(m.star, m.width, m.height, b.String())
}

// viewExecuted is the after-picture: what ran, how it ended, and the
// fresh re-diagnosis the executor printed.
func (m RepairModel) viewExecuted() string {
	var b strings.Builder
	title := lipgloss.NewStyle().Bold(true).Foreground(style.Text)

	result := ""
	if m.execSummary != nil {
		result = m.execSummary.Result
	}
	switch result {
	case "complete":
		fmt.Fprintln(&b, style.SuccessText.Render(style.SymbolMark)+" "+title.Render("Repairs applied"))
	case "declined":
		fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark)+" "+title.Render("Nothing was changed"))
	case "unactionable":
		fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark)+" "+title.Render("Nothing safe to run"))
	case "empty":
		fmt.Fprintln(&b, style.SuccessText.Render(style.SymbolMark)+" "+title.Render("Nothing to repair"))
	case "failed":
		fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+title.Render("Some repairs failed"))
	default:
		fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark)+" "+title.Render("Repair run ended"))
	}
	fmt.Fprintln(&b)

	for _, e := range m.executeResults {
		fmt.Fprintln(&b, executeLine(e))
	}
	if len(m.executeResults) > 0 {
		fmt.Fprintln(&b)
	}
	if m.execSummary != nil && (m.execSummary.Done > 0 || m.execSummary.Failed > 0) {
		fmt.Fprintln(&b, style.Tagline.Render(fmt.Sprintf("%d done · %d failed", m.execSummary.Done, m.execSummary.Failed)))
	}

	// The after-picture: the executor's own honest re-diagnosis.
	if m.diagnosis != nil {
		if m.diagnosis.Result == "healthy" {
			fmt.Fprintln(&b, style.SuccessText.Render("diagnosis clear after repairs"))
		} else {
			fmt.Fprintln(&b, style.Tagline.Render("still standing after repairs:"))
			for _, f := range sortedFindings(m.findings) {
				fmt.Fprintln(&b, findingLine(f))
			}
		}
	}
	fmt.Fprintln(&b)
	writeStackedMenu(&b, executedMenu, m.menuSel)
	return skyBlock(m.star, m.width, m.height, b.String())
}

// executeLine renders one execution outcome in honest words.
func executeLine(e engine.ExecuteResult) string {
	var glyph string
	switch e.Result {
	case "done":
		glyph = style.SuccessText.Render(style.SymbolSuccess)
	case "failed":
		glyph = style.ErrorText.Render(style.SymbolFailure)
	default:
		glyph = style.Tagline.Render(style.SymbolQueued)
	}
	words := lipgloss.NewStyle().Foreground(style.Text).Render(actionWords(e.Action))
	if e.Result == "skipped" {
		words = style.Tagline.Render(actionWords(e.Action))
	}
	return glyph + " " + words + style.Tagline.Render(" — "+classWords(e.Resolves))
}

func (m RepairModel) viewUnavailable() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.AccentText.Render(style.SymbolMark))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Diagnosis needs a newer Orbit"))
	fmt.Fprintln(&b)
	fmt.Fprintln(&b, style.MutedText.Render("This Orbit release doesn't publish its repair diagnosis"))
	fmt.Fprintln(&b, style.MutedText.Render("yet. Nothing on the deployment was touched."))
	fmt.Fprintln(&b)
	writeStackedMenu(&b, repairMenu, m.menuSel)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m RepairModel) viewError() string {
	var b strings.Builder
	fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Diagnosis couldn't run"))
	fmt.Fprintln(&b)
	if m.runErr != nil {
		fmt.Fprintln(&b, style.Tagline.Render(m.runErr.Error()))
		fmt.Fprintln(&b)
	}
	writeStackedMenu(&b, repairMenu, m.menuSel)
	return skyBlock(m.star, m.width, m.height, b.String())
}

func (m RepairModel) viewDiagnosis() string {
	var b strings.Builder

	result := ""
	if m.diagnosis != nil {
		result = m.diagnosis.Result
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(style.Text)
	switch {
	case m.exitCode == 5:
		fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark)+" "+title.Render("No Orbit installation here"))
	case m.planSummary != nil && m.planSummary.Result == "empty",
		result == "healthy",
		m.planSummary != nil && m.exitCode == 0:
		fmt.Fprintln(&b, style.SuccessText.Render(style.SymbolMark)+" "+title.Render("Diagnosis clear"))
	case m.planSummary != nil && m.exitCode == 3:
		// Plan mode's stdout carries only the plan (verified against
		// the real script): the proposal is the story.
		fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark)+" "+title.Render("Repairs proposed"))
	case m.planSummary != nil:
		fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark)+" "+title.Render("Needs your attention"))
	case result == "attention":
		fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark)+" "+title.Render("Needs attention"))
	default:
		fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+title.Render("Problems found"))
	}
	fmt.Fprintln(&b)

	for _, f := range sortedFindings(m.findings) {
		fmt.Fprintln(&b, findingLine(f))
	}
	if len(m.findings) > 0 {
		fmt.Fprintln(&b)
	}

	if m.diagnosis != nil {
		fmt.Fprintln(&b, style.Tagline.Render(fmt.Sprintf("%d checked · %d skipped", m.diagnosis.Checked, m.diagnosis.Skipped)))
	}

	m.writePlan(&b, result)

	fmt.Fprintln(&b)
	writeStackedMenu(&b, m.planMenu(), m.menuSel)
	return skyBlock(m.star, m.width, m.height, b.String())
}

// writePlan renders the proposed plan (orbit#261 slice 3): what the
// engine would do, classified — and the plain truth that nothing here
// can execute yet.
func (m *RepairModel) writePlan(b *strings.Builder, result string) {
	if len(m.planActions) == 0 {
		// No plan lines: healthy, a --check fallback run, or nothing
		// plannable. Keep the honest note whenever something's wrong.
		if result != "healthy" && m.exitCode != 5 {
			fmt.Fprintln(b, style.Tagline.Render("repair actions arrive with a later Orbit release"))
		}
		return
	}

	for _, p := range m.planActions {
		fmt.Fprintln(b, planLine(p))
	}
	for _, note := range m.manualNotes {
		if step, ok := strings.CutPrefix(note, "manual step: "); ok {
			fmt.Fprintln(b, style.Tagline.Render(step))
		}
	}
	fmt.Fprintln(b)
	fmt.Fprintln(b, style.Tagline.Render(planSummaryWords(m.planSummary)))
}

// planLine renders one proposed action: the action in plain words and
// what it resolves, with the backup requirement when the contract
// demands one. The mutation class is deliberately not repeated — the
// action words already carry it, and every line must hold inside 80
// cells (the cell-truth bar). Unknown enum values render verbatim.
// Plan mode's stdout carries no separate finding lines, so the
// resolves class is the reader's only pointer at the underlying
// problem — always shown.
func planLine(p engine.PlanAction) string {
	glyph := style.AccentText.Render(style.SymbolQueued)
	if p.Action == "manual" {
		glyph = style.DegradedText.Render(style.SymbolQueued)
	}
	context := classWords(p.Resolves)
	if p.Backup == "required" {
		context += " · backup first"
	}
	return glyph + " " + lipgloss.NewStyle().Foreground(style.Text).Render(actionWords(p.Action)) +
		style.Tagline.Render(" — "+context)
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
		return action
	}
}

// planSummaryWords is the one-line truth under the plan.
func planSummaryWords(s *engine.PlanSummary) string {
	if s == nil {
		return "execution arrives with a later Orbit release — nothing here has run"
	}
	switch s.Result {
	case "ready":
		return "a safe plan is ready — execution arrives with a later Orbit release"
	case "manual-required":
		return "some steps need your hands — execution arrives with a later Orbit release"
	case "empty":
		return "nothing to plan"
	default:
		return s.Result + " — execution arrives with a later Orbit release"
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
// verbatim — honest, never guessed at.
func findingLine(f engine.Finding) string {
	glyph := style.Tagline.Render(style.SymbolQueued)
	switch f.Severity {
	case "fail":
		glyph = style.ErrorText.Render(style.SymbolFailure)
	case "warn":
		glyph = style.DegradedText.Render("!")
	}
	return glyph + " " + lipgloss.NewStyle().Foreground(style.Text).Render(targetWords(f.Target)) + style.Tagline.Render(" — "+classWords(f.Class))
}

// targetWords maps target classes to the thing a person knows.
func targetWords(target string) string {
	switch target {
	case "directory":
		return "target directory"
	case "env-file":
		return ".env-orbit"
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
		return target
	}
}

// classWords maps reason classes to honest words.
func classWords(class string) string {
	switch class {
	case "not-orbit-directory":
		return "no recognizable Orbit installation"
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
	default:
		return class
	}
}
