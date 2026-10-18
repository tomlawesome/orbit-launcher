package ui

// Pins #253 (Repair's manual steps): on the "Needs your attention"
// screen a manual step from Orbit's engine must wrap to the prose width
// and never run off the screen edge, must not show the engine's raw
// "(resolves=...)" tag or other protocol text, and each plan line must
// name what it is about ("the database can't be reached", not just
// "can't be reached").

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// manualStepProse is the engine's guidance for an unreachable database,
// as the owner saw it cut off; manualStepLine is the stderr line that
// carries it, machine tag and all.
const (
	manualStepProse = "verify the database container/service is running and reachable, " +
		"then re-run diagnosis; repair never starts a service to investigate"
	manualStepLine = "manual step: " + manualStepProse + " (resolves=database-unreachable)"
)

// manualStepPlan is a --plan run whose only step needs the operator's
// hands: plan line and summary on stdout, the guidance on stderr, exit 4
// (the shape repair_teatest_test.go's fixtures use).
const manualStepPlan = `
echo 'plan action=manual resolves=database-unreachable mutation=none backup=not-required'
echo '` + manualStepLine + `' >&2
echo 'plan result=manual-required actions=0 manual=1'
exit 4`

// rawProtocol is engine wire text that must never reach the screen.
var rawProtocol = []string{"resolves=", "database-unreachable", "manual step:", "action=", "mutation=", "backup="}

// manualStepTerminal drives Repair through the plan run on a cols x rows
// terminal and returns the "Needs your attention" screen as the terminal
// emulator shows it.
func manualStepTerminal(t *testing.T, cols, rows int) string {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m.prepareRepair = fakeRepairStream(manualStepPlan)
	tm := newRepairTestModel(t, m, cols, rows)

	var s string
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		s = screenOf(out, cols, rows)
		return strings.Contains(s, "Needs your attention") && strings.Contains(s, "▸ Menu")
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
	return s
}

// manualStepFrame is the same screen as the model draws it at the given
// width, before any terminal gets to cut or wrap it.
func manualStepFrame(t *testing.T, cols int) string {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: cols, Height: 60})
	m = repairLines(t, m,
		"plan action=manual resolves=database-unreachable mutation=none backup=not-required",
		"plan result=manual-required actions=0 manual=1",
	)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, StderrTail: []string{manualStepLine}})
	s := screen(m)
	if !strings.Contains(s, "Needs your attention") {
		t.Fatalf("setup: the plan did not reach \"Needs your attention\":\n%s", s)
	}
	return s
}

func TestRepairModel_ManualStepReadsWholeAt200Columns(t *testing.T) {
	assertManualStepReadsWhole(t, 200, 40)
}

func TestRepairModel_ManualStepReadsWholeAt60Columns(t *testing.T) {
	assertManualStepReadsWhole(t, 60, 50)
}

func assertManualStepReadsWhole(t *testing.T, cols, rows int) {
	t.Helper()
	words := strings.Fields(manualStepProse)
	shown := manualStepTerminal(t, cols, rows)

	// Every word of the step must be on screen, in order, however it was
	// wrapped. Whitespace is dropped on both sides so line breaks and
	// continuation indents do not matter.
	flat := strings.Join(strings.Fields(shown), "")
	at := 0
	for _, w := range words {
		i := strings.Index(flat[at:], w)
		if i < 0 {
			t.Errorf("at %d columns the manual step loses %q (and what follows): the operator cannot read the whole step.\nscreen:\n%s", cols, w, shown)
			break
		}
		at += i + len(w)
	}

	// A row that reaches the right edge must not end in part of a word:
	// that is text the terminal cut off.
	for _, row := range strings.Split(shown, "\n") {
		if lipgloss.Width(row) < cols {
			continue
		}
		fields := strings.Fields(row)
		if len(fields) == 0 {
			continue
		}
		if last := fields[len(fields)-1]; isCutWord(last, words) {
			t.Errorf("at %d columns a row runs to the screen edge and ends mid-word in %q: the step was cut off, not wrapped.\nrow: %q", cols, last, row)
		}
	}

	// No row the model draws may be wider than the terminal: the terminal
	// would cut it (or wrap it mid-word) on its own.
	frame := manualStepFrame(t, cols)
	for _, row := range strings.Split(frame, "\n") {
		if w := lipgloss.Width(row); w > cols {
			t.Errorf("at %d columns the model draws a %d-cell row, so the terminal cuts it at the edge: %q", cols, w, row)
		}
	}

	// The step's rows must wrap to the prose width, not run as one line
	// as wide as the terminal allows. Only the step's own words count:
	// the background's stars share the row.
	limit := min(cols-4, proseWidth)
	for _, row := range strings.Split(frame, "\n") {
		if w := stepSpan(row, words); w > limit {
			t.Errorf("at %d columns a row of the manual step spans %d cells, over the %d-cell prose width: the step is not wrapped.\nrow: %q", cols, w, limit, strings.TrimSpace(row))
		}
	}

	// The engine's machine tags are wire protocol, not words for a person.
	var leaked []string
	for _, raw := range rawProtocol {
		if strings.Contains(shown, raw) || strings.Contains(frame, raw) {
			leaked = append(leaked, raw)
		}
	}
	if len(leaked) > 0 {
		t.Errorf("at %d columns raw engine protocol reaches the screen: %q.\nscreen:\n%s", cols, leaked, shown)
	}
}

// isCutWord reports whether tok is the front part of a step word (or of
// the engine's tag) without being a whole word: what a terminal leaves
// when it cuts a row at its edge.
func isCutWord(tok string, words []string) bool {
	for _, w := range append(words, "(resolves=database-unreachable)") {
		if tok == w {
			return false
		}
	}
	for _, w := range append(words, "(resolves=database-unreachable)") {
		if strings.HasPrefix(w, tok) {
			return true
		}
	}
	return false
}

// stepSpan is the width in cells of the stretch of row from the first
// to the last word of the step (or its tag) on it; 0 when it holds none.
func stepSpan(row string, words []string) int {
	isStep := map[string]bool{"(resolves=database-unreachable)": true}
	for _, w := range words {
		isStep[w] = true
	}
	first, end := -1, -1
	pos := 0
	for _, f := range strings.Fields(row) {
		i := pos + strings.Index(row[pos:], f)
		pos = i + len(f)
		if !isStep[f] {
			continue
		}
		if first < 0 {
			first = i
		}
		end = pos
	}
	if first < 0 {
		return 0
	}
	return lipgloss.Width(row[first:end])
}

// TestPlanLine_NamesWhatItIsAbout: a plan line must say what its reason
// is about. "needs your hands — can't be reached" leaves the operator
// guessing what cannot be reached; the subject must be named. The manual
// action is used so the action's own words cannot supply the subject.
func TestPlanLine_NamesWhatItIsAbout(t *testing.T) {
	subjects := map[string]string{
		"database-unreachable":         "database",
		"database-credential-mismatch": "database",
		"application-unhealthy":        "application",
		"stale-container":              "container",
		"secret-missing":               "secret",
	}
	for class, subject := range subjects {
		got := ansi.Strip(planLine(engine.PlanAction{Action: "manual", Resolves: class, Mutation: "none", Backup: "not-required"}))
		if !strings.Contains(got, subject) {
			t.Errorf("plan line for %q reads %q, which never says it is about the %s (want e.g. \"the %s ...\")", class, got, subject, subject)
		}
		if strings.Contains(got, "resolves=") || strings.Contains(got, class) {
			t.Errorf("plan line for %q shows the raw class: %q", class, got)
		}
	}
}

// TestPlanLine_EveryLineFitsIn80Columns (review F5): every plan line
// the other tests know stays within 80 cells: each action and reason
// pair in a fixture (with its own mutation and backup), each action word
// against the reason TestPlanLine_ActionWords uses, and the manual action
// against each reason TestPlanLine_NamesWhatItIsAbout uses.
func TestPlanLine_EveryLineFitsIn80Columns(t *testing.T) {
	known := []engine.PlanAction{
		// Fixtures across the package's tests.
		{Action: "fix-permissions", Resolves: "managed-file-permissions", Mutation: "reversible", Backup: "not-required"},
		{Action: "fix-permissions", Resolves: "secret-missing", Mutation: "reversible", Backup: "not-required"},
		{Action: "manual", Resolves: "database-unreachable", Mutation: "none", Backup: "not-required"},
		{Action: "manual", Resolves: "volume-retained-without-credentials", Mutation: "none", Backup: "not-required"},
		{Action: "manual", Resolves: "secret-stale", Mutation: "none", Backup: "required"},
		{Action: "regenerate-secret", Resolves: "secret-missing", Mutation: "reversible", Backup: "not-required"},
		{Action: "rerun-configuration", Resolves: "configuration-incomplete", Mutation: "none", Backup: "not-required"},
		{Action: "restart-services", Resolves: "stale-container", Mutation: "reversible", Backup: "not-required"},
		{Action: "rotate-database-credential", Resolves: "database-credential-mismatch", Mutation: "credential-rotation", Backup: "required"},
	}
	for _, a := range []string{
		"fix-permissions", "rerun-configuration", "restore-transaction",
		"rotate-database-credential", "regenerate-secret", "restart-services", "manual",
	} {
		known = append(known, engine.PlanAction{Action: a, Resolves: "migration-failed", Backup: "not-required"})
	}
	for _, c := range []string{
		"database-unreachable", "database-credential-mismatch", "application-unhealthy",
		"stale-container", "secret-missing",
	} {
		known = append(known, engine.PlanAction{Action: "manual", Resolves: c, Mutation: "none", Backup: "not-required"})
	}

	for _, p := range known {
		got := ansi.Strip(planLine(p))
		if w := lipgloss.Width(got); w > 80 {
			t.Errorf("%d cells, want at most 80: action=%s resolves=%s mutation=%s backup=%s: %q", w, p.Action, p.Resolves, p.Mutation, p.Backup, got)
		}
	}
}

// The line found too wide still says what it is about once shortened.
func TestPlanLine_RestartForAStaleContainerFitsAndSaysWhatItIsAbout(t *testing.T) {
	got := ansi.Strip(planLine(engine.PlanAction{Action: "restart-services", Resolves: "stale-container", Mutation: "reversible", Backup: "not-required"}))
	if w := lipgloss.Width(got); w > 80 {
		t.Errorf("the restart line for a stale container is %d cells, want at most 80: %q", w, got)
	}
	for _, want := range []string{"restart Orbit's services", "container"} {
		if !strings.Contains(got, want) {
			t.Errorf("the restart line for a stale container reads %q, which lacks %q", got, want)
		}
	}
}
