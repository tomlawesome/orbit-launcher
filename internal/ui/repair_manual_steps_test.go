package ui

// Pins #253 (Repair's manual steps): on the "Needs your attention"
// screen a manual step from Orbit's engine must wrap to the prose width
// and never run off the screen edge, must not show the engine's raw
// "(resolves=...)" tag or other protocol text, and each plan line must
// name what it is about ("the database can't be reached", not just
// "can't be reached").

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
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
// These classes are the ones whose reason has to carry the subject
// itself; each must be a class Orbit really sends (realPlanPairs), so the
// list cannot go on naming a class that has been renamed or dropped.
func TestPlanLine_NamesWhatItIsAbout(t *testing.T) {
	subjects := map[string]string{
		"database-unreachable":         "database",
		"database-credential-mismatch": "database",
		"application-unhealthy":        "application",
		"stale-container":              "container",
		"secret-missing":               "secret",
	}
	real := map[string]bool{}
	for _, c := range realClasses() {
		real[c] = true
	}
	for class, subject := range subjects {
		if !real[class] {
			t.Errorf("%q is not a class Orbit's repair.sh sends (realPlanPairs); drop it from this test or rename it", class)
			continue
		}
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
// Orbit can send fits an 80-column screen, once laid out for it: planRows
// wraps a long line onto continuation rows, none wider than 80 cells, and
// "backup first" stays on the screen. The lines come from realPlanPairs,
// not from a list kept here: each real pair with its own mutation and
// backup, and again with the backup value flipped, so a pair added to
// realPlanPairs is measured at once. (An action against a class Orbit
// never gives it, such as a credential rotation for a symlinked file, is
// not a line Orbit sends and is not measured. Unwrapped, five real lines
// run past 80 cells, up to 99, which is why the screen wraps them.)
func TestPlanLine_EveryLineFitsIn80Columns(t *testing.T) {
	var known []engine.PlanAction
	for _, p := range realPlanPairs {
		known = append(known, p.planAction())
		flipped := p.planAction()
		if flipped.Backup == "required" {
			flipped.Backup = "not-required"
		} else {
			flipped.Backup = "required"
		}
		known = append(known, flipped)
	}
	if len(known) != 2*len(realPlanPairs) || len(known) < 40 {
		t.Fatalf("%d plan lines to measure; realPlanPairs is not being walked", len(known))
	}

	for _, p := range known {
		for i, row := range planRows(p, 80) {
			if w := lipgloss.Width(row); w > 80 {
				t.Errorf("row %d is %d cells, want at most 80: action=%s resolves=%s mutation=%s backup=%s: %q", i, w, p.Action, p.Resolves, p.Mutation, p.Backup, ansi.Strip(row))
			}
		}
		// A break between "backup" and "first" is fine; losing them is not.
		if p.Backup == "required" && !strings.Contains(strings.Join(strings.Fields(ansi.Strip(strings.Join(planRows(p, 80), " "))), " "), "backup first") {
			t.Errorf("the rows for action=%s resolves=%s lose \"backup first\"", p.Action, p.Resolves)
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

// TestPlanLine_ManagedFileLinesNameTheirSubject (v0.4.2 audit A5): the
// manual lines for a missing or symlinked managed file say it is a
// managed file, not a bare "— missing".
func TestPlanLine_ManagedFileLinesNameTheirSubject(t *testing.T) {
	words := map[string]string{
		"managed-file-missing": "a managed file is missing",
		"managed-file-symlink": "a managed file is a symlink, refusing to trust it",
	}
	for class, want := range words {
		got := ansi.Strip(planLine(engine.PlanAction{Action: "manual", Resolves: class, Mutation: "none", Backup: "not-required"}))
		if !strings.Contains(got, want) {
			t.Errorf("the manual plan line for %q reads %q, want it to say %q", class, got, want)
		}
	}
}

// manualSubjects is what each manual pair Orbit sends is about: its
// plan line must name it.
var manualSubjects = map[string]string{
	"secret-missing":                       "secret",
	"document-volume-retained-without-key": "document",
	"database-schema-mismatch":             "database",
	"migration-failed":                     "migration",
	"image-identity-mismatch":              "image",
	"deployment-version-unsupported":       "Orbit version",
	"not-orbit-directory":                  "Orbit installation",
	"managed-file-missing":                 "managed file",
	"managed-file-symlink":                 "managed file",
	"compose-interpolation-failed":         "compose",
	"container-foreign-owner":              "container",
	"docker-unavailable":                   "docker",
	"unrelated-resource-present":           "volume",
	"database-unreachable":                 "database",
	"database-credential-unverifiable":     "password",
}

// TestPlanLine_EveryRealManualPairNamesWhatItIsAbout (v0.4.2 audit A5):
// every manual pair Orbit sends has a plan reason naming its subject,
// and none is a bare predicate such as "— missing" or "— is a symlink".
func TestPlanLine_EveryRealManualPairNamesWhatItIsAbout(t *testing.T) {
	for _, p := range realPlanPairs {
		if p.action != "manual" {
			continue
		}
		got := ansi.Strip(planLine(p.planAction()))
		subject, ok := manualSubjects[p.class]
		if !ok {
			t.Errorf("manual class %q has no subject in manualSubjects: add what its plan line is about", p.class)
			continue
		}
		if !strings.Contains(got, subject) {
			t.Errorf("the manual plan line for %q reads %q, which never says it is about the %s", p.class, got, subject)
		}
		_, reason, found := strings.Cut(got, "— ")
		if !found {
			t.Errorf("the manual plan line for %q reads %q, with no reason after the action", p.class, got)
			continue
		}
		for _, bare := range []string{"missing", "is ", "absent", "wrong "} {
			if strings.HasPrefix(reason, bare) {
				t.Errorf("the manual plan line for %q reads %q: its reason starts %q and names no subject", p.class, got, bare)
			}
		}
	}
}

// skyWords is a stripped screen as a run of words, the starfield taken
// off: a sky glyph can sit in the blank cell right after a word, so it
// is trimmed from both ends of every word, and a word that was only sky
// is dropped (the plan bullet "·" with it).
func skyWords(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		if w := strings.Trim(f, ".·✦●⟡*+"); w != "" {
			out = append(out, w)
		}
	}
	return out
}

// containsRun reports whether want appears in words as one unbroken run.
func containsRun(words, want []string) bool {
	if len(want) == 0 {
		return false
	}
	for i := 0; i+len(want) <= len(words); i++ {
		match := true
		for j, w := range want {
			if words[i+j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// manyStepsStep is the k-th manual step's guidance: long enough to wrap
// at 80 columns, marked at both ends so a step cut part-way is seen.
func manyStepsStep(k int) string {
	return fmt.Sprintf("step%02d verify service %02d is running and reachable, then re-run diagnosis; repair never starts a service to investigate end%02d", k, k, k)
}

// manyStepsScreen renders the "Needs your attention" screen at cols x
// rows after a --plan run with every real manual pair Orbit sends, each
// with its own wrapped manual step on stderr. It returns the screen and
// every step on it, in order: each plan row and each manual step is one
// step, as the words a person reads.
func manyStepsScreen(t *testing.T, cols, rows int) (string, [][]string) {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: cols, Height: rows})
	var lines, tail []string
	var steps [][]string
	for _, p := range realPlanPairs {
		if p.action != "manual" {
			continue
		}
		lines = append(lines, p.wire())
		steps = append(steps, skyWords(ansi.Strip(planLine(p.planAction()))))
	}
	for k, p := range realPlanPairs {
		if p.action != "manual" {
			continue
		}
		tail = append(tail, "manual step: "+manyStepsStep(k)+" (resolves="+p.class+")")
		steps = append(steps, skyWords(manyStepsStep(k)))
	}
	lines = append(lines, fmt.Sprintf("plan result=manual-required actions=0 manual=%d", len(tail)))
	m = repairLines(t, m, lines...)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, StderrTail: tail})
	s := screen(m)
	if !strings.Contains(s, "Needs your attention") {
		t.Fatalf("setup: the plan did not reach \"Needs your attention\":\n%s", s)
	}
	return s, steps
}

const (
	manyStepsSummary = "some steps need your hands — nothing has run yet"
	moreStepsWords   = "make the terminal taller to see them"
)

var moreStepsRow = regexp.MustCompile(`and (\d+) more — ` + moreStepsWords)

// shownSteps counts the steps on screen whole, and reports any manual
// step of which only a part is shown.
func shownSteps(t *testing.T, s string, steps [][]string) int {
	t.Helper()
	words := skyWords(s)
	shown := 0
	for _, step := range steps {
		if containsRun(words, step) {
			shown++
			continue
		}
		first, last := step[0], step[len(step)-1]
		if strings.HasPrefix(first, "step") && (strings.Contains(s, first) || strings.Contains(s, last)) {
			t.Errorf("a manual step is shown cut part-way (%s ... %s): show whole steps only.\nscreen:\n%s", first, last, s)
		}
	}
	return shown
}

// TestRepairScreen_ManyManualStepsKeepTheSummaryAndMenuAt80x24 (v0.4.2
// audit A3): when the steps do not fit an 80x24 terminal, the summary
// and every menu row stay on screen, as many whole steps as fit are
// shown, and one row says how many are not: "and N more — make the
// terminal taller to see them".
func TestRepairScreen_ManyManualStepsKeepTheSummaryAndMenuAt80x24(t *testing.T) {
	const cols, rows = 80, 24
	s, steps := manyStepsScreen(t, cols, rows)

	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > rows {
		t.Errorf("the screen draws %d rows on a %d-row terminal", len(lines), rows)
	}
	for _, row := range lines {
		if w := lipgloss.Width(row); w > cols {
			t.Errorf("a %d-cell row at %d columns: %q", w, cols, row)
		}
	}
	flat := strings.Join(skyWords(s), " ")
	if !strings.Contains(flat, manyStepsSummary) {
		t.Errorf("the summary %q is not on the 80x24 screen:\n%s", manyStepsSummary, s)
	}
	if got := screenLabels(s); strings.Join(got, ",") != "Menu,Exit" {
		t.Errorf("menu rows on the 80x24 screen = %q, want Menu and Exit:\n%s", got, s)
	}

	shown := shownSteps(t, s, steps)
	if shown == 0 {
		t.Errorf("no step is shown whole on the 80x24 screen:\n%s", s)
	}
	if shown == len(steps) {
		t.Fatalf("setup: all %d steps fit at 80x24, so nothing overflows:\n%s", len(steps), s)
	}
	found := moreStepsRow.FindAllStringSubmatch(flat, -1)
	if len(found) != 1 {
		t.Fatalf("want one \"and N more — %s\" row, found %d:\n%s", moreStepsWords, len(found), s)
	}
	if n, _ := strconv.Atoi(found[0][1]); n != len(steps)-shown {
		t.Errorf("the row says \"and %d more\", but %d of %d steps are not shown whole:\n%s", n, len(steps)-shown, len(steps), s)
	}
}

// TestRepairScreen_ManyManualStepsAllShowOnATallTerminal (v0.4.2 audit
// A3): where everything fits nothing is cut and no "and N more" row is
// drawn.
func TestRepairScreen_ManyManualStepsAllShowOnATallTerminal(t *testing.T) {
	s, steps := manyStepsScreen(t, 80, 200)
	if shown := shownSteps(t, s, steps); shown != len(steps) {
		t.Errorf("only %d of %d steps are shown whole on an 80x200 terminal:\n%s", shown, len(steps), s)
	}
	flat := strings.Join(skyWords(s), " ")
	if strings.Contains(flat, moreStepsWords) || moreStepsRow.MatchString(flat) {
		t.Errorf("an \"and N more\" row is drawn although everything fits:\n%s", s)
	}
	if !strings.Contains(flat, manyStepsSummary) {
		t.Errorf("the summary %q is missing:\n%s", manyStepsSummary, s)
	}
	if got := screenLabels(s); strings.Join(got, ",") != "Menu,Exit" {
		t.Errorf("menu rows = %q, want Menu and Exit:\n%s", got, s)
	}
}

// TestRepairScreen_NoPlanNeverShowsAndZeroMore (#253): with an old
// repair.sh that rejects --plan, Repair falls back to --check and has
// findings but no plan steps. However many findings overfill an 80x24
// terminal, "and N more" appears only when N is at least 1.
func TestRepairScreen_NoPlanNeverShowsAndZeroMore(t *testing.T) {
	const cols, rows = 80, 24
	for n := 1; n <= 40; n++ {
		m := NewRepairModel(t.TempDir(), "v0.6.0")
		m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: cols, Height: rows})
		// The old script rejects --plan (exit 2): the flow reruns --check.
		m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: engine.ExitUsage, Err: errors.New("exit status 2")})

		lines := make([]string, 0, n+1)
		for k := 0; k < n; k++ {
			class := realPlanPairs[k%len(realPlanPairs)].class
			lines = append(lines, fmt.Sprintf("finding class=%s target=target%02d severity=fail", class, k))
		}
		lines = append(lines, fmt.Sprintf("diagnosis result=failed checked=%d skipped=0", n))
		m = repairLines(t, m, lines...)
		m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})

		s := screen(m)
		flat := strings.Join(skyWords(s), " ")
		if strings.Contains(flat, "and 0 more") {
			t.Errorf("%d findings at %dx%d: an \"and 0 more\" row is drawn:\n%s", n, cols, rows, s)
		}
		for _, f := range regexp.MustCompile(`and (\d+) more`).FindAllStringSubmatch(flat, -1) {
			if got, _ := strconv.Atoi(f[1]); got < 1 {
				t.Errorf("%d findings at %dx%d: row says \"and %d more\":\n%s", n, cols, rows, got, s)
			}
		}
	}
}
