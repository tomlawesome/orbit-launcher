package ui

// Pins the Repair items of #267 (v0.4.2 Lows): plan rows and manual steps
// wrap to the same width, the restart line for a stale container reads
// like the others, an older repair.sh with many findings keeps the menu
// on a 24-row screen, and the plan words for a target or class the
// launcher has no words for are shown as visibleText (#236 c12).

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// The longest real plan line wraps; its words as a person reads them
// (the "·" bullet is the starfield's and the plan's, and is dropped).
const lowsLongPlanLine = "restore the interrupted install transaction — a configuration change was interrupted · backup first"

// lowsMixedFrame is the "Needs your attention" screen at cols x 60 for a
// plan holding one long action and one manual step with a long guidance.
func lowsMixedFrame(t *testing.T, cols int) string {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: cols, Height: 60})
	m = repairLines(t, m,
		"plan action=restore-transaction resolves=configuration-migration-interrupted mutation=reversible backup=required",
		"plan action=manual resolves=database-unreachable mutation=none backup=not-required",
		"plan result=manual-required actions=1 manual=1",
	)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, StderrTail: []string{manualStepLine}})
	s := screen(m)
	if !strings.Contains(s, "Needs your attention") && !strings.Contains(s, "Repairs proposed") {
		t.Fatalf("setup: the plan did not reach the plan screen:\n%s", s)
	}
	return s
}

// widestSpan is the widest stretch, in cells, from the first to the last
// word of words on one row of s.
func widestSpan(s string, words []string) int {
	widest := 0
	for _, row := range strings.Split(s, "\n") {
		if w := stepSpan(row, words); w > widest {
			widest = w
		}
	}
	return widest
}

// Plan rows and manual steps wrap to the same width: at every terminal
// width neither is wider than the prose width the screen's other text
// uses, so they do not wrap at different places.
func TestRepairScreen_PlanRowsAndManualStepsWrapToTheSameWidth(t *testing.T) {
	plan := strings.Fields(strings.ReplaceAll(lowsLongPlanLine, "·", ""))
	step := strings.Fields(manualStepProse)
	for _, cols := range []int{50, 80, 200} {
		t.Run(fmt.Sprintf("%dcols", cols), func(t *testing.T) {
			s := lowsMixedFrame(t, cols)
			limit := min(cols-4, proseWidth)
			if got := widestSpan(s, step); got > limit {
				t.Errorf("setup: a manual step row spans %d cells, over %d:\n%s", got, limit, s)
			}
			if got := widestSpan(s, plan); got > limit {
				t.Errorf("a plan row spans %d cells at %d columns, over the %d-cell width the manual steps wrap to:\n%s", got, cols, limit, s)
			}
			flat := strings.Join(skyWords(s), " ")
			if !strings.Contains(flat, "restore the interrupted install transaction") || !strings.Contains(flat, "backup first") {
				t.Errorf("the plan row lost words when wrapped:\n%s", s)
			}
		})
	}
}

// The restart line for a stale container keeps no wording workaround: it
// reads "a container is running ..." as the other actions' lines do, and
// wraps instead of being shortened.
func TestRepairScreen_RestartLineForAStaleContainerReadsInFull(t *testing.T) {
	for _, cols := range []int{80, 200} {
		s := realPairScreen(t, realPlanPair{"stale-container", "restart-services"}, cols)
		flat := strings.Join(skyWords(s), " ")
		want := "restart Orbit's services — a container is running an older image than configured"
		if !strings.Contains(flat, want) {
			t.Errorf("at %d columns the restart line for a stale container does not read %q:\n%s", cols, want, s)
		}
		for _, row := range strings.Split(s, "\n") {
			if w := lipgloss.Width(row); w > cols {
				t.Errorf("at %d columns a %d-cell row: %q", cols, w, row)
			}
		}
	}
}

// oldScriptScreen is the screen of an older repair.sh with no plan mode
// (--plan refused, then --check) that found n problems, on cols x rows.
func oldScriptScreen(t *testing.T, n, cols, rows int) string {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: cols, Height: rows})
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: engine.ExitUsage, Err: errors.New("exit status 2")})
	lines := make([]string, 0, n+1)
	for k := 0; k < n; k++ {
		class := realPlanPairs[k%len(realPlanPairs)].class
		lines = append(lines, fmt.Sprintf("finding class=%s target=target%02d severity=fail", class, k))
	}
	lines = append(lines, fmt.Sprintf("diagnosis result=failed checked=%d skipped=0", n))
	m = repairLines(t, m, lines...)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})
	return screen(m)
}

var findingMoreRow = regexp.MustCompile(`and (\d+) more`)

// With 16 or more findings the menu and the summary stay on an 80x24
// screen; findings are cut to the rows left, whole, and one row says how
// many are not shown, as the plan steps do.
func TestRepairScreen_ManyFindingsKeepTheMenuAt80x24(t *testing.T) {
	const cols, rows = 80, 24
	for _, n := range []int{16, 17, 18, 24, 40} {
		t.Run(fmt.Sprintf("%dfindings", n), func(t *testing.T) {
			s := oldScriptScreen(t, n, cols, rows)
			if lines := strings.Split(strings.TrimRight(s, "\n"), "\n"); len(lines) > rows {
				t.Errorf("the screen draws %d rows on a %d-row terminal", len(lines), rows)
			}
			if got := strings.Join(screenLabels(s), ","); got != "Menu,Exit" {
				t.Errorf("menu rows = %q, want Menu and Exit:\n%s", got, s)
			}
			flat := strings.Join(skyWords(s), " ")
			if !strings.Contains(flat, fmt.Sprintf("%d checked", n)) {
				t.Errorf("the summary %q is not on the screen:\n%s", fmt.Sprintf("%d checked", n), s)
			}
			shown := 0
			for k := 0; k < n; k++ {
				if strings.Contains(s, fmt.Sprintf("target%02d", k)) {
					shown++
				}
			}
			found := findingMoreRow.FindAllStringSubmatch(flat, -1)
			if shown == n {
				if len(found) != 0 {
					t.Errorf("every finding is shown, but an \"and N more\" row is drawn:\n%s", s)
				}
				return
			}
			if len(found) != 1 {
				t.Fatalf("%d of %d findings are shown; want one \"and N more\" row, found %d:\n%s", shown, n, len(found), s)
			}
			if got, _ := strconv.Atoi(found[0][1]); got != n-shown {
				t.Errorf("the row says \"and %d more\", but %d of %d findings are not shown:\n%s", got, n-shown, n, s)
			}
		})
	}
}

// On a tall terminal every finding shows and nothing is counted as hidden.
func TestRepairScreen_ManyFindingsAllShowOnATallTerminal(t *testing.T) {
	s := oldScriptScreen(t, 40, 80, 80)
	for k := 0; k < 40; k++ {
		if !strings.Contains(s, fmt.Sprintf("target%02d", k)) {
			t.Fatalf("finding %d is missing on an 80x80 terminal:\n%s", k, s)
		}
	}
	if findingMoreRow.MatchString(strings.Join(skyWords(s), " ")) {
		t.Errorf("an \"and N more\" row is drawn although everything fits:\n%s", s)
	}
	if got := strings.Join(screenLabels(s), ","); got != "Menu,Exit" {
		t.Errorf("menu rows = %q, want Menu and Exit:\n%s", got, s)
	}
}

// #236 c12: the words for a target or class the launcher does not know
// are the value as visibleText, never raw control or format characters.
func TestTargetWords_UnknownTargetIsShownAsVisibleText(t *testing.T) {
	for _, in := range []string{"x\x1b[2JEVIL", "x\rEVIL", "x\u202eEVIL", "x\x7fEVIL"} {
		got := targetWords(in)
		if got != visibleText(in) {
			t.Errorf("targetWords(%q) = %q, want %q", in, got, visibleText(in))
		}
		if strings.ContainsAny(got, "\x1b\r\x7f\u202e") {
			t.Errorf("targetWords(%q) = %q still holds a control or format character", in, got)
		}
	}
}

func TestClassWords_UnknownClassIsShownAsVisibleText(t *testing.T) {
	for _, in := range []string{"x\x1b[2JEVIL", "x\rEVIL", "x\u202eEVIL", "x\x7fEVIL"} {
		got := classWords(in)
		if got != visibleText(in) {
			t.Errorf("classWords(%q) = %q, want %q", in, got, visibleText(in))
		}
		if strings.ContainsAny(got, "\x1b\r\x7f\u202e") {
			t.Errorf("classWords(%q) = %q still holds a control or format character", in, got)
		}
	}
}

func TestFindingLine_UnknownTargetAndClassHoldNoControlCharacters(t *testing.T) {
	line := findingLine(engine.Finding{Class: "weird\rclass", Target: "odd\rtarget", Severity: "fail"})
	if strings.ContainsAny(line, "\r\x7f\u202e") {
		t.Errorf("findingLine holds a control character: %q", line)
	}
}
