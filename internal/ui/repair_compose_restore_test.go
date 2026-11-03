package ui

// Pins #309 (Orbit #1484): Repair treats Orbit's safe action
// restore-compose-file as safe, and draws it and the two Compose-file
// finding classes in words, in a plan and in a diagnosis. The words are
// the ones the issue gives, spelled out here, never read from the code.

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

const (
	composeRestoreAction   = "restore-compose-file"
	composeRestoreWords    = "put back docker-compose.yml from this version's image"
	composeRestorableClass = "compose-file-restorable"
	composeRestorableWords = "missing, and this version's image can put it back"
	composeDriftClass      = "compose-file-drift"
	composeDriftWords      = "differs from this version's copy (left as it is; a deliberate edit is fine)"

	composeRestorePlanLine = "plan action=restore-compose-file resolves=compose-file-restorable mutation=reversible backup=not-required"
)

// composeFlat joins a screen's words with single spaces, so a phrase the
// 80-column screen wrapped onto two rows still reads as one.
func composeFlat(s string) string { return strings.Join(strings.Fields(s), " ") }

// composePlanScreen renders the Repair screen after a --plan run made of
// lines, ending with exit.
func composePlanScreen(t *testing.T, exit int, lines ...string) (RepairModel, string) {
	t.Helper()
	m := sizedRepair(t)
	m = repairLines(t, m, lines...)
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: exit})
	s := screen(m)
	if !strings.Contains(s, "Repairs proposed") && !strings.Contains(s, "Needs your attention") {
		t.Fatalf("setup: the stream did not reach the plan screen:\n%s", s)
	}
	return m, s
}

// Catches: planHasSafe leaving restore-compose-file out of the safe set,
// so a plan whose only action is the Compose restore offers no way to run
// it and a launcher user cannot repair a deleted docker-compose.yml.
func TestRepairScreen_ComposeRestoreOnlyPlanOffersTheSafeRepairs(t *testing.T) {
	_, s := composePlanScreen(t, 3, composeRestorePlanLine, "plan result=ready actions=1 manual=0")
	if !strings.Contains(s, "Run the safe repairs") {
		t.Errorf("a plan whose only action is %s offers no safe-repairs item:\n%s", composeRestoreAction, s)
	}
	if !strings.Contains(composeFlat(s), composeRestoreWords) {
		t.Errorf("the plan screen lacks %q:\n%s", composeRestoreWords, s)
	}
	if !strings.Contains(composeFlat(s), composeRestorableWords) {
		t.Errorf("the plan screen lacks %q:\n%s", composeRestorableWords, s)
	}
	for _, raw := range []string{composeRestoreAction, composeRestorableClass} {
		if strings.Contains(s, raw) {
			t.Errorf("the plan screen shows the raw code %q:\n%s", raw, s)
		}
	}
}

// The pair for the case above: a plan holding only a manual action offers
// no safe-repairs item, so the positive case is not just "always offered".
// Catches: the safe-repairs item shown for any plan, or manual treated as
// safe. managed-file-missing is what Orbit plans for a missing Compose
// file when the pinned image cannot be read.
func TestRepairScreen_ComposeManualOnlyPlanOffersNoSafeRepairs(t *testing.T) {
	_, s := composePlanScreen(t, 4,
		"plan action=manual resolves=managed-file-missing mutation=none backup=not-required",
		"plan result=manual-required actions=0 manual=1")
	if strings.Contains(s, "Run the safe repairs") {
		t.Errorf("a plan holding only a manual action offers the safe repairs:\n%s", s)
	}
}

// Catches: restore-compose-file counted as safe on the plan screen only,
// but Enter on "Run the safe repairs" not running the safe batch or not
// reaching the after-picture, so the repair never actually happens.
func TestRepairModel_TeaTest_ComposeRestoreRunsAsASafeRepair(t *testing.T) {
	m := NewRepairModel(t.TempDir(), "v0.6.0")
	m.prepareRepair = modalRepairStream(`
echo 'plan action=restore-compose-file resolves=compose-file-restorable mutation=reversible backup=not-required'
echo 'plan result=ready actions=1 manual=0'
exit 3`, `
echo 'execute action=restore-compose-file resolves=compose-file-restorable result=done'
echo 'execution result=complete done=1 failed=0'
echo 'diagnosis result=healthy checked=15 skipped=0'
exit 0`)
	tm := newRepairTestModel(t, m, 200, 26)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Repairs proposed")) &&
			bytes.Contains(out, []byte(composeRestoreWords)) &&
			bytes.Contains(out, []byte("Run the safe repairs"))
	}, teatest.WithDuration(5*time.Second))

	// "Run the safe repairs" is preselected: the plan proposed it.
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Repairs applied")) &&
			bytes.Contains(out, []byte("1 done · 0 failed")) &&
			bytes.Contains(out, []byte("diagnosis clear after repairs"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyDown})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

// Catches: actionWords falling through to the raw token for
// restore-compose-file, in the plan line or on the plan screen.
func TestPlanLine_ComposeRestoreActionWords(t *testing.T) {
	got := ansi.Strip(planLine(engine.PlanAction{
		Action: composeRestoreAction, Resolves: composeRestorableClass,
		Mutation: "reversible", Backup: "not-required",
	}))
	if !strings.Contains(got, composeRestoreWords) {
		t.Errorf("the plan line reads %q, want it to say %q", got, composeRestoreWords)
	}
	if strings.Contains(got, composeRestoreAction) {
		t.Errorf("the plan line shows the raw action %q: %q", composeRestoreAction, got)
	}
	if strings.Contains(got, "backup first") {
		t.Errorf("the plan line says %q for an action with backup=not-required: %q", "backup first", got)
	}
}

// Catches: classWords falling through to the raw class code for either
// Compose-file class, on a plan line (restorable is planned as
// restore-compose-file; drift is worded too, should a manual line name it).
func TestPlanLine_ComposeFileClassWords(t *testing.T) {
	for _, tc := range []struct{ action, class, words string }{
		{composeRestoreAction, composeRestorableClass, composeRestorableWords},
		{"manual", composeDriftClass, composeDriftWords},
	} {
		got := ansi.Strip(planLine(engine.PlanAction{Action: tc.action, Resolves: tc.class, Mutation: "none", Backup: "not-required"}))
		if !strings.Contains(got, tc.words) {
			t.Errorf("a plan line resolving %s reads %q, want it to say %q", tc.class, got, tc.words)
		}
		if strings.Contains(got, tc.class) {
			t.Errorf("a plan line resolving %s shows the raw class: %q", tc.class, got)
		}
	}
}

// Catches: classWords falling through to the raw class code for either
// Compose-file class on a finding line, for the target Orbit sends.
func TestFindingLine_ComposeFileClassWords(t *testing.T) {
	for _, tc := range []struct{ class, severity, words string }{
		{composeRestorableClass, "fail", composeRestorableWords},
		{composeDriftClass, "info", composeDriftWords},
	} {
		got := ansi.Strip(findingLine(engine.Finding{Class: tc.class, Target: "compose-file", Severity: tc.severity}))
		if !strings.HasSuffix(got, tc.words) {
			t.Errorf("finding class=%s renders %q, want it to end %q", tc.class, got, tc.words)
		}
		if strings.Contains(got, tc.class) {
			t.Errorf("finding class=%s shows the raw class: %q", tc.class, got)
		}
	}
}

// Catches: the diagnosis screen showing either Compose-file class as a
// raw code, or dropping one of the two findings.
func TestRepairScreen_ComposeFileFindingsInADiagnosisReadInWords(t *testing.T) {
	m := sizedRepair(t)
	m = repairLines(t, m,
		"finding class=compose-file-restorable target=compose-file severity=fail",
		"finding class=compose-file-drift target=compose-file severity=info",
		"diagnosis result=failed checked=2 skipped=0")
	m, _ = repairDone(t, m, engine.DoneMsg{ExitCode: 4, Err: errors.New("exit status 4")})
	s := screen(m)
	flat := composeFlat(s)
	for _, want := range []string{composeRestorableWords, composeDriftWords} {
		if !strings.Contains(flat, want) {
			t.Errorf("the diagnosis screen lacks %q:\n%s", want, s)
		}
	}
	for _, raw := range []string{composeRestorableClass, composeDriftClass} {
		if strings.Contains(s, raw) {
			t.Errorf("the diagnosis screen shows the raw class %q:\n%s", raw, s)
		}
	}
}
