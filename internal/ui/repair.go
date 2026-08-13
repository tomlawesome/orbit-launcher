package ui

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
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
// behaviour. Repair *execution* is orbit's next slice; this screen
// says so plainly instead of pretending.
type RepairModel struct {
	width, height int
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

	stream  *engine.Stream
	menuSel int

	// Done/WantsMenu surface the outcome to AppModel, exactly like a
	// flow's engine run.
	Done      bool
	WantsMenu bool

	// prepare is the test seam; nil gets the real implementation.
	prepare prepareRepairFunc
}

type repairState int

const (
	repairPreparing repairState = iota
	repairDiagnosis
	repairUnavailable
	repairError
)

type prepareRepairFunc func(ctx context.Context, targetDir string, mode deploy.RepairMode) (*engine.Stream, error)

// repairReadyMsg carries the started diagnosis run (or why there is
// none).
type repairReadyMsg struct {
	stream *engine.Stream
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
		m.width, m.height = msg.Width, msg.Height
		return m, nil

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

	case repairStreamMsg:
		return m.handleStream(msg.msg)

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m RepairModel) handleStream(msg any) (tea.Model, tea.Cmd) {
	switch s := msg.(type) {
	case engine.RawLineMsg:
		if f, ok := engine.ParseFinding(s.Text); ok {
			m.findings = append(m.findings, f)
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
		m.exitCode = s.ExitCode
		switch s.ExitCode {
		case 0, 3, 4, 5:
			// The contract's outcomes — shared by --check and --plan,
			// including "this isn't an orbit installation", which
			// arrives with its own finding line and deserves the same
			// honest rendering.
			m.state = repairDiagnosis
			if m.mode == deploy.RepairPlan {
				// The manual guidance lines ride stderr, value-free
				// by the script's contract.
				m.manualNotes = s.StderrTail
			}
		case 2:
			if m.mode == deploy.RepairPlan {
				// A repair.sh too old for --plan: usage error. Fall
				// back to the diagnosis it does speak.
				m.mode = deploy.RepairCheck
				m.findings, m.diagnosis = nil, nil
				m.planActions, m.planSummary, m.manualNotes = nil, nil, nil
				m.stream = nil
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
	return m, nil
}

var repairMenu = []string{"Menu", "Exit"}

func (m RepairModel) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.Type == tea.KeyCtrlC {
		if m.stream != nil {
			m.stream.Kill()
		}
		return m, tea.Quit
	}
	if m.state == repairPreparing {
		if msg.Type == tea.KeyEsc {
			if m.stream != nil {
				m.stream.Kill()
			}
			m.Done = true
			m.WantsMenu = true
		}
		return m, nil
	}
	switch msg.Type {
	case tea.KeyEsc:
		m.Done = true
		m.WantsMenu = true
		return m, nil
	case tea.KeyUp:
		m.menuSel = (m.menuSel - 1 + len(repairMenu)) % len(repairMenu)
		return m, nil
	case tea.KeyDown:
		m.menuSel = (m.menuSel + 1) % len(repairMenu)
		return m, nil
	case tea.KeyEnter:
		if m.menuSel == 0 {
			m.Done = true
			m.WantsMenu = true
			return m, nil
		}
		return m, tea.Quit
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
		return centreBlock(m.width, m.height, style.MutedText.Render("reading the deployment — nothing will be changed"))
	case repairUnavailable:
		return m.viewUnavailable()
	case repairError:
		return m.viewError()
	}
	return m.viewDiagnosis()
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
	return centreBlock(m.width, m.height, b.String())
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
	return centreBlock(m.width, m.height, b.String())
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
	writeStackedMenu(&b, repairMenu, m.menuSel)
	return centreBlock(m.width, m.height, b.String())
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
