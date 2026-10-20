package ui

// Pins review round 1b (F6, F7) of #253: the plan pairs Orbit's real
// repair.sh sends. F6: on the Repair screen at 80 columns no row of the
// plan is wider than 80 cells for any real pair; a long plan line wraps
// onto continuation rows and still says "backup first". F7: every real
// class has words, so no finding line or plan line shows a raw class code.

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// realPlanPair is one class -> action pair Orbit's repair.sh plans.
type realPlanPair struct{ class, action string }

// realPlanPairs is every pair the spec lists, in its order.
var realPlanPairs = []realPlanPair{
	{"managed-file-permissions", "fix-permissions"},
	{"secrets-directory-invalid", "fix-permissions"},
	{"secret-permissions", "fix-permissions"},
	{"configuration-incomplete", "rerun-configuration"},
	{"configuration-invalid", "rerun-configuration"},
	{"configuration-migration-interrupted", "restore-transaction"},
	{"staging-evidence-present", "restore-transaction"},
	{"volume-retained-without-credentials", "rotate-database-credential"},
	{"database-credential-mismatch", "rotate-database-credential"},
	{"stale-container", "restart-services"},
	{"application-unhealthy", "restart-services"},
	{"secret-missing", "regenerate-secret"},
	{"secret-missing", "manual"},
	{"document-volume-retained-without-key", "manual"},
	{"database-schema-mismatch", "manual"},
	{"migration-failed", "manual"},
	{"image-identity-mismatch", "manual"},
	{"deployment-version-unsupported", "manual"},
	{"not-orbit-directory", "manual"},
	{"managed-file-missing", "manual"},
	{"managed-file-symlink", "manual"},
	{"compose-interpolation-failed", "manual"},
	{"container-foreign-owner", "manual"},
	{"docker-unavailable", "manual"},
	{"unrelated-resource-present", "manual"},
	{"database-unreachable", "manual"},
	{"database-credential-unverifiable", "manual"},
}

// orbitClassOrder is Orbit's repair.sh class_order at dev 7264f015, in
// its order, with each class's plan action from the same file's
// action_for_class map (manual where the map has no entry).
// secret-missing has no entry: resolve_secret_missing_action picks
// regenerate-secret or manual, so both appear in realPlanPairs.
var orbitClassOrder = []realPlanPair{
	{"not-orbit-directory", "manual"},
	{"managed-file-missing", "manual"},
	{"managed-file-symlink", "manual"},
	{"managed-file-permissions", "fix-permissions"},
	{"secrets-directory-invalid", "fix-permissions"},
	{"secret-missing", "regenerate-secret|manual"},
	{"secret-permissions", "fix-permissions"},
	{"configuration-incomplete", "rerun-configuration"},
	{"configuration-invalid", "rerun-configuration"},
	{"configuration-migration-interrupted", "restore-transaction"},
	{"deployment-version-unsupported", "manual"},
	{"staging-evidence-present", "restore-transaction"},
	{"compose-interpolation-failed", "manual"},
	{"docker-unavailable", "manual"},
	{"container-foreign-owner", "manual"},
	{"volume-retained-without-credentials", "rotate-database-credential"},
	{"document-volume-retained-without-key", "manual"},
	{"unrelated-resource-present", "manual"},
	{"database-unreachable", "manual"},
	{"database-credential-mismatch", "rotate-database-credential"},
	{"database-credential-unverifiable", "manual"},
	{"stale-container", "restart-services"},
	{"image-identity-mismatch", "manual"},
	{"application-unhealthy", "restart-services"},
	{"database-schema-mismatch", "manual"},
	{"migration-failed", "manual"},
}

// TestRealPlanPairs_HoldEveryClassInOrbitsClassOrder (v0.4.2 audit A2):
// the pair list the "no raw code" and 80-column tests walk holds every
// class Orbit's repair.sh can plan, each with the action Orbit gives it,
// and nothing Orbit does not send.
func TestRealPlanPairs_HoldEveryClassInOrbitsClassOrder(t *testing.T) {
	actions := map[string][]string{}
	for _, p := range realPlanPairs {
		actions[p.class] = append(actions[p.class], p.action)
	}
	inOrbit := map[string]bool{}
	for _, o := range orbitClassOrder {
		inOrbit[o.class] = true
		got := strings.Join(actions[o.class], "|")
		if got == "" {
			t.Errorf("class %q is in Orbit's class_order but not in realPlanPairs", o.class)
			continue
		}
		if got != o.action {
			t.Errorf("class %q is paired with %q in realPlanPairs, Orbit plans %q", o.class, got, o.action)
		}
	}
	for class := range actions {
		if !inOrbit[class] {
			t.Errorf("realPlanPairs holds %q, which is not in Orbit's class_order", class)
		}
	}
	if n := len(realClasses()); n != len(orbitClassOrder) {
		t.Errorf("realPlanPairs holds %d classes, Orbit's class_order %d", n, len(orbitClassOrder))
	}
}

// realBackup is the backup value Orbit sends with an action: required
// only for restore-transaction and rotate-database-credential.
func realBackup(action string) string {
	if action == "restore-transaction" || action == "rotate-database-credential" {
		return "required"
	}
	return "not-required"
}

// realMutation is a plausible mutation value for an action (the spec
// does not pin it; the plan line does not depend on it in the fixtures).
func realMutation(action string) string {
	switch action {
	case "manual", "rerun-configuration":
		return "none"
	case "rotate-database-credential":
		return "credential-rotation"
	default:
		return "reversible"
	}
}

func (p realPlanPair) planAction() engine.PlanAction {
	return engine.PlanAction{Action: p.action, Resolves: p.class, Mutation: realMutation(p.action), Backup: realBackup(p.action)}
}

func (p realPlanPair) wire() string {
	return fmt.Sprintf("plan action=%s resolves=%s mutation=%s backup=%s", p.action, p.class, realMutation(p.action), realBackup(p.action))
}

// realPairScreen renders the Repair screen at cols x 60 after a --plan
// run whose only step is p, ANSI stripped.
func realPairScreen(t *testing.T, p realPlanPair, cols int) string {
	t.Helper()
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: cols, Height: 60})
	summary, exit := "plan result=ready actions=1 manual=0", 3
	if p.action == "manual" {
		summary, exit = "plan result=manual-required actions=0 manual=1", 4
	}
	m = repairLines(t, m, p.wire(), summary)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: exit})
	s := screen(m)
	if !strings.Contains(s, "Repairs proposed") && !strings.Contains(s, "Needs your attention") {
		t.Fatalf("setup: %s did not reach the plan screen:\n%s", p.wire(), s)
	}
	return s
}

// TestRepairScreen_EveryRealPlanPairFitsAt80Columns (F6): for every
// real pair, no row of the Repair screen at 80 columns is wider than 80
// cells, and every word of the plan line is still on screen in order
// (wrapped, not cut off).
func TestRepairScreen_EveryRealPlanPairFitsAt80Columns(t *testing.T) {
	const cols = 80
	for _, p := range realPlanPairs {
		s := realPairScreen(t, p, cols)
		for _, row := range strings.Split(s, "\n") {
			if w := lipgloss.Width(ansi.Strip(row)); w > cols {
				t.Errorf("%s: the Repair screen draws a %d-cell row at %d columns, so the terminal cuts it: %q", p.wire(), w, cols, row)
			}
		}

		flat := strings.Join(strings.Fields(s), "")
		at := 0
		for _, w := range strings.Fields(ansi.Strip(planLine(p.planAction()))) {
			i := strings.Index(flat[at:], w)
			if i < 0 {
				t.Errorf("%s: the screen loses the plan line's %q (and what follows): it was cut, not wrapped.\nscreen:\n%s", p.wire(), w, s)
				break
			}
			at += i + len(w)
		}

		if strings.Contains(s, p.class) {
			t.Errorf("%s: the screen shows the raw class %q:\n%s", p.wire(), p.class, s)
		}
	}
}

// TestRepairScreen_BackupFirstStaysOnWrappedLines (F6): the pairs Orbit
// sends with backup=required still say "backup first" on the 80-column
// screen, including the ones too long for one row.
func TestRepairScreen_BackupFirstStaysOnWrappedLines(t *testing.T) {
	for _, p := range realPlanPairs {
		if realBackup(p.action) != "required" {
			continue
		}
		s := realPairScreen(t, p, 80)
		flat := strings.Join(strings.Fields(s), " ")
		if !strings.Contains(flat, "backup first") {
			t.Errorf("%s: the 80-column screen no longer says \"backup first\":\n%s", p.wire(), s)
		}
	}
}

// TestRepairScreen_AllRealPairsTogetherFitAt80Columns (F6): one plan
// holding every real pair at once still keeps every row within 80 cells.
func TestRepairScreen_AllRealPairsTogetherFitAt80Columns(t *testing.T) {
	const cols = 80
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: cols, Height: 200})
	var lines []string
	actions, manual := 0, 0
	for _, p := range realPlanPairs {
		lines = append(lines, p.wire())
		if p.action == "manual" {
			manual++
		} else {
			actions++
		}
	}
	lines = append(lines, fmt.Sprintf("plan result=manual-required actions=%d manual=%d", actions, manual))
	m = repairLines(t, m, lines...)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4})
	s := screen(m)
	if !strings.Contains(s, "Repairs proposed") && !strings.Contains(s, "Needs your attention") {
		t.Fatalf("setup: the combined plan did not reach the plan screen:\n%s", s)
	}
	for _, row := range strings.Split(s, "\n") {
		if w := lipgloss.Width(ansi.Strip(row)); w > cols {
			t.Errorf("the combined plan draws a %d-cell row at %d columns: %q", w, cols, row)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(s), " "), "backup first") {
		t.Errorf("the combined plan no longer says \"backup first\":\n%s", s)
	}
}

// realTargets are the finding targets the contract names (as in
// TestFindingLine_WordsForEveryContractEnum).
var realTargets = []string{
	"directory", "env-file", "compose-file", "compose", "configuration",
	"secrets-directory", "postgres-password", "document-kek", "oidc-client-secret",
	"staging", "container", "database-volume", "database", "application",
}

// realClasses is every class in realPlanPairs, once.
func realClasses() []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range realPlanPairs {
		if !seen[p.class] {
			seen[p.class] = true
			out = append(out, p.class)
		}
	}
	return out
}

// TestFindingLine_NoRealClassShowsItsRawCode (F7): for every real class
// and any target, the finding line shows words, never a raw class code.
func TestFindingLine_NoRealClassShowsItsRawCode(t *testing.T) {
	classes := realClasses()
	for _, class := range classes {
		for _, target := range realTargets {
			got := ansi.Strip(findingLine(engine.Finding{Class: class, Target: target, Severity: "warn"}))
			for _, raw := range classes {
				if strings.Contains(got, raw) {
					t.Errorf("finding class=%s target=%s reads %q, which shows the raw class code %q", class, target, got, raw)
				}
			}
		}
	}
}

// TestPlanLine_NoRealPairShowsARawClassCode (F7): for every real pair,
// the plan line shows words, never a raw class code.
func TestPlanLine_NoRealPairShowsARawClassCode(t *testing.T) {
	classes := realClasses()
	for _, p := range realPlanPairs {
		got := ansi.Strip(planLine(p.planAction()))
		for _, raw := range classes {
			if strings.Contains(got, raw) {
				t.Errorf("%s reads %q, which shows the raw class code %q", p.wire(), got, raw)
			}
		}
	}
}

// TestFindingLine_WordsForTheThreeUnwordedClasses (F7): the three real
// classes that showed their raw code now read as the spec words them.
func TestFindingLine_WordsForTheThreeUnwordedClasses(t *testing.T) {
	words := map[string]string{
		"configuration-migration-interrupted":  "a configuration change was interrupted",
		"document-volume-retained-without-key": "document data kept but its encryption key is gone",
		"database-schema-mismatch":             "migrated by a different Orbit version",
	}
	for class, want := range words {
		got := ansi.Strip(findingLine(engine.Finding{Class: class, Target: "database", Severity: "warn"}))
		if !strings.HasSuffix(got, "database — "+want) {
			t.Errorf("class %q renders %q, want it to end %q", class, got, "database — "+want)
		}
	}
}

// TestFindingLine_WordsForDatabaseCredentialUnverifiable (v0.4.2 audit
// A2): the class Orbit reports when the application restarts too fast
// for repair to read the credential it presents reads in words, on the
// finding line and on its manual plan line.
func TestFindingLine_WordsForDatabaseCredentialUnverifiable(t *testing.T) {
	const want = "password couldn't be checked: the application restarts too fast"
	got := ansi.Strip(findingLine(engine.Finding{Class: "database-credential-unverifiable", Target: "database", Severity: "fail"}))
	if !strings.HasSuffix(got, "database — "+want) {
		t.Errorf("the finding renders %q, want it to end %q", got, "database — "+want)
	}
	plan := ansi.Strip(planLine(realPlanPair{"database-credential-unverifiable", "manual"}.planAction()))
	if !strings.Contains(plan, want) {
		t.Errorf("the manual plan line reads %q, want it to say %q", plan, want)
	}
	if strings.Contains(plan, "database-credential-unverifiable") {
		t.Errorf("the manual plan line shows the raw class: %q", plan)
	}
}
