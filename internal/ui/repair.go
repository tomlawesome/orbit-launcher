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

// RepairModel is the real read-only diagnosis (orbit#261 first slice):
// the launcher fetches orbit's standalone repair.sh fresh, stages it
// into the deployment, runs `--check`, and renders the finding/
// diagnosis contract — enums in, honest words out, outcome keyed off
// the exit code (0 healthy / 3 attention / 4 failed / 5 not an orbit
// installation), never prose. Repair *execution* is a later orbit
// slice; this screen says so plainly instead of pretending.
type RepairModel struct {
	width, height int
	targetDir     string
	version       string

	state     repairState
	findings  []engine.Finding
	diagnosis *engine.Diagnosis
	exitCode  int
	runErr    error

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

type prepareRepairFunc func(ctx context.Context, targetDir string) (*engine.Stream, error)

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
	return RepairModel{targetDir: targetDir, version: version}
}

// Outcome surfaces the flow result to AppModel.
func (m RepairModel) Outcome() flowOutcome {
	return flowOutcome{Done: m.Done, WantsMenu: m.WantsMenu}
}

// Init implements tea.Model: the diagnosis starts immediately — it is
// read-only by the script's own contract, so there is nothing to
// confirm first.
func (m RepairModel) Init() tea.Cmd {
	prepare := m.prepare
	if prepare == nil {
		prepare = defaultPrepareRepair
	}
	targetDir := m.targetDir
	return func() tea.Msg {
		stream, err := prepare(context.Background(), targetDir)
		return repairReadyMsg{stream: stream, err: err}
	}
}

// defaultPrepareRepair fetches, stages and starts the diagnosis.
func defaultPrepareRepair(ctx context.Context, targetDir string) (*engine.Stream, error) {
	script, err := deploy.FetchRepairScript(ctx)
	if err != nil {
		return nil, err
	}
	if err := deploy.StageRepairScript(targetDir, script); err != nil {
		return nil, err
	}
	return engine.Start(deploy.BuildRepairCheckCommand(targetDir))
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
			// The contract's diagnosis outcomes — including "this
			// isn't an orbit installation", which arrives with its own
			// finding line and deserves the same honest rendering.
			m.state = repairDiagnosis
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
	switch {
	case m.exitCode == 5:
		fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark)+" "+lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("No Orbit installation here"))
	case result == "healthy":
		fmt.Fprintln(&b, style.SuccessText.Render(style.SymbolMark)+" "+lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Diagnosis clear"))
	case result == "attention":
		fmt.Fprintln(&b, style.DegradedText.Render(style.SymbolMark)+" "+lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Needs attention"))
	default:
		fmt.Fprintln(&b, style.ErrorText.Render(style.SymbolFailure)+" "+lipgloss.NewStyle().Bold(true).Foreground(style.Text).Render("Problems found"))
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
	if result != "healthy" && m.exitCode != 5 {
		fmt.Fprintln(&b, style.Tagline.Render("repair actions arrive with a later Orbit release"))
	}
	fmt.Fprintln(&b)
	writeStackedMenu(&b, repairMenu, m.menuSel)
	return centreBlock(m.width, m.height, b.String())
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
	default:
		return class
	}
}
