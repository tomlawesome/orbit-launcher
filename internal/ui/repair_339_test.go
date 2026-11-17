package ui

// Pins #339 (Repair retires the old SMTP mail lines), written from the
// issue alone: the plan action retire-smtp-lines counts as a safe repair,
// and its execute result reads back in the after-picture.

import (
	"strings"
	"testing"
)

const (
	smtpPlanLine = "plan action=retire-smtp-lines resolves=smtp-lines-retirable mutation=reversible backup=required target=env-file"
	smtpPlanDone = "plan result=ready actions=1 manual=0"
	smtpExecLine = "execute action=retire-smtp-lines resolves=smtp-lines-retirable result=applied"
	smtpAction   = "remove the old SMTP mail lines (Orbit keeps a backup)"
)

func TestRepair339_RetireSMTPLinesIsASafeRepair(t *testing.T) {
	m := ssPlan(t, 80, 26, []string{smtpPlanLine, smtpPlanDone}, nil, 3)
	if !m.planHasSafe() {
		t.Errorf("planHasSafe() = false for a plan of retire-smtp-lines alone")
	}
	if s := screen(m); !strings.Contains(s, "Run the safe repairs") {
		t.Errorf("the plan menu does not offer Run the safe repairs:\n%s", s)
	}
}

func TestRepair339_AMadeUpActionIsNotASafeRepair(t *testing.T) {
	m := ssPlan(t, 80, 26, []string{
		"plan action=frobnicate-everything resolves=smtp-lines-retirable mutation=reversible backup=required",
		smtpPlanDone,
	}, nil, 3)
	if m.planHasSafe() {
		t.Errorf("planHasSafe() = true for an action the launcher does not know")
	}
	if s := screen(m); strings.Contains(s, "Run the safe repairs") {
		t.Errorf("a made-up action offers Run the safe repairs:\n%s", s)
	}
}

func TestRepair339_AppliedRetirementIsNamedInTheAfterPicture(t *testing.T) {
	s := screen(ssExecuted(t, 80, 26, []string{
		smtpExecLine, "execution result=complete done=1 failed=0", "diagnosis result=healthy checked=20 skipped=0",
	}, nil, 0))
	for _, want := range []string{"Repairs applied", smtpAction, "diagnosis clear after repairs"} {
		if !strings.Contains(ssFlat(s), ssFlat(want)) {
			t.Errorf("after-picture lacks %q:\n%s", want, s)
		}
	}
	for _, raw := range []string{"retire-smtp-lines", "smtp-lines-retirable", "result=applied"} {
		if strings.Contains(s, raw) {
			t.Errorf("the screen shows %q:\n%s", raw, s)
		}
	}
}
